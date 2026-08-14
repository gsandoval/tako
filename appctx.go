package tako

import (
	"context"

	"github.com/gsandoval/tako/internal/boot"
)

// appKey carries a test app's backend set inside its contexts. Resource
// operations resolve backends from the context first, so parallel
// NewTestApp instances stay isolated; production contexts carry nothing
// and fall through to the process-global wiring (hooks).
type appKey struct{}

func appFrom(ctx context.Context) *boot.App {
	a, _ := ctx.Value(appKey{}).(*boot.App)
	return a
}

// unwiredPanic is the message every resource raises when its backend is
// missing. One function because there are eight of them and they are one
// sentence: a family of copies is how the entity one ends up saying
// something the queue one does not.
//
// The fourth way is the one a reader in trouble is least likely to
// guess. A tool that writes into a deployed store — a backfill, an
// importer, an operator script — is not run by tako dev, is not a
// generated service, and must not be a test app; it boots the cloud
// backends itself, and until it does, every handle it touches panics
// here (FL26).
func unwiredPanic(backend string) string {
	return "tako: no " + backend + " backend wired — run via tako dev, a generated service, or " +
		"tako.NewTestApp; a tool that reaches a deployed app calls runtime.BootGCPWith first"
}
