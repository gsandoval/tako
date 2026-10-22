package tako

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gsandoval/tako/internal/hooks"
	"github.com/gsandoval/tako/pkg/backends"
)

// Handlers are declared exactly like resources: a package-level var, a
// constructor, options. The var is the registration, the documentation,
// and — for API and Page — the callable endpoint. Handler bodies are
// ordinary (usually unexported) functions.

// Endpoint is a declared API or Page route. The value is the callable
// surface: within a package Call is a direct invocation, and an exported
// Endpoint var is callable from other service packages as a typed RPC.
type Endpoint[Req, Res any] struct {
	route string
	pkg   string // declaring package path — the callee identity remote transports key on
	cfg   []Route
	fn    func(Ctx, Req) (Res, error)
}

// Call invokes the endpoint. In dev, tests, and co-located packages this is
// a direct in-process call; against a remote service it is an authenticated
// HTTP call. The hop is deliberately visible at the call site.
func (e Endpoint[Req, Res]) Call(ctx Ctx, req Req) (Res, error) {
	if e.fn == nil {
		panic("tako: Call on a zero Endpoint — endpoints are declared with tako.API or tako.Page")
	}
	// A remote transport, when the generated main wired one for this
	// endpoint's service (cloud only — dev and tests stay in-process,
	// and so do co-located packages, which are never registered).
	if raw := hooks.RemoteCallFor(e.pkg + " " + e.route); raw != nil {
		t, ok := raw.(func(Ctx, Req) (Res, error))
		if !ok {
			// A registered transport of the wrong type used to fall
			// through to the direct call below — which runs the CALLEE's
			// handler inside the CALLER's process, against the caller's
			// backends: another service's entity store, silently, with a
			// successful-looking result. A wiring bug has to be loud.
			var zero Res
			return zero, fmt.Errorf("tako: the remote transport registered for %s %s has type %T, "+
				"not func(tako.Ctx, %T) (%T, error) — generated code wired this, so it is a tako bug; "+
				"re-run tako deploy, and file it if it persists",
				e.pkg, e.route, raw, req, zero)
		}
		// The callee's own mount enforces its gates, and it is the
		// only side that can: a policy may read the callee's store.
		return t(ctx, req)
	}
	// The declared gates, for the direct call — the same rule the
	// generated mount applies to an HTTP request. Without this, a
	// cross-package call would walk past the callee's authorization at
	// exactly the boundary that has one.
	if err := authorize(ctx, e.cfg); err != nil {
		var zero Res
		return zero, err
	}
	return e.fn(ctx, req)
}

// Handler is a declared asynchronous or scheduled handler binding, returned
// by Worker, Subscribe, Runner, Cron, and Singleton.
type Handler struct {
	kind   string
	source string
	// name is the handler FUNCTION's name — a log label, nothing more.
	// It is deliberately not an identity: a function name is not what
	// tako deploys a handler as, and using it as one gave crons a
	// second name that disagreed with the deployed one.
	name string
	// id is this declaration's registration number, unique within the
	// process and assigned in declaration order. It is how a Handler
	// VALUE is matched — app.AdvanceCron(Cleanup) — without comparing
	// function pointers, and how the runtime attaches the declared name.
	id       int
	parallel int           // RunnerConfig.Parallel fan-out for jobs; 0/1 = one task
	attempts int           // RunnerConfig.Attempts for jobs
	timeout  time.Duration // RunnerConfig.Timeout / CronConfig.Deadline / SingletonConfig.Timeout
	timezone string        // CronConfig.Timezone
	invoke   func(Ctx, []byte) error
}

// Async handler constructors also append their record to this registry so
// NewTestApp can wire every worker and subscriber without a handler list.
// The append is order-independent and read only by tests — production
// mains keep their explicit, generated Register lines (the approved
// rule-1 carve-out; see the F7 task log).
var (
	registryMu    sync.Mutex
	asyncRegistry []Handler
)

// handlerSeq numbers declarations. Ids start at 1 so a zero Handler —
// a var that was never assigned a constructor — is distinguishable from
// a real one and can be refused by name rather than silently missing.
var handlerSeq int

