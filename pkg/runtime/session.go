package runtime

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	tako "github.com/gsandoval/tako"
	"github.com/gsandoval/tako/internal/hooks"
	"github.com/gsandoval/tako/internal/routes"
)

// Sessions are Datastore-backed records (W5 amendment): the cookie
// carries only an opaque random id; the record — a framework-internal
// entity no user declaration can produce — holds the identity and the
// session-bound CSRF token. Revocation is real: sign-out (or an
// operator, in the console) deletes the record and the session is dead
// on the next request. The honest cost is one entity Get per
// authenticated request; v0 does not cache.
const (
	sessionKind   = "_tako_session"
	sessionCookie = "_session"
	sessionTTL    = 7 * 24 * time.Hour
)

// authRequired is the exact 401 text — product UX, pinned by tests.
const authRequired = "sign in to continue"

type sessionRecord struct {
	ID      string    `json:"id"`
	UID     string    `json:"uid"`
	Email   string    `json:"email"`
	Name    string    `json:"name"`
	CSRF    string    `json:"csrf"`
	Created time.Time `json:"created"`
	Expires time.Time `json:"expires"`
}

func randomHex(n int) string {
	raw := make([]byte, n)
	_, _ = rand.Read(raw)
	return hex.EncodeToString(raw)
}

// mintSession stores a new session record and returns it.
func mintSession(ctx context.Context, u tako.User) (*sessionRecord, error) {
	if hooks.Entity == nil {
		return nil, errors.New("tako: sessions need the entity backend — Boot wires it")
	}
	rec := &sessionRecord{
		ID:      randomHex(16),
		UID:     u.ID,
		Email:   u.Email,
		Name:    u.Name,
		CSRF:    randomHex(16),
		Created: time.Now().UTC(),
		Expires: time.Now().UTC().Add(sessionTTL),
	}
	doc, err := json.Marshal(rec)
	if err != nil {
		return nil, err
	}
	return rec, hooks.Entity.Put(ctx, sessionKind, rec.ID, doc)
}

// loadSession resolves the request's session record, treating expired
// records as absent (and lazily deleting them).
func loadSession(r *http.Request) *sessionRecord {
	id := ""
	if c, err := r.Cookie(sessionCookie); err == nil {
		id = c.Value
	}
	if id == "" {
		// Cross-service calls carry the caller's session ID (§2.12,
		// W9 amendment): presenting the id is equivalent to presenting
		// the cookie — the record lookup below IS the verification, and
		// revocation kills the identity mid-chain.
		id = r.Header.Get(sessionHeader)
	}
	if id == "" || hooks.Entity == nil {
		return nil
	}
	doc, err := hooks.Entity.Get(r.Context(), sessionKind, id)
	if err != nil {
		return nil
	}
	var rec sessionRecord
	if json.Unmarshal(doc, &rec) != nil {
		return nil
	}
	if time.Now().After(rec.Expires) {
		_ = hooks.Entity.Delete(r.Context(), sessionKind, rec.ID)
		return nil
	}
	return &rec
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, id string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    id,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
		SameSite: http.SameSiteLaxMode,
	})
}

