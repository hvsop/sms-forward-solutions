package main

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

func pairCode(t *testing.T, a *Accounts, i Invitation) string {
	t.Helper()
	w := request(a, "POST", "/v1/android/pairing", i.ReadToken, `{}`, "")
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var v struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	return v.Code
}
func redeem(t *testing.T, a *Accounts, code string) string {
	t.Helper()
	w := request(a, "POST", "/v1/android/pair", "", `{"code":"`+code+`"}`, "")
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var v struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	return v.Token
}
func TestAndroidPairingIsolationAndReplacement(t *testing.T) {
	a, c := accountFixture(t)
	alice := invite(t, c, "alice")
	bob := invite(t, c, "bob")
	code := pairCode(t, a, alice)
	token := redeem(t, a, code)
	if w := request(a, "POST", "/v1/android/pair", "", `{"code":"`+code+`"}`, ""); w.Code != 401 {
		t.Fatal("code replay", w.Code)
	}
	if w := request(a, "POST", "/v1/android/pairing", alice.IngestToken, `{}`, ""); w.Code != 401 {
		t.Fatal("ingest can pair")
	}
	for _, path := range []string{"/v1/messages", "/v1/status", "/v1/android/pairing"} {
		if w := request(a, "GET", path, token, "", ""); w.Code != 401 {
			t.Fatal("android can read", path, w.Code)
		}
	}
	body := `{"sender":"fixture","content":"alice only"}`
	if w := request(a, "POST", "/v1/ingest", token, body, ""); w.Code != 201 {
		t.Fatal(w.Code)
	}
	if w := request(a, "GET", "/v1/messages", bob.ReadToken, "", ""); strings.Contains(w.Body.String(), "alice only") {
		t.Fatal("tenant leak")
	}
	if w := request(a, "DELETE", "/v1/android/device", bob.ReadToken, "", ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := request(a, "GET", "/v1/android/status", token, "", ""); w.Code != 200 {
		t.Fatal("other account revoked device")
	}
	replacement := redeem(t, a, pairCode(t, a, alice))
	if w := request(a, "POST", "/v1/ingest", token, body, ""); w.Code != 401 {
		t.Fatal("old token survived pairing")
	}
	// Reload only credential file; avoid opening a second live inbox writer.
	a.mu.Lock()
	err := a.loadAndroid()
	a.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if w := request(a, "GET", "/v1/android/status", replacement, "", ""); w.Code != 200 {
		t.Fatal("credential not durable")
	}
	if w := request(a, "DELETE", "/v1/account", alice.ReadToken, `{"confirmation":"delete-account"}`, ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := request(a, "POST", "/v1/ingest", replacement, body, ""); w.Code != 401 {
		t.Fatal("deleted account still ingests")
	}
}
func TestAndroidPairExpiryLimitsAndRevocation(t *testing.T) {
	a, c := accountFixture(t)
	i := invite(t, c, "fixture")
	old := pairCode(t, a, i)
	fresh := pairCode(t, a, i)
	if w := request(a, "POST", "/v1/android/pair", "", `{"code":"`+old+`"}`, ""); w.Code != 401 {
		t.Fatal("old code accepted")
	}
	a.mu.Lock()
	for h, p := range a.pairings {
		p.Expires = time.Now().Add(-time.Second)
		a.pairings[h] = p
	}
	a.mu.Unlock()
	if w := request(a, "POST", "/v1/android/pair", "", `{"code":"`+fresh+`"}`, ""); w.Code != 401 {
		t.Fatal("expired code accepted")
	}
	token := redeem(t, a, pairCode(t, a, i))
	if w := request(a, "DELETE", "/v1/android/device", token, "", ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := request(a, "GET", "/v1/android/status", token, "", ""); w.Code != 401 {
		t.Fatal("revoked token accepted")
	}
	for n := 0; n < 30; n++ {
		request(a, "POST", "/v1/android/pair", "", `{"code":"AAAAAAAAAAAAAAAA"}`, "")
	}
	if w := request(a, "POST", "/v1/android/pair", "", `{}`, ""); w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatal("no rate limit")
	}
}
func TestAndroidConcurrentCodeRedemption(t *testing.T) {
	a, c := accountFixture(t)
	code := pairCode(t, a, invite(t, c, "fixture"))
	var wg sync.WaitGroup
	results := make(chan int, 8)
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- request(a, "POST", "/v1/android/pair", "", `{"code":"`+code+`"}`, "").Code
		}()
	}
	wg.Wait()
	close(results)
	success := 0
	for status := range results {
		if status == 201 {
			success++
		} else if status != 401 {
			t.Fatal(status)
		}
	}
	if success != 1 {
		t.Fatal("pairing consumed multiple times", success)
	}
}
