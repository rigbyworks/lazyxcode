package xcodecloud

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func writeTestKey(t *testing.T, mode os.FileMode) (string, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "AuthKey_ABC123.p8")
	data := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
	return path, key
}

func envFrom(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

func TestLoadCredentialsDistinguishesMissingAndIncompleteConfiguration(t *testing.T) {
	if _, err := LoadCredentials(envFrom(nil)); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("empty environment error = %v", err)
	}
	_, err := LoadCredentials(envFrom(map[string]string{EnvIssuerID: "issuer"}))
	if !errors.Is(err, ErrIncompleteConfig) || !strings.Contains(err.Error(), EnvKeyID) || !strings.Contains(err.Error(), EnvPrivateKeyPath) {
		t.Fatalf("partial environment error = %v", err)
	}
	credentials, err := LoadCredentials(envFrom(map[string]string{EnvIssuerID: " issuer ", EnvKeyID: "KEY", EnvPrivateKeyPath: "/tmp/key.p8"}))
	if err != nil || credentials.IssuerID != "issuer" || credentials.KeyID != "KEY" || credentials.PrivateKeyPath != "/tmp/key.p8" {
		t.Fatalf("credentials = %#v, %v", credentials, err)
	}
}

func TestKeyTokenSourceMintsAndRefreshesES256Tokens(t *testing.T) {
	path, key := writeTestKey(t, 0o600)
	now := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	source, warnings, err := NewKeyTokenSource(Credentials{IssuerID: "issuer-id", KeyID: "KEY1234567", PrivateKeyPath: path}, clock)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v", warnings)
	}
	source.SetScopes([]string{"GET /v1/ciProducts"})
	first, err := source.Token(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := jwt.Parse(first, func(*jwt.Token) (any, error) { return &key.PublicKey, nil },
		jwt.WithValidMethods([]string{"ES256"}), jwt.WithAudience(tokenAudience), jwt.WithIssuer("issuer-id"), jwt.WithTimeFunc(clock))
	if err != nil || !parsed.Valid {
		t.Fatalf("token did not validate: %v", err)
	}
	if parsed.Header["kid"] != "KEY1234567" {
		t.Fatalf("kid = %v", parsed.Header["kid"])
	}
	claims := parsed.Claims.(jwt.MapClaims)
	issued, _ := claims.GetIssuedAt()
	expires, _ := claims.GetExpirationTime()
	if expires.Sub(issued.Time) != tokenLifetime {
		t.Fatalf("lifetime = %v", expires.Sub(issued.Time))
	}
	if scopes, ok := claims["scope"].([]any); !ok || len(scopes) != 1 || scopes[0] != "GET /v1/ciProducts" {
		t.Fatalf("scope claim = %#v", claims["scope"])
	}
	now = now.Add(5 * time.Minute)
	if again, _ := source.Token(context.Background()); again != first {
		t.Fatal("token was re-minted before the refresh margin")
	}
	now = now.Add(4*time.Minute + 30*time.Second)
	refreshed, err := source.Token(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if refreshed == first {
		t.Fatal("token was not refreshed before expiry")
	}
}

func TestKeyTokenSourceWarnsAboutPermissiveKeyFiles(t *testing.T) {
	path, _ := writeTestKey(t, 0o644)
	_, warnings, err := NewKeyTokenSource(Credentials{IssuerID: "issuer", KeyID: "KEY", PrivateKeyPath: path}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "chmod 600") {
		t.Fatalf("warnings = %v", warnings)
	}
}

func TestKeyTokenSourceRejectsUnusableKeys(t *testing.T) {
	dir := t.TempDir()
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	rsaPath := filepath.Join(dir, "rsa.p8")
	rsaDER, _ := x509.MarshalPKCS8PrivateKey(rsaKey)
	os.WriteFile(rsaPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: rsaDER}), 0o600)
	p384, _ := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	p384Path := filepath.Join(dir, "p384.p8")
	p384DER, _ := x509.MarshalECPrivateKey(p384)
	os.WriteFile(p384Path, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: p384DER}), 0o600)
	garbagePath := filepath.Join(dir, "garbage.p8")
	os.WriteFile(garbagePath, []byte("SECRETMATERIAL not pem"), 0o600)
	brokenPath := filepath.Join(dir, "broken.p8")
	os.WriteFile(brokenPath, []byte("-----BEGIN PRIVATE KEY-----\nSECRETMATERIAL\n-----END PRIVATE KEY-----\n"), 0o600)

	cases := map[string]error{
		filepath.Join(dir, "missing.p8"): ErrKeyUnreadable,
		rsaPath:                          ErrKeyMalformed,
		p384Path:                         ErrKeyMalformed,
		garbagePath:                      ErrKeyMalformed,
		brokenPath:                       ErrKeyMalformed,
	}
	for path, want := range cases {
		_, _, err := NewKeyTokenSource(Credentials{IssuerID: "issuer", KeyID: "KEY", PrivateKeyPath: path}, nil)
		if !errors.Is(err, want) {
			t.Fatalf("%s: error = %v, want %v", filepath.Base(path), err, want)
		}
		if strings.Contains(err.Error(), "SECRETMATERIAL") {
			t.Fatalf("error leaked key contents: %v", err)
		}
	}
}

func TestConnectReturnsConfigurationErrors(t *testing.T) {
	if _, _, err := Connect(envFrom(nil)); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("Connect without configuration = %v", err)
	}
	path, _ := writeTestKey(t, 0o600)
	client, warnings, err := Connect(envFrom(map[string]string{EnvIssuerID: "issuer", EnvKeyID: "KEY", EnvPrivateKeyPath: path}))
	if err != nil || client == nil || len(warnings) != 0 {
		t.Fatalf("Connect = %v, %v, %v", client, warnings, err)
	}
}
