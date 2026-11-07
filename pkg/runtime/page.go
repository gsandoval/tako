package runtime

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"

	tako "github.com/gsandoval/tako"
	"github.com/gsandoval/tako/internal/hooks"
)

// csrfTokenKey carries the request's CSRF token to the template funcs.
type csrfTokenKey struct{}

// sessionUserKey carries the signed-in user to the template funcs.
type sessionUserKey struct{}

// HandlerOption tunes a generated handler wrapper.
type HandlerOption func(*handlerConf)

type handlerConf struct {
	requireAuth bool
	// policy is the NAME of the route's declared tako.Policy. The check
	// itself is not here: Endpoint.Call enforces it, so one rule runs at
	// one place whether the caller is a browser or another service. What
	// the mount adds is the name, which is what a status page needs to
	// tell this refusal from a stale CSRF token's.
	policy string
	// maxAge is Route.MaxAge in seconds: how long a shared cache may
	// keep this page. Zero is no-cache, which is every page that does
	// not ask otherwise.
	maxAge                 int
	idempotent             bool
	rateLimitRequests      int
	rateLimitWindowSeconds int
}

// RequireAuth enforces Route.SignIn: no valid session → 401 before
// the handler runs; past it, ctx.User() is guaranteed.
func RequireAuth() HandlerOption {
	return func(c *handlerConf) { c.requireAuth = true }
}

// RequirePolicy names the route's declared tako.Policy (Route.Allow) so
// a refusal from it can be told apart from every other 403 — it arrives
// at the status page as Status.Policy.
//
// It carries the name and not the handle deliberately: the policy is
// evaluated by Endpoint.Call, which is the ONE enforcement point for
// browsers, in-process callers and cross-service calls alike. A second
// check here would run an app's rule twice per request, and a rule that
// reads the entity store is not free.
func RequirePolicy(name string) HandlerOption {
	return func(c *handlerConf) { c.policy = name }
}

// CacheFor sets Route.MaxAge on a generated page handler: the response
// may be kept by shared caches for that many seconds. Without it every
// rendered page answers no-cache, which makes a read-heavy site
// revalidate every visitor's request against the app and its store.
//
// The analyzer refuses it on a SignIn route (tako/cache-signin), so a
// public cache can never be handed an authenticated page.
func CacheFor(seconds int) HandlerOption {
	return func(c *handlerConf) { c.maxAge = seconds }
}

// csrfCookie is the double-submit cookie; when auth lands (W5) the
// token binds to the session instead.
const csrfCookie = "__tako_csrf"

// csrfRejection is the exact 403 text — product UX, pinned by tests.
const csrfRejection = "CSRF token missing or invalid — forms need {{ csrf }}"

// PageHandler wraps a Page endpoint: bind the request (path/query/form
// tags), enforce CSRF on unsafe methods, invoke, and render — markup as
// a document (same header policy as the static site), tako.Redirect as
// 303 See Other (the POST-redirect-GET status). Errors render plain
// (styled error pages are a later slice): explicit statuses as-is,
// everything else an opaque 500 with the request id logged.
func PageHandler[Req any](e tako.Endpoint[Req, tako.HTML], decode func(*http.Request) (Req, error), opts ...HandlerOption) http.Handler {
	var conf handlerConf
	for _, o := range opts {
		o(&conf)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := incomingRequestID(r)
		// The session (when present) carries the user AND the CSRF
		// truth: a stolen anonymous token cannot ride a signed-in
		// session (W5). Anonymous browsers keep the double-submit
		// cookie from W2.
		session := loadSession(r)
		if conf.requireAuth && session == nil {
			// A PAGE's 401 is read by a person, so it renders like every
			// other answer tako gives (FL11). The API's stays JSON —
			// http.go writes that one, and a client parses it.
			writeStatus(w, r, tako.Status{Code: http.StatusUnauthorized, RequestID: reqID, Message: authRequired})
			return
		}
		token := ""
		if session != nil {
			token = session.CSRF
			if unsafeMethod(r.Method) && !csrfValidAgainst(r, token) {
				writeStatus(w, r, tako.Status{Code: http.StatusForbidden, RequestID: reqID, Message: csrfRejection})
				return
			}
		} else if conf.maxAge > 0 && !unsafeMethod(r.Method) {
			// A page declared cacheable is a page whose response is the
			// same for everybody, so it must carry no per-visitor state
			// — and a CSRF token is exactly that. Minting one here would
			// bake one visitor's token into a page a shared cache hands
			// to the next.
			//
			// It also makes the header REACHABLE: Cloud Run's frontend
			// downgrades public to private whenever a response carries a
			// Set-Cookie, so a cacheable page that minted a cookie was
			// never publicly cached on the visit that matters. Measured
			// live before this existed:
			//
			//	cache-control: private, max-age=300   (first visit, Set-Cookie)
			//	cache-control: public, max-age=300    (cookie already held)
			//
			// The trade is that {{ csrf }} renders empty on such a page,
			// so a cacheable page cannot carry a form. That is the same
			// bargain as tako/cache-signin, and the same reason.
		} else {
			var minted bool
			token, minted = csrfEnsure(w, r)
			if unsafeMethod(r.Method) {
				if minted || !csrfValidAgainst(r, token) {
					writeStatus(w, r, tako.Status{Code: http.StatusForbidden, RequestID: reqID, Message: csrfRejection})
					return
				}
			}
		}
		// Cross-service context (W9): session + trace ids ride as
		// values so outgoing Calls propagate them; a propagated
		// deadline bounds this handler too.
		base, cancelDeadline := callContext(r, session, reqID)
		defer cancelDeadline()
		rctx := context.WithValue(base, csrfTokenKey{}, token)
		// IsHTMX is the HX-Request header, nothing else (the F10 fence:
		// a request-shape probe — the framework never switches modes on
		// it by itself; handlers do).
		ctx := &requestCtx{Context: rctx, log: requestLogger(r, reqID),
			client: clientOf(r),
			isHTMX: r.Header.Get("HX-Request") == "true"}
		if session != nil {
			u := session.user()
			ctx.user = &u
			rctx = context.WithValue(rctx, sessionUserKey{}, ctx.user)
			ctx.Context = rctx
		}

		runWithSafety(w, r, rctx, session, reqID, true, conf, func(w http.ResponseWriter) {
			var req Req
			if decode != nil {
				var err error
				if req, err = decode(r); err != nil {
					pageError(w, r, ctx, reqID, err, conf.policy)
					return
				}
			}
			res, err := e.Call(ctx, req)
			if err != nil {
				pageError(w, r, ctx, reqID, err, conf.policy)
				return
			}
			markup, redirect, status, fragment := hooks.HTMLParts(res)
			if redirect != "" {
				if ctx.isHTMX {
					w.Header().Set("HX-Redirect", redirect)
					w.WriteHeader(http.StatusOK)
					return
				}
				if status == 0 {
					status = http.StatusSeeOther
				}
				http.Redirect(w, r, redirect, status)
				return
			}
			h := w.Header()
			ct := "text/html; charset=utf-8"
			if hooks.HTMLContentType != nil {
				if custom := hooks.HTMLContentType(res); custom != "" {
					ct = custom
				}
			}
			h.Set("Content-Type", ct)
			h.Set("Cache-Control", cacheControl(conf.maxAge))
			if fragment {
				markFragment(r)
			}
			securityHeaders(h, fragment)
			_, _ = w.Write(markup)
		})
	})
}