// SessionHandler serves /_tako/api/session — injected when the app declares
// tako.Auth. POST exchanges a Firebase (Identity Platform) ID token for
// a session; on the local backend it also accepts the dev bar's
// dev_user form (no GCP anywhere near tako dev). DELETE signs out by
// deleting the record — real revocation.
func SessionHandler(providers ...string) http.Handler {
	// The declaration reaches the verifier: a session may only come
	// from a provider the app declared (FL14). Before this, AuthConfig
	// decided which buttons the sign-in page rendered and nothing else,
	// so any Identity Platform token for the project was accepted —
	// including one from a provider enabled on the project by something
	// other than this app.
	declaredProviders = providers
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			var u tako.User
			if name := r.PostFormValue("dev_user"); name != "" && isLocalBackend() {
				u = tako.User{ID: name, Email: name + "@example.test", Name: name}
			} else {
				var body struct {
					IDToken string `json:"idToken"`
				}
				raw, _ := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
				if json.Unmarshal(raw, &body) != nil || body.IDToken == "" {
					http.Error(w, "POST {\"idToken\": …} (the Identity Platform sign-in result)", http.StatusBadRequest)
					return
				}
				verified, err := verifyIDToken(r.Context(), body.IDToken)
				if err != nil {
					http.Error(w, "the ID token did not verify: "+err.Error(), http.StatusUnauthorized)
					return
				}
				u = verified
			}
			rec, err := mintSession(r.Context(), u)
			if err != nil {
				http.Error(w, "session store error", http.StatusInternalServerError)
				return
			}
			setSessionCookie(w, r, rec.ID, rec.Expires)
			// The dev bar (and dev sign-in page) post plain forms:
			// bounce to next, else back to the page.
			if r.PostFormValue("dev_user") != "" {
				to := r.PostFormValue("next")
				if to == "" || !strings.HasPrefix(to, "/") {
					to = r.Referer()
				}
				if to == "" {
					to = "/"
				}
				http.Redirect(w, r, to, http.StatusSeeOther)
				return
			}
			writeJSON(w, http.StatusOK, map[string]string{"uid": u.ID})
		case http.MethodDelete:
			if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" && hooks.Entity != nil {
				_ = hooks.Entity.Delete(r.Context(), sessionKind, c.Value)
			}
			setSessionCookie(w, r, "", time.Unix(0, 0))
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

// --- Firebase / Identity Platform ID-token verification ---------------
//
// ID tokens are RS256 JWTs signed by securetoken@system; the public
// certs rotate and are fetched (and cached against their Cache-Control)
// from Google's well-known endpoint. Verification is pure stdlib.

const secureTokenCerts = "https://www.googleapis.com/robot/v1/metadata/x509/securetoken@system.gserviceaccount.com"

var (
	certMu      sync.Mutex
	certCache   map[string]*rsa.PublicKey
	certExpires time.Time
)

func signingKeys(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	certMu.Lock()
	defer certMu.Unlock()
	if certCache != nil && time.Now().Before(certExpires) {
		return certCache, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, secureTokenCerts, nil)
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
		block, _ := pem.Decode([]byte(certPEM))
		if block == nil {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			continue
		}
		if pub, ok := cert.PublicKey.(*rsa.PublicKey); ok {
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
	certCache, certExpires = keys, time.Now().Add(ttl)
	return keys, nil
}

func b64url(s string) ([]byte, error) { return base64.RawURLEncoding.DecodeString(s) }

// verifyIDToken checks signature, issuer, audience, and expiry against
// this project (TAKO_PROJECT — the Identity Platform project).
func verifyIDToken(ctx context.Context, token string) (tako.User, error) {
	project := os.Getenv("TAKO_PROJECT")
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return tako.User{}, errors.New("not a JWT")
	}
	headRaw, err1 := b64url(parts[0])
	claimRaw, err2 := b64url(parts[1])
	sig, err3 := b64url(parts[2])
	if err1 != nil || err2 != nil || err3 != nil {
		return tako.User{}, errors.New("malformed JWT")
	}
	var head struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if json.Unmarshal(headRaw, &head) != nil || head.Alg != "RS256" {
		return tako.User{}, errors.New("unexpected algorithm")
	}
	keys, err := signingKeys(ctx)
	if err != nil {
		return tako.User{}, fmt.Errorf("fetching signing keys: %v", err)
	}
	key, ok := keys[head.Kid]
	if !ok {
		return tako.User{}, errors.New("unknown signing key")
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], sig) != nil {
		return tako.User{}, errors.New("bad signature")
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
	}, nil
}

// declaredProviders is the app's tako.AuthConfig, installed by the
// generated main. Empty means no app declared auth, in which case
// nothing reaches verifyIDToken anyway.
var declaredProviders []string

// csrfMatches compares a submitted token in constant time.
func csrfMatches(sent, want string) bool {
	return sent != "" && subtle.ConstantTimeCompare([]byte(sent), []byte(want)) == 1
}

// --- The dev sign-in bar ----------------------------------------------

// WithDevBar injects the "sign in as test user" bar into HTML documents
// — LOCAL ONLY (tako dev's promise: auth with no GCP anywhere near it).
// Cloud builds pass through untouched; the generated main stays
// identical across backends.
func WithDevBar(next http.Handler) http.Handler {
	if !isLocalBackend() {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A pointer in the context for the same reason routeLabel is
		// one: the decision is made INSIDE the handler and read out
		// here, after it has returned.
		frag := &fragmentFlag{}
		r = r.WithContext(context.WithValue(r.Context(), fragmentKey{}, frag))
		bw := &barWriter{ResponseWriter: w, req: r, fragment: frag}
		next.ServeHTTP(bw, r)
		bw.flush()
	})
}

