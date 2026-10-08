package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestClearMessagesPreservesAccountDevicePairingAndOtherUsers(t *testing.T) {
	a, c := accountFixture(t)
	alice := invite(t, c, "alice")
	bob := invite(t, c, "bob")
	android := redeem(t, a, pairCode(t, a, alice))
	device := strings.Repeat("b", 64)
	if w := request(a, "PUT", "/v1/device", alice.ReadToken, `{"token":"`+device+`","environment":"production"}`, ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, token := range []string{alice.IngestToken, android, bob.IngestToken} {
		if w := request(a, "POST", "/v1/ingest", token, `{"sender":"fixture","content":"synthetic message"}`, ""); w.Code != 201 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	store := a.tenants[alice.AccountID].api.store
	sequence := store.state.Sequence
	for _, token := range []string{"", alice.IngestToken, android} {
		if w := request(a, "DELETE", "/v1/messages", token, "", ""); w.Code != 401 {
			t.Fatal("non-reader cleared inbox", w.Code)
		}
	}
	if store.status(time.Now())["messages"] != 2 {
		t.Fatal("unauthorized request changed inbox")
	}
	for i := 0; i < 2; i++ {
		if w := request(a, "DELETE", "/v1/messages", alice.ReadToken, "", ""); w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	// Verify the encrypted persisted state, not just the in-memory response.
	reopened, err := openStore(filepath.Join(c.DataDir, "inboxes", alice.AccountID+".enc"), accountKey(c.StorageKey, alice.AccountID), c.Retention)
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.state.Records) != 0 || reopened.state.Device != device || reopened.state.Sequence != sequence {
		t.Fatal("clearing changed registration/sequence or did not persist")
	}
	if _, _, ok := reopened.due(time.Now()); ok {
		t.Fatal("deleted messages still pending push")
	}
	if a.tenants[bob.AccountID].api.store.status(time.Now())["messages"] != 1 {
		t.Fatal("another user's inbox changed")
	}
	if w := request(a, "GET", "/v1/status", alice.ReadToken, "", ""); w.Code != 200 {
		t.Fatal("reader invalid after clear", w.Code)
	}
	if w := request(a, "GET", "/v1/android/status", android, "", ""); w.Code != 200 {
		t.Fatal("pairing invalid after clear", w.Code)
	}
	if w := request(a, "POST", "/v1/ingest", android, `{"sender":"fixture","content":"after clearing"}`, ""); w.Code != 201 {
		t.Fatal("new SMS rejected", w.Code)
	}
	if store.state.Sequence <= sequence || store.state.Device != device || len(store.state.Records) != 1 {
		t.Fatal("new SMS or notification binding lost after clear")
	}
}

func TestAccountDeletionRequiresExplicitConfirmation(t *testing.T) {
	a, c := accountFixture(t)
	i := invite(t, c, "owner")
	for _, body := range []string{"", `{}`, `{"confirmation":"clear-messages"}`, `{"confirmation":"delete-account"} {}`} {
		if w := request(a, "DELETE", "/v1/account", i.ReadToken, body, ""); w.Code != 400 {
			t.Fatal("unconfirmed account deletion accepted", w.Code)
		}
		if w := request(a, "GET", "/v1/status", i.ReadToken, "", ""); w.Code != 200 {
			t.Fatal("account lost without confirmation", w.Code)
		}
	}
	if w := request(a, "DELETE", "/v1/account", i.ReadToken, `{"confirmation":"delete-account"}`, ""); w.Code != 200 {
		t.Fatal("confirmed account deletion failed", w.Code)
	}
	if w := request(a, "GET", "/v1/status", i.ReadToken, "", ""); w.Code != 401 {
		t.Fatal("deleted account still active", w.Code)
	}
}
