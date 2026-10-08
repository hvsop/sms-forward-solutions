package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type API struct {
	store                *Store
	config               Config
	pushEnabled          bool
	ingestHash, readHash [32]byte
	registerDevice       func(string) error
	deleteAccount        func() error
}

func reply(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func fail(w http.ResponseWriter, status int, message string) {
	reply(w, status, map[string]string{"error": message})
}

func (a *API) auth(want [32]byte, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := sha256.Sum256([]byte(r.Header.Get("Authorization")))
		if subtle.ConstantTimeCompare(want[:], got[:]) != 1 {
			fail(w, 401, "unauthorized")
			return
		}
		next(w, r)
	}
}

func (a *API) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { reply(w, 200, map[string]bool{"ok": true}) })
	mux.HandleFunc("POST /v1/ingest", a.auth(a.ingestHash, a.ingest))
	mux.HandleFunc("GET /v1/messages", a.auth(a.readHash, a.list))
	mux.HandleFunc("POST /v1/messages/seen", a.auth(a.readHash, a.seen))
	mux.HandleFunc("PUT /v1/messages/{id}/read", a.auth(a.readHash, a.markRead))
	mux.HandleFunc("DELETE /v1/messages", a.auth(a.readHash, a.clearMessages))
	mux.HandleFunc("DELETE /v1/messages/{id}", a.auth(a.readHash, a.remove))
	mux.HandleFunc("PUT /v1/device", a.auth(a.readHash, a.register))
	mux.HandleFunc("DELETE /v1/device", a.auth(a.readHash, a.unregister))
	mux.HandleFunc("DELETE /v1/account", a.auth(a.readHash, a.deleteUser))
	mux.HandleFunc("GET /v1/status", a.auth(a.readHash, func(w http.ResponseWriter, r *http.Request) {
		status := a.store.status(time.Now())
		status["apns_configured"] = a.pushEnabled
		reply(w, 200, status)
	}))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
		mux.ServeHTTP(w, r)
	})
}

func decodeJSON(r *http.Request, value any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(value); err != nil {
		return err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return errors.New("expected one JSON object")
	}
	return nil
}

func (a *API) ingest(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Sender     string `json:"sender"`
		Content    string `json:"content"`
		SIM        string `json:"sim"`
		SourceTime string `json:"source_time"`
	}
	media, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	switch media {
	case "application/json":
		if decodeJSON(r, &input) != nil {
			fail(w, 400, "invalid JSON or body too large")
			return
		}
	case "application/x-www-form-urlencoded":
		if r.ParseForm() != nil {
			fail(w, 400, "invalid form or body too large")
			return
		}
		input.Sender = r.PostForm.Get("sender")
		input.Content = r.PostForm.Get("content")
		input.SIM = r.PostForm.Get("sim")
		input.SourceTime = r.PostForm.Get("source_time")
	default:
		fail(w, 415, "use JSON or form encoding")
		return
	}
	if strings.TrimSpace(input.Sender) == "" || strings.TrimSpace(input.Content) == "" || len(input.Sender) > 256 || len(input.Content) > 16<<10 || len(input.SIM) > 256 || len(input.SourceTime) > 100 {
		fail(w, 400, "sender and content required; field size limit exceeded")
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if len(key) > 256 {
		fail(w, 400, "idempotency key too long")
		return
	}
	var dedupe string
	if key != "" {
		sum := sha256.Sum256([]byte("key:" + key))
		dedupe = hex.EncodeToString(sum[:])
	} else if input.SourceTime != "" {
		b, _ := json.Marshal(input)
		sum := sha256.Sum256(b)
		dedupe = hex.EncodeToString(sum[:])
	}
	m, duplicate, err := a.store.add(Message{Sender: input.Sender, Content: input.Content, SIM: input.SIM, SourceTime: input.SourceTime}, dedupe, time.Now())
	if errors.Is(err, errFull) {
		fail(w, 507, "inbox full; delete messages or reduce retention")
		return
	}
	if err != nil {
		fail(w, 503, "storage unavailable; retry later")
		return
	}
	status := 201
	if duplicate {
		status = 200
	}
	reply(w, status, map[string]any{"ok": true, "id": m.ID, "duplicate": duplicate})
}