// fragmentFlag records that the response in flight is a PARTIAL — an
// htmx swap, not a document.
//
// The dev bar cannot tell from the outside: a fragment answers
// text/html like a page does. So every htmx swap under tako dev used
// to carry a copy of the bar into whatever it swapped into — in an app
// with its own fixed bottom bar, one stacking on another on every
// keystroke.
type fragmentFlag struct{ is bool }

type fragmentKey struct{}

// markFragment says this response is a partial. Called where the
// runtime already branches on exactly this — the same signal that
// decides a fragment carries no CSP.
func markFragment(r *http.Request) {
	if f, ok := r.Context().Value(fragmentKey{}).(*fragmentFlag); ok && f != nil {
		f.is = true
	}
}

type barWriter struct {
	http.ResponseWriter
	req      *http.Request
	fragment *fragmentFlag
	buf      []byte
	html     bool
	probed   bool
	status   int
}

func (b *barWriter) WriteHeader(status int) { b.status = status }

func (b *barWriter) Write(p []byte) (int, error) {
	if !b.probed {
		b.probed = true
		b.html = strings.HasPrefix(b.Header().Get("Content-Type"), "text/html")
	}
	if !b.html {
		b.commitHeader()
		return b.ResponseWriter.Write(p)
	}
	b.buf = append(b.buf, p...)
	return len(p), nil
}

func (b *barWriter) commitHeader() {
	if b.status != 0 {
		b.ResponseWriter.WriteHeader(b.status)
		b.status = 0
	}
}

func (b *barWriter) flush() {
	// A fragment is not a page, so there is nothing for the bar to sit
	// at the bottom OF. Injecting into one puts a position:fixed strip
	// inside whatever the swap replaced, once per swap.
	if !b.html || (b.fragment != nil && b.fragment.is) {
		b.commitHeader()
		if len(b.buf) > 0 {
			_, _ = b.ResponseWriter.Write(b.buf)
		}
		return
	}
	// The bar REFLECTS the session instead of listing every control:
	// consistent labels, and only the actions that make sense now.
	rec := loadSession(b.req)
	who := "anonymous"
	var controls strings.Builder
	controls.WriteString(`<form method="post" action="` + routes.Session + `" style="display:inline-flex;gap:6px;margin:0">`)
	for _, u := range []string{"alice", "bob"} {
		switch {
		case rec == nil:
			fmt.Fprintf(&controls, `<button name="dev_user" value="%s">sign in as %s</button>`, u, u)
		case rec.Name != u:
			fmt.Fprintf(&controls, `<button name="dev_user" value="%s">switch to %s</button>`, u, u)
		}
	}
	controls.WriteString(`</form>`)
	if rec != nil {
		who = "signed in as " + rec.Name
		controls.WriteString(`<form method="post" action="` + routes.SignOut + `" style="margin:0"><button>sign out</button></form>`)
	}
	// The id is a CONTRACT, not decoration: an app whose own layout puts
	// something at the bottom of the window needs a way to move or hide
	// this, and inline styles are nothing to target. Without it the only
	// handle is behaviour — "the fixed child of <body> holding a
	// dev_user control" — which is a guess at something tako never
	// promised. `#tako-dev-bar { display: none }` is now supported.
	bar := `<div id="tako-dev-bar" style="position:fixed;bottom:0;left:0;right:0;background:#1a1a1a;color:#eee;font:13px sans-serif;padding:6px 12px;display:flex;gap:12px;align-items:center;z-index:9999">` +
		`<span>tako dev · ` + who + `</span>` +
		controls.String() +
		`</div>`
	out := b.buf
	if i := strings.LastIndex(string(out), "</body>"); i >= 0 {
		out = append(append(append([]byte{}, out[:i]...), []byte(bar)...), out[i:]...)
	} else {
		out = append(out, []byte(bar)...)
	}
	b.Header().Set("Content-Length", fmt.Sprint(len(out)))
	b.commitHeader()
	_, _ = b.ResponseWriter.Write(out)
}

