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
	// routes declared with WithAuth; calling it on an unauthenticated request
	// panics — use MaybeUser on public routes.
	User() User

	// MaybeUser returns the authenticated user, if any.
	MaybeUser() (User, bool)

	// Log returns a structured logger correlated with the request trace.
	Log() *slog.Logger

	// IsHTMX reports whether the request was issued by htmx, letting one
	// route serve both full pages and fragments.
	IsHTMX() bool
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
}
