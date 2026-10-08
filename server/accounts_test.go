package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func accountFixture(t *testing.T) (*Accounts, Config) {
	t.Helper()
	c := Config{DataDir: t.TempDir(), StorageKey: bytes.Repeat([]byte{9}, 32), Retention: 7 * 24 * time.Hour, APNSEnvironment: "production"}
	ctx, cancel := context.WithCancel(context.Background())
	a, err := newAccounts(ctx, c, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); a.workers.Wait() })
	return a, c
}

func invite(t *testing.T, c Config, label string) Invitation {
	t.Helper()
	i, err := createAccount(c, label, "https://sms.example.com", filepath.Join(t.TempDir(), "invitation.json"))
	if err != nil {
		t.Fatal(err)
	}
	return i
}

func TestUserIsolationAndNoClientSelectedTenant(t *testing.T) {
	a, c := accountFixture(t)
	alice := invite(t, c, "alice")
	bob := invite(t, c, "bob")
	for _, i := range []Invitation{alice, bob} {
		body := `{"sender":"bank","content":"` + i.Label + ` private message"}`
		if w := request(a, "POST", "/v1/ingest", i.IngestToken, body, ""); w.Code != 201 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	w := request(a, "GET", "/v1/messages?user_id="+alice.AccountID, bob.ReadToken, "", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "alice") || !strings.Contains(w.Body.String(), "bob private message") {
		t.Fatal("tenant isolation failed", w.Body.String())
	}
	alicePage := request(a, "GET", "/v1/messages", alice.ReadToken, "", "")
	var page struct {
		Messages []Message `json:"messages"`
	}
	_ = json.Unmarshal(alicePage.Body.Bytes(), &page)
	if len(page.Messages) != 1 {
		t.Fatal("alice inbox missing")
	}
	if w := request(a, "DELETE", "/v1/messages/"+page.Messages[0].ID, bob.ReadToken, "", ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	alicePage = request(a, "GET", "/v1/messages", alice.ReadToken, "", "")
	if !strings.Contains(alicePage.Body.String(), "alice private message") {
		t.Fatal("cross-tenant deletion")
	}
	if w := request(a, "GET", "/v1/messages", alice.IngestToken, "", ""); w.Code != 401 {
		t.Fatal("ingest token can read")
	}
	if w := request(a, "POST", "/v1/ingest", bob.IngestToken, `{"sender":"x","content":"x","user_id":"`+alice.AccountID+`"}`, ""); w.Code != 400 {
		t.Fatal("client-supplied user accepted")
	}
	if _, err := openStore(filepath.Join(c.DataDir, "inboxes", alice.AccountID+".enc"), accountKey(c.StorageKey, bob.AccountID), c.Retention); err == nil {
		t.Fatal("accounts share encryption key")
	}
}

func TestDevicesAreBoundToExactlyOneUser(t *testing.T) {
	a, c := accountFixture(t)
	alice := invite(t, c, "alice")
	bob := invite(t, c, "bob")
	device := strings.Repeat("c", 64)
	body := `{"token":"` + device + `","environment":"production"}`
	if w := request(a, "PUT", "/v1/device", alice.ReadToken, body, ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := request(a, "PUT", "/v1/device", bob.ReadToken, body, ""); w.Code != 409 {
		t.Fatal("device incorrectly shared", w.Code)
	}
	// Bob cannot unregister Alice's binding using his own reader credential.
	if w := request(a, "DELETE", "/v1/device", bob.ReadToken, `{"token":"`+device+`"}`, ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := request(a, "PUT", "/v1/device", bob.ReadToken, body, ""); w.Code != 409 {
		t.Fatal("cross-user unregister")
	}
	if w := request(a, "DELETE", "/v1/device", alice.ReadToken, `{"token":"`+device+`"}`, ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := request(a, "PUT", "/v1/device", bob.ReadToken, body, ""); w.Code != 200 {
		t.Fatal("cannot rebind after release", w.Code)
	}
}

func TestAccountDeletionRevokesBothCredentialsAndBlocksStaleWrites(t *testing.T) {
	a, c := accountFixture(t)
	alice := invite(t, c, "alice")
	bob := invite(t, c, "bob")
	for _, i := range []Invitation{alice, bob} {
		if w := request(a, "POST", "/v1/ingest", i.IngestToken, `{"sender":"bank","content":"hello"}`, ""); w.Code != 201 {
			t.Fatal(w.Code)
		}
	}
	stale := a.tenants[alice.AccountID].api.store
	if w := request(a, "DELETE", "/v1/account", alice.ReadToken, `{"confirmation":"delete-account"}`, ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := request(a, "GET", "/v1/messages", alice.ReadToken, "", ""); w.Code != 401 {
		t.Fatal("deleted read token accepted")
	}
	if w := request(a, "POST", "/v1/ingest", alice.IngestToken, `{"sender":"x","content":"x"}`, ""); w.Code != 401 {
		t.Fatal("deleted ingest token accepted")
	}
	if _, _, err := stale.add(Message{Sender: "x", Content: "race"}, "", time.Now()); err == nil {
		t.Fatal("stale writer recreated inbox")
	}
	if _, err := os.Stat(filepath.Join(c.DataDir, "inboxes", alice.AccountID+".enc")); !os.IsNotExist(err) {
		t.Fatal("deleted inbox persists")
	}
	if w := request(a, "GET", "/v1/messages", bob.ReadToken, "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "hello") {
		t.Fatal("other account affected")
	}
	meta, err := os.ReadFile(filepath.Join(c.DataDir, "accounts", alice.AccountID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(meta, []byte("alice")) {
		t.Fatal("deleted alias retained")
	}
}

func TestServerProcessLock(t *testing.T) {
	dir := t.TempDir()
	first, err := lockDataDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if second, err := lockDataDirectory(dir); err == nil {
		second.Close()
		t.Fatal("multiple writers allowed")
	}
}
