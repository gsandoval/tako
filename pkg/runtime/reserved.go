package runtime

import (
	"net/http"

	"github.com/gsandoval/tako/internal/routes"
)

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
		if routes.IsReserved(r.URL.Path) {
			framework.ServeHTTP(w, r)
			return
		}
		app.ServeHTTP(w, r)
	})
}
