package runtime

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	tako "github.com/gsandoval/tako"
)

// --- Shared RS256 X.509 certificate caching & JWT verification --------
//
// Identity Platform ID tokens (securetoken@system) and Google OIDC
// tokens (Cloud Scheduler and Pub/Sub push) are both RS256 JWTs whose
// rotating public X.509 certificates are served from well-known Google
// endpoints. Verification is pure stdlib.

const (
	secureTokenCerts = "https://www.googleapis.com/robot/v1/metadata/x509/securetoken@system.gserviceaccount.com"
	googleCerts      = "https://www.googleapis.com/oauth2/v1/certs"
)

type certStore struct {
	url     string
	mu      sync.Mutex
	cache   map[string]*rsa.PublicKey
	expires time.Time
}

var (
	secureTokenCertStore = certStore{url: secureTokenCerts}
	googleCertStore      = certStore{url: googleCerts}
)

func (s *certStore) keys(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cache != nil && time.Now().Before(s.expires) {
		return s.cache, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return nil, err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	var pems map[string]string
	if err := json.NewDecoder(res.Body).Decode(&pems); err != nil {
		return nil, err
	}
	keys := map[string]*rsa.PublicKey{}
	for kid, certPEM := range pems {
		if pub := parsePublicKey(certPEM); pub != nil {
			keys[kid] = pub
		}
	}
	ttl := 30 * time.Minute
	if cc := res.Header.Get("Cache-Control"); cc != "" {
		if i := strings.Index(cc, "max-age="); i >= 0 {
			var secs int
			fmt.Sscanf(cc[i+len("max-age="):], "%d", &secs)
			if secs > 0 {
				ttl = time.Duration(secs) * time.Second
			}
		}
	}
	s.cache, s.expires = keys, time.Now().Add(ttl)
	return keys, nil
}

func parsePublicKey(certPEM string) *rsa.PublicKey {
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		return nil
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil
	}
	pub, _ := cert.PublicKey.(*rsa.PublicKey)
	return pub
}

func signingKeys(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	return secureTokenCertStore.keys(ctx)
}

func googleSigningKeys(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	return googleCertStore.keys(ctx)
}

func b64url(s string) ([]byte, error) { return base64.RawURLEncoding.DecodeString(s) }

func verifyRS256(ctx context.Context, token string, store *certStore, keySource string) ([]byte, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("not a JWT")
	}
	headRaw, err1 := b64url(parts[0])
	claimRaw, err2 := b64url(parts[1])
	sig, err3 := b64url(parts[2])
	if err1 != nil || err2 != nil || err3 != nil {
		return nil, errors.New("malformed JWT")
	}
	var head struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if json.Unmarshal(headRaw, &head) != nil || head.Alg != "RS256" {
		return nil, errors.New("unexpected algorithm")
	}
	keys, err := store.keys(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetching %s: %v", keySource, err)
	}
	key, ok := keys[head.Kid]
	if !ok {
		return nil, errors.New("unknown signing key")
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], sig) != nil {
		return nil, errors.New("bad signature")
	}
	return claimRaw, nil
}

// verifyIDToken checks signature, issuer, audience, and expiry against
// this project (TAKO_PROJECT — the Identity Platform project).
func verifyIDToken(ctx context.Context, token string) (tako.User, error) {
	project := os.Getenv("TAKO_PROJECT")
	claimRaw, err := verifyRS256(ctx, token, &secureTokenCertStore, "signing keys")
	if err != nil {
		return tako.User{}, err
	}
	var claims idTokenClaims
	if json.Unmarshal(claimRaw, &claims) != nil {
		return tako.User{}, errors.New("malformed claims")
	}
	if claims.Iss != "https://securetoken.google.com/"+project || claims.Aud != project {
		return tako.User{}, errors.New("token is for a different project")
	}
	if time.Now().Unix() > claims.Exp {
		return tako.User{}, errors.New("token expired")
	}
	return userFromClaims(claims, declaredProviders)
}

// idTokenClaims is the part of an Identity Platform ID token tako
// reads. The firebase object is the half that was missing: the token
// says which provider minted it, and until FL14 nothing looked.
type idTokenClaims struct {
	Iss           string `json:"iss"`
	Aud           string `json:"aud"`
	Exp           int64  `json:"exp"`
	Sub           string `json:"sub"`
	Email         string `json:"email"`
	Name          string `json:"name"`
	EmailVerified bool   `json:"email_verified"`
	Firebase      struct {
		SignInProvider string `json:"sign_in_provider"`
	} `json:"firebase"`
	// Roles is tako's custom claim, set through auth.Users().Grant.
	// Identity Platform puts custom claims at the top level of the ID
	// token, so this is read exactly like email or name — and like
	// them, it is whatever the token says: a role granted since the
	// token was issued is not in it, which is what the stale-session
	// refresh exists for.
	Roles []string `json:"roles"`
}

// providerNames maps Identity Platform's sign_in_provider onto the
// names an app declares in tako.AuthConfig.
var providerNames = map[string]string{
	"google.com": "google",
	"password":   "email",
}

// userFromClaims applies the rule the declaration always looked like it
// applied: a session may only come from a provider the app declared.
//
// Split from verifyIDToken so the rule is testable without a signing
// key — the signature check above is Google's business, this is tako's.
func userFromClaims(claims idTokenClaims, declared []string) (tako.User, error) {
	if claims.Sub == "" {
		return tako.User{}, errors.New("no subject")
	}
	raw := claims.Firebase.SignInProvider
	provider, known := providerNames[raw]
	if raw == "" {
		// No provider claim at all is not "any provider" — it is a
		// token tako cannot place, and the safe reading of an absent
		// claim is refusal.
		return tako.User{}, errors.New("the token names no sign-in provider")
	}
	if !known {
		provider = raw
	}
	if len(declared) > 0 && !slices.Contains(declared, provider) {
		return tako.User{}, fmt.Errorf("this token was minted by %s, and this app declares %s — tako.AuthConfig decides which doors exist",
			provider, strings.Join(declared, " and "))
	}
	return tako.User{
		ID: claims.Sub, Email: claims.Email, Name: claims.Name,
		Provider: provider, EmailVerified: claims.EmailVerified,
		Roles: claims.Roles,
	}, nil
}

// declaredProviders is the app's tako.AuthConfig, installed by the
// generated main. Empty means no app declared auth, in which case
// nothing reaches verifyIDToken anyway.
var declaredProviders []string
