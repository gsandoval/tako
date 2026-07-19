package runtime

import (
	"context"
	"log/slog"

	tako "github.com/gsandoval/tako"
)

// requestCtx implements tako.Ctx for one request. Auth arrives in a later
// slice: User panics honestly and MaybeUser reports no user.
type requestCtx struct {
	context.Context
	log    *slog.Logger
	isHTMX bool
}

// NewCtx wraps a context as a tako.Ctx. Generated services and tests are
// the callers; user code receives its Ctx from the framework.
func NewCtx(parent context.Context) tako.Ctx {
	return &requestCtx{Context: parent, log: slog.Default()}
}

func (c *requestCtx) User() tako.User {
	panic("tako: Ctx.User on a request with no authenticated user — auth is declared with tako.Auth and required per-route with tako.WithAuth")
}

func (c *requestCtx) MaybeUser() (tako.User, bool) { return tako.User{}, false }

func (c *requestCtx) Log() *slog.Logger { return c.log }

func (c *requestCtx) IsHTMX() bool { return c.isHTMX }
