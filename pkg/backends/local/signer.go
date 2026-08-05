package local

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gsandoval/tako/internal/routes"
	"github.com/gsandoval/tako/pkg/backends"
)

// URLSigner mints local signed URLs: <base>/_tako/api/signed/<claims>.<mac>.
// The HMAC secret persists at <dir>/signing.key so URLs survive tako
// dev reloads and restarts.
type URLSigner struct {
	base   string // e.g. "http://localhost:8090"
	secret []byte
}

// signedClaims is the token payload. Field names are single letters
// because the token rides in a URL.
type signedClaims struct {
	Bucket      string `json:"b"`
	Key         string `json:"k"`
	Method      string `json:"m"` // "put" or "get"
	Expires     int64  `json:"e"` // unix seconds
	MaxSize     int64  `json:"s,omitempty"`
	ContentType string `json:"c,omitempty"`
}

// NewURLSigner roots a signer at dir (sharing the backend data root) with
// URLs served from base.
func NewURLSigner(dir, base string) (*URLSigner, error) {
	path := filepath.Join(dir, "signing.key")
	secret, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		secret = make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return nil, err
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
		err = os.WriteFile(path, secret, 0o600)
	}
	if err != nil {
		return nil, fmt.Errorf("signing key: %w", err)
	}
	return &URLSigner{base: strings.TrimRight(base, "/"), secret: secret}, nil
}

func (s *URLSigner) SignPut(ctx context.Context, bucket, key string, ttl time.Duration, c backends.SignConstraints) (string, error) {
	return s.sign(signedClaims{
		Bucket:      bucket,
		Key:         key,
		Method:      "put",
		Expires:     time.Now().Add(ttl).Unix(),
		MaxSize:     c.MaxSize,
		ContentType: c.ContentType,
	})
}

func (s *URLSigner) SignGet(ctx context.Context, bucket, key string, ttl time.Duration) (string, error) {
	return s.sign(signedClaims{
		Bucket:  bucket,
		Key:     key,
		Method:  "get",
		Expires: time.Now().Add(ttl).Unix(),
	})
}

func (s *URLSigner) sign(c signedClaims) (string, error) {
	raw, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	body := base64.RawURLEncoding.EncodeToString(raw)
	return s.base + routes.Signed + body + "." + s.mac(body), nil
}

// verify authenticates a token (the path segment after /_tako/api/signed/)
// and returns its claims. Signature errors and expiry are distinct: the
// endpoint maps them to distinct texts.
func (s *URLSigner) verify(token string) (signedClaims, error) {
	body, mac, ok := strings.Cut(token, ".")
	if !ok || !hmac.Equal([]byte(s.mac(body)), []byte(mac)) {
		return signedClaims{}, errBadSignature
	}
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return signedClaims{}, errBadSignature
	}
	var c signedClaims
	if err := json.Unmarshal(raw, &c); err != nil {
		return signedClaims{}, errBadSignature
	}
	if time.Now().Unix() > c.Expires {
		return signedClaims{}, errExpired
	}
	return c, nil
}

func (s *URLSigner) mac(body string) string {
	h := hmac.New(sha256.New, s.secret)
	h.Write([]byte(body))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

var (
	errBadSignature = errors.New("invalid signature")
	errExpired      = errors.New("signed URL expired")
)
