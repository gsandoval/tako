package runtime

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	tako "github.com/gsandoval/tako"
	"github.com/gsandoval/tako/internal/boot"
	"github.com/gsandoval/tako/internal/hooks"
	"github.com/gsandoval/tako/pkg/backends"
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
	ID    string `json:"id"`
	UID   string `json:"uid"`
	Email string `json:"email"`
	Name  string `json:"name"`
	// Roles are the roles the identity held when this session was
	// minted. They are stored rather than re-read per request: the
	// session lookup already happens, and asking Identity Platform for
	// a user's claims on every request would put an admin API call on
	// the hot path. A grant or revoke marks the session stale instead
	// (AB10b), and the next request refreshes exactly one of them.
	Roles         []string `json:"roles,omitempty"`
	Provider      string   `json:"provider,omitempty"`
	EmailVerified bool     `json:"emailVerified,omitempty"`
	// Stale marks a session whose roles are known to be out of date: a
	// grant or revoke landed after it was minted. The next request
	// re-reads that ONE account and rewrites this record, which is how a
	// role change takes effect without either signing anybody out or
	// putting an account read on every request.
	Stale   bool      `json:"stale,omitempty"`
	CSRF    string    `json:"csrf"`
	Created time.Time `json:"created"`
	Expires time.Time `json:"expires"`
}

func randomHex(n int) string {
	raw := make([]byte, n)
	_, _ = rand.Read(raw)
	return hex.EncodeToString(raw)
}

func entityStore(ctx context.Context) backends.Entity {
	if app := boot.FromContext(ctx); app != nil && app.Entity != nil {
		return app.Entity
	}
	return hooks.Entity
}

func usersStore(ctx context.Context) backends.Users {
	if app := boot.FromContext(ctx); app != nil && app.Users != nil {
		return app.Users
	}
	return hooks.Users
}

func topicStore(ctx context.Context) backends.Topic {
	if app := boot.FromContext(ctx); app != nil && app.Dispatcher != nil {
		return app.Dispatcher.Topics()
	}
	return hooks.Topic
}

func flagStore(ctx context.Context) backends.Flags {
	if app := boot.FromContext(ctx); app != nil && app.Flags != nil {
		return app.Flags
	}
	return hooks.Flags
}

// mintSession stores a new session record and returns it.
func mintSession(ctx context.Context, u tako.User) (*sessionRecord, error) {
	store := entityStore(ctx)
	if store == nil {
		return nil, errors.New("tako: sessions need the entity backend — Boot wires it")
	}
	rec := &sessionRecord{
		ID:            randomHex(16),
		UID:           u.ID,
		Email:         u.Email,
		Name:          u.Name,
		Roles:         u.Roles,
		Provider:      u.Provider,
		EmailVerified: u.EmailVerified,
		CSRF:          randomHex(16),
		Created:       time.Now().UTC(),
		Expires:       time.Now().UTC().Add(sessionTTL),
	}
	doc, err := json.Marshal(rec)
	if err != nil {
		return nil, err
	}
	return rec, store.Put(ctx, sessionKind, rec.ID, doc, sessionIndexed)
}

// The SDK changes a role; the runtime owns what a session is. This is
// where the two meet, wired in init rather than at boot because it is
// not a backend — it is this package's own behaviour, and it is correct
// from the moment the package is linked in.
func init() {
	hooks.StaleSessions = StaleSessions
	hooks.EndSessions = EndSessions
}

// seenKind marks identities tako has minted a session for. One tiny
// record per person, ever.
//
// This is what makes auth.FirstSignIn honest without an Identity
// Platform hook: tako cannot observe a registration, but it can observe
// the first session IT issues, and that is what the event is named for.
// The cost is one entity read per sign-in — not per request — beside the
// session write that is already happening.
const seenKind = "_tako_seen"

// announceFirstSignIn publishes auth.FirstSignIn the first time an
// identity appears, and never again.
//
// The marker is written BEFORE the publish. A crash between them loses
// the announcement rather than repeating it forever, which is the right
// way round: a subscriber that misses one is a gap somebody notices,
// while a loop that re-announces on every sign-in is a bug that looks
// like a feature until the welcome emails pile up.
func announceFirstSignIn(ctx context.Context, u tako.User) {
	store := entityStore(ctx)
	topic := topicStore(ctx)
	if store == nil || topic == nil || u.ID == "" {
		return
	}
	seen, err := store.Exists(ctx, seenKind, u.ID)
	if err != nil || seen {
		return
	}
	doc, err := json.Marshal(map[string]string{"uid": u.ID, "email": u.Email})
	if err != nil {
		return
	}
	if err := store.Put(ctx, seenKind, u.ID, doc, seenIndexed); err != nil {
		return
	}
	raw, err := json.Marshal(u)
	if err != nil {
		return
	}
	if err := topic.Publish(ctx, hooks.AuthFirstSignInSource, raw); err != nil {
		slog.Error("tako: announcing a first sign-in failed", "user", u.ID, "err", err)
	}
}

