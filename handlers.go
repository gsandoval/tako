package tako

import (
	"encoding/json"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"sync"
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
	opts  []Option
	fn    func(Ctx, Req) (Res, error)
}

// Call invokes the endpoint. In dev, tests, and co-located packages this is
// a direct in-process call; against a remote service it is an authenticated
// HTTP call. The hop is deliberately visible at the call site.
func (e Endpoint[Req, Res]) Call(ctx Ctx, req Req) (Res, error) {
	if e.fn == nil {
		panic("tako: Call on a zero Endpoint — endpoints are declared with tako.API or tako.Page")
	}
	return e.fn(ctx, req)
}

// Handler is a declared asynchronous or scheduled handler binding, returned
// by Worker, Subscribe, JobHandler, Cron, and Singleton.
type Handler struct {
	kind   string
	source string
	name   string // handler function's name; NewTestApp's wiring identity
	invoke func(Ctx, []byte) error
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
func API[Req, Res any](route string, fn func(Ctx, Req) (Res, error), opts ...Option) Endpoint[Req, Res] {
	return Endpoint[Req, Res]{route: route, opts: opts, fn: fn}
}

// Page declares a server-rendered route. It binds Req exactly like API
// (form: tags cover browser form posts) and returns HTML; CSRF is enforced
// automatically on unsafe-method page routes.
func Page[Req any](route string, fn func(Ctx, Req) (HTML, error), opts ...Option) Endpoint[Req, HTML] {
	return Endpoint[Req, HTML]{route: route, opts: opts, fn: fn}
}

// Worker binds fn as the single consumer of queue. Like every async
// handler it is func(Ctx, T) error: return nil to ack, an error to retry,
// tako.Permanent to give up and dead-letter. A queue with two workers is
// an analysis error.
func Worker[T any](queue *QueueHandle[T], fn func(Ctx, T) error, opts ...Option) Handler {
	return register(Handler{
		kind:   "worker",
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
func Subscribe[T any](source EventSource[T], fn func(Ctx, T) error, opts ...Option) Handler {
	return register(Handler{
		kind:   "subscribe",
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
// (analyzer-enforced); WithSingleflight collapses overlapping runs.
// Cron handlers run inside the package's app service. In tests,
// app.AdvanceCron(name) fires one run synchronously.
func Cron(schedule string, fn func(Ctx) error, opts ...Option) Handler {
	return register(Handler{
		kind:   "cron",
		source: schedule,
		name:   fnName(fn),
		invoke: func(ctx Ctx, _ []byte) error { return fn(ctx) },
	})
}

// Singleton declares a long-running loop of which exactly one instance runs
// across all revisions, with clean handoff between deploys.
func Singleton(name string, fn func(Ctx) error, opts ...Option) Handler {
	return Handler{kind: "singleton"}
}
