package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeApple struct {
	failRevoke bool
	revoked    int
}

func (p *fakeApple) exchange(_ context.Context, identity, code, nonce string) (string, string, error) {
	if code != nonce || identity == "" {
		return "", "", errAppleIdentity
	}
	return identity, "refresh-" + identity, nil
}
func (p *fakeApple) revoke(_ context.Context, _ string) error {
	if p.failRevoke {
		return errors.New("offline")
	}
	p.revoked++
	return nil
}
func signupCode(t *testing.T, c Config) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "signup.json")
	if err := createSignupInvite(c, out); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(out)
	var v map[string]string
	_ = json.Unmarshal(b, &v)
	return v["signup_code"]
}
func loginBody(t *testing.T, a *Accounts, subject, invite string) string {
	t.Helper()
	w := request(a, "POST", "/v1/auth/challenge", "", "", "")
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var c map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &c); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(map[string]string{"challenge_id": c["challenge_id"], "identity_token": subject, "authorization_code": c["nonce"], "invite_code": invite})
	return string(b)
}
func loginToken(t *testing.T, a *Accounts, subject, invite, bearer string) string {
	t.Helper()
	w := request(a, "POST", "/v1/auth/apple", bearer, loginBody(t, a, subject, invite), "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var result map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &result)
	return result["read_token"].(string)
}
func TestSelfServiceSignupRecoveryAndDeletion(t *testing.T) {
	a, c := accountFixture(t)
	apple := &fakeApple{}
	a.apple = apple
	code := signupCode(t, c)
	token := loginToken(t, a, "apple-alice", code, "")
	pair := pairAndroid(t, a, token)
	if w := request(a, "POST", "/v1/ingest", pair, `{"sender":"test","content":"preserved"}`, ""); w.Code != 201 {
		t.Fatal(w.Code)
	}
	if w := request(a, "PUT", "/v1/device", token, `{"token":"`+strings.Repeat("c", 64)+`","environment":"production"}`, ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := request(a, "POST", "/v1/auth/apple", "", loginBody(t, a, "apple-bob", code), ""); w.Code != 403 {
		t.Fatal("invite reused", w.Code)
	}
	next := loginToken(t, a, "apple-alice", "", "")
	if next == token {
		t.Fatal("read session did not rotate")
	}
	if w := request(a, "GET", "/v1/status", token, "", ""); w.Code != 401 {
		t.Fatal("old session valid", w.Code)
	}
	w := request(a, "GET", "/v1/messages", next, "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "preserved") {
		t.Fatal("lost inbox")
	}
	if w := request(a, "GET", "/v1/android/status", pair, "", ""); w.Code != 200 {
		t.Fatal("lost android")
	}
	if w := request(a, "GET", "/v1/status", next, "", ""); strings.Contains(w.Body.String(), `"device_registered":true`) {
		t.Fatal("old receiver retained")
	}
	for _, tt := range a.tenants {
		if tt.account.AppleSubject == "apple-alice" || strings.Contains(tt.account.AppleRefresh, "refresh-alice") {
			t.Fatal("Apple secrets stored plaintext")
		}
		plain, err := a.openAppleRefresh(tt.account)
		if err != nil || plain != "refresh-apple-alice" {
			t.Fatal("refresh cannot decrypt")
		}
	}
	apple.failRevoke = true
	if w := request(a, "DELETE", "/v1/account", next, `{"confirmation":"delete-account"}`, ""); w.Code != 503 {
		t.Fatal("failed revoke ignored")
	}
	if w := request(a, "GET", "/v1/status", next, "", ""); w.Code != 200 {
		t.Fatal("failed revoke deleted account")
	}
	apple.failRevoke = false
	if w := request(a, "DELETE", "/v1/account", next, `{"confirmation":"delete-account"}`, ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if apple.revoked != 1 {
		t.Fatal("Apple authorization not revoked")
	}
	if w := request(a, "GET", "/v1/android/status", pair, "", ""); w.Code != 401 {
		t.Fatal("android still active")
	}
	if w := request(a, "POST", "/v1/auth/apple", "", loginBody(t, a, "apple-alice", code), ""); w.Code != 403 {
		t.Fatal("deletion revived used invite")
	}
}
func pairAndroid(t *testing.T, a *Accounts, token string) string {
	t.Helper()
	w := request(a, "POST", "/v1/android/pairing", token, "", "")
	var code map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &code)
	b, _ := json.Marshal(map[string]any{"code": code["code"]})
	w = request(a, "POST", "/v1/android/pair", "", string(b), "")
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var result map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &result)
	return result["token"]
}
func TestLinkPreservesLegacyAndCannotMergeUsers(t *testing.T) {
	a, c := accountFixture(t)
	a.apple = &fakeApple{}
	old := invite(t, c, "legacy")
	other := invite(t, c, "other")
	pair := pairAndroid(t, a, old.ReadToken)
	token := loginToken(t, a, "subject", "", old.ReadToken)
	if token != old.ReadToken {
		t.Fatal("link rotated legacy token")
	}
	for _, auth := range []string{other.ReadToken, other.IngestToken, pair, "bad"} {
		w := request(a, "POST", "/v1/auth/apple", auth, loginBody(t, a, "subject", ""), "")
		if w.Code != 409 && w.Code != 401 {
			t.Fatal("cross-account link", w.Code)
		}
	}
	if w := request(a, "POST", "/v1/auth/apple", old.ReadToken, loginBody(t, a, "different", ""), ""); w.Code != 409 {
		t.Fatal("relinked different Apple")
	}
	if w := request(a, "GET", "/v1/account/profile", pair, "", ""); w.Code != 401 {
		t.Fatal("android read profile")
	}
	if w := request(a, "POST", "/v1/account/logout", token, "", ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := request(a, "GET", "/v1/status", token, "", ""); w.Code != 401 {
		t.Fatal("logout session still active")
	}
	recovered := loginToken(t, a, "subject", "", "")
	if w := request(a, "GET", "/v1/account/profile", recovered, "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"android_paired":true`) {
		t.Fatal("lost pairing", w.Body.String())
	}
}
func TestAppleChallengeReplayAndInvitationExpiry(t *testing.T) {
	a, c := accountFixture(t)
	a.apple = &fakeApple{}
	code := signupCode(t, c)
	body := loginBody(t, a, "subject", code)
	if w := request(a, "POST", "/v1/auth/apple", "", body, ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := request(a, "POST", "/v1/auth/apple", "", body, ""); w.Code != 401 {
		t.Fatal("replay accepted")
	}
	body = loginBody(t, a, "subject", "")
	for id, ch := range a.challenges {
		ch.Expires = time.Now().Add(-time.Second)
		a.challenges[id] = ch
	}
	if w := request(a, "POST", "/v1/auth/apple", "", body, ""); w.Code != 401 {
		t.Fatal("expired challenge accepted")
	}
	expired := signupCode(t, c)
	b, _ := json.Marshal(signupInvite{Expires: time.Now().Add(-time.Hour)})
	_ = os.WriteFile(filepath.Join(c.DataDir, "signup-invites", codeHash(expired)+".json"), b, 0600)
	if w := request(a, "POST", "/v1/auth/apple", "", loginBody(t, a, "other", expired), ""); w.Code != 403 {
		t.Fatal("expired invite accepted")
	}
}
func TestSignupInviteConcurrentClaim(t *testing.T) {
	a, c := accountFixture(t)
	a.apple = &fakeApple{}
	code := signupCode(t, c)
	bodies := []string{loginBody(t, a, "one", code), loginBody(t, a, "two", code)}
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for _, b := range bodies {
		wg.Add(1)
		go func(body string) { defer wg.Done(); codes <- request(a, "POST", "/v1/auth/apple", "", body, "").Code }(b)
	}
	wg.Wait()
	close(codes)
	counts := map[int]int{}
	for code := range codes {
		counts[code]++
	}
	if counts[200] != 1 || counts[403] != 1 {
		t.Fatal(counts)
	}
}
func TestAppleLinkedAccountSurvivesRestart(t *testing.T) {
	a, c := accountFixture(t)
	a.apple = &fakeApple{}
	code := signupCode(t, c)
	token := loginToken(t, a, "persistent", code, "")
	pair := pairAndroid(t, a, token)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b, err := newAccounts(ctx, c, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); b.workers.Wait() }()
	b.apple = &fakeApple{}
	next := loginToken(t, b, "persistent", "", "")
	if w := request(b, "GET", "/v1/account/profile", next, "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"android_paired":true`) {
		t.Fatal("restart lost identity", w.Body.String())
	}
	if w := request(b, "GET", "/v1/android/status", pair, "", ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
}