// StaleSessions marks every live session of an account for refresh.
// The SDK calls it through the boot seam when a role changes.
//
// A query, not a scan of everything: sessions are indexed by uid for
// exactly this. An account with no live session costs one empty query
// and needs nothing else — their next sign-in reads the roles fresh.
func StaleSessions(ctx context.Context, uid string) error {
	store := entityStore(ctx)
	return eachSession(ctx, uid, func(rec sessionRecord) error {
		if rec.Stale {
			return nil
		}
		rec.Stale = true
		doc, err := json.Marshal(rec)
		if err != nil {
			return err
		}
		return store.Put(ctx, sessionKind, rec.ID, doc, sessionIndexed)
	})
}

// EndSessions deletes every session of an account — what disabling or
// deleting one means. Revocation is real here for the same reason
// sign-out is: the cookie carries an id, and the record is the identity.
func EndSessions(ctx context.Context, uid string) error {
	store := entityStore(ctx)
	return eachSession(ctx, uid, func(rec sessionRecord) error {
		return store.Delete(ctx, sessionKind, rec.ID)
	})
}

// sessionUIDField is the ONE field a session is ever looked up by, and
// it is a constant so that the query below and the index declared for
// the write CANNOT disagree.
//
// That is not tidiness. eachSession queries sessions by uid, and
// sign-out, revocation and role stale-marking are all that query. If
// the write declared a different field — or none — nothing would error:
// a Datastore filter on an unindexed property matches nothing and
// reports success, so revocation would quietly stop revoking. A test
// would not catch it either, because the local backend scans and
// indexes nothing, so it answers the query whatever was declared.
//
// Two names that must agree, one of which only fails in production, is
// exactly the shape FL20 exists to remove. So there are not two names.
const sessionUIDField = "uid"

var sessionIndexed = []string{sessionUIDField}

// seenIndexed is empty, and correctly so: _tako_seen is read by Exists
// on a key and never queried. Naming it anyway keeps the two kinds
// symmetric, so a reader can see the answer was chosen rather than
// forgotten.
var seenIndexed []string

func eachSession(ctx context.Context, uid string, fn func(sessionRecord) error) error {
	store := entityStore(ctx)
	if store == nil {
		return nil
	}
	docs, err := store.Query(ctx, sessionKind, backends.EntityQuery{
		Filters: []backends.Filter{{Field: sessionUIDField, Op: "=", Value: uid}},
	})
	if err != nil {
		return err
	}
	for _, doc := range docs {
		var rec sessionRecord
		if err := json.Unmarshal(doc, &rec); err != nil {
			continue // a record this process cannot read is not one it can fix
		}
		if err := fn(rec); err != nil {
			return err
		}
	}
	return nil
}

// refreshRoles re-reads one account and rewrites the session with what
// it finds. Called only for a session something marked stale, so the
// steady-state cost of the whole mechanism is zero.
func refreshRoles(ctx context.Context, rec *sessionRecord) {
	users := usersStore(ctx)
	store := entityStore(ctx)
	if users == nil || store == nil {
		return
	}
	account, err := users.Get(ctx, rec.UID)
	if err != nil {
		// The account is gone or unreadable. Leaving the session as it
		// is means the roles stay what they were, which is the safe
		// direction: a failed refresh must not silently GRANT anything.
		return
	}
	rec.Roles, rec.Stale = account.Roles, false
	if doc, err := json.Marshal(rec); err == nil {
		_ = store.Put(ctx, sessionKind, rec.ID, doc, sessionIndexed)
	}
}

// user is the identity a session record stands for. It exists because
// four handlers built this struct field by field, and the fifth field
// (Roles) would have been dropped by whichever one nobody edited —
// a bug that does not fail, it just quietly refuses somebody at a door.
func (s *sessionRecord) user() tako.User {
	if s == nil {
		return tako.User{}
	}
	return tako.User{
		ID: s.UID, Email: s.Email, Name: s.Name,
		Roles: s.Roles, Provider: s.Provider, EmailVerified: s.EmailVerified,
	}
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
	if id == "" {
		if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
			id = strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
		}
	}
	store := entityStore(r.Context())
	if id == "" || store == nil {
		return nil
	}
	doc, err := store.Get(r.Context(), sessionKind, id)
	if err != nil {
		return nil
	}
	var rec sessionRecord
	if json.Unmarshal(doc, &rec) != nil {
		return nil
	}
	if time.Now().After(rec.Expires) {
		_ = store.Delete(r.Context(), sessionKind, rec.ID)
		return nil
	}
	// A role changed since this session was minted. One account read,
	// once, on the first request after the change — and then never
	// again until the next one.
	if rec.Stale {
		refreshRoles(r.Context(), &rec)
	}
	return &rec
}

