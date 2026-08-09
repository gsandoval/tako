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
	user   *tako.User
	// client is who sent this request; the zero value in a worker, a
	// cron or a job, where there is nobody on the other end.
	client    tako.Client
	taskIndex int
	taskCount int
}

// NewCtx wraps a context as a tako.Ctx. Generated services and tests are
// the callers; user code receives its Ctx from the framework.
func NewCtx(parent context.Context) tako.Ctx {
	return &requestCtx{Context: parent, log: slog.Default()}
}

func (c *requestCtx) User() tako.User {
	if c.user == nil {
		panic("tako: ctx.User on a request with no authenticated user — auth is declared with tako.Auth and required per-route with tako.Route{SignIn: true}")
	}
	return *c.user
}

func (c *requestCtx) MaybeUser() (tako.User, bool) {
	if c.user == nil {
		return tako.User{}, false
	}
	return *c.user, true
}

func (c *requestCtx) Log() *slog.Logger { return c.log }

func (c *requestCtx) Client() tako.Client { return c.client }

func (c *requestCtx) IsHTMX() bool { return c.isHTMX }

// Task reports the parallel-job shard (tako.Ctx contract): (0, 1)
// everywhere except inside a parallel job task.
func (c *requestCtx) Task() (int, int) {
	if c.taskCount > 1 {
		return c.taskIndex, c.taskCount
	}
	return 0, 1
}
