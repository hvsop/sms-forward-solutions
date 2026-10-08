package main

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Listen, DataDir                                                string
	StorageKey                                                     []byte
	Retention                                                      time.Duration
	APNSKeyFile, APNSKeyID, APNSTeamID, APNSTopic, APNSEnvironment string
	TLSCert, TLSKey                                                string
	AppleClientID, AppleTeamID, AppleKeyID, AppleKeyFile           string
}

func initConfig(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	secret := func() []byte {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			panic(err)
		}
		return b
	}
	data := fmt.Sprintf(`# Keep this file private. Do not source it as a shell script.
LISTEN=127.0.0.1:8080
DATA_DIR=data
STORAGE_KEY=%s
RETENTION_DAYS=7

# Optional TLS when not using a local HTTPS reverse proxy.
TLS_CERT=
TLS_KEY=

# Set all APNS_* values to enable push; Xcode debug builds use sandbox.
APNS_KEY_FILE=
APNS_KEY_ID=
APNS_TEAM_ID=
APNS_TOPIC=com.example.privatesms
APNS_ENVIRONMENT=production
`, base64.StdEncoding.EncodeToString(secret()))
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err = f.WriteString(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func loadConfig(path string) (Config, error) {
	values := map[string]string{}
	f, err := os.Open(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Config{}, err
	}
	if err == nil {
		defer f.Close()
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			key, value, ok := strings.Cut(line, "=")
			if !ok {
				return Config{}, errors.New("invalid config line: expected KEY=value")
			}
			values[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
		if err := scanner.Err(); err != nil {
			return Config{}, err
		}
	}
	get := func(key, fallback string) string {
		if v, ok := os.LookupEnv(key); ok {
			return v
		}
		if v, ok := values[key]; ok {
			return v
		}
		return fallback
	}
	c := Config{Listen: get("LISTEN", "127.0.0.1:8080"), DataDir: get("DATA_DIR", "data"), APNSKeyFile: get("APNS_KEY_FILE", ""), APNSKeyID: get("APNS_KEY_ID", ""), APNSTeamID: get("APNS_TEAM_ID", ""), APNSTopic: get("APNS_TOPIC", ""), APNSEnvironment: get("APNS_ENVIRONMENT", "production"), TLSCert: get("TLS_CERT", ""), TLSKey: get("TLS_KEY", "")}
	c.StorageKey, err = base64.StdEncoding.DecodeString(get("STORAGE_KEY", ""))
	c.AppleClientID = get("APPLE_CLIENT_ID", "")
	c.AppleTeamID = get("APPLE_TEAM_ID", "")
	c.AppleKeyID = get("APPLE_KEY_ID", "")
	c.AppleKeyFile = get("APPLE_KEY_FILE", "")
	if err != nil || len(c.StorageKey) != 32 {
		return Config{}, errors.New("STORAGE_KEY must be a base64-encoded 32-byte key")
	}
	days, err := strconv.Atoi(get("RETENTION_DAYS", "7"))
	if err != nil || days < 1 || days > 365 {
		return Config{}, errors.New("RETENTION_DAYS must be 1..365")
	}
	c.Retention = time.Duration(days) * 24 * time.Hour
	if (c.TLSCert == "") != (c.TLSKey == "") {
		return Config{}, errors.New("TLS_CERT and TLS_KEY must be configured together")
	}
	if c.APNSEnvironment != "sandbox" && c.APNSEnvironment != "production" {
		return Config{}, errors.New("APNS_ENVIRONMENT must be sandbox or production")
	}
	if c.APNSKeyFile != "" && (c.APNSKeyID == "" || c.APNSTeamID == "" || c.APNSTopic == "" || c.APNSTopic == "YOUR_UNIQUE_BUNDLE_ID") {
		return Config{}, errors.New("complete the APNs key, team and topic configuration")
	}
	return c, nil
}
