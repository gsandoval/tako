package analyzer

// Two routes Go's router cannot order (item 27).
//
// Go 1.22's ServeMux refuses to register two patterns where neither is
// more specific than the other, and it refuses by PANICKING at startup.
// So a pair like
//
//	POST /moderate/{id}/retract
//	POST /moderate/people/{id}
//
// — a request for /moderate/people/retract matches both — is a service
// that will not boot, discovered when someone runs it. That is the
// wrong moment: routes are declared, the analyzer reads every one of
// them, and this is precisely the class of mistake it exists to catch.
//
// The check REUSES Go's algorithm rather than restating it: it registers
// the patterns on a real ServeMux and turns the panic into a
// diagnostic. A reimplementation would be a second set of precedence
// rules to keep in step with the standard library's, and the two would
// disagree exactly once, quietly, years from now.

import (
	"fmt"
	"net/http"
	"strings"
)

// MuxPattern is the route as the generated service registers it: Go's
// exact-match form for the root, so "GET /" claims the root and not
// every path under it.
//
// It lives here rather than in codegen because it is a fact about a
// declared route, and both the generator and this check need the same
// answer — checking one string and mounting another would be a check of
// nothing.
func MuxPattern(route string) string {
	if strings.HasSuffix(route, " /") {
		return route + "{$}"
	}
	return route
}

// checkRouteConflicts reports declared routes that cannot share a mux.
//
// Per SERVICE, because that is what a mux is: two packages may both
// declare GET /, and they deploy as two services that never meet.
func checkRouteConflicts(graph *AppGraph, errs *ErrorList) {
	for _, svc := range graph.Services {
		mux := http.NewServeMux()
		// Registered in declaration order, so the SECOND of a pair is
		// the one reported — the one somebody just added.
		for _, h := range graph.Handlers {
			if h.Service != svc.Name || (h.Kind != "api" && h.Kind != "page") {
				continue
			}
			if err := register(mux, MuxPattern(h.Route)); err != nil {
				*errs = append(*errs, &Error{
					Pos: h.Pos,
					Msg: fmt.Sprintf("%s cannot be mounted beside a route this service already declares: %s — "+
						"Go's router refuses a pair where neither pattern is more specific, and the service panics at startup. "+
						"Make one of them narrower (a literal segment beats a wildcard) or move one to a different path",
						ShortVar(h), conflictWith(err, h.Route)),
					Rule: "route-conflict",
				})
			}
		}
	}
}

// register adds one pattern, converting ServeMux's panic into an error.
func register(mux *http.ServeMux, pattern string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()
	mux.Handle(pattern, http.NotFoundHandler())
	return nil
}

// conflictWith pulls the OTHER pattern out of Go's panic text, so the
// message names both halves of the pair rather than only the one being
// added. The panic reads:
//
//	pattern "POST /a/{id}" (registered at …) conflicts with pattern "POST /b/{id}" …
//
// If the wording ever changes, the fallback still names this route and
// says what is wrong; only the sharper half is lost.
func conflictWith(err error, route string) string {
	text := err.Error()
	const marker = "conflicts with pattern "
	i := strings.Index(text, marker)
	if i < 0 {
		return "another declared route"
	}
	rest := text[i+len(marker):]
	if q := strings.IndexByte(rest, '"'); q >= 0 {
		rest = rest[q+1:]
		if end := strings.IndexByte(rest, '"'); end >= 0 {
			return rest[:end]
		}
	}
	return "another declared route"
}
