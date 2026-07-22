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
	hooks.DeclaredSecrets = declaredSecrets
}
