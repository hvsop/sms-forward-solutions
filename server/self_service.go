package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type authChallenge struct {
	Nonce   string
	Expires time.Time
}
type signupInvite struct {
	Expires time.Time `json:"expires_at"`
}

func normalizeCode(s string) string {
	return strings.ToUpper(strings.Join(strings.Fields(strings.ReplaceAll(s, "-", "")), ""))
}
func codeHash(s string) string {
	sum := sha256.Sum256([]byte(normalizeCode(s)))
	return hex.EncodeToString(sum[:])
}
func newHumanCode() string {
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	s := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)
	return s[:4] + "-" + s[4:8] + "-" + s[8:12] + "-" + s[12:]
}

// An invite is consumed by the account metadata write, including tombstones.
// Thus a crash cannot grant two accounts from the same invitation.
func createSignupInvite(c Config, out string) error {
	if out == "" {
		return errors.New("provide -out for the private signup invitation")
	}
	code := newHumanCode()
	invitation := signupInvite{Expires: time.Now().Add(7 * 24 * time.Hour)}
	dir := filepath.Join(c.DataDir, "signup-invites")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data, _ := json.Marshal(invitation)
	if err := os.WriteFile(filepath.Join(dir, codeHash(code)+".json"), data, 0600); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(out), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	b, _ := json.MarshalIndent(map[string]any{"signup_code": code, "expires_at": invitation.Expires}, "", "  ")
	if _, err = f.Write(b); err != nil {
		return err
	}
	return f.Sync()
}
func (a *Accounts) validSignupInviteLocked(code string) (string, bool) {
	if len(normalizeCode(code)) != 16 {
		return "", false
	}
	h := codeHash(code)
	b, err := os.ReadFile(filepath.Join(a.config.DataDir, "signup-invites", h+".json"))
	if err != nil {
		return "", false
	}
	var invite signupInvite
	if json.Unmarshal(b, &invite) != nil || !time.Now().Before(invite.Expires) {
		return "", false
	}
	files, err := os.ReadDir(filepath.Join(a.config.DataDir, "accounts"))
	if err != nil {
		return "", false
	}
	for _, f := range files {
		if !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(a.config.DataDir, "accounts", f.Name()))
		if err != nil {
			return "", false
		}
		var meta Account
		if json.Unmarshal(b, &meta) != nil {
			return "", false
		}
		if meta.SignupInviteHash == h {
			return "", false
		}
	}
	return h, true
}
func (a *Accounts) appleSubject(subject string) string {
	mac := hmac.New(sha256.New, a.config.StorageKey)
	mac.Write([]byte("private-sms/apple-subject/v1\x00" + subject))
	return hex.EncodeToString(mac.Sum(nil))
}
func (a *Accounts) sealAppleRefresh(id, token string) (string, error) {
	block, err := aes.NewCipher(accountKey(a.config.StorageKey, id))
	if err != nil {
		return "", err
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.RawStdEncoding.EncodeToString(g.Seal(nonce, nonce, []byte(token), []byte("apple-refresh/v1"))), nil
}
func (a *Accounts) openAppleRefresh(meta Account) (string, error) {
	b, err := base64.RawStdEncoding.DecodeString(meta.AppleRefresh)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(accountKey(a.config.StorageKey, meta.ID))
	if err != nil {
		return "", err
	}
	g, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(b) < g.NonceSize() {
		return "", errors.New("invalid refresh record")
	}
	plain, err := g.Open(nil, b[:g.NonceSize()], b[g.NonceSize():], []byte("apple-refresh/v1"))
	return string(plain), err
}
func (a *Accounts) readerLocked(auth string) *tenant {
	if len(auth) != 71 {
		return nil
	}
	sum := sha256.Sum256([]byte(auth))
	for _, t := range a.tenants {
		if t.account.ReadHash == sum {
			return t
		}
	}
	return nil
}

// Called only under Accounts.mu. Existing requests retain an immutable API copy.
func (a *Accounts) updateAccountLocked(t *tenant, meta Account) error {
	if err := writeAccount(a.config, meta); err != nil {
		return err
	}
	api := *t.api
	api.readHash = meta.ReadHash
	api.ingestHash = meta.IngestHash
	t.account = meta
	t.api = &api
	t.handler = api.handler()
	return nil
}
func (a *Accounts) resetReceiverLocked(t *tenant) error {
	s := t.api.store
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.copyState(time.Now())
	next.Device = ""
	next.LastPushError = ""
	return s.commit(next)
}
func authFailure(w http.ResponseWriter, status int, code string) {
	reply(w, status, map[string]string{"error": code})
}

func (a *Accounts) authRequest(w http.ResponseWriter, r *http.Request) bool {
	path := r.URL.Path
	if path != "/v1/account/profile" && path != "/v1/account/logout" && !strings.HasPrefix(path, "/v1/auth/") {
		return false
	}
	if path == "/v1/account/profile" || path == "/v1/account/logout" {
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.refreshLocked() != nil {
			authFailure(w, 503, "storage_unavailable")
			return true
		}
		t := a.readerLocked(r.Header.Get("Authorization"))
		if t == nil {
			authFailure(w, 401, "unauthorized")
			return true
		}
		if path == "/v1/account/profile" && r.Method == "GET" {
			d, paired := a.android[t.account.ID]
			reply(w, 200, map[string]any{"apple_linked": t.account.AppleSubject != "", "android_paired": paired, "android_device_id": d.ID, "android_paired_at": d.PairedAt, "android_last_seen": d.LastSeen, "apple_available": a.apple != nil})
			return true
		}
		if path == "/v1/account/logout" && r.Method == "POST" {
			if t.account.AppleSubject == "" {
				authFailure(w, 409, "apple_link_required")
				return true
			}
			if a.resetReceiverLocked(t) != nil {
				authFailure(w, 503, "storage_unavailable")
				return true
			}
			meta := t.account
			meta.ReadHash = tokenHash(randomHex(32))
			if a.updateAccountLocked(t, meta) != nil {
				authFailure(w, 503, "storage_unavailable")
				return true
			}
			reply(w, 200, map[string]bool{"ok": true})
			return true
		}
		authFailure(w, 405, "method_not_allowed")
		return true
	}
	if a.apple == nil {
		authFailure(w, 503, "apple_not_configured")
		return true
	}
	if r.Method != "POST" {
		authFailure(w, 405, "method_not_allowed")
		return true
	}
	a.mu.Lock()
	now := time.Now()
	if now.Sub(a.authWindow) >= time.Minute {
		a.authWindow = now
		a.authAttempts = 0
	}
	a.authAttempts++
	if a.authAttempts > 60 {
		a.mu.Unlock()
		w.Header().Set("Retry-After", "60")
		authFailure(w, 429, "try_later")
		return true
	}
	for id, c := range a.challenges {
		if !now.Before(c.Expires) {
			delete(a.challenges, id)
		}
	}
	if path == "/v1/auth/challenge" {
		if len(a.challenges) >= 256 {
			a.mu.Unlock()
			authFailure(w, 429, "try_later")
			return true
		}
		id, nonce := randomHex(16), randomHex(32)
		expires := now.Add(5 * time.Minute)
		a.challenges[id] = authChallenge{nonce, expires}
		a.mu.Unlock()
		reply(w, 201, map[string]any{"challenge_id": id, "nonce": nonce, "expires_at": expires})
		return true
	}
	if path != "/v1/auth/apple" {
		a.mu.Unlock()
		authFailure(w, 404, "not_found")
		return true
	}
	var input struct {
		ChallengeID       string `json:"challenge_id"`
		IdentityToken     string `json:"identity_token"`
		AuthorizationCode string `json:"authorization_code"`
		InviteCode        string `json:"invite_code"`
	}
	if decodeJSON(r, &input) != nil || len(input.IdentityToken) > 16384 || len(input.AuthorizationCode) > 4096 || input.AuthorizationCode == "" || len(input.InviteCode) > 128 {
		a.mu.Unlock()
		authFailure(w, 400, "invalid_request")
		return true
	}
	challenge, ok := a.challenges[input.ChallengeID]
	delete(a.challenges, input.ChallengeID)
	a.mu.Unlock()
	if !ok || !time.Now().Before(challenge.Expires) {
		authFailure(w, 401, "challenge_expired")
		return true
	}
	subject, refresh, err := a.apple.exchange(r.Context(), input.IdentityToken, input.AuthorizationCode, challenge.Nonce)
	if err != nil {
		authFailure(w, 401, "apple_identity_invalid")
		return true
	}
	subjectHash := a.appleSubject(subject)
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.refreshLocked() != nil {
		authFailure(w, 503, "storage_unavailable")
		return true
	}
	var existing *tenant
	for _, t := range a.tenants {
		if t.account.AppleSubject == subjectHash {
			existing = t
			break
		}
	}
	auth := r.Header.Get("Authorization")
	if auth != "" { // Linking never silently switches to another user's inbox.
		target := a.readerLocked(auth)
		if target == nil {
			authFailure(w, 401, "unauthorized")
			return true
		}
		if (existing != nil && existing != target) || (target.account.AppleSubject != "" && target.account.AppleSubject != subjectHash) {
			authFailure(w, 409, "apple_account_conflict")
			return true
		}
		meta := target.account
		meta.AppleSubject = subjectHash
		meta.AppleRefresh, err = a.sealAppleRefresh(meta.ID, refresh)
		if err != nil || a.updateAccountLocked(target, meta) != nil {
			authFailure(w, 503, "storage_unavailable")
			return true
		}
		reply(w, 200, map[string]any{"read_token": strings.TrimPrefix(auth, "Bearer "), "apple_linked": true})
		return true
	}
	token := randomHex(32)
	if existing != nil {
		meta := existing.account
		meta.ReadHash = tokenHash(token)
		meta.AppleRefresh, err = a.sealAppleRefresh(meta.ID, refresh)
		if err != nil || a.resetReceiverLocked(existing) != nil || a.updateAccountLocked(existing, meta) != nil {
			authFailure(w, 503, "storage_unavailable")
			return true
		}
	} else {
		h, valid := a.validSignupInviteLocked(input.InviteCode)
		if !valid {
			authFailure(w, 403, "invite_required")
			return true
		}
		meta := Account{ID: randomHex(16), Label: "我的收件箱", ReadHash: tokenHash(token), IngestHash: tokenHash(randomHex(32)), AppleSubject: subjectHash, SignupInviteHash: h}
		meta.AppleRefresh, err = a.sealAppleRefresh(meta.ID, refresh)
		if err != nil || writeAccount(a.config, meta) != nil || a.refreshLocked() != nil {
			authFailure(w, 503, "storage_unavailable")
			return true
		}
	}
	reply(w, 200, map[string]any{"read_token": token, "apple_linked": true})
	return true
}
