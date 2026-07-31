package tako

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gsandoval/tako/internal/boot"
	"github.com/gsandoval/tako/internal/hooks"
	"github.com/gsandoval/tako/internal/secrets"
	"github.com/gsandoval/tako/pkg/backends/local"
)

// NewTestApp returns an app instance backed entirely by fresh in-process
// backends — no cloud credentials, no network. Every declared worker and
// subscriber is wired automatically; handlers are invoked directly
// (Endpoint.Call); DrainQueues waits for async work; everything is
// cleaned up when the test ends. Each call is an isolated app: contexts
// from Ctx carry this app's backends, so parallel tests don't share
// state.
func NewTestApp(tb testing.TB) *TestApp {
	tb.Helper()
	app, err := boot.Local(boot.Options{
		Dir:     tb.TempDir(),
		BaseURL: "http://tako.test",
		// Tests exercise the retry CONTRACT, not production pacing.
		Queue: local.QueueOptions{MaxAttempts: 5, Backoff: 5 * time.Millisecond},
	})
	if err != nil {
		tb.Fatalf("tako: NewTestApp: %v", err)
	}
	// Delayed publishes wait on a fake clock only Advance moves — a test
	// never sleeps out a WithDelay.
	app.Dispatcher.UseManualClock()
	ta := &TestApp{tb: tb, app: app}
	for _, h := range registeredAsync() {
		deliver := func(invoke func(Ctx, []byte) error, name string) func(context.Context, []byte) error {
			return func(ctx context.Context, payload []byte) error {
				return invoke(ta.handlerCtx(ctx, name), payload)
			}
		}
		switch h.kind {
		case "worker":
			app.Dispatcher.Consume(h.source, deliver(h.invoke, h.name))
		case "subscribe":
			app.Dispatcher.Topics().Subscribe(h.source, h.name, deliver(h.invoke, h.name))
		case "job":
			// Jobs ride the dispatcher locally: DrainQueues waits for
			// them, failures dead-letter inspectably. WithParallel runs
			// its N tasks (ctx.Task() shards) joined per execution.
			par, invoke, name := h.parallel, h.invoke, h.name
			if par < 1 {
				par = 1
			}
			app.Dispatcher.Consume(hooks.JobQueue(h.source), func(ctx context.Context, payload []byte) error {
				if par == 1 {
					return invoke(ta.handlerCtx(ctx, name), payload)
				}
				errs := make(chan error, par)
				for i := range par {
					go func(i int) {
						c := ta.handlerCtx(ctx, name).(*testCtx)
						c.taskIndex, c.taskCount = i, par
						errs <- invoke(c, payload)
					}(i)
				}
				var first error
				for range par {
					if err := <-errs; err != nil && first == nil {
						first = err
					}
				}
				return first
			})
		}
	}
	tb.Cleanup(func() {
		// Settle in-flight deliveries first: a worker still writing to
		// the temp dir would race the TempDir cleanup.
		_ = app.Dispatcher.WaitIdle(30 * time.Second)
		_ = app.Close()
	})
	return ta
}

// TestApp is an in-memory instance of the app under test.
type TestApp struct {
	tb  testing.TB
	app *boot.App
}

// SignIn returns a stub identity for name — identity injection for
// tests, not auth enforcement (WithAuth middleware arrives with the
// auth feature).
func (a *TestApp) SignIn(name string) User {
	return User{ID: name, Email: name + "@example.test", Name: name}
}

// Ctx returns a request context bound to this app's backends,
// authenticated as the given user if one is passed.
func (a *TestApp) Ctx(as ...User) Ctx {
	c := &testCtx{
		Context: context.WithValue(context.Background(), appKey{}, a.app),
		log:     slog.Default(),
	}
	if len(as) > 0 {
		u := as[0]
		c.user = &u
	}
	return c
}

// HTMX returns a Ctx like Ctx(as...) whose IsHTMX() reports true — for
// driving a dual-mode Page handler down its htmx branch (fragment swap)
// instead of the no-JS redirect. Everything else matches Ctx.
func (a *TestApp) HTMX(as ...User) Ctx {
	c := a.Ctx(as...).(*testCtx)
	c.htmx = true
	return c
}

