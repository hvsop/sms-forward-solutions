package main

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func signedAppleToken(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	h, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "test"})
	b, _ := json.Marshal(claims)
	enc := base64.RawURLEncoding.EncodeToString
	s := enc(h) + "." + enc(b)
	sum := sha256.Sum256([]byte(s))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return s + "." + enc(sig)
}
func TestAppleJWTValidationAndCodeBinding(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signing, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	now := time.Now().Unix()
	claims := func() map[string]any {
		return map[string]any{"iss": "https://appleid.apple.com", "aud": "com.kuaapi.sms", "sub": "subject", "nonce": "nonce", "iat": now, "exp": now + 300}
	}
	p := &appleIdentity{clientID: "com.kuaapi.sms", teamID: "TEAM", keyID: "KEY", key: signing, keys: map[string]*rsa.PublicKey{"test": &key.PublicKey}, keysAt: time.Now()}
	valid := signedAppleToken(t, key, claims())
	if s, e := p.verify(context.Background(), valid, "nonce"); e != nil || s != "subject" {
		t.Fatal(s, e)
	}
	for name, value := range map[string]any{"iss": "https://evil.test", "aud": "other.bundle", "sub": "", "nonce": "wrong", "iat": now - 700, "exp": now - 1} {
		c := claims()
		c[name] = value
		if _, e := p.verify(context.Background(), signedAppleToken(t, key, c), "nonce"); e == nil {
			t.Fatal("accepted invalid", name)
		}
	}
	pieces := strings.Split(valid, ".")
	sig, _ := base64.RawURLEncoding.DecodeString(pieces[2])
	sig[0] ^= 1
	pieces[2] = base64.RawURLEncoding.EncodeToString(sig)
	if _, e := p.verify(context.Background(), strings.Join(pieces, "."), "nonce"); e == nil {
		t.Fatal("accepted forged signature")
	}
	calls := 0
	returned := valid
	p.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != "https://appleid.apple.com/auth/token" {
			t.Fatal("wrong token endpoint")
		}
		_ = r.ParseForm()
		if r.Form.Get("client_id") != "com.kuaapi.sms" || r.Form.Get("code") != "one-time-code" || r.Form.Get("grant_type") != "authorization_code" {
			t.Fatal("wrong exchange fields")
		}
		// Validate the ES256 client assertion without logging it.
		assertion := strings.Split(r.Form.Get("client_secret"), ".")
		if len(assertion) != 3 {
			t.Fatal("missing assertion")
		}
		data, _ := base64.RawURLEncoding.DecodeString(assertion[1])
		var c map[string]any
		_ = json.Unmarshal(data, &c)
		if c["aud"] != "https://appleid.apple.com" || c["sub"] != p.clientID || c["iss"] != p.teamID {
			t.Fatal("wrong assertion claims")
		}
		b, _ := json.Marshal(map[string]string{"id_token": returned, "refresh_token": "private-refresh"})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(b)))}, nil
	})}
	if s, r, e := p.exchange(context.Background(), valid, "one-time-code", "nonce"); e != nil || s != "subject" || r != "private-refresh" {
		t.Fatal("valid exchange failed")
	}
	c := claims()
	c["sub"] = "other"
	returned = signedAppleToken(t, key, c)
	if _, _, e := p.exchange(context.Background(), valid, "one-time-code", "nonce"); e == nil {
		t.Fatal("identity/code from different users accepted")
	}
	if calls != 2 {
		t.Fatal(calls)
	}
}
func TestAppleJWKCacheAndUnknownKey(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	enc := base64.RawURLEncoding.EncodeToString
	calls := 0
	p := &appleIdentity{clientID: "com.kuaapi.sms"}
	p.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != "https://appleid.apple.com/auth/keys" {
			t.Fatal("untrusted JWK URL")
		}
		b, _ := json.Marshal(map[string]any{"keys": []map[string]string{{"kid": "test", "kty": "RSA", "alg": "RS256", "use": "sig", "n": enc(key.N.Bytes()), "e": "AQAB"}}})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(b)))}, nil
	})}
	if k, e := p.publicKey(context.Background(), "test"); e != nil || k.N.Cmp(key.N) != 0 {
		t.Fatal(e)
	}
	_, _ = p.publicKey(context.Background(), "test")
	_, err := p.publicKey(context.Background(), "attacker")
	if err == nil || calls != 1 {
		t.Fatal("unknown key bypassed cache limit")
	}
}
