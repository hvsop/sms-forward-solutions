package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var errFull = errors.New("inbox full")

type Message struct {
	ID         string    `json:"id"`
	Read       bool      `json:"read"`
	Sender     string    `json:"sender"`
	Content    string    `json:"content"`
	SIM        string    `json:"sim"`
	SourceTime string    `json:"source_time"`
	ReceivedAt time.Time `json:"received_at"`
}

type Record struct {
	Message     Message   `json:"message"`
	Sequence    uint64    `json:"sequence"`
	Dedupe      string    `json:"dedupe,omitempty"`
	Pending     bool      `json:"pending"`
	Attempts    int       `json:"attempts"`
	NextAttempt time.Time `json:"next_attempt"`
}

type State struct {
	Version       int      `json:"version"`
	Sequence      uint64   `json:"sequence"`
	SeenSequence  uint64   `json:"seen_sequence"` // Migration only: build 5 treated these messages as read.
	Revision      uint64   `json:"revision"`
	Device        string   `json:"device"`
	Records       []Record `json:"records"`
	LastPushError string   `json:"last_push_error"`
}

// Store is intentionally a single-process, bounded personal inbox. Every mutation
// is an authenticated encrypted snapshot, committed before the API acknowledges it.
type Store struct {
	deleted   bool
	mu        sync.Mutex
	pushMu    sync.Mutex // Orders push submission before an inbox acknowledgement.
	path      string
	aead      cipher.AEAD
	state     State
	retention time.Duration
}

func openStore(path string, key []byte, retention time.Duration) (*Store, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	s := &Store{path: path, aead: aead, retention: retention, state: State{Version: 1}}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if len(data) < 4+aead.NonceSize() || string(data[:4]) != "PSM1" {
		return nil, errors.New("invalid encrypted store")
	}
	nonce := data[4 : 4+aead.NonceSize()]
	plain, err := aead.Open(nil, nonce, data[4+aead.NonceSize():], []byte("private-sms/store/v1"))
	if err != nil {
		return nil, errors.New("cannot decrypt store: wrong key or damaged data")
	}
	if err := json.Unmarshal(plain, &s.state); err != nil {
		return nil, err
	}
	if s.state.Version != 1 {
		return nil, errors.New("unsupported store version")
	}
	// Preserve the old acknowledged baseline without marking newer messages read.
	for i := range s.state.Records {
		if s.state.Records[i].Sequence <= s.state.SeenSequence {
			s.state.Records[i].Message.Read = true
			s.state.Records[i].Pending = false
		}
	}
	return s, nil
}