func (a *API) list(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		n, e := strconv.Atoi(v)
		if e != nil || n < 1 || n > 100 {
			fail(w, 400, "limit must be 1..100")
			return
		}
		limit = n
	}
	var before uint64
	if v := r.URL.Query().Get("before"); v != "" {
		n, e := strconv.ParseUint(v, 10, 64)
		if e != nil || n == 0 {
			fail(w, 400, "invalid cursor")
			return
		}
		before = n
	}
	page, _ := a.store.inboxPage(before, limit, time.Now())
	reply(w, 200, page)
}

func (a *API) seen(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Through string `json:"through"`
	}
	if decodeJSON(r, &input) != nil {
		fail(w, 400, "invalid JSON")
		return
	}
	through, err := strconv.ParseUint(input.Through, 10, 64)
	if err != nil {
		fail(w, 400, "invalid sequence")
		return
	}
	badge, err := a.store.acknowledge(through, time.Now())
	if errors.Is(err, errInvalidSeen) {
		fail(w, 400, "invalid sequence")
		return
	}
	if err != nil {
		fail(w, 503, "storage unavailable")
		return
	}
	reply(w, 200, map[string]int{"badge": badge})
}

func (a *API) markRead(w http.ResponseWriter, r *http.Request) {
	state, err := a.store.markRead(r.PathValue("id"), time.Now())
	if errors.Is(err, errMessageNotFound) {
		fail(w, 404, "message not found")
		return
	}
	if err != nil {
		fail(w, 503, "storage unavailable")
		return
	}
	reply(w, 200, state)
}

func (a *API) remove(w http.ResponseWriter, r *http.Request) {
	if err := a.store.delete(r.PathValue("id"), time.Now()); err != nil {
		fail(w, 503, "storage unavailable")
		return
	}
	reply(w, 200, a.store.badgeState(time.Now()))
}

func (a *API) clearMessages(w http.ResponseWriter, r *http.Request) {
	if err := a.store.clearMessages(); err != nil {
		fail(w, 503, "storage unavailable")
		return
	}
	reply(w, 200, a.store.badgeState(time.Now()))
}

func (a *API) register(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Token       string `json:"token"`
		Environment string `json:"environment"`
	}
	if decodeJSON(r, &input) != nil {
		fail(w, 400, "invalid JSON")
		return
	}
	b, err := hex.DecodeString(input.Token)
	if err != nil || len(b) < 16 || len(b) > 256 {
		fail(w, 400, "invalid device token")
		return
	}
	if input.Environment != a.config.APNSEnvironment {
		fail(w, 409, "APNs environment mismatch")
		return
	}
	register := a.registerDevice
	if register == nil {
		register = func(token string) error { return a.store.register(token, time.Now()) }
	}
	if err := register(strings.ToLower(input.Token)); err != nil {
		if errors.Is(err, errDeviceInUse) {
			fail(w, 409, "device already belongs to another account; disconnect it first")
			return
		}
		fail(w, 503, "storage unavailable")
		return
	}
	reply(w, 200, map[string]bool{"ok": true, "apns_configured": a.pushEnabled})
}

func (a *API) unregister(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Token string `json:"token"`
	}
	if decodeJSON(r, &input) != nil {
		fail(w, 400, "invalid JSON")
		return
	}
	if err := a.store.unregister(input.Token, time.Now()); err != nil {
		fail(w, 503, "storage unavailable")
		return
	}
	reply(w, 200, map[string]bool{"ok": true})
}

func (a *API) deleteUser(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Confirmation string `json:"confirmation"`
	}
	if decodeJSON(r, &input) != nil || input.Confirmation != "delete-account" {
		fail(w, 400, "explicit account deletion confirmation required; update the app")
		return
	}
	if a.deleteAccount == nil {
		fail(w, 503, "account service unavailable")
		return
	}
	if err := a.deleteAccount(); err != nil {
		fail(w, 503, "account deletion unavailable")
		return
	}
	reply(w, 200, map[string]bool{"ok": true})
}