// isCrossSiteCookieRequest reports whether an HTTP request carries a browser
// _session cookie without an explicit Authorization/X-Tako-Session header and
// was initiated from a cross-site origin (Sec-Fetch-Site: cross-site).
func isCrossSiteCookieRequest(r *http.Request) bool {
	if !strings.EqualFold(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")), "cross-site") {
		return false
	}
	if r.Header.Get(sessionHeader) != "" || strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		return false
	}
	c, err := r.Cookie(sessionCookie)
	return err == nil && c.Value != ""
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
				// Roles ride the dev sign-in because there is no
				// Identity Platform on a laptop to hold a claim. Ticking
				// them is deliberate rather than automatic: a local
				// session that silently held every declared role would
				// make the refusal path — the half of authorization
				// worth walking — unreachable in dev.
				//
				// The sign-in also UPSERTS the account, because in the
				// cloud a session cannot exist without one: Identity
				// Platform holds the account and issues the token. A dev
				// session that skipped it left auth.Users().List() empty
				// while somebody was demonstrably signed in, so an admin
				// screen worked deployed and was inert on a laptop.
				var err error
				explicitRoles := r.PostFormValue("dev_roles_form") != "" || len(r.PostForm["dev_role"]) > 0
				u, err = devAccount(
					r.Context(),
					name,
					r.PostForm["dev_role"],
					explicitRoles,
					r.PostFormValue("dev_toggle_role"),
					r.PostFormValue("dev_provider"),
				)
				if err != nil {
					http.Error(w, "dev sign-in: "+err.Error(), http.StatusInternalServerError)
					return
				}
			} else if email := r.PostFormValue("email"); email != "" && isLocalBackend() {
				if ok, retryAfter := checkRateLimit(r.Context(), r, nil, handlerConf{rateLimitRequests: 10, rateLimitWindowSeconds: 60}); !ok {
					w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
					http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
					return
				}
				// AB10c: signing in with an email and a password, on a
				// laptop.
				//
				// In the cloud this exchange belongs to Identity
				// Platform — the browser posts credentials to Google and
				// tako only ever sees the ID token that comes back. There
				// is no Identity Platform locally, so tako verifies
				// against the account store it owns, and this branch is
				// LOCAL-ONLY by construction: it cannot be reached in a
				// deployed service, which is what keeps password
				// verification out of the production path entirely.
				//
				// Without it, auth.Users().Create(email, password) makes
				// an account a laptop cannot sign in as, and the local
				// half of the accounts contract is a stub.
				verified, err := passwordSignIn(r.Context(), email, r.PostFormValue("password"))
				if err != nil {
					// One message for a wrong address and a wrong
					// password. Which of the two it was is exactly what
					// somebody guessing wants to learn.
					http.Error(w, signInRefused, http.StatusUnauthorized)
					return
				}
				u = verified
			} else {
				var body struct {
					IDToken  string `json:"idToken"`
					Email    string `json:"email"`
					Password string `json:"password"`
				}
				raw, _ := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
				if json.Unmarshal(raw, &body) != nil || (body.IDToken == "" && body.Email == "") {
					http.Error(w, "POST {\"idToken\": …} or {\"email\": …, \"password\": …} (the Identity Platform sign-in result)", http.StatusBadRequest)
					return
				}
				if body.IDToken == "" && body.Email != "" {
					if ok, retryAfter := checkRateLimit(r.Context(), r, nil, handlerConf{rateLimitRequests: 10, rateLimitWindowSeconds: 60}); !ok {
						w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
						http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
						return
					}
					if isLocalBackend() {
						verified, err := passwordSignIn(r.Context(), body.Email, body.Password)
						if err != nil {
							http.Error(w, signInRefused, http.StatusUnauthorized)
							return
						}
						u = verified
					} else {
						idTok, err := exchangeCloudPassword(r.Context(), body.Email, body.Password)
						if err != nil {
							http.Error(w, signInRefused, http.StatusUnauthorized)
							return
						}
						verified, err := verifyIDToken(r.Context(), idTok)
						if err != nil {
							http.Error(w, "the ID token did not verify: "+err.Error(), http.StatusUnauthorized)
							return
						}
						u = verified
					}
				} else {
					verified, err := verifyIDToken(r.Context(), body.IDToken)
					if err != nil {
						http.Error(w, "the ID token did not verify: "+err.Error(), http.StatusUnauthorized)
						return
					}
					u = verified
				}
			}
			rec, err := mintSession(r.Context(), u)
			if err != nil {
				http.Error(w, "session store error", http.StatusInternalServerError)
				return
			}
			// The first time tako has ever seen this identity. After the
			// session exists, so a subscriber that reads the session
			// finds one — and best-effort, because a welcome email
			// failing must not fail a sign-in.
			announceFirstSignIn(r.Context(), u)
			setSessionCookie(w, r, rec.ID, rec.Expires)
			// The dev bar (and dev sign-in page) post plain forms:
			// bounce to next, else back to the page.
			if r.PostFormValue("dev_user") != "" || r.PostFormValue("next") != "" {
				// Same guard as the sign-in page's: a bare "starts with /"
				// admits //evil.example, and this one ends in a Location
				// header. The Referer fallback goes through it too — that
				// header is the browser's, not ours.
				rawNext := r.PostFormValue("next")
				to := safeNext(rawNext)
				if rawNext == "" {
					if ref := r.Referer(); ref != "" {
						if u, err := url.Parse(ref); err == nil && u.Host == r.Host {
							to = safeNext(u.RequestURI())
						}
					}
				}
				http.Redirect(w, r, to, http.StatusSeeOther)
				return
			}
			writeJSON(w, http.StatusOK, map[string]string{"uid": u.ID, "email": u.Email, "token": rec.ID})
		case http.MethodDelete:
			if isCrossSiteCookieRequest(r) {
				http.Error(w, "cross-site cookie request refused", http.StatusForbidden)
				return
			}
			if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
				if store := entityStore(r.Context()); store != nil {
					_ = store.Delete(r.Context(), sessionKind, c.Value)
				}
			}
			setSessionCookie(w, r, "", time.Unix(0, 0))
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

