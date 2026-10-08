package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const shareChunkSize int64 = 1 << 20
const shareFileLimit int64 = 100 << 20
const shareOwnerLimit int64 = 500 << 20
const shareGlobalLimit int64 = 2 << 30

//go:embed shareweb/*
var shareWeb embed.FS

type shareOwner struct {
	ID    string   `json:"id"`
	Label string   `json:"label"`
	Hash  [32]byte `json:"hash"`
}
type shareFile struct {
	ID       string    `json:"id"`
	Owner    string    `json:"owner"`
	Size     int64     `json:"size"`
	Created  time.Time `json:"created_at"`
	Expires  time.Time `json:"expires_at"`
	ReadHash [32]byte  `json:"read_hash"`
	Nonce    string    `json:"nonce"`
	Manifest string    `json:"manifest"`
	Ready    bool      `json:"ready"`
	Revoked  bool      `json:"revoked"`
}

func (f shareFile) chunks() int { return int(max(1, (f.Size+shareChunkSize-1)/shareChunkSize)) }
func (f shareFile) chunkBytes(n int) int64 {
	return min(shareChunkSize, max(0, f.Size-int64(n)*shareChunkSize)) + 16
}
func (f shareFile) alive(now time.Time) bool {
	return !f.Revoked && now.Before(f.Expires) && (f.Ready || now.Before(f.Created.Add(time.Hour)))
}
func (f shareFile) public() map[string]any {
	return map[string]any{"id": f.ID, "size": f.Size, "chunks": f.chunks(), "chunk_size": shareChunkSize, "created_at": f.Created, "expires_at": f.Expires, "ready": f.Ready, "nonce": f.Nonce, "manifest": f.Manifest}
}

type Shares struct {
	mu    sync.Mutex
	dir   string
	files map[string]shareFile
	slots chan struct{}
}

