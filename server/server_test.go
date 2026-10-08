package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, e := openStore(filepath.Join(t.TempDir(), "inbox.enc"), bytes.Repeat([]byte{7}, 32), 7*24*time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	return s
}

const testIngestToken = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const testReadToken = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func testAPI(t *testing.T) (*API, http.Handler) {
	a := &API{store: testStore(t), config: Config{APNSEnvironment: "sandbox"}, ingestHash: tokenHash(testIngestToken), readHash: tokenHash(testReadToken)}
	return a, a.handler()
}

func request(h http.Handler, method, path, token, body, media string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if media == "" {
		media = "application/json"
	}
	r.Header.Set("Content-Type", media)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestRoleIsolation(t *testing.T) {
	_, h := testAPI(t)
	for _, tc := range []struct{ method, path, token string }{
		{"POST", "/v1/ingest", testReadToken},
		{"GET", "/v1/messages", testIngestToken},
		{"GET", "/v1/status", testIngestToken},
		{"PUT", "/v1/device", testIngestToken},
		{"DELETE", "/v1/messages/x", testIngestToken},
		{"GET", "/v1/messages", ""},
	} {
		if w := request(h, tc.method, tc.path, tc.token, `{}`, ""); w.Code != 401 {
			t.Fatalf("%s %s: %d", tc.method, tc.path, w.Code)
		}
	}
	if w := request(h, "GET", "/v1/messages?token="+testReadToken, "", "", ""); w.Code != 401 {
		t.Fatal("query token accepted")
	}
}

func TestIngestFormSpecialCharactersAndDedupe(t *testing.T) {
	a, h := testAPI(t)
	content := "验证码 123456\n引号\" & plus+ = 问候👋"
	body := url.Values{"sender": {"测试银行"}, "content": {content}, "sim": {"SIM 1"}, "source_time": {"2026-10-04T12:30:00.123+09:00"}}.Encode()
	for i, want := range []int{201, 200} {
		w := request(h, "POST", "/v1/ingest", testIngestToken, body, "application/x-www-form-urlencoded")
		if w.Code != want {
			t.Fatalf("attempt %d: %d %s", i, w.Code, w.Body.String())
		}
	}
	w := request(h, "GET", "/v1/messages", testReadToken, "", "")
	var page struct {
		Messages []Message `json:"messages"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Messages) != 1 || page.Messages[0].Content != content {
		t.Fatalf("bad inbox: %v", page.Messages)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("missing cache protection")
	}
	data, err := os.ReadFile(a.store.path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("123456")) || bytes.Contains(data, []byte("测试银行")) {
		t.Fatal("plaintext on disk")
	}
}

func TestRejectMalformedAndOversizedRequests(t *testing.T) {
	_, h := testAPI(t)
	for _, body := range []string{`{"sender":"a","content":"x"} {}`, `{"sender":"a","content":"x","unexpected":1}`, `{"sender":"","content":"x"}`, `{"sender":"a","content":"` + strings.Repeat("x", 33000) + `"}`} {
		if w := request(h, "POST", "/v1/ingest", testIngestToken, body, ""); w.Code != 400 {
			t.Fatalf("malformed request accepted: %d", w.Code)
		}
	}
	if w := request(h, "POST", "/v1/ingest", testIngestToken, "test", "text/plain"); w.Code != 415 {
		t.Fatal(w.Code)
	}
	for _, query := range []string{"limit=1000", "limit=-1", "before=abc", "before=0"} {
		if w := request(h, "GET", "/v1/messages?"+query, testReadToken, "", ""); w.Code != 400 {
			t.Fatal(query, w.Code)
		}
	}
}

func TestDeviceValidationAndEnvironment(t *testing.T) {
	_, h := testAPI(t)
	token := strings.Repeat("c", 64)
	if w := request(h, "PUT", "/v1/device", testReadToken, `{"token":"`+token+`","environment":"production"}`, ""); w.Code != 409 {
		t.Fatal(w.Code)
	}
	if w := request(h, "PUT", "/v1/device", testReadToken, `{"token":"hello","environment":"sandbox"}`, ""); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if w := request(h, "PUT", "/v1/device", testReadToken, `{"token":"`+token+`","environment":"sandbox"}`, ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
}

func TestEncryptionRestartTamperAndWrongKey(t *testing.T) {
	s := testStore(t)
	now := time.Now()
	m, _, err := s.add(Message{Sender: "bank", Content: "secret code"}, "stable", now)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := openStore(s.path, bytes.Repeat([]byte{7}, 32), s.retention)
	if err != nil {
		t.Fatal(err)
	}
	list, _ := reopened.list(0, 50, now)
	if len(list) != 1 || list[0].ID != m.ID {
		t.Fatal("restart lost message")
	}
	if _, err = openStore(s.path, bytes.Repeat([]byte{8}, 32), s.retention); err == nil {
		t.Fatal("wrong key accepted")
	}
	b, _ := os.ReadFile(s.path)
	b[len(b)-1] ^= 1
	if err = os.WriteFile(s.path, b, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = openStore(s.path, bytes.Repeat([]byte{7}, 32), s.retention); err == nil {
		t.Fatal("tampering undetected")
	}
	info, _ := os.Stat(s.path)
	if info.Mode().Perm() != 0600 {
		t.Fatalf("permissions: %v", info.Mode())
	}
}

func TestPendingPushPersistsAndRetries(t *testing.T) {
	s := testStore(t)
	now := time.Now()
	device := strings.Repeat("a", 64)
	m, _, err := s.add(Message{Sender: "bank", Content: "code"}, "", now)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.register(device, now); err != nil {
		t.Fatal(err)
	}
	if err = s.pushResult(m.ID, device, errors.New("offline"), false, now); err != nil {
		t.Fatal(err)
	}
	s, err = openStore(s.path, bytes.Repeat([]byte{7}, 32), s.retention)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := s.due(now); ok {
		t.Fatal("backoff ignored")
	}
	r, _, ok := s.due(now.Add(time.Minute))
	if !ok || r.Message.ID != m.ID {
		t.Fatal("pending push lost on restart")
	}
	if err = s.pushResult(m.ID, device, nil, false, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, _, ok = s.due(now.Add(time.Hour)); ok {
		t.Fatal("successful push still pending")
	}
}

func TestOldPushCannotInvalidateNewDevice(t *testing.T) {
	s := testStore(t)
	now := time.Now()
	m, _, _ := s.add(Message{Sender: "a", Content: "b"}, "", now)
	if err := s.register("old", now); err != nil {
		t.Fatal(err)
	}
	if err := s.register("new", now); err != nil {
		t.Fatal(err)
	}
	if err := s.pushResult(m.ID, "old", errors.New("unregistered"), true, now); err != nil {
		t.Fatal(err)
	}
	_, device, ok := s.due(now)
	if !ok || device != "new" {
		t.Fatal("new device invalidated")
	}
}

func TestRetentionPaginationAndDeletion(t *testing.T) {
	s := testStore(t)
	now := time.Now()
	if _, _, err := s.add(Message{Sender: "expired", Content: "old"}, "", now.Add(-8*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, _, err := s.add(Message{Sender: "new", Content: "new"}, "", now); err != nil {
			t.Fatal(err)
		}
	}
	first, next := s.list(0, 2, now)
	if len(first) != 2 || next == "" {
		t.Fatal("bad first page")
	}
	var before uint64
	if _, err := json.Number(next).Int64(); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(next), &before); err != nil {
		t.Fatal(err)
	}
	second, end := s.list(before, 2, now)
	if len(second) != 1 || end != "" || second[0].ID == first[0].ID {
		t.Fatal("bad next page")
	}
	if err := s.delete(first[0].ID, now); err != nil {
		t.Fatal(err)
	}
	all, _ := s.list(0, 10, now)
	if len(all) != 2 {
		t.Fatal("delete failed")
	}
	if err := s.purge(now.Add(8 * 24 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	all, _ = s.list(0, 10, now.Add(8*24*time.Hour))
	if len(all) != 0 {
		t.Fatal("retention failed")
	}
}

func TestConcurrentIngress(t *testing.T) {
	s := testStore(t)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := s.add(Message{Sender: "a", Content: "b"}, "", time.Now()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	all, _ := s.list(0, 100, time.Now())
	if len(all) != 20 {
		t.Fatal("lost concurrent writes")
	}
}

func TestAPNSHTTP2AndPrivatePayload(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	var seen bool
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = true
		if r.ProtoMajor != 2 {
			t.Error("APNs requires HTTP/2")
		}
		if r.Header.Get("apns-push-type") != "alert" || r.Header.Get("apns-topic") != "com.test.inbox" {
			t.Error("bad APNs headers")
		}
		jwt := strings.TrimPrefix(r.Header.Get("authorization"), "bearer ")
		parts := strings.Split(jwt, ".")
		if len(parts) != 3 {
			t.Error("invalid JWT")
			w.WriteHeader(403)
			return
		}
		sig, err := base64.RawURLEncoding.DecodeString(parts[2])
		if err != nil || len(sig) != 64 {
			t.Error("invalid signature")
			w.WriteHeader(403)
			return
		}
		hash := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
		if !ecdsa.Verify(&key.PublicKey, hash[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
			t.Error("JWT signature not ES256")
		}
		b, _ := io.ReadAll(r.Body)
		var payload map[string]json.RawMessage
		if json.Unmarshal(b, &payload) != nil || len(payload) != 1 || payload["aps"] == nil {
			t.Error("payload contains unexpected data")
		}
		var aps map[string]json.RawMessage
		_ = json.Unmarshal(payload["aps"], &aps)
		if len(aps) != 4 || string(aps["badge"]) != "7" || aps["alert"] == nil || aps["sound"] == nil || aps["thread-id"] == nil {
			t.Error("unexpected aps payload")
		}
		if bytes.Contains(b, []byte("sender")) || bytes.Contains(b, []byte("content")) {
			t.Error("sensitive payload")
		}
		w.WriteHeader(200)
	}))
	ts.EnableHTTP2 = true
	ts.StartTLS()
	defer ts.Close()
	a := &APNS{key: key, keyID: "KEY", teamID: "TEAM", topic: "com.test.inbox", host: ts.URL, client: ts.Client()}
	invalid, err := a.send(context.Background(), hex.EncodeToString(bytes.Repeat([]byte{1}, 32)), 7)
	if err != nil || invalid || !seen {
		t.Fatalf("send failed: %v", err)
	}
}

func TestAPNSRejectsInvalidDeviceAndDoesNotFollowRedirect(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	for _, status := range []int{410, 307} {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", "https://example.invalid/steal")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"reason":"Unregistered"}`))
		}))
		a := &APNS{key: key, host: ts.URL, client: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
		invalid, err := a.send(context.Background(), "abc", 1)
		if err == nil || !invalid {
			t.Fatal("rejection not handled")
		}
		ts.Close()
	}
}

func TestInitDoesNotOverwriteSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := initConfig(path); err != nil {
		t.Fatal(err)
	}
	if err := initConfig(path); err == nil {
		t.Fatal("configuration overwritten")
	}
	c, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.StorageKey) != 32 || c.DataDir == "" {
		t.Fatal("invalid generated secrets")
	}
}
