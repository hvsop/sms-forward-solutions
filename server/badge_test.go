package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestUnreadTwoOneZeroAndRestart(t *testing.T) {
	s := testStore(t)
	now := time.Now()
	if err := s.register("device", now); err != nil {
		t.Fatal(err)
	}
	add := func(key string) Message {
		t.Helper()
		m, _, e := s.add(Message{Content: "synthetic"}, key, now)
		if e != nil {
			t.Fatal(e)
		}
		return m
	}
	first, second := add("one"), add("two")
	add("two")
	page, _ := s.inboxPage(0, 1, now)
	if page.Badge != 2 || page.Messages[0].Read || page.NextBefore == "" {
		t.Fatal("unread list/count or paging incorrect")
	}
	// Neither normal refresh nor an old build's snapshot acknowledgement reads messages.
	if count, err := s.acknowledge(2, now); err != nil || count != 2 {
		t.Fatal("old app marked all read")
	}
	read, err := s.markRead(second.ID, now)
	if err != nil || read.Badge != 1 || read.Revision == page.Revision {
		t.Fatal("2 -> 1 failed", read, err)
	}
	again, err := s.markRead(second.ID, now)
	if err != nil || again != read {
		t.Fatal("repeat read deducted again")
	}
	if pending, _, ok := s.due(now); !ok || pending.Message.ID != first.ID {
		t.Fatal("read message still pending or unread push lost")
	}
	reopened, err := openStore(s.path, bytes.Repeat([]byte{7}, 32), s.retention)
	if err != nil || reopened.badge(now) != 1 {
		t.Fatal("read state not persisted", err)
	}
	page, _ = reopened.inboxPage(0, 100, now)
	if !page.Messages[0].Read || page.Messages[1].Read {
		t.Fatal("wrong message marked read")
	}
	read, err = reopened.markRead(first.ID, now)
	if err != nil || read.Badge != 0 {
		t.Fatal("1 -> 0 failed")
	}
	newer := add("three")
	if s.badge(now) != 2 {
		t.Fatal("new arrival not counted")
	}
	if err := s.delete(second.ID, now); err != nil || s.badge(now) != 2 {
		t.Fatal("deleting read message changed unread count")
	}
	if err := s.delete(newer.ID, now); err != nil || s.badge(now) != 1 {
		t.Fatal("deleting unread message did not decrement")
	}
	if s.badge(now.Add(8*24*time.Hour)) != 0 {
		t.Fatal("expired message counted")
	}
	if _, err := s.markRead("missing", now); !errors.Is(err, errMessageNotFound) {
		t.Fatal("missing read accepted")
	}
	if err := s.clearMessages(); err != nil || s.badge(now) != 0 {
		t.Fatal("clear did not reset")
	}
	add("four")
	if s.badge(now) != 1 {
		t.Fatal("new message after clear missing")
	}
}

func TestLegacyReadMigration(t *testing.T) {
	s := testStore(t)
	now := time.Now()
	for i := 0; i < 3; i++ {
		if _, _, err := s.add(Message{Content: "test"}, "", now); err != nil {
			t.Fatal(err)
		}
	}
	next := s.state
	next.SeenSequence = 2
	if err := s.commit(next); err != nil {
		t.Fatal(err)
	}
	reopened, err := openStore(s.path, bytes.Repeat([]byte{7}, 32), s.retention)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := reopened.inboxPage(0, 100, now)
	if page.Badge != 1 || page.Messages[0].Read || !page.Messages[1].Read || !page.Messages[2].Read {
		t.Fatal("legacy baseline migration incorrect")
	}
	if _, err := reopened.acknowledge(3, now); err != nil || reopened.badge(now) != 1 {
		t.Fatal("legacy request swallowed new unread")
	}
}

func TestConcurrentReadAndArrival(t *testing.T) {
	s := testStore(t)
	now := time.Now()
	m, _, err := s.add(Message{Content: "test"}, "", now)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.markRead(m.ID, now); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		if _, _, err := s.add(Message{Content: "new"}, "new", now); err != nil {
			t.Error(err)
		}
	}()
	wg.Wait()
	if s.badge(now) != 1 {
		t.Fatal("concurrent read lost new arrival")
	}
}

func TestReadEndpointIsolationAndPagination(t *testing.T) {
	a, c := accountFixture(t)
	alice, bob := invite(t, c, "alice"), invite(t, c, "bob")
	for _, i := range []Invitation{alice, bob} {
		for n := 0; n < 2; n++ {
			if w := request(a, "POST", "/v1/ingest", i.IngestToken, `{"sender":"test","content":"synthetic"}`, ""); w.Code != 201 {
				t.Fatal(w.Code)
			}
		}
	}
	getPage := func(token, path string) InboxPage {
		t.Helper()
		w := request(a, "GET", path, token, "", "")
		var p InboxPage
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &p) != nil {
			t.Fatal("invalid page")
		}
		return p
	}
	page := getPage(alice.ReadToken, "/v1/messages?limit=1")
	if page.Badge != 2 || len(page.Messages) != 1 {
		t.Fatal("global count limited by pagination")
	}
	path := "/v1/messages/" + page.Messages[0].ID + "/read"
	for _, token := range []string{"", alice.IngestToken} {
		if w := request(a, "PUT", path, token, "", ""); w.Code != 401 {
			t.Fatal("reader role not enforced")
		}
	}
	if w := request(a, "PUT", path, bob.ReadToken, "", ""); w.Code != 404 {
		t.Fatal("cross-tenant read")
	}
	if w := request(a, "PUT", path, alice.ReadToken, "", ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	page = getPage(alice.ReadToken, "/v1/messages?limit=1&before="+page.NextBefore)
	if page.Badge != 1 || page.Messages[0].Read {
		t.Fatal("older unread page mismatch")
	}
	if page = getPage(bob.ReadToken, "/v1/messages"); page.Badge != 2 {
		t.Fatal("other tenant changed")
	}
	// Old build remains connected but cannot clear unread state with an inbox refresh.
	if w := request(a, "POST", "/v1/messages/seen", bob.ReadToken, `{"through":"2"}`, ""); w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"badge":2`)) {
		t.Fatal("old client compatibility failed")
	}
}