// handlerCtx is the ctx async deliveries run under: the app's backends
// plus a logger naming the handler.
func (a *TestApp) handlerCtx(ctx context.Context, name string) Ctx {
	return &testCtx{
		Context: context.WithValue(ctx, appKey{}, a.app),
		log:     slog.Default().With("handler", name),
	}
}

// DrainQueues blocks until every published message — including chaos
// duplicates — has acked or dead-lettered.
func (a *TestApp) DrainQueues() {
	a.tb.Helper()
	if err := a.app.Dispatcher.WaitIdle(30 * time.Second); err != nil {
		a.tb.Fatalf("tako: DrainQueues: %v", err)
	}
}

// Advance moves the app's fake clock forward, releasing every delayed
// queue message (WithDelay) that has come due. It moves scheduled time
// only — retry backoff and chaos reordering keep their real
// (millisecond) timers — and DrainQueues treats not-yet-due messages as
// idle rather than waiting out their delays. Crons never ride this
// clock: they fire by name via AdvanceCron, because a cron test means
// "this cron ran once", not "time passed and whatever was due fired".
func (a *TestApp) Advance(d time.Duration) {
	a.app.Dispatcher.Advance(d)
}

// SetSecret provides a declared secret's value, removed again when the
// test ends. Secrets resolve without a Ctx (Value takes none), so unlike
// every other backend they are process-wide, not per-app.
func (a *TestApp) SetSecret(name, value string) {
	secrets.Set(name, value)
	a.tb.Cleanup(func() {
		// Cleanups run LIFO: this one fires before the app's own
		// teardown, so settle in-flight deliveries first — a handler
		// mid-delivery may still read the secret.
		_ = a.app.Dispatcher.WaitIdle(30 * time.Second)
		secrets.Unset(name)
	})
}

// AdvanceCron runs the named cron handler once, synchronously, as if its
// schedule fired — tests own scheduled time (see Advance). name is the
// handler function's name ("cleanupOrphans"), the same identity the
// wiring uses.
func (a *TestApp) AdvanceCron(name string) {
	a.tb.Helper()
	var crons []string
	for _, h := range registeredAsync() {
		if h.kind != "cron" {
			continue
		}
		if h.name == name {
			if err := h.invoke(a.handlerCtx(context.Background(), name), nil); err != nil {
				a.tb.Fatalf("tako: AdvanceCron(%q): %v", name, err)
			}
			return
		}
		crons = append(crons, h.name)
	}
	a.tb.Fatalf("tako: AdvanceCron(%q): no such cron — declared crons: %s", name, strings.Join(crons, ", "))
}

// Chaos enables chaos mode on the app's queues and topics: duplicate
// delivery, redelivery after ack, and reordering — seeded and
// reproducible, so at-least-once bugs surface locally instead of in
// production. The seed prints; set TAKO_CHAOS_SEED to replay a failure
// exactly.
func (a *TestApp) Chaos() {
	a.tb.Helper()
	seed := time.Now().UnixNano()
	if v := os.Getenv("TAKO_CHAOS_SEED"); v != "" {
		parsed, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			a.tb.Fatalf("tako: TAKO_CHAOS_SEED must be a seed integer, got %q", v)
		}
		seed = parsed
	}
	a.app.Dispatcher.EnableChaos(seed)
	a.tb.Logf("tako: chaos enabled (seed %d) — rerun with TAKO_CHAOS_SEED=%d to reproduce", seed, seed)
}

// testCtx is the Ctx implementation behind TestApp.Ctx.
type testCtx struct {
	context.Context
	user                 *User
	log                  *slog.Logger
	htmx                 bool
	taskIndex, taskCount int
}

func (c *testCtx) User() User {
	if c.user == nil {
		panic("tako: ctx.User on an unauthenticated context — pass a user to app.Ctx, or use MaybeUser")
	}
	return *c.user
}

func (c *testCtx) MaybeUser() (User, bool) {
	if c.user == nil {
		return User{}, false
	}
	return *c.user, true
}

func (c *testCtx) Log() *slog.Logger { return c.log }

func (c *testCtx) IsHTMX() bool { return c.htmx }

func (c *testCtx) Task() (int, int) {
	if c.taskCount > 1 {
		return c.taskIndex, c.taskCount
	}
	return 0, 1
}
