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