func openShares(dataDir string) (*Shares, error) {
	s := &Shares{dir: filepath.Join(dataDir, "shares"), files: map[string]shareFile{}, slots: make(chan struct{}, 8)}
	for _, p := range []string{"owners", "files"} {
		if err := os.MkdirAll(filepath.Join(s.dir, p), 0700); err != nil {
			return nil, err
		}
	}
	dirs, err := os.ReadDir(filepath.Join(s.dir, "files"))
	if err != nil {
		return nil, err
	}
	for _, d := range dirs {
		if !d.IsDir() || !accountIDPattern.MatchString(d.Name()) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(s.dir, "files", d.Name(), "meta.json"))
		if errors.Is(err, os.ErrNotExist) {
			if err = os.RemoveAll(filepath.Join(s.dir, "files", d.Name())); err != nil {
				return nil, err
			}
			continue
		}
		if err != nil {
			return nil, err
		}
		var f shareFile
		if json.Unmarshal(b, &f) != nil || f.ID != d.Name() || !accountIDPattern.MatchString(f.Owner) || f.Size < 0 || f.Size > shareFileLimit {
			return nil, errors.New("invalid share metadata")
		}
		s.files[f.ID] = f
	}
	if err = s.cleanup(time.Now()); err != nil {
		return nil, err
	}
	return s, nil
}
func shareAtomic(path string, b []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".share-*")
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
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
func (s *Shares) save(f shareFile) error {
	b, err := json.Marshal(f)
	if err != nil {
		return err
	}
	if err = shareAtomic(filepath.Join(s.dir, "files", f.ID, "meta.json"), b); err != nil {
		return err
	}
	s.files[f.ID] = f
	return nil
}
func (s *Shares) cleanup(now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, f := range s.files {
		if !f.alive(now) {
			if err := os.RemoveAll(filepath.Join(s.dir, "files", id)); err != nil {
				return err
			}
			delete(s.files, id)
		}
	}
	return nil
}
func createShareOwner(c Config, label, endpoint, out string) error {
	if strings.TrimSpace(label) == "" || len(label) > 100 || out == "" {
		return errors.New("provide alias and private -out path")
	}
	if !strings.HasPrefix(endpoint, "https://") {
		return errors.New("share endpoint must use HTTPS")
	}
	dir := filepath.Join(c.DataDir, "shares", "owners")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	token := randomHex(32)
	owner := shareOwner{ID: randomHex(16), Label: label, Hash: tokenHash(token)}
	if err := os.MkdirAll(filepath.Dir(out), 0700); err != nil {
		return err
	}
	invitation := map[string]string{"owner_id": owner.ID, "label": label, "upload_token": token, "activation_url": strings.TrimRight(endpoint, "/") + "/share#access=" + token}
	b, _ := json.MarshalIndent(invitation, "", "  ")
	f, err := os.OpenFile(out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
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
	b, _ = json.Marshal(owner)
	return shareAtomic(filepath.Join(dir, owner.ID+".json"), b)
}
func (s *Shares) owner(auth string) (string, error) {
	if len(auth) != 71 || !strings.HasPrefix(auth, "Bearer ") {
		return "", nil
	}
	files, err := os.ReadDir(filepath.Join(s.dir, "owners"))
	if err != nil {
		return "", err
	}
	got := sha256.Sum256([]byte(auth))
	for _, p := range files {
		if !strings.HasSuffix(p.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(s.dir, "owners", p.Name()))
		if err != nil {
			return "", err
		}
		var o shareOwner
		if json.Unmarshal(b, &o) != nil || !accountIDPattern.MatchString(o.ID) || p.Name() != o.ID+".json" {
			return "", errors.New("invalid share owner")
		}
		if subtle.ConstantTimeCompare(got[:], o.Hash[:]) == 1 {
			return o.ID, nil
		}
	}
	return "", nil
}
func shareHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data: blob:; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
	w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
}
func (s *Shares) handle(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != "/share" && !strings.HasPrefix(r.URL.Path, "/share/") && !strings.HasPrefix(r.URL.Path, "/v1/share/") {
		return false
	}
	shareHeaders(w)
	if r.URL.Path == "/share" || strings.HasPrefix(r.URL.Path, "/share/") {
		name := strings.TrimPrefix(r.URL.Path, "/share/")
		kind := ""
		switch r.URL.Path {
		case "/share", "/share/":
			name = "index.html"
			kind = "text/html; charset=utf-8"
		case "/share/app.js", "/share/crypto.js", "/share/qrcode.js":
			kind = "text/javascript; charset=utf-8"
		case "/share/style.css":
			kind = "text/css; charset=utf-8"
		case "/share/qrcode.LICENSE":
			kind = "text/plain; charset=utf-8"
		default:
			http.NotFound(w, r)
			return true
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			fail(w, 405, "method not allowed")
			return true
		}
		b, err := shareWeb.ReadFile("shareweb/" + name)
		if err != nil {
			fail(w, 503, "page unavailable")
			return true
		}
		w.Header().Set("Content-Type", kind)
		if r.Method == "GET" {
			_, _ = w.Write(b)
		}
		return true
	}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		w.Header().Set("Retry-After", "3")
		fail(w, 503, "busy; retry shortly")
		return true
	}
	// File chunks need more time on mobile links; these deadlines apply only here.
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(time.Now().Add(90 * time.Second))
	_ = controller.SetWriteDeadline(time.Now().Add(90 * time.Second))
	s.mu.Lock()
	owner, err := s.owner(r.Header.Get("Authorization"))
	s.mu.Unlock()
	if err != nil {
		fail(w, 503, "share service unavailable")
		return true
	}
	write := r.Method == "POST" || r.Method == "PUT" || r.Method == "DELETE"
	if write && owner == "" {
		fail(w, 401, "upload access required")
		return true
	}
	limit := int64(8192)
	if strings.Contains(r.URL.Path, "/chunks/") {
		limit = shareChunkSize + 16
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	var body []byte
	if r.Method == "POST" || r.Method == "PUT" {
		body, err = io.ReadAll(r.Body)
		if err != nil {
			fail(w, 413, "body too large or incomplete")
			return true
		}
	}
	s.mu.Lock()
	status, value, raw := s.apiLocked(r, owner, body, time.Now())
	s.mu.Unlock()
	if raw != nil {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(status)
		_, _ = w.Write(raw)
	} else {
		reply(w, status, value)
	}
	return true
}
func shareError(code int, message string) (int, any, []byte) {
	return code, map[string]string{"error": message}, nil
}
func (s *Shares) apiLocked(r *http.Request, owner string, body []byte, now time.Time) (int, any, []byte) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/share/")
	if path == "session" && r.Method == "GET" {
		if owner == "" {
			return shareError(401, "upload access required")
		}
		return 200, map[string]any{"ok": true, "max_file_size": shareFileLimit, "chunk_size": shareChunkSize, "owner_quota": shareOwnerLimit}, nil
	}
	if path == "files" {
		if owner == "" {
			return shareError(401, "upload access required")
		}
		switch r.Method {
		case "GET":
			files := []map[string]any{}
			for _, f := range s.files {
				if f.Owner == owner && f.alive(now) {
					files = append(files, f.public())
				}
			}
			return 200, map[string]any{"files": files}, nil
		case "POST":
			var in struct {
				Size      *int64 `json:"size"`
				ReadToken string `json:"read_token"`
			}
			if json.Unmarshal(body, &in) != nil || in.Size == nil || *in.Size < 0 || *in.Size > shareFileLimit || len(in.ReadToken) != 64 {
				return shareError(400, "invalid file size or access token")
			}
			if _, err := strconv.ParseUint(in.ReadToken[:16], 16, 64); err != nil {
				return shareError(400, "invalid access token")
			}
			for _, c := range in.ReadToken {
				if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
					return shareError(400, "invalid access token")
				}
			}
			var userBytes, total int64
			count, globalCount := 0, 0
			for _, f := range s.files {
				// Count even expired files until physical deletion has freed disk space.
				total += f.Size + int64(f.chunks()*16)
				globalCount++
				if f.Owner == owner {
					userBytes += f.Size
					count++
				}
			}
			if count >= 10 || userBytes+*in.Size > shareOwnerLimit {
				return shareError(409, "your storage quota is full")
			}
			if total+*in.Size+2048 > shareGlobalLimit || globalCount >= 512 {
				return shareError(507, "server storage quota is full")
			}
			f := shareFile{ID: randomHex(16), Owner: owner, Size: *in.Size, Created: now.UTC(), Expires: now.Add(24 * time.Hour).UTC(), ReadHash: tokenHash(in.ReadToken)}
			if err := os.Mkdir(filepath.Join(s.dir, "files", f.ID), 0700); err != nil {
				return shareError(503, "storage unavailable")
			}
			if err := s.save(f); err != nil {
				_ = os.RemoveAll(filepath.Join(s.dir, "files", f.ID))
				return shareError(503, "storage unavailable")
			}
			return 201, f.public(), nil
		default:
			return shareError(405, "method not allowed")
		}
	}
	parts := strings.Split(path, "/")
	if len(parts) < 2 || parts[0] != "files" || !accountIDPattern.MatchString(parts[1]) {
		return shareError(404, "not found")
	}
	f, ok := s.files[parts[1]]
	if !ok || !f.alive(now) {
		return shareError(404, "file missing, expired or revoked")
	}
	isOwner := owner != "" && f.Owner == owner
	got := sha256.Sum256([]byte(r.Header.Get("Authorization")))
	canRead := f.Ready && len(r.Header.Get("Authorization")) == 71 && subtle.ConstantTimeCompare(got[:], f.ReadHash[:]) == 1
	if !isOwner && !canRead {
		return shareError(404, "file missing, expired or revoked")
	}
	if r.Method != "GET" && !isOwner {
		return shareError(403, "owner access required")
	}
	if len(parts) == 2 {
		switch r.Method {
		case "GET":
			return 200, f.public(), nil
		case "DELETE":
			f.Revoked = true
			if err := s.save(f); err != nil {
				return shareError(503, "revocation unavailable")
			}
			if err := os.RemoveAll(filepath.Join(s.dir, "files", f.ID)); err == nil {
				delete(s.files, f.ID)
			}
			return 200, map[string]bool{"ok": true}, nil
		}
	}
	if len(parts) == 3 && parts[2] == "manifest" && r.Method == "PUT" {
		if f.Ready {
			return shareError(409, "file is finalized")
		}
		var in struct {
			Nonce    string `json:"nonce"`
			Manifest string `json:"manifest"`
		}
		if json.Unmarshal(body, &in) != nil {
			return shareError(400, "invalid metadata")
		}
		nonce, e1 := base64.RawURLEncoding.DecodeString(in.Nonce)
		manifest, e2 := base64.RawURLEncoding.DecodeString(in.Manifest)
		if e1 != nil || len(nonce) != 8 || e2 != nil || len(manifest) < 16 || len(manifest) > 4096 {
			return shareError(400, "invalid encrypted metadata")
		}
		if f.Manifest != "" && (f.Manifest != in.Manifest || f.Nonce != in.Nonce) {
			return shareError(409, "metadata cannot be replaced")
		}
		f.Manifest = in.Manifest
		f.Nonce = in.Nonce
		if err := s.save(f); err != nil {
			return shareError(503, "storage unavailable")
		}
		return 200, map[string]bool{"ok": true}, nil
	}
	if len(parts) == 3 && parts[2] == "complete" && r.Method == "POST" {
		if f.Manifest == "" {
			return shareError(409, "metadata missing")
		}
		for n := 0; n < f.chunks(); n++ {
			stat, err := os.Stat(filepath.Join(s.dir, "files", f.ID, strconv.Itoa(n)+".enc"))
			if err != nil || stat.Size() != f.chunkBytes(n) {
				return shareError(409, "upload incomplete")
			}
		}
		f.Ready = true
		if err := s.save(f); err != nil {
			return shareError(503, "storage unavailable")
		}
		return 200, f.public(), nil
	}
	if len(parts) == 4 && parts[2] == "chunks" {
		n, err := strconv.Atoi(parts[3])
		if err != nil || n < 0 || n >= f.chunks() || strconv.Itoa(n) != parts[3] {
			return shareError(404, "chunk missing")
		}
		target := filepath.Join(s.dir, "files", f.ID, strconv.Itoa(n)+".enc")
		switch r.Method {
		case "PUT":
			if f.Ready {
				return shareError(409, "file is finalized")
			}
			if int64(len(body)) != f.chunkBytes(n) {
				return shareError(400, "incorrect encrypted chunk length")
			}
			old, err := os.ReadFile(target)
			if err == nil {
				if sha256.Sum256(old) != sha256.Sum256(body) {
					return shareError(409, "chunk cannot be replaced")
				}
				return 200, map[string]bool{"ok": true}, nil
			}
			if !errors.Is(err, os.ErrNotExist) {
				return shareError(503, "storage unavailable")
			}
			if err = shareAtomic(target, body); err != nil {
				return shareError(503, "storage unavailable")
			}
			return 201, map[string]bool{"ok": true}, nil
		case "GET":
			if !f.Ready {
				return shareError(409, "upload incomplete")
			}
			b, err := os.ReadFile(target)
			if err != nil || int64(len(b)) != f.chunkBytes(n) {
				return shareError(503, "chunk unavailable")
			}
			return 200, nil, b
		}
	}
	return shareError(405, "method not allowed")
}