// SignOutHandler is the dev bar's form-friendly sign-out (browsers
// cannot DELETE from a form); it shares DELETE's behavior.
func SignOutHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
			if store := entityStore(r.Context()); store != nil {
				_ = store.Delete(r.Context(), sessionKind, c.Value)
			}
		}
		setSessionCookie(w, r, "", time.Unix(0, 0))
		if r.Header.Get("Accept") != "" && strings.Contains(r.Header.Get("Accept"), "text/html") {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// safeNext bounds where a ?next= may send someone: this origin, by
// path, or nowhere.
//
// The old guard was strings.HasPrefix(next, "/"), which admits
// "//evil.example" — protocol-relative, and absolute to every browser —
// and "/\evil.example", which browsers normalise to the same thing. It
// also admitted control characters, which are stripped before the URL is
// resolved, so "/\t/evil.example" becomes "//evil.example" after the
// guard has already passed it.
//
// What it does NOT do is bound the characters: "/a?q=<b>" is a perfectly
// ordinary path, so every sink still escapes for its own context. This
// function answers "whose origin", not "is it printable".
func safeNext(raw string) string {
	if raw == "" || raw[0] != '/' {
		return "/"
	}
	for _, r := range raw {
		// Anything a browser strips before resolving the URL cannot be
		// allowed to change what the URL means after this check.
		if r < 0x20 || r == 0x7f {
			return "/"
		}
	}
	if len(raw) > 1 && (raw[1] == '/' || raw[1] == '\\') {
		return "/"
	}
	return raw
}

// exchangeCloudPassword exchanges email and password credentials against
// Identity Platform's accounts:signInWithPassword endpoint for programmatic
// API and agent sign-in when the app declared Password: true.
func exchangeCloudPassword(ctx context.Context, email, password string) (string, error) {
	if !slices.Contains(declaredProviders, "email") {
		return "", errors.New("this app does not declare tako.AuthConfig{Password: true}")
	}
	apiKey := os.Getenv("TAKO_AUTH_APIKEY")
	if apiKey == "" {
		return "", errors.New("sign-in is not configured")
	}
	payload, err := json.Marshal(map[string]any{
		"email":             strings.TrimSpace(email),
		"password":          password,
		"returnSecureToken": true,
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://identitytoolkit.googleapis.com/v1/accounts:signInWithPassword?key="+url.QueryEscape(apiKey),
		bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", errors.New("invalid credentials")
	}
	var out struct {
		IDToken string `json:"idToken"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil || out.IDToken == "" {
		return "", errors.New("invalid response from identity provider")
	}
	return out.IDToken, nil
}
