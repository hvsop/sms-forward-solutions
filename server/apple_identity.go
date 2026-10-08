package main

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

var errAppleIdentity = errors.New("Apple identity could not be verified")

type appleIdentityProvider interface {
	exchange(context.Context, string, string, string) (string, string, error)
	revoke(context.Context, string) error
}

type appleIdentity struct {
	clientID, teamID, keyID string
	key                     *ecdsa.PrivateKey
	client                  *http.Client
	mu                      sync.Mutex
	keys                    map[string]*rsa.PublicKey
	keysAt                  time.Time
}

func newAppleIdentity(c Config) (appleIdentityProvider, error) {
	if c.AppleClientID == "" && c.AppleTeamID == "" && c.AppleKeyID == "" && c.AppleKeyFile == "" {
		return nil, nil
	}
	if c.AppleClientID == "" || c.AppleTeamID == "" || c.AppleKeyID == "" || c.AppleKeyFile == "" {
		return nil, errors.New("complete APPLE_CLIENT_ID/TEAM_ID/KEY_ID/KEY_FILE")
	}
	data, err := os.ReadFile(c.AppleKeyFile)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("invalid Apple sign-in key")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok || key.Curve != elliptic.P256() {
		return nil, errors.New("Apple sign-in key must be P-256")
	}
	return &appleIdentity{clientID: c.AppleClientID, teamID: c.AppleTeamID, keyID: c.AppleKeyID, key: key,
		client: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (p *appleIdentity) clientSecret() (string, error) {
	now := time.Now()
	head, _ := json.Marshal(map[string]string{"alg": "ES256", "kid": p.keyID})
	body, _ := json.Marshal(map[string]any{"iss": p.teamID, "sub": p.clientID, "aud": "https://appleid.apple.com", "iat": now.Unix(), "exp": now.Add(5 * time.Minute).Unix()})
	enc := base64.RawURLEncoding.EncodeToString
	unsigned := enc(head) + "." + enc(body)
	sum := sha256.Sum256([]byte(unsigned))
	r, s, err := ecdsa.Sign(rand.Reader, p.key, sum[:])
	if err != nil {
		return "", err
	}
	signature := make([]byte, 64)
	r.FillBytes(signature[:32])
	s.FillBytes(signature[32:])
	return unsigned + "." + enc(signature), nil
}

// Only Apple's fixed HTTPS endpoints are used; token contents cannot choose URLs.
func (p *appleIdentity) post(ctx context.Context, path string, values url.Values, out any) error {
	secret, err := p.clientSecret()
	if err != nil {
		return err
	}
	values.Set("client_id", p.clientID)
	values.Set("client_secret", secret)
	req, err := http.NewRequestWithContext(ctx, "POST", "https://appleid.apple.com/auth/"+path, strings.NewReader(values.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := p.client.Do(req)
	if err != nil {
		return errAppleIdentity
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return errAppleIdentity
	}
	if out == nil {
		_, err = io.Copy(io.Discard, io.LimitReader(res.Body, 65536))
		return err
	}
	return json.NewDecoder(io.LimitReader(res.Body, 65536)).Decode(out)
}

func (p *appleIdentity) exchange(ctx context.Context, identityToken, code, nonce string) (string, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	subject, err := p.verify(ctx, identityToken, nonce)
	if err != nil {
		return "", "", err
	}
	var result struct {
		IDToken      string `json:"id_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err = p.post(ctx, "token", url.Values{"grant_type": {"authorization_code"}, "code": {code}}, &result); err != nil {
		return "", "", err
	}
	// The code and identity token must belong to the same Apple authorization.
	other, err := p.verify(ctx, result.IDToken, nonce)
	if err != nil || other != subject || result.RefreshToken == "" || len(result.RefreshToken) > 8192 {
		return "", "", errAppleIdentity
	}
	return subject, result.RefreshToken, nil
}
func (p *appleIdentity) revoke(ctx context.Context, token string) error {
	return p.post(ctx, "revoke", url.Values{"token": {token}, "token_type_hint": {"refresh_token"}}, nil)
}

func (p *appleIdentity) publicKey(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if time.Since(p.keysAt) < time.Hour {
		if key := p.keys[kid]; key != nil {
			return key, nil
		}
	}
	// Bound refetches for unknown kids to avoid turning invalid JWTs into network amplification.
	if time.Since(p.keysAt) < time.Minute {
		return nil, errAppleIdentity
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://appleid.apple.com/auth/keys", nil)
	res, err := p.client.Do(req)
	if err != nil {
		return nil, errAppleIdentity
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, errAppleIdentity
	}
	var set struct {
		Keys []struct{ Kid, Kty, Use, Alg, N, E string }
	}
	if json.NewDecoder(io.LimitReader(res.Body, 65536)).Decode(&set) != nil {
		return nil, errAppleIdentity
	}
	keys := map[string]*rsa.PublicKey{}
	for _, k := range set.Keys {
		if k.Kty != "RSA" || k.Alg != "RS256" || k.Use != "sig" {
			continue
		}
		n, e1 := base64.RawURLEncoding.DecodeString(k.N)
		e, e2 := base64.RawURLEncoding.DecodeString(k.E)
		if e1 != nil || e2 != nil || len(n) < 256 || len(e) > 4 || len(e) == 0 {
			continue
		}
		exp := 0
		for _, b := range e {
			exp = exp*256 + int(b)
		}
		if exp < 3 || exp%2 == 0 {
			continue
		}
		keys[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: exp}
	}
	if len(keys) == 0 {
		return nil, errAppleIdentity
	}
	p.keys = keys
	p.keysAt = time.Now()
	if key := keys[kid]; key != nil {
		return key, nil
	}
	return nil, errAppleIdentity
}

func (p *appleIdentity) verify(ctx context.Context, token, nonce string) (string, error) {
	if len(token) > 16384 {
		return "", errAppleIdentity
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", errAppleIdentity
	}
	dec := base64.RawURLEncoding.DecodeString
	head, e1 := dec(parts[0])
	body, e2 := dec(parts[1])
	signature, e3 := dec(parts[2])
	if e1 != nil || e2 != nil || e3 != nil {
		return "", errAppleIdentity
	}
	var h struct{ Alg, Kid string }
	if json.Unmarshal(head, &h) != nil || h.Alg != "RS256" || h.Kid == "" {
		return "", errAppleIdentity
	}
	key, err := p.publicKey(ctx, h.Kid)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(key, crypto.SHA256, sum[:], signature) != nil {
		return "", errAppleIdentity
	}
	var claims struct {
		Iss, Sub, Aud, Nonce string
		Exp, Iat             int64
	}
	if json.Unmarshal(body, &claims) != nil {
		return "", errAppleIdentity
	}
	now := time.Now().Unix()
	if claims.Iss != "https://appleid.apple.com" || claims.Aud != p.clientID || claims.Sub == "" || len(claims.Sub) > 255 || claims.Nonce != nonce || nonce == "" || claims.Exp <= now || claims.Iat > now+60 || claims.Iat < now-600 {
		return "", errAppleIdentity
	}
	return claims.Sub, nil
}