// nextHandlerID reserves an id BEFORE the Handler is built, because the
// singleflight closure has to capture it: the lease key is resolved at
// invoke time from the id, and the closure exists before register runs.
func nextHandlerID() int {
	registryMu.Lock()
	defer registryMu.Unlock()
	handlerSeq++
	return handlerSeq
}

func register(h Handler) Handler {
	registryMu.Lock()
	defer registryMu.Unlock()
	asyncRegistry = append(asyncRegistry, h)
	return h
}

func registeredAsync() []Handler {
	registryMu.Lock()
	defer registryMu.Unlock()
	return append([]Handler{}, asyncRegistry...)
}

// Secret declarations register their names the same way, so boot can
// refuse to start with any declared secret unset (same carve-out).
var secretRegistry []string

func registerSecret(name string) {
	registryMu.Lock()
	defer registryMu.Unlock()
	secretRegistry = append(secretRegistry, name)
}

func declaredSecrets() []string {
	registryMu.Lock()
	defer registryMu.Unlock()
	return append([]string{}, secretRegistry...)
}

// fnName names a handler function for test wiring: the last segment of
// its qualified name ("shortlink.countClick" → "countClick").
// pkgOf is the declaring package path of a handler function —
// constructor-time reflection (never the request path). The cut is the
// first dot after the last slash: closures carry extra .funcN segments.
func pkgOf(fn any) string {
	pc := reflect.ValueOf(fn).Pointer()
	full := runtime.FuncForPC(pc).Name()
	slash := strings.LastIndexByte(full, '/')
	if dot := strings.IndexByte(full[slash+1:], '.'); dot >= 0 {
		return full[:slash+1+dot]
	}
	return full
}

func fnName(fn any) string {
	pc := reflect.ValueOf(fn).Pointer()
	full := runtime.FuncForPC(pc).Name()
	if i := strings.LastIndexByte(full, '.'); i >= 0 {
		return full[i+1:]
	}
	return full
}

// API declares a synchronous JSON endpoint on route (a Go 1.22+
// method-and-pattern route like "POST /api/photos", constant —
// analyzer-enforced). Req fields bind by tag — path:, query:, form: — and
// untagged fields bind from the JSON body; binding code is generated
// statically. An empty Req struct is fine.
func API[Req, Res any](route string, fn func(Ctx, Req) (Res, error), cfg ...Route) Endpoint[Req, Res] {
	return Endpoint[Req, Res]{route: route, pkg: pkgOf(fn), cfg: cfg, fn: fn}
}

// Page declares a server-rendered route. It binds Req exactly like API
// (form: tags cover browser form posts) and returns HTML; CSRF is enforced
// automatically on unsafe-method page routes.
func Page[Req any](route string, fn func(Ctx, Req) (HTML, error), cfg ...Route) Endpoint[Req, HTML] {
	return Endpoint[Req, HTML]{route: route, pkg: pkgOf(fn), cfg: cfg, fn: fn}
}

// Worker binds fn as the single consumer of queue. Like every async
// handler it is func(Ctx, T) error: return nil to ack, an error to retry,
// tako.Permanent to give up and dead-letter. A queue with two workers is
// an analysis error.
func Worker[T any](queue *QueueHandle[T], fn func(Ctx, T) error, cfg ...Compute) Handler {
	return register(Handler{
		kind:   "worker",
		id:     nextHandlerID(),
		source: queue.name,
		name:   fnName(fn),
		invoke: func(ctx Ctx, raw []byte) error {
			var msg T
			if err := json.Unmarshal(raw, &msg); err != nil {
				return Permanent(fmt.Errorf("payload does not decode: %w", err))
			}
			return fn(ctx, msg)
		},
	})
}

// Subscribe binds fn as one subscriber of an event source — a topic or a
// bucket event. Subscribers fan out independently: each gets every event,
// retries on its own, and dead-letters on its own; one subscriber failing
// never delays another. Like every async handler it is func(Ctx, T) error:
// return nil to ack, an error to retry, tako.Permanent to give up.
func Subscribe[T any](source EventSource[T], fn func(Ctx, T) error, cfg ...Compute) Handler {
	return register(Handler{
		kind:   "subscribe",
		id:     nextHandlerID(),
		source: source.sourceName(),
		name:   fnName(fn),
		invoke: func(ctx Ctx, raw []byte) error {
			var msg T
			if err := json.Unmarshal(raw, &msg); err != nil {
				return Permanent(fmt.Errorf("payload does not decode: %w", err))
			}
			return fn(ctx, msg)
		},
	})
}