func (s *Store) commit(next State) error {
	if s.deleted {
		return errors.New("account deleted")
	}
	next.Revision = s.state.Revision + 1
	plain, err := json.Marshal(next)
	if err != nil {
		return err
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	data := append([]byte("PSM1"), nonce...)
	data = s.aead.Seal(data, nonce, plain, []byte("private-sms/store/v1"))
	f, err := os.CreateTemp(filepath.Dir(s.path), ".inbox-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), s.path); err != nil {
		return err
	}
	// Rename has committed: keep memory consistent even if directory sync fails.
	s.state = next
	dir, err := os.Open(filepath.Dir(s.path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func (s *Store) copyState(now time.Time) State {
	next := s.state
	next.Records = make([]Record, 0, len(s.state.Records))
	for _, r := range s.state.Records {
		if r.Message.ReceivedAt.After(now.Add(-s.retention)) {
			next.Records = append(next.Records, r)
		}
	}
	return next
}

func newID() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func (s *Store) add(m Message, dedupe string, now time.Time) (Message, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.copyState(now)
	if dedupe != "" {
		for _, r := range next.Records {
			if r.Dedupe == dedupe {
				return r.Message, true, nil
			}
		}
	}
	if len(next.Records) >= 5000 {
		return Message{}, false, errFull
	}
	m.ID, m.ReceivedAt, m.Read = newID(), now.UTC(), false
	next.Sequence++
	next.Records = append(next.Records, Record{Message: m, Sequence: next.Sequence, Dedupe: dedupe, Pending: true, NextAttempt: now})
	if err := s.commit(next); err != nil {
		return Message{}, false, err
	}
	return m, false, nil
}

func (s *Store) list(before uint64, limit int, now time.Time) ([]Message, string) {
	items, cursor, _ := s.snapshot(before, limit, now)
	return items, cursor
}

type BadgeState struct {
	Badge    int    `json:"badge"`
	Revision string `json:"revision"`
}

type InboxPage struct {
	Messages         []Message `json:"messages"`
	NextBefore       string    `json:"next_before"`
	SnapshotSequence string    `json:"snapshot_sequence"`
	BadgeState
}

func (s *Store) badgeStateLocked(now time.Time) BadgeState {
	return BadgeState{Badge: badgeCount(s.state, now, s.retention), Revision: fmt.Sprint(s.state.Revision)}
}
func (s *Store) badgeState(now time.Time) BadgeState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.badgeStateLocked(now)
}
func (s *Store) snapshot(before uint64, limit int, now time.Time) ([]Message, string, uint64) {
	page, sequence := s.inboxPage(before, limit, now)
	return page.Messages, page.NextBefore, sequence
}
func (s *Store) inboxPage(before uint64, limit int, now time.Time) (InboxPage, uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	page := InboxPage{Messages: make([]Message, 0, limit), SnapshotSequence: fmt.Sprint(s.state.Sequence), BadgeState: s.badgeStateLocked(now)}
	var last uint64
	for i := len(s.state.Records) - 1; i >= 0; i-- {
		r := s.state.Records[i]
		if !r.Message.ReceivedAt.After(now.Add(-s.retention)) || (before > 0 && r.Sequence >= before) {
			continue
		}
		if len(page.Messages) == limit {
			page.NextBefore = fmt.Sprint(last)
			break
		}
		page.Messages = append(page.Messages, r.Message)
		last = r.Sequence
	}
	return page, s.state.Sequence
}

// Count retained, individually unread messages across every page.
func (s *Store) badge(now time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return badgeCount(s.state, now, s.retention)
}

func badgeCount(state State, now time.Time, retention time.Duration) int {
	count := 0
	for _, r := range state.Records {
		if !r.Message.Read && r.Message.ReceivedAt.After(now.Add(-retention)) {
			count++
		}
	}
	return count
}

var errInvalidSeen = errors.New("invalid seen sequence")

// Compatibility for build 5: refreshing its inbox no longer marks everything read.
func (s *Store) acknowledge(through uint64, now time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if through > s.state.Sequence {
		return 0, errInvalidSeen
	}
	return badgeCount(s.state, now, s.retention), nil
}

var errMessageNotFound = errors.New("message not found")

func (s *Store) markRead(id string, now time.Time) (BadgeState, error) {
	s.pushMu.Lock()
	defer s.pushMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.copyState(now)
	for i := range next.Records {
		if next.Records[i].Message.ID != id {
			continue
		}
		if !next.Records[i].Message.Read {
			next.Records[i].Message.Read = true
			next.Records[i].Pending = false
			if err := s.commit(next); err != nil {
				return BadgeState{}, err
			}
		}
		return s.badgeStateLocked(now), nil
	}
	return BadgeState{}, errMessageNotFound
}

func (s *Store) delete(id string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.copyState(now)
	for i, r := range next.Records {
		if r.Message.ID == id {
			next.Records = append(next.Records[:i], next.Records[i+1:]...)
			break
		}
	}
	return s.commit(next)
}

// Clear only inbox contents; keep the device binding and monotonic sequence.
func (s *Store) clearMessages() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.state
	next.Records = nil
	next.LastPushError = ""
	return s.commit(next)
}

func (s *Store) register(token string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.copyState(now)
	if token != next.Device {
		for i := range next.Records {
			if next.Records[i].Pending {
				next.Records[i].NextAttempt = now
				next.Records[i].Attempts = 0
			}
		}
	}
	next.Device, next.LastPushError = token, ""
	return s.commit(next)
}

func (s *Store) due(now time.Time) (Record, string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Device == "" {
		return Record{}, "", false
	}
	for _, r := range s.state.Records {
		if r.Pending && !r.Message.Read && !r.NextAttempt.After(now) && r.Message.ReceivedAt.After(now.Add(-s.retention)) {
			return r, s.state.Device, true
		}
	}
	return Record{}, "", false
}

func (s *Store) pushResult(id, device string, pushErr error, invalidDevice bool, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.copyState(now)
	// A registration during an in-flight request must not invalidate the new token.
	if next.Device != device {
		return nil
	}
	if invalidDevice {
		next.Device = ""
	}
	next.LastPushError = ""
	if pushErr != nil {
		next.LastPushError = "push_failed"
		if invalidDevice {
			next.LastPushError = "device_invalid"
		}
	}
	for i := range next.Records {
		r := &next.Records[i]
		if r.Message.ID != id {
			continue
		}
		if pushErr == nil {
			r.Pending = false
		} else {
			r.Attempts++
			shift := min(r.Attempts, 8)
			r.NextAttempt = now.Add(time.Duration(1<<shift) * 5 * time.Second)
		}
		break
	}
	return s.commit(next)
}

func (s *Store) purge(now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.copyState(now)
	if len(next.Records) == len(s.state.Records) {
		return nil
	}
	return s.commit(next)
}

func (s *Store) status(now time.Time) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	count, pending := 0, 0
	for _, r := range s.state.Records {
		if r.Message.ReceivedAt.After(now.Add(-s.retention)) {
			count++
			if r.Pending {
				pending++
			}
		}
	}
	return map[string]any{"badge": badgeCount(s.state, now, s.retention), "revision": fmt.Sprint(s.state.Revision), "messages": count, "pending_notifications": pending, "device_registered": s.state.Device != "", "last_push_error": s.state.LastPushError}
}
