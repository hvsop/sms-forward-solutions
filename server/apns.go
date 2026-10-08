package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
)

type APNS struct {
	key                        *ecdsa.PrivateKey
	keyID, teamID, topic, host string
	client                     *http.Client
	mu                         sync.Mutex
	jwt                        string
	issued                     time.Time
}

func newAPNS(c Config) (*APNS, error) {
	if c.APNSKeyFile == "" {
		return nil, nil
	}
	data, err := os.ReadFile(c.APNSKeyFile)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("invalid APNs PEM key")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok || key.Curve != elliptic.P256() {
		return nil, errors.New("APNs requires a P-256 key")
	}
	host := "https://api.sandbox.push.apple.com"
	if c.APNSEnvironment == "production" {
		host = "https://api.push.apple.com"
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ForceAttemptHTTP2 = true
	return &APNS{key: key, keyID: c.APNSKeyID, teamID: c.APNSTeamID, topic: c.APNSTopic, host: host, client: &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (a *APNS) token(now time.Time) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.jwt != "" && now.Sub(a.issued) < 40*time.Minute {
		return a.jwt, nil
	}
	header, _ := json.Marshal(map[string]string{"alg": "ES256", "kid": a.keyID})
	claims, _ := json.Marshal(map[string]any{"iss": a.teamID, "iat": now.Unix()})
	enc := base64.RawURLEncoding.EncodeToString
	unsigned := enc(header) + "." + enc(claims)
	digest := sha256.Sum256([]byte(unsigned))
	r, s, err := ecdsa.Sign(rand.Reader, a.key, digest[:])
	if err != nil {
		return "", err
	}
	signature := make([]byte, 64)
	r.FillBytes(signature[:32])
	s.FillBytes(signature[32:])
	a.jwt, a.issued = unsigned+"."+enc(signature), now
	return a.jwt, nil
}

// The APNs payload never contains SMS content, phone numbers, message IDs, or OTPs.
func (a *APNS) send(ctx context.Context, device string, badge int) (invalidDevice bool, err error) {
	token, err := a.token(time.Now())
	if err != nil {
		return false, err
	}
	payload, _ := json.Marshal(map[string]any{"aps": map[string]any{
		"alert": map[string]string{"title": "private-sms-server", "body": "收到新短信，打开应用查看"},
		"sound": "default", "thread-id": "inbox", "badge": max(0, badge),
	}})
	req, err := http.NewRequestWithContext(ctx, "POST", a.host+"/3/device/"+device, bytes.NewReader(payload))
	if err != nil {
		return false, err
	}
	req.Header.Set("authorization", "bearer "+token)
	req.Header.Set("apns-topic", a.topic)
	req.Header.Set("apns-push-type", "alert")
	req.Header.Set("apns-priority", "10")
	req.Header.Set("apns-collapse-id", "private-sms-inbox")
	req.Header.Set("apns-expiration", strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))
	req.Header.Set("content-type", "application/json")
	res, err := a.client.Do(req)
	if err != nil {
		return false, errors.New("APNs transport failed")
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 4096))
	if err != nil {
		return false, errors.New("APNs response failed")
	}
	if res.StatusCode == http.StatusOK {
		return false, nil
	}
	var failure struct {
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(body, &failure)
	invalid := failure.Reason == "Unregistered" || failure.Reason == "BadDeviceToken" || failure.Reason == "DeviceTokenNotForTopic"
	if failure.Reason == "ExpiredProviderToken" {
		a.mu.Lock()
		a.jwt = ""
		a.mu.Unlock()
	}
	// Never include the remote body or request URL (which contains the token).
	return invalid, fmt.Errorf("APNs rejected push (HTTP %d)", res.StatusCode)
}