// Cron declares a scheduled handler: func(Ctx) error, run on the given
// schedule ("every <duration>" for now — cron expressions arrive with
// cloud scheduling). The schedule must be a string constant
// (analyzer-enforced); CronConfig.Singleflight collapses overlapping
// runs. Cron handlers run inside the package's app service. In tests,
// app.AdvanceCron(cron) fires one run synchronously.
func Cron(schedule string, fn func(Ctx) error, cfg ...CronConfig) Handler {
	id := nextHandlerID()
	var timezone string
	var deadline time.Duration
	var singleflight bool
	for _, c := range cfg {
		if c.Timezone != "" {
			timezone = c.Timezone
		}
		if c.Deadline > 0 {
			deadline = c.Deadline
		}
		if c.Singleflight {
			singleflight = true
		}
	}
	baseFn := fn
	if deadline > 0 {
		d := deadline
		inner := baseFn
		baseFn = func(ctx Ctx) error {
			c, cancel := context.WithTimeout(ctx, d)
			defer cancel()
			return inner(reroot(ctx, c))
		}
	}
	invoke := func(ctx Ctx, _ []byte) error { return baseFn(ctx) }
	if singleflight {
		invoke = singleflightInvoke(id, baseFn)
	}
	return register(Handler{
		kind:     "cron",
		source:   schedule,
		name:     fnName(fn),
		id:       id,
		timezone: timezone,
		timeout:  deadline,
		invoke:   invoke,
	})
}

// leaseKey names a handler's lease. It resolves at INVOKE time, not at
// declaration, because the declared name arrives later: the generated
// main's RegisterCron hands the runtime the variable name the analyzer
// read, and that is the only identity stable across instances and
// deploys — and the only one that cannot collide when two crons share
// one function.
//
// The lease key is the ONE place a second name survives this slice, and
// deliberately: NewTestApp seeds the identity from the function name
// (see registerTestIdentities) so a test that simulates an overlapping
// run can NAME the lease — tako.Lock("cron-cleanupOrphans") — which an
// id could not. It is internal plumbing either way; what matters is
// that the key is stable within its scope, and both are.
func leaseKey(id int) string {
	if declared, ok := hooks.HandlerIdentity(id); ok {
		return "cron-" + declared
	}
	return "cron-#" + strconv.Itoa(id)
}

func singleflightInvoke(id int, fn func(Ctx) error) func(Ctx, []byte) error {
	return func(ctx Ctx, _ []byte) error {
		lease := hooks.Lease
		if a := appFrom(ctx); a != nil && a.Lease != nil {
			lease = a.Lease
		}
		if lease == nil {
			return fn(ctx) // unwired (bare test): run plainly
		}
		key := leaseKey(id)
		ran, err := backends.TryDoLease(ctx, lease, key, hooks.LeaseOwner, backends.LeaseTTL, func(c context.Context) error {
			return fn(reroot(ctx, c))
		})
		if err == nil && !ran && hooks.CronSkipped != nil {
			hooks.CronSkipped(strings.TrimPrefix(key, "cron-"))
		}
		return err
	}
}

// Singleton declares a long-running loop of which exactly one instance runs
// across all revisions, with clean handoff between deploys: the loop
// executes only while holding the §4.4 lease, so min=1/max=1 plus the
// lease — not either alone — is the guarantee.
func Singleton(fn func(Ctx) error, cfg ...SingletonConfig) Handler {
	var timeout time.Duration
	for _, c := range cfg {
		if c.Timeout > 0 {
			timeout = c.Timeout
		}
	}
	baseFn := fn
	if timeout > 0 {
		d := timeout
		inner := baseFn
		baseFn = func(ctx Ctx) error {
			c, cancel := context.WithTimeout(ctx, d)
			defer cancel()
			return inner(reroot(ctx, c))
		}
	}
	return register(Handler{
		kind:    "singleton",
		name:    fnName(fn),
		id:      nextHandlerID(),
		timeout: timeout,
		invoke:  func(ctx Ctx, _ []byte) error { return baseFn(ctx) },
	})
}
