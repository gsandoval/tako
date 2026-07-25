package gcp

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gsandoval/tako/pkg/backends"
)

// Signer mints V4 GCS signed URLs via the IAM Credentials signBlob API:
// the service's own SA signs as itself, so no key file exists anywhere
// (the derived self-tokenCreator grant authorizes it). Enforcement is
// whatever a bare PUT URL lets GCS enforce: identity, expiry, and an
// EXACT content type (signed into the URL — clients send Content-Type
// naturally). A wildcard type and MaxSize would need client-cooperating
// headers or POST policies, so in the cloud they fall through to the
// pipeline's safety net (decode failures dead-letter, the cron reaps
// orphans) — the conformance params pin this platform gap explicitly.
type Signer struct {
	c *Client

	mu    sync.Mutex
	email string
}

func NewSigner(c *Client) *Signer { return &Signer{c: c} }

// identity resolves the signing SA: the explicit override (conformance
// harnesses), or this service's own identity from the metadata server.
func (s *Signer) identity(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.email != "" {
		return s.email, nil
	}
	if s.c.Opts.SignerEmail != "" {
		s.email = s.c.Opts.SignerEmail
		return s.email, nil
	}
	req, err := http.NewRequestWithContext(ctx, "GET",
		"http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/email", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Metadata-Flavor", "Google")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("cannot resolve this service's identity from the metadata server (not on Cloud Run? set SignerEmail): %v", err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return "", err
	}
	s.email = strings.TrimSpace(string(raw))
	return s.email, nil
}

// signBlob signs payload as email via the IAM Credentials API.
func (s *Signer) signBlob(ctx context.Context, email string, payload []byte) ([]byte, error) {
	u := "https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/" + email + ":signBlob"
	var out struct {
		SignedBlob []byte `json:"signedBlob"` // encoding/json base64-decodes
	}
	if err := s.c.do(ctx, "POST", u, map[string]any{
		"payload": base64.StdEncoding.EncodeToString(payload),
	}, &out); err != nil {
		return nil, fmt.Errorf("signBlob as %s: %w", email, err)
	}
	return out.SignedBlob, nil
}

// maxTTL is GCS's ceiling for V4 signed URLs; longer requests clamp.
const maxTTL = 7 * 24 * time.Hour

// signV4 builds a V4 signed URL for one verb on one object, with the
// given extra signed headers (beyond host).
func (s *Signer) signV4(ctx context.Context, method, bucket, key string, ttl time.Duration, extraHeaders map[string]string) (string, error) {
	email, err := s.identity(ctx)
	if err != nil {
		return "", err
	}
	if ttl > maxTTL {
		ttl = maxTTL
	}
	now := time.Now().UTC()
	stamp := now.Format("20060102T150405Z")
	scope := now.Format("20060102") + "/auto/storage/goog4_request"

	physical := BucketName(s.c.Opts.Project, s.c.Opts.App, s.c.Opts.Env, bucket)
	path := "/" + physical + "/" + encodePath(key)

	headers := map[string]string{"host": "storage.googleapis.com"}
	for k, v := range extraHeaders {
		headers[strings.ToLower(k)] = v
	}
	names := make([]string, 0, len(headers))
	for k := range headers {
		names = append(names, k)
	}
	sort.Strings(names)
	signedHeaders := strings.Join(names, ";")
	var canonicalHeaders strings.Builder
	for _, k := range names {
		canonicalHeaders.WriteString(k + ":" + headers[k] + "\n")
	}

	query := url.Values{
		"X-Goog-Algorithm":     {"GOOG4-RSA-SHA256"},
		"X-Goog-Credential":    {email + "/" + scope},
		"X-Goog-Date":          {stamp},
		"X-Goog-Expires":       {fmt.Sprint(int(ttl.Seconds()))},
		"X-Goog-SignedHeaders": {signedHeaders},
	}
	canonicalQuery := query.Encode() // sorted + strictly escaped

	canonical := strings.Join([]string{
		method, path, canonicalQuery, canonicalHeaders.String(), signedHeaders, "UNSIGNED-PAYLOAD",
	}, "\n")
	sum := sha256.Sum256([]byte(canonical))
	toSign := strings.Join([]string{
		"GOOG4-RSA-SHA256", stamp, scope, hex.EncodeToString(sum[:]),
	}, "\n")
	sig, err := s.signBlob(ctx, email, []byte(toSign))
	if err != nil {
		return "", err
	}
	return "https://storage.googleapis.com" + path + "?" + canonicalQuery +
		"&X-Goog-Signature=" + hex.EncodeToString(sig), nil
}

// encodePath escapes an object key for the URL path, keeping slashes.
func encodePath(key string) string {
	parts := strings.Split(key, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

func (s *Signer) SignPut(ctx context.Context, bucket, key string, ttl time.Duration, c backends.SignConstraints) (string, error) {
	headers := map[string]string{}
	// An exact content type is signed into the URL and enforced by GCS
	// (clients send Content-Type naturally); wildcards and MaxSize are
	// the documented platform gap — see the type comment.
	if c.ContentType != "" && !strings.Contains(c.ContentType, "*") {
		headers["content-type"] = c.ContentType
	}
	return s.signV4(ctx, http.MethodPut, bucket, key, ttl, headers)
}

func (s *Signer) SignGet(ctx context.Context, bucket, key string, ttl time.Duration) (string, error) {
	return s.signV4(ctx, http.MethodGet, bucket, key, ttl, nil)
}