// SignOutHandler is the dev bar's form-friendly sign-out (browsers
// cannot DELETE from a form); it shares DELETE's behavior.
func SignOutHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" && hooks.Entity != nil {
			_ = hooks.Entity.Delete(r.Context(), sessionKind, c.Value)
		}
		setSessionCookie(w, r, "", time.Unix(0, 0))
		if r.Header.Get("Accept") != "" && strings.Contains(r.Header.Get("Accept"), "text/html") {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// SignInPage serves GET /_tako/signin — the framework-owned
// sign-in UI ("auth is one declaration": the app links here, tako owns
// the flow). Locally it lists dev users (no GCP anywhere near tako
// dev); in the cloud it runs Identity Platform's browser flow with the
// client config the deploy injected, POSTs the resulting ID token to
// /_tako/api/session, and returns to ?next= (default /).
func SignInPage(providers ...string) http.Handler {
	prov := map[string]bool{}
	for _, p := range providers {
		prov[p] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next := r.URL.Query().Get("next")
		if next == "" || !strings.HasPrefix(next, "/") {
			next = "/"
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		if isLocalBackend() {
			fmt.Fprintf(w, signInDevPage, next)
			return
		}
		apiKey, authDomain, project := os.Getenv("TAKO_AUTH_APIKEY"), os.Getenv("TAKO_AUTH_DOMAIN"), os.Getenv("TAKO_PROJECT")
		if apiKey == "" {
			http.Error(w, "sign-in is not configured — tako deploy injects the auth client config, so this is a tako bug; re-run tako deploy", http.StatusInternalServerError)
			return
		}
		google, email := "", ""
		if prov["google"] {
			google = `<button id="g" style="font:16px sans-serif;padding:10px 18px">Sign in with Google</button>`
		}
		if prov["email"] {
			email = `<form id="e" style="display:flex;flex-direction:column;gap:8px;max-width:280px;margin-top:16px">
  <input name="email" type="email" placeholder="email" required>
  <input name="password" type="password" placeholder="password" required>
  <button>Sign in with email</button></form>`
		}
		fmt.Fprintf(w, signInCloudPage, google, email, apiKey, authDomain, project, next)
	})
}

const signInDevPage = `<!doctype html><meta charset="utf-8"><title>sign in · tako dev</title>
<body style="font:16px sans-serif;max-width:32rem;margin:4rem auto">
<h1>Sign in (dev)</h1>
<p>Local sessions are dev-minted — no GCP involved. Pick a user:</p>
<form method="post" action="` + routes.Session + `"><input type="hidden" name="next" value="%s">
<button name="dev_user" value="alice">alice</button>
<button name="dev_user" value="bob">bob</button>
</form></body>`

// The cloud page runs the standard Identity Platform browser flow via
// the Firebase Auth compat SDK (gstatic). One POST of the resulting ID
// token establishes the server session; the SDK's client state is not
// used afterwards.
const signInCloudPage = `<!doctype html><meta charset="utf-8"><title>sign in</title>
<body style="font:16px sans-serif;max-width:32rem;margin:4rem auto">
<h1>Sign in</h1>
%s%s
<p id="err" style="color:#b00"></p>
<script src="https://www.gstatic.com/firebasejs/10.14.1/firebase-app-compat.js"></script>
<script src="https://www.gstatic.com/firebasejs/10.14.1/firebase-auth-compat.js"></script>
<script>
firebase.initializeApp({apiKey: %q, authDomain: %q, projectId: %q});
const next = %q;
async function establish(cred) {
  const idToken = await cred.user.getIdToken();
  const res = await fetch("` + routes.Session + `", {method: "POST",
    headers: {"Content-Type": "application/json"},
    body: JSON.stringify({idToken})});
  if (!res.ok) throw new Error("session exchange failed: " + await res.text());
  location.assign(next);
}
const g = document.getElementById("g");
if (g) g.onclick = () =>
  firebase.auth().signInWithPopup(new firebase.auth.GoogleAuthProvider())
    .then(establish).catch(e => err.textContent = e.message);
const e = document.getElementById("e");
if (e) e.onsubmit = ev => { ev.preventDefault();
  firebase.auth().signInWithEmailAndPassword(e.email.value, e.password.value)
    .then(establish).catch(x => err.textContent = x.message); };
</script></body>`
