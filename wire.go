package tako

import "github.com/gsandoval/tako/internal/hooks"

// The runtime wires backends and handler registrations through
// internal/hooks; this init hands it a reader for Handler records
// without exporting their internals.
func init() {
	hooks.HandlerInfo = func(h any) (string, string, any) {
		hd, ok := h.(Handler)
		if !ok {
			return "", "", nil
		}
		return hd.kind, hd.source, hd.invoke
	}
	hooks.HandlerParallel = func(h any) int {
		if hd, ok := h.(Handler); ok && hd.parallel > 1 {
			return hd.parallel
		}
		return 1
	}
	// The runtime attaches the DECLARED name (the analyzer's variable
	// name, carried by the generated Register calls) to a handler by id.
	hooks.HandlerID = func(h any) int {
		hd, ok := h.(Handler)
		if !ok {
			return 0
		}
		return hd.id
	}
	hooks.DeclaredSecrets = declaredSecrets
	hooks.HTMLParts = func(h any) ([]byte, string, bool) {
		html, ok := h.(HTML)
		if !ok {
			return nil, "", false
		}
		return html.markup, html.redirect, html.fragment
	}
}
