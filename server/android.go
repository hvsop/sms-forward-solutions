package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type androidDevice struct {
	Hash     [32]byte  `json:"hash"`
	ID       string    `json:"id"`
	PairedAt time.Time `json:"paired_at,omitempty"`
	LastSeen time.Time `json:"last_seen,omitempty"`
}
type androidPairing struct {
	AccountID string
	Expires   time.Time
}

func (a *Accounts) loadAndroid() error {
	a.android = map[string]androidDevice{}
	a.pairings = map[[32]byte]androidPairing{}
	b, err := os.ReadFile(filepath.Join(a.config.DataDir, "android.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err = json.Unmarshal(b, &a.android); err != nil {
		return err
	}
	if a.android == nil {
		a.android = map[string]androidDevice{}
	}
	return nil
}

// Called under Accounts.mu. Only hashes are persisted; pairing codes expire on restart.
func (a *Accounts) saveAndroid(next map[string]androidDevice) error {
	b, err := json.Marshal(next)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(a.config.DataDir, ".android-*")
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
	if err = os.Rename(f.Name(), filepath.Join(a.config.DataDir, "android.json")); err != nil {
		return err
	}
	a.android = next
	d, err := os.Open(a.config.DataDir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func (a *Accounts) deviceCopy() map[string]androidDevice {
	next := make(map[string]androidDevice, len(a.android))
	for id, device := range a.android {
		next[id] = device
	}
	return next
}

// Intercepts only Android credentials and Android routes; existing clients retain their API.
func (a *Accounts) androidRequest(w http.ResponseWriter, r *http.Request) bool {
	isRoute := strings.HasPrefix(r.URL.Path, "/v1/android/")
	auth := r.Header.Get("Authorization")
	if !isRoute && len(auth) != 71 {
		return false
	}
	hash := sha256.Sum256([]byte(auth))
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.refreshLocked(); err != nil {
		fail(w, 503, "account service unavailable")
		return true
	}
	now := time.Now()
	for h, p := range a.pairings {
		if !now.Before(p.Expires) || a.tenants[p.AccountID] == nil {
			delete(a.pairings, h)
		}
	}
	if r.URL.Path == "/v1/android/pair" && r.Method == "POST" {
		// A global bounded limiter cannot be bypassed by forged proxy/IP headers.
		if now.Sub(a.pairWindow) >= time.Minute {
			a.pairWindow, a.pairAttempts = now, 0
		}
		a.pairAttempts++
		if a.pairAttempts > 30 {
			w.Header().Set("Retry-After", "60")
			fail(w, 429, "try again later")
			return true
		}
		var input struct {
			Code string `json:"code"`
		}
		if decodeJSON(r, &input) != nil {
			fail(w, 400, "invalid JSON")
			return true
		}
		code := normalizeCode(input.Code)
		h := sha256.Sum256([]byte(code))
		p, ok := a.pairings[h]
		if len(code) != 16 || !ok {
			fail(w, 401, "pairing code invalid or expired")
			return true
		}
		token := randomHex(32)
		d := androidDevice{Hash: tokenHash(token), ID: randomHex(16), PairedAt: now}
		next := a.deviceCopy()
		next[p.AccountID] = d
		// One active companion per account; a new pairing replaces its old credential.
		if err := a.saveAndroid(next); err != nil {
			fail(w, 503, "storage unavailable")
			return true
		}
		delete(a.pairings, h)
		reply(w, 201, map[string]any{"token": token, "device_id": d.ID, "label": a.tenants[p.AccountID].account.Label})
		return true
	}
	for id, t := range a.tenants {
		if len(auth) == 71 && hash == t.account.ReadHash {
			switch {
			case r.URL.Path == "/v1/android/pairing" && r.Method == "POST":
				for h, p := range a.pairings {
					if p.AccountID == id {
						delete(a.pairings, h)
					}
				}
				b := make([]byte, 10)
				if _, err := rand.Read(b); err != nil {
					fail(w, 503, "random source unavailable")
					return true
				}
				code := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)
				expires := now.Add(10 * time.Minute)
				a.pairings[sha256.Sum256([]byte(code))] = androidPairing{id, expires}
				reply(w, 201, map[string]any{"code": code[:4] + "-" + code[4:8] + "-" + code[8:12] + "-" + code[12:], "expires_at": expires})
				return true
			case r.URL.Path == "/v1/android/device" && r.Method == "DELETE":
				next := a.deviceCopy()
				delete(next, id)
				if err := a.saveAndroid(next); err != nil {
					fail(w, 503, "storage unavailable")
					return true
				}
				for h, p := range a.pairings {
					if p.AccountID == id {
						delete(a.pairings, h)
					}
				}
				reply(w, 200, map[string]bool{"ok": true})
				return true
			}
		}
		d, ok := a.android[id]
		if !ok || len(auth) != 71 || d.Hash != hash {
			continue
		}
		switch {
		case r.URL.Path == "/v1/ingest" && r.Method == "POST":
			// Last seen means a successful authenticated contact, not SMS permission or uptime.
			if now.Sub(d.LastSeen) >= time.Minute {
				next := a.deviceCopy()
				d.LastSeen = now
				next[id] = d
				if a.saveAndroid(next) != nil {
					fail(w, 503, "storage unavailable")
					return true
				}
			}
			// Holding the account lock serializes credential revocation with ingestion.
			t.api.ingest(w, r)
		case r.URL.Path == "/v1/android/status" && r.Method == "GET":
			if now.Sub(d.LastSeen) >= time.Minute {
				next := a.deviceCopy()
				d.LastSeen = now
				next[id] = d
				if a.saveAndroid(next) != nil {
					fail(w, 503, "storage unavailable")
					return true
				}
			}
			reply(w, 200, map[string]any{"ok": true, "device_id": d.ID})
		case r.URL.Path == "/v1/android/device" && r.Method == "DELETE":
			next := a.deviceCopy()
			delete(next, id)
			if err := a.saveAndroid(next); err != nil {
				fail(w, 503, "storage unavailable")
				return true
			}
			reply(w, 200, map[string]bool{"ok": true})
		default:
			fail(w, 401, "unauthorized")
		}
		return true
	}
	if isRoute {
		fail(w, 401, "unauthorized")
		return true
	}
	return false
}
