package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

var errDeviceInUse = errors.New("device belongs to another account")
var accountIDPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

// Metadata contains hashes, never bearer tokens or message content. Labels should
// be aliases, not email addresses. The inbox encryption key is unique per account.
type Account struct {
	ID               string   `json:"id"`
	Label            string   `json:"label,omitempty"`
	IngestHash       [32]byte `json:"ingest_hash,omitempty"`
	ReadHash         [32]byte `json:"read_hash,omitempty"`
	Deleted          bool     `json:"deleted,omitempty"`
	AppleSubject     string   `json:"apple_subject,omitempty"`
	AppleRefresh     string   `json:"apple_refresh,omitempty"`
	SignupInviteHash string   `json:"signup_invite_hash,omitempty"`
}

type Invitation struct {
	AccountID   string `json:"account_id"`
	Label       string `json:"label"`
	Endpoint    string `json:"endpoint"`
	IngestToken string `json:"ingest_token"`
	ReadToken   string `json:"read_token"`
}

func tokenHash(token string) [32]byte { return sha256.Sum256([]byte("Bearer " + token)) }

func accountKey(master []byte, id string) []byte {
	mac := hmac.New(sha256.New, master)
	mac.Write([]byte("private-sms/account-key/v1\x00" + id))
	return mac.Sum(nil)
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func revokeAccount(c Config, id string) error {
	if !accountIDPattern.MatchString(id) {
		return errors.New("invalid account ID")
	}
	raw, err := os.ReadFile(filepath.Join(c.DataDir, "accounts", id+".json"))
	if err != nil {
		return err
	}
	var meta Account
	if err = json.Unmarshal(raw, &meta); err != nil {
		return err
	}
	if meta.AppleRefresh != "" {
		provider, err := newAppleIdentity(c)
		if err != nil {
			return err
		}
		if provider == nil {
			return errors.New("Apple sign in not configured")
		}
		auth := &Accounts{config: c}
		refresh, err := auth.openAppleRefresh(meta)
		if err != nil {
			return err
		}
		if err = provider.revoke(context.Background(), refresh); err != nil {
			return err
		}
	}
	if err := writeAccount(c, Account{ID: id, Deleted: true, SignupInviteHash: meta.SignupInviteHash}); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(c.DataDir, "inboxes", id+".enc")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func writeAccount(c Config, account Account) error {
	if !accountIDPattern.MatchString(account.ID) {
		return errors.New("invalid account ID")
	}
	dir := filepath.Join(c.DataDir, "accounts")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	b, err := json.Marshal(account)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".account-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err != nil {
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
	if err = os.Rename(f.Name(), filepath.Join(dir, account.ID+".json")); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func createAccount(c Config, label, endpoint, out string) (Invitation, error) {
	if strings.TrimSpace(label) == "" || len(label) > 100 || out == "" {
		return Invitation{}, errors.New("provide a short alias and -out invitation path")
	}
	i := Invitation{AccountID: randomHex(16), Label: label, Endpoint: endpoint, IngestToken: randomHex(32), ReadToken: randomHex(32)}
	if err := os.MkdirAll(filepath.Dir(out), 0700); err != nil {
		return Invitation{}, err
	}
	f, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return Invitation{}, err
	}
	b, _ := json.MarshalIndent(i, "", "  ")
	if _, err = f.Write(b); err != nil {
		f.Close()
		return Invitation{}, err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return Invitation{}, err
	}
	if err = f.Close(); err != nil {
		return Invitation{}, err
	}
	a := Account{ID: i.AccountID, Label: label, IngestHash: tokenHash(i.IngestToken), ReadHash: tokenHash(i.ReadToken)}
	if err = writeAccount(c, a); err != nil {
		return Invitation{}, err
	}
	return i, nil
}

type tenant struct {
	account Account
	api     *API
	handler http.Handler
	cancel  context.CancelFunc
}
type Accounts struct {
	shares       *Shares
	mu           sync.Mutex
	config       Config
	apns         *APNS
	ctx          context.Context
	tenants      map[string]*tenant
	workers      sync.WaitGroup
	android      map[string]androidDevice
	pairings     map[[32]byte]androidPairing
	pairWindow   time.Time
	pairAttempts int
	apple        appleIdentityProvider
	challenges   map[string]authChallenge
	authWindow   time.Time
	authAttempts int
}

func newAccounts(ctx context.Context, c Config, apns *APNS) (*Accounts, error) {
	a := &Accounts{config: c, apns: apns, ctx: ctx, tenants: map[string]*tenant{}}
	a.challenges = map[string]authChallenge{}
	var appleErr error
	a.apple, appleErr = newAppleIdentity(c)
	if appleErr != nil {
		return nil, appleErr
	}
	var shareErr error
	a.shares, shareErr = openShares(c.DataDir)
	if shareErr != nil {
		return nil, shareErr
	}
	if err := a.loadAndroid(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(c.DataDir, "accounts"), 0700); err != nil {
		return nil, err
	}
	a.mu.Lock()
	err := a.refreshLocked()
	a.mu.Unlock()
	if err != nil {
		return nil, err
	}
	a.workers.Add(1)
	go func() {
		defer a.workers.Done()
		tick := time.NewTicker(time.Minute)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-tick.C:
				_ = a.shares.cleanup(now)
			}
		}
	}()
	return a, nil
}

