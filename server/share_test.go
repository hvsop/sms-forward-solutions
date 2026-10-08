package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func shareInvite(t *testing.T, c Config, label string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "share.json")
	if err := createShareOwner(c, label, "https://sms.example.com", out); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var i map[string]string
	if json.Unmarshal(b, &i) != nil {
		t.Fatal("bad invitation")
	}
	return i["upload_token"]
}
func shareReq(h http.Handler, method, path, token string, body []byte) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, bytes.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func createShare(t *testing.T, a *Accounts, owner, reader string, size int64) string {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"size": size, "read_token": reader})
	w := shareReq(a, "POST", "/v1/share/files", owner, b)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var f shareFile
	if json.Unmarshal(w.Body.Bytes(), &f) != nil {
		t.Fatal("bad response")
	}
	return f.ID
}
func completeShare(t *testing.T, a *Accounts, owner, id string, size int64) {
	t.Helper()
	metadata, _ := json.Marshal(map[string]string{"nonce": base64.RawURLEncoding.EncodeToString(make([]byte, 8)), "manifest": base64.RawURLEncoding.EncodeToString(make([]byte, 32))})
	if w := shareReq(a, "PUT", "/v1/share/files/"+id+"/manifest", owner, metadata); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := shareReq(a, "PUT", "/v1/share/files/"+id+"/chunks/0", owner, bytes.Repeat([]byte{42}, int(size)+16)); w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := shareReq(a, "POST", "/v1/share/files/"+id+"/complete", owner, []byte(`{}`)); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestShareOwnerIsolationAndNoSMSAccess(t *testing.T) {
	a, c := accountFixture(t)
	alice := shareInvite(t, c, "alice")
	bob := shareInvite(t, c, "bob")
	sms := invite(t, c, "sms")
	reader := strings.Repeat("a", 64)
	for _, token := range []string{"", sms.ReadToken, sms.IngestToken} {
		if w := shareReq(a, "POST", "/v1/share/files", token, []byte(`{}`)); w.Code != 401 {
			t.Fatal("non-share token authorized", w.Code)
		}
	}
	if w := request(a, "GET", "/v1/messages", alice, "", ""); w.Code != 401 {
		t.Fatal("share token can read SMS")
	}
	id := createShare(t, a, alice, reader, 12)
	completeShare(t, a, alice, id, 12)
	for _, method := range []string{"GET", "DELETE"} {
		if w := shareReq(a, method, "/v1/share/files/"+id, bob, nil); w.Code != 404 {
			t.Fatal("cross-owner access", w.Code)
		}
	}
	if w := shareReq(a, "GET", "/v1/share/files", bob, nil); strings.Contains(w.Body.String(), id) {
		t.Fatal("cross-owner listing")
	}
	if w := shareReq(a, "DELETE", "/v1/share/files/"+id, reader, nil); w.Code != 401 {
		t.Fatal("reader revoked share")
	}
	if w := shareReq(a, "GET", "/v1/share/files/"+id, reader, nil); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := shareReq(a, "GET", "/v1/share/files/"+id+"/chunks/0", reader, nil); w.Code != 200 || len(w.Body.Bytes()) != 28 {
		t.Fatal(w.Code)
	}
	if w := shareReq(a, "GET", "/v1/share/files/"+id+"?token="+reader, "", nil); w.Code != 404 {
		t.Fatal("query credentials accepted")
	}
}
func TestShareIncompleteImmutableAndRevoked(t *testing.T) {
	a, c := accountFixture(t)
	owner := shareInvite(t, c, "fixture")
	reader := strings.Repeat("b", 64)
	id := createShare(t, a, owner, reader, 4)
	if w := shareReq(a, "GET", "/v1/share/files/"+id, reader, nil); w.Code != 404 {
		t.Fatal("incomplete visible")
	}
	if w := shareReq(a, "POST", "/v1/share/files/"+id+"/complete", owner, []byte(`{}`)); w.Code != 409 {
		t.Fatal("incomplete finalized")
	}
	path := "/v1/share/files/" + id + "/chunks/0"
	body := bytes.Repeat([]byte{42}, 20)
	for _, want := range []int{201, 200} {
		if w := shareReq(a, "PUT", path, owner, body); w.Code != want {
			t.Fatal("retry not idempotent", w.Code)
		}
	}
	if w := shareReq(a, "PUT", path, owner, bytes.Repeat([]byte{43}, 20)); w.Code != 409 {
		t.Fatal("chunk replaced")
	}
	if w := shareReq(a, "PUT", path, owner, make([]byte, 21)); w.Code != 400 {
		t.Fatal("size mismatch accepted")
	}
	if w := shareReq(a, "PUT", path, owner, make([]byte, shareChunkSize+17)); w.Code != 413 {
		t.Fatal("oversized chunk accepted")
	}
	if w := shareReq(a, "DELETE", "/v1/share/files/"+id, owner, nil); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := shareReq(a, "GET", path, reader, nil); w.Code != 404 {
		t.Fatal("revoked file available")
	}
	if _, err := os.Stat(filepath.Join(c.DataDir, "shares", "files", id)); !os.IsNotExist(err) {
		t.Fatal("revoked data retained")
	}
}
func TestShareRestartExpiryAndZeroLength(t *testing.T) {
	a, c := accountFixture(t)
	owner := shareInvite(t, c, "fixture")
	reader := strings.Repeat("c", 64)
	id := createShare(t, a, owner, reader, 0)
	completeShare(t, a, owner, id, 0)
	s, err := openShares(c.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	a.shares = s
	if w := shareReq(a, "GET", "/v1/share/files/"+id+"/chunks/0", reader, nil); w.Code != 200 || w.Body.Len() != 16 {
		t.Fatal("restart lost empty file")
	}
	s.mu.Lock()
	f := s.files[id]
	f.Expires = time.Now().Add(-time.Second)
	if err = s.save(f); err != nil {
		t.Fatal(err)
	}
	s.mu.Unlock()
	if w := shareReq(a, "GET", "/v1/share/files/"+id, reader, nil); w.Code != 404 {
		t.Fatal("expiry not enforced before cleanup")
	}
	if err = s.cleanup(time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(c.DataDir, "shares", "files", id)); !os.IsNotExist(err) {
		t.Fatal("expired data retained")
	}
}
func TestShareQuotaAndNoSecretsAtRest(t *testing.T) {
	a, c := accountFixture(t)
	owner := shareInvite(t, c, "fixture")
	reader := strings.Repeat("d", 64)
	id := createShare(t, a, owner, reader, shareFileLimit)
	b, err := os.ReadFile(filepath.Join(c.DataDir, "shares", "files", id, "meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte(owner)) || bytes.Contains(b, []byte(reader)) {
		t.Fatal("raw access tokens persisted")
	}
	for n := 0; n < 4; n++ {
		createShare(t, a, owner, reader, shareFileLimit)
	}
	req, _ := json.Marshal(map[string]any{"size": 1, "read_token": reader})
	if w := shareReq(a, "POST", "/v1/share/files", owner, req); w.Code != 409 {
		t.Fatal("quota exceeded", w.Code)
	}
	req, _ = json.Marshal(map[string]any{"size": shareFileLimit + 1, "read_token": reader})
	if w := shareReq(a, "POST", "/v1/share/files", owner, req); w.Code != 400 {
		t.Fatal("oversized file accepted")
	}
}
func TestShareWebSecurityAndNoDirectoryExposure(t *testing.T) {
	a, _ := accountFixture(t)
	for _, path := range []string{"/share", "/share/app.js", "/share/crypto.js", "/share/style.css", "/share/qrcode.js"} {
		w := shareReq(a, "GET", path, "", nil)
		if w.Code != 200 || w.Header().Get("Content-Security-Policy") == "" || w.Header().Get("Referrer-Policy") != "no-referrer" {
			t.Fatal(path, w.Code)
		}
	}
	for _, path := range []string{"/share/../.env", "/share/owners/anything.json", "/share/index.html"} {
		if w := shareReq(a, "GET", path, "", nil); w.Code != 404 {
			t.Fatal("unexpected static path", path, w.Code)
		}
	}
}
