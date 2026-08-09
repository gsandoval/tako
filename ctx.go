package tako

import (
	"context"
	"log/slog"
)

// Ctx is the per-request context passed to every handler and every resource
// operation. It carries cancellation and deadlines (it is a context.Context),
// the authenticated user, a structured trace-correlated logger, and
// request-shape hints for hypermedia handlers.
type Ctx interface {
	context.Context

	// User returns the authenticated user. It is guaranteed to be present on
	// routes declared with Route{SignIn: true}; calling it on an unauthenticated request
	// panics — use MaybeUser on public routes.
	User() User

	// MaybeUser returns the authenticated user, if any.
	MaybeUser() (User, bool)

	// Log returns a structured logger correlated with the request trace.
	Log() *slog.Logger

	// IsHTMX reports whether the request was issued by htmx, letting one
	// route serve both full pages and fragments.
	IsHTMX() bool

	// Task reports this execution's shard: (index, count). Everywhere
	// except inside a parallel job task it is (0, 1); inside one, index
	// is this task's 0-based number and count the RunnerConfig.Parallel fan-out —
	// shard work by them (hash the key, take index's share).
	Task() (index, count int)
}

// User is an authenticated identity as established by the app's Auth
// declaration.
type User struct {
	// ID is the stable identity of the user.
	ID string
	// Email is the user's email address, when the provider supplies one.
	Email string
	// Name is the user's display name, when the provider supplies one.
	Name string
	// Provider is the sign-in method that minted this session:
	// "google", "email". A session can only come from a provider the
	// app declared in tako.AuthConfig — this says which one it was, for
	// an app that declares more than one and treats them differently.
	Provider string
	// Roles are the declared roles this identity carries: an Identity
	// Platform custom claim in the cloud, the local user record on a
	// laptop. Read them with HasRole, or put a whole route behind one
	// with Route{Allow: tako.RequireRole(Editor)}.
	//
	// They are what the SESSION was minted with. A role granted or
	// revoked since then marks the session stale and is picked up on the
	// next request — see reference/auth.md.
	Roles []string
	// EmailVerified is the provider's claim about the address.
	//
	// It matters when an app authorizes BY EMAIL, which is the obvious
	// shape for an editor allowlist: such a check is only as strong as
	// the guarantee that the address belongs to whoever signed in. For
	// Google that guarantee is Google's. For email/password it is
	// whatever the Identity Platform project was configured to require,
	// which no app can see — so the claim is exposed rather than
	// assumed.
	EmailVerified bool
}