// Refresh before authorization so invitations and deletion take effect without a
// restart. This simple implementation targets a small invited TestFlight cohort.
func (a *Accounts) refreshLocked() error {
	files, err := os.ReadDir(filepath.Join(a.config.DataDir, "accounts"))
	if err != nil {
		return err
	}
	active := map[string]Account{}
	for _, f := range files {
		if !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(a.config.DataDir, "accounts", f.Name()))
		if err != nil {
			return err
		}
		var meta Account
		if json.Unmarshal(b, &meta) != nil || !accountIDPattern.MatchString(meta.ID) || meta.ID+".json" != f.Name() {
			return errors.New("invalid account metadata")
		}
		if !meta.Deleted {
			active[meta.ID] = meta
		}
	}
	for id, t := range a.tenants {
		if _, ok := active[id]; !ok {
			t.cancel()
			t.api.store.mu.Lock()
			t.api.store.deleted = true
			t.api.store.state = State{Version: 1}
			t.api.store.mu.Unlock()
			delete(a.tenants, id)
		}
	}
	// Tombstones make credentials unusable even if a previous disk unlink failed.
	for _, f := range files {
		id := strings.TrimSuffix(f.Name(), ".json")
		if !accountIDPattern.MatchString(id) {
			continue
		}
		if _, ok := active[id]; !ok {
			if err := os.Remove(filepath.Join(a.config.DataDir, "inboxes", id+".enc")); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	for id, meta := range active {
		if existing, ok := a.tenants[id]; ok {
			if existing.account == meta {
				continue
			}
			// Credential replacement is performed only after a restart. Account
			// deletion always takes effect immediately; issue a new account to rotate.
			return errors.New("account metadata changed; restart required")
		}
		s, err := openStore(filepath.Join(a.config.DataDir, "inboxes", id+".enc"), accountKey(a.config.StorageKey, id), a.config.Retention)
		if err != nil {
			return err
		}
		if err = s.purge(time.Now()); err != nil {
			return err
		}
		api := &API{store: s, config: a.config, pushEnabled: a.apns != nil, ingestHash: meta.IngestHash, readHash: meta.ReadHash}
		api.registerDevice = func(token string) error { return a.registerLocked(id, token) }
		api.deleteAccount = func() error { return a.removeLocked(id) }
		ctx, cancel := context.WithCancel(a.ctx)
		a.tenants[id] = &tenant{account: meta, api: api, handler: api.handler(), cancel: cancel}
		a.workers.Add(1)
		go func() { defer a.workers.Done(); worker(ctx, s, a.apns) }()
	}
	return nil
}

func (a *Accounts) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if a.shares != nil && a.shares.handle(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	if a.authRequest(w, r) {
		return
	}
	if r.Method == "GET" && r.URL.Path == "/download/android.apk" {
		w.Header().Set("Content-Type", "application/vnd.android.package-archive")
		w.Header().Set("Content-Disposition", `attachment; filename="private-sms-android.apk"`)
		http.ServeFile(w, r, filepath.Join(a.config.DataDir, "downloads", "private-sms-android.apk"))
		return
	}
	if a.androidRequest(w, r) {
		return
	}
	if r.Method == "GET" && r.URL.Path == "/healthz" {
		reply(w, 200, map[string]bool{"ok": true})
		return
	}
	if len(r.Header.Get("Authorization")) != 71 {
		fail(w, 401, "unauthorized")
		return
	}
	hash := sha256.Sum256([]byte(r.Header.Get("Authorization")))
	a.mu.Lock()
	if err := a.refreshLocked(); err != nil {
		a.mu.Unlock()
		fail(w, 503, "account service unavailable")
		return
	}
	var handler http.Handler
	for _, t := range a.tenants {
		if hash == t.account.IngestHash || hash == t.account.ReadHash {
			handler = t.handler
			break
		}
	}
	// Serialize authorization and execution with session rotation/deletion. A
	// request using the old read token cannot rebind a receiver after a new login.
	defer a.mu.Unlock()
	if handler == nil {
		fail(w, 401, "unauthorized")
		return
	}
	handler.ServeHTTP(w, r)
}

func (a *Accounts) registerLocked(id, token string) error {
	current, ok := a.tenants[id]
	if !ok {
		return errors.New("account inactive")
	}
	for otherID, t := range a.tenants {
		if otherID == id {
			continue
		}
		t.api.store.mu.Lock()
		same := t.api.store.state.Device == token
		t.api.store.mu.Unlock()
		if same {
			return errDeviceInUse
		}
	}
	return current.api.store.register(token, time.Now())
}

func (s *Store) unregister(token string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Device != token {
		return nil
	}
	next := s.copyState(now)
	next.Device = ""
	next.LastPushError = ""
	return s.commit(next)
}

func (a *Accounts) removeLocked(id string) error {
	t, ok := a.tenants[id]
	if !ok {
		return nil
	}
	if t.account.AppleRefresh != "" {
		if a.apple == nil {
			return errors.New("Apple sign in not configured")
		}
		refresh, err := a.openAppleRefresh(t.account)
		if err != nil {
			return err
		}
		if err = a.apple.revoke(a.ctx, refresh); err != nil {
			return err
		}
	}
	// Serialize against all in-flight store mutations; reject subsequent mutations
	// so a request authorized just before deletion cannot recreate an inbox.
	t.api.store.mu.Lock()
	defer t.api.store.mu.Unlock()
	if err := writeAccount(a.config, Account{ID: id, Deleted: true, SignupInviteHash: t.account.SignupInviteHash}); err != nil {
		return err
	}
	t.api.store.deleted = true
	t.api.store.state = State{Version: 1}
	t.cancel()
	delete(a.tenants, id)
	if err := os.Remove(t.api.store.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func lockDataDirectory(dir string) (*os.File, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, ".server.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("data directory already in use by another server")
	}
	return f, nil
}
