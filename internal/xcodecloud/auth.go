package xcodecloud

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Environment variables that configure App Store Connect team API keys.
const (
	EnvIssuerID       = "LAZYXCODE_ASC_ISSUER_ID"
	EnvKeyID          = "LAZYXCODE_ASC_KEY_ID"
	EnvPrivateKeyPath = "LAZYXCODE_ASC_PRIVATE_KEY_PATH"
)

const (
	tokenAudience      = "appstoreconnect-v1"
	tokenLifetime      = 10 * time.Minute
	tokenRefreshMargin = time.Minute
)

// Configuration and credential errors. They never contain key material.
var (
	ErrNotConfigured    = errors.New("Xcode Cloud credentials are not configured")
	ErrIncompleteConfig = errors.New("Xcode Cloud credentials are incomplete")
	ErrKeyUnreadable    = errors.New("App Store Connect private key is unreadable")
	ErrKeyMalformed     = errors.New("App Store Connect private key is malformed")
)

// Credentials identify an App Store Connect team API key. The private key
// stays in the user-controlled file until a token source reads it.
type Credentials struct {
	IssuerID       string
	KeyID          string
	PrivateKeyPath string
}

// LoadCredentials reads the credential environment variables through getenv.
// It returns ErrNotConfigured when none are set and ErrIncompleteConfig when
// only some are set.
func LoadCredentials(getenv func(string) string) (Credentials, error) {
	credentials := Credentials{
		IssuerID:       strings.TrimSpace(getenv(EnvIssuerID)),
		KeyID:          strings.TrimSpace(getenv(EnvKeyID)),
		PrivateKeyPath: strings.TrimSpace(getenv(EnvPrivateKeyPath)),
	}
	var missing []string
	if credentials.IssuerID == "" {
		missing = append(missing, EnvIssuerID)
	}
	if credentials.KeyID == "" {
		missing = append(missing, EnvKeyID)
	}
	if credentials.PrivateKeyPath == "" {
		missing = append(missing, EnvPrivateKeyPath)
	}
	if len(missing) == 3 {
		return Credentials{}, ErrNotConfigured
	}
	if len(missing) > 0 {
		return Credentials{}, fmt.Errorf("%w: set %s", ErrIncompleteConfig, strings.Join(missing, ", "))
	}
	return credentials, nil
}

// TokenSource produces bearer tokens for App Store Connect requests.
type TokenSource interface {
	Token(context.Context) (string, error)
}

// KeyTokenSource mints short-lived ES256 JWTs from a team API key and caches
// them until shortly before they expire.
type KeyTokenSource struct {
	mu       sync.Mutex
	issuer   string
	keyID    string
	key      *ecdsa.PrivateKey
	now      func() time.Time
	lifetime time.Duration
	scopes   []string
	token    string
	expires  time.Time
}

// NewKeyTokenSource loads and validates the private key referenced by the
// credentials. It returns non-fatal warnings, such as permissive file modes.
func NewKeyTokenSource(credentials Credentials, now func() time.Time) (*KeyTokenSource, []string, error) {
	if now == nil {
		now = time.Now
	}
	info, err := os.Stat(credentials.PrivateKeyPath)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %s", ErrKeyUnreadable, describeFileError(err))
	}
	var warnings []string
	if info.Mode().Perm()&0o077 != 0 {
		warnings = append(warnings, fmt.Sprintf("private key %s is readable by other users; run chmod 600 on it", credentials.PrivateKeyPath))
	}
	data, err := os.ReadFile(credentials.PrivateKeyPath)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %s", ErrKeyUnreadable, describeFileError(err))
	}
	key, err := parsePrivateKey(data)
	for i := range data {
		data[i] = 0
	}
	if err != nil {
		return nil, nil, err
	}
	return &KeyTokenSource{issuer: credentials.IssuerID, keyID: credentials.KeyID, key: key, now: now, lifetime: tokenLifetime}, warnings, nil
}

// SetScopes restricts minted tokens to the given App Store Connect scope
// entries, such as "GET /v1/ciProducts". An empty list omits the claim.
func (s *KeyTokenSource) SetScopes(scopes []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scopes = append([]string(nil), scopes...)
	s.token, s.expires = "", time.Time{}
}

func parsePrivateKey(data []byte) (*ecdsa.PrivateKey, error) {
	if !strings.Contains(string(data), "-----BEGIN") {
		return nil, fmt.Errorf("%w: file is not PEM encoded", ErrKeyMalformed)
	}
	key, err := jwt.ParseECPrivateKeyFromPEM(data)
	if err != nil {
		return nil, fmt.Errorf("%w: expected a PEM encoded EC private key", ErrKeyMalformed)
	}
	if key.Curve != elliptic.P256() {
		return nil, fmt.Errorf("%w: ES256 requires a P-256 key", ErrKeyMalformed)
	}
	return key, nil
}

func describeFileError(err error) string {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Op + " " + pathErr.Path + ": " + pathErr.Err.Error()
	}
	return err.Error()
}

// Token returns a cached token or mints a new one when the cached token is
// missing or about to expire.
func (s *KeyTokenSource) Token(context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if s.token != "" && now.Add(tokenRefreshMargin).Before(s.expires) {
		return s.token, nil
	}
	expires := now.Add(s.lifetime)
	claims := jwt.MapClaims{
		"iss": s.issuer,
		"iat": now.Unix(),
		"exp": expires.Unix(),
		"aud": tokenAudience,
	}
	if len(s.scopes) > 0 {
		claims["scope"] = s.scopes
	}
	token := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	token.Header["kid"] = s.keyID
	signed, err := token.SignedString(s.key)
	if err != nil {
		return "", fmt.Errorf("sign App Store Connect token: %w", err)
	}
	s.token, s.expires = signed, expires
	return signed, nil
}
