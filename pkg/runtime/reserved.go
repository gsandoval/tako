package runtime

import (
	"io"
	"net/http"
	"os"

	"github.com/gsandoval/tako/internal/routes"
)

// devNonce is the value `tako dev` wrote into this process's
// environment before starting it. Empty everywhere else — a deployed
// service has no dev loop to have set it — which is what keeps the
// endpoint below from existing in production.
var devNonce = os.Getenv("TAKO_DEV_NONCE")

// answerDevReady identifies this process to the dev loop that started
// it, and reports whether it answered.
//
// The dev loop cannot learn this from a TCP dial. A dial to a port
// another process already holds succeeds, and succeeds fast, so the
// probe that was meant to mean "my app is up" meant "something is up" —
// and `tako dev` printed `ready` about a stranger's server while its own
// child was exiting with an address-in-use error. Two tako apps on one
// machine is the normal case for anyone working on tako and an app
// together, and the app then answers with the OTHER one's pages, which
// reads as a broken version of the app you meant to run.
//
// The nonce is what makes the answer proof rather than evidence: a
// second tako dev on the same port answers this path too, with a
// different value.
// isDevProbe reports whether this request is the dev loop identifying
// its child — the one request a tako service answers that no client
// made and no log should carry.
func isDevProbe(r *http.Request) bool {
	return activeDevNonce() != "" && (r.URL.Path == routes.DevReady || r.URL.Path == devReadyAliasPath)
}

// devReadyFirst answers the dev loop's identity probe ahead of the app.
// Serve wraps every service with it; outside `tako dev` the nonce is
// empty and this is one string comparison per request.
func devReadyFirst(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if answerDevReady(w, r) || answerDevUsers(w, r) {
			return
		}
		h.ServeHTTP(w, r)
	})
}

func answerDevReady(w http.ResponseWriter, r *http.Request) bool {
	if !isDevProbe(r) {
		return false
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = io.WriteString(w, activeDevNonce())
	return true
}

// Reserved answers tako's own URL namespace before the app's router is
// consulted.
//
// The two handlers are separate muxes on purpose, and the separation is
// the whole point rather than an implementation detail. Go's ServeMux
// refuses to register two patterns where neither is more specific on
// both path and method, and every framework mount beside a user's
// wildcard page is that pair — /_tako/api/signed/ against
// GET /{user}/{album}/{photo}, /api/_session against
// GET /{section}/{slug}. Sharing one mux made those apps panic at
// startup, before a line of user code ran. Sharing none makes the
// conflict unrepresentable: two patterns that never meet cannot fail to
// be ordered.
//
// It cannot be written as a mount. mux.Handle("/_tako/", framework)
// looks like the idiomatic move — a ServeMux is an http.Handler — but
// registration goes through the parent's conflict check first, and
// "/_tako/" is a method-less subtree pattern, so it panics against a
// wildcard page exactly like the routes it was meant to protect. Go's
// mux has no mount primitive and no fallthrough; a matched pattern ends
// routing. So the dispatch happens above both, which is here.
//
// Precedence is unconditional and by design: nothing under /_tako/ can
// be declared, shadowed or reordered by an app, static files in a site
// directory included. tako/RESERVED-PREFIX refuses the declaration at
// analysis time so that a route typed there is a build error rather
// than a handler that silently never runs.
func Reserved(framework, app http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The dev probe used to be answered HERE, and that was the bug:
		// Reserved is only wrapped around a service that declares auth,
		// a bucket or a cron, so `hello` and `shortlink` never answered
		// it — the dev loop read the 404 as a stranger's server and
		// killed its own healthy child. It lives in Serve now, which
		// every service calls.
		if answerDevReady(w, r) || answerDevUsers(w, r) {
			return
		}
		if routes.IsReserved(r.URL.Path) {
			framework.ServeHTTP(w, r)
			return
		}
		app.ServeHTTP(w, r)
	})
}