func unsafeMethod(m string) bool {
	return m != http.MethodGet && m != http.MethodHead && m != http.MethodOptions
}

// csrfEnsure returns the browser's token, minting (and setting) one
// when absent. minted=true on a first contact — an unsafe request
// cannot possibly carry a matching token then.
func csrfEnsure(w http.ResponseWriter, r *http.Request) (string, bool) {
	if c, err := r.Cookie(csrfCookie); err == nil && c.Value != "" {
		return c.Value, false
	}
	raw := make([]byte, 16)
	_, _ = rand.Read(raw)
	token := hex.EncodeToString(raw)
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	return token, true
}

func csrfValidAgainst(r *http.Request, token string) bool {
	sent := r.Header.Get("X-CSRF-Token")
	if sent == "" {
		sent = r.PostFormValue("_csrf")
	}
	return csrfMatches(sent, token)
}

// csrfMatches compares a submitted token in constant time.
func csrfMatches(sent, want string) bool {
	return sent != "" && subtle.ConstantTimeCompare([]byte(sent), []byte(want)) == 1
}

// pageError renders errors as pages: explicit statuses (NotFound, decode
// 400s) as-is; everything else an opaque 500 — the real error is logged
// with the request id, never sent to the client.
//
// EVERY status goes through the app's status page now, not just 404
// (FL11). The old rule — render for a 404, plain text for anything
// else, because a 500 means the process is already broken and running
// the template engine is the worst moment to depend on it — was right
// about the risk and wrong about the remedy: writeStatus catches a
// panicking status page and falls through to the same plain text. The
// downside is bounded, and the upside is that a 403 and a 500 stop
// being the two places a site drops its own skin.
func pageError(w http.ResponseWriter, r *http.Request, ctx tako.Ctx, reqID string, err error, policy string) {
	if he, ok := err.(interface{ HTTPStatus() int }); ok {
		// The policy name rides along only on a policy's refusal. A
		// stale CSRF token is also a 403 on the same route, and a status
		// page that could not tell them apart would send a signed-in
		// editor to ask for access they already have.
		named := ""
		if tako.IsDenied(err) {
			named = policy
		}
		writeStatus(w, r, tako.Status{Code: he.HTTPStatus(), RequestID: reqID, Message: err.Error(), Policy: named})
		return
	}
	ctx.Log().Error("page error", "err", err)
	writeStatus(w, r, tako.Status{Code: http.StatusInternalServerError, RequestID: reqID})
}

// (The 404 writers that used to live here are one writer now, in
// status.go — every response tako produces for itself goes through
// writeStatus, so its headers and its fallback are decided once.)

// cacheControl renders the page's caching posture. "public" is the
// point of the setting — a private max-age would only help the one
// browser that already has the page.
func cacheControl(maxAge int) string {
	if maxAge <= 0 {
		return "no-cache"
	}
	return fmt.Sprintf("public, max-age=%d", maxAge)
}
