package tako

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/gsandoval/tako/internal/boot"
	"github.com/gsandoval/tako/internal/hooks"
	"github.com/gsandoval/tako/internal/secrets"
	"github.com/gsandoval/tako/internal/trace"
	"github.com/gsandoval/tako/pkg/backends"
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
	// never sleeps out a PublishAfter delay.
	app.Dispatcher.UseManualClock()
	ta := &TestApp{tb: tb, app: app}
	registerTestIdentities()
	for _, h := range registeredAsync() {
		deliver := func(invoke func(Ctx, []byte) error, kind, name string) func(context.Context, []byte) error {
			return func(ctx context.Context, payload []byte) error {
				tctx, finish := ta.spanHandlerCtx(ctx, kind, name)
				err := invoke(tctx, payload)
				finish(0, err)
				return err
			}
		}
		switch h.kind {
		case "worker":
			app.Dispatcher.Consume(h.source, deliver(h.invoke, "worker", h.name))
		case "subscribe":
			app.Dispatcher.Topics().Subscribe(h.source, h.name, deliver(h.invoke, "subscribe", h.name))
		case "job":
			// Jobs ride the dispatcher locally: DrainQueues waits for
			// them, failures dead-letter inspectably. RunnerConfig.Parallel runs
			// its N tasks (ctx.Task() shards) joined per execution.
			par, attempts, invoke, name := h.parallel, h.attempts, h.invoke, h.name
			if par < 1 {
				par = 1
			}
			if attempts > 0 {
				app.Dispatcher.SetQueueDelivery(hooks.JobQueue(h.source), attempts, 0, 0)
			}
			app.Dispatcher.Consume(hooks.JobQueue(h.source), func(ctx context.Context, payload []byte) error {
				if par == 1 {
					tctx, finish := ta.spanHandlerCtx(ctx, "job", name)
					err := invoke(tctx, payload)
					finish(0, err)
					return err
				}
				errs := make(chan error, par)
				for i := range par {
					go func(i int) {
						tctx, finish := ta.spanHandlerCtx(ctx, "job", name)
						c := tctx.(*testCtx)
						c.taskIndex, c.taskCount = i, par
						err := invoke(c, payload)
						finish(0, err)
						errs <- err
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

// registerTestIdentities gives every declared handler a lease identity
// under test. Deployed, the generated main supplies the DECLARED
// (variable) name; a test has no generated main, so the function name
// stands in — chosen over the registration id because a test that
// simulates an overlapping cron run has to be able to NAME the lease:
//
//	tako.Lock("cron-cleanupOrphans").Do(...)   // hold it, then AdvanceCron
//
// The key is internal either way; both forms are stable within their
// scope, which is all a lease requires.
func registerTestIdentities() {
	for _, h := range registeredAsync() {
		if h.name != "" {
			hooks.SetHandlerIdentity(h.id, h.name)
		}
	}
}

// TestApp is an in-memory instance of the app under test.
type TestApp struct {
	tb  testing.TB
	app *boot.App
}

// SignIn returns a stub identity for name — identity injection for
// tests, not auth enforcement (sign-in middleware arrives with the
// auth feature).
//
// Roles ride along, so a test says who somebody IS in the same breath
// as their name:
//
//	editor := app.SignIn("ana", Editor)
//	stranger := app.SignIn("sam")   // signed in, and behind every door
func (a *TestApp) SignIn(name string, roles ...RoleHandle) User {
	u := User{ID: name, Email: name + "@example.test", Name: name, EmailVerified: true}
	for _, r := range roles {
		u.Roles = append(u.Roles, r.Name())
	}
	if a.app != nil && a.app.Users != nil {
		ctx := context.Background()
		rec, err := a.app.Users.ByEmail(ctx, u.Email)
		if errors.Is(err, backends.ErrNotFound) {
			rec, err = a.app.Users.Create(ctx, backends.NewUserSpec{
				ID:            name,
				Email:         u.Email,
				Name:          name,
				Roles:         u.Roles,
				EmailVerified: true,
			})
		} else if err == nil && !sameStrings(rec.Roles, u.Roles) {
			if err = a.app.Users.SetRoles(ctx, rec.ID, u.Roles); err == nil {
				rec.Roles = u.Roles
			}
		}
		if err == nil && rec.ID != "" {
			u.ID = rec.ID
		}
	}
	return u
}

// Handler is the app's real HTTP surface: every declared route with the
// binder codegen generates for it, RequireAuth and RequirePolicy and
// CacheFor as declared, the site handler last, the status page, and
// tako's own /_tako/ endpoints — assembled by the SAME emitter that
// builds the deployed service.
//
//	app := tako.NewTestApp(t)
//	srv := httptest.NewServer(app.Handler())
//	res, _ := http.Get(srv.URL + "/admin")   // 401, through YOUR status page
//
// Before this, asserting real HTML meant hand-writing the mounts and the
// binders, and that mirror drifted: routes nothing drove, and mount
// options — MaxAge, RequireAuth, RequirePolicy — that no test could see
// at all. Fidelity here is by construction rather than by care.
//
// It needs the generated harness, which `tako test` maps in; plain
// `go test` cannot see it, and this says so rather than handing back an
// empty mux.
func (a *TestApp) Handler() http.Handler {
	a.tb.Helper()
	if hooks.TestMux == nil {
		a.tb.Fatalf("tako: app.Handler needs the generated mounts, which `go test` cannot see.\n" +
			"      Run `tako test` (it regenerates first) — or `tako test ./pkg/...` for a subset.")
		return nil
	}
	base := hooks.TestMux()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base.ServeHTTP(w, r.WithContext(withApp(r.Context(), a.app)))
	})
}

func withApp(ctx context.Context, app *boot.App) context.Context {
	ctx = boot.WithApp(ctx, app)
	return context.WithValue(ctx, appKey{}, app)
}

// Ctx returns a request context bound to this app's backends,
// authenticated as the given user if one is passed.
func (a *TestApp) Ctx(as ...User) Ctx {
	base := withApp(context.Background(), a.app)
	base = trace.WithContext(base, trace.Context{
		TraceID: trace.NewTraceID(),
		SpanID:  trace.NewSpanID(),
		Sampled: true,
	})
	base = trace.WithPropagatedFlags(base, nil)
	base = withFlagPinHolder(base)
	c := &testCtx{
		Context: base,
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
		Context: withApp(ctx, a.app),
		log:     slog.Default().With("handler", name),
	}
}

func (a *TestApp) spanHandlerCtx(ctx context.Context, kind, name string) (Ctx, func(int, error)) {
	base := withApp(ctx, a.app)
	base, _, finish := trace.StartRoot(base, kind, name, "", "", "", "")
	if a.app != nil && a.app.Flags != nil {
		base = backends.WithFlagSnapshot(base, a.app.Flags.Snapshot())
	}
	return &testCtx{
		Context: base,
		log:     slog.Default().With("handler", name),
	}, finish
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
// queue message (PublishAfter) that has come due. It moves scheduled time
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

// SetFlag overrides a declared tako.Flag[T] in this test app's flag store.
// Pass the flag handle (or its name) and the override value:
//
//	app.SetFlag(NewCheckout, true)
//	app.SetFlag(CheckoutFlow, "treatment")
func (a *TestApp) SetFlag(flag any, value any) {
	a.tb.Helper()
	name, err := flagNameOf(flag)
	if err != nil {
		a.tb.Fatalf("tako: SetFlag: %v", err)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		a.tb.Fatalf("tako: SetFlag(%s): marshal value: %v", name, err)
	}
	if err := a.app.Flags.Put(context.Background(), backends.FlagRule{
		Name:      name,
		Value:     raw,
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		a.tb.Fatalf("tako: SetFlag(%s): %v", name, err)
	}
}

// AdvanceCron runs a cron once, synchronously, as if its schedule fired
// — tests own scheduled time (see Advance). Pass the cron itself:
//
//	app.AdvanceCron(Cleanup)
//
// The var IS the cron's identity — it is what tako deploys it as, and
// what Cloud Scheduler addresses — so referencing it directly is both
// the most faithful thing a test can say and the only form the compiler
// can check. It also matches how every other declaration is reached in
// a test: Create.Call, links.Get, thumbs.Publish.
func (a *TestApp) AdvanceCron(cron Handler) {
	a.tb.Helper()
	if cron.id == 0 {
		a.tb.Fatal("tako: AdvanceCron: the handler is empty — pass the var a tako.Cron was assigned to, like app.AdvanceCron(Cleanup)")
		return
	}
	if cron.kind != "cron" {
		a.tb.Fatalf("tako: AdvanceCron: that is a %s, not a cron — %s", cron.kind, wrongKindHint(cron.kind))
		return
	}
	tctx, finish := a.spanHandlerCtx(context.Background(), "cron", cron.name)
	err := cron.invoke(tctx, nil)
	finish(0, err)
	if err != nil {
		a.tb.Fatalf("tako: AdvanceCron(%s): %v", cron.name, err)
	}
}

// wrongKindHint says how the handler you DID pass gets run, so the
// error ends somewhere useful instead of just refusing.
func wrongKindHint(kind string) string {
	switch kind {
	case "worker":
		return "workers run when their queue delivers, so publish to the queue and DrainQueues"
	case "subscribe":
		return "subscribers run when their topic publishes, so publish the event and DrainQueues"
	case "job":
		return "jobs run when started, so call the job's Run and DrainQueues"
	case "singleton":
		return "a singleton loops for the life of its service; there is no single run to advance"
	}
	return "only tako.Cron handlers can be advanced"
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

// AuditRecords returns all audit entries recorded on stream (which may be
// a *AuditHandle[E] or a stream name string) during this test run.
func (a *TestApp) AuditRecords(stream any) []AuditRecord {
	a.tb.Helper()
	if a.app == nil || a.app.Audits == nil {
		return nil
	}
	var name string
	switch s := stream.(type) {
	case string:
		name = s
	case interface{ Name() string }:
		name = s.Name()
	default:
		a.tb.Fatalf("tako: AuditRecords wants a *tako.AuditHandle[E] or stream name, got %T", stream)
	}
	recs := a.app.Audits.ForStream(name)
	out := make([]AuditRecord, len(recs))
	for i, r := range recs {
		out[i] = AuditRecord{
			Stream:      r.Stream,
			ActorID:     r.ActorID,
			ActorEmail:  r.ActorEmail,
			CallerIP:    r.CallerIP,
			CallerAgent: r.CallerAgent,
			DeviceID:    r.DeviceID,
			TraceID:     r.TraceID,
			Event:       r.Event,
			RecordedAt:  r.RecordedAt,
		}
	}
	return out
}

// PushMessages returns all mobile push notifications sent on push (which
// may be a *PushHandle or channel name string) during this test run.
func (a *TestApp) PushMessages(push any) []PushMessage {
	a.tb.Helper()
	if a.app == nil || a.app.Push == nil {
		return nil
	}
	var name string
	switch p := push.(type) {
	case string:
		name = p
	case interface{ Name() string }:
		name = p.Name()
	default:
		a.tb.Fatalf("tako: PushMessages wants a *tako.PushHandle or channel name, got %T", push)
	}
	msgs := a.app.Push.Messages(name)
	out := make([]PushMessage, len(msgs))
	for i, m := range msgs {
		out[i] = PushMessage{
			Token: m.Token,
			Topic: m.Topic,
			Title: m.Title,
			Body:  m.Body,
			Data:  m.Data,
		}
	}
	return out
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

// Client is the zero value: a Ctx from NewTestApp stands for a call, not
// a request. A test that needs a client fact drives the app's real HTTP
// surface instead — app.Handler(), where the header is a header.
func (c *testCtx) Client() Client { return Client{} }

func (c *testCtx) Task() (int, int) {
	if c.taskCount > 1 {
		return c.taskIndex, c.taskCount
	}
	return 0, 1
}
