// Package hooks is the boot seam between generated services and the SDK:
// pkg/runtime wires backends in at startup, and the root tako package
// reads them per operation. Internal on purpose — user code never touches
// backends directly.
package hooks

import (
	"context"
	"sync"

	"github.com/gsandoval/tako/pkg/backends"
)

// Entity is the wired entity backend; nil until boot.
var Entity backends.Entity

// Bucket is the wired bucket backend; nil until boot.
var Bucket backends.Bucket

// Queue is the wired queue backend; nil until boot.
var Queue backends.Queue

// Topic is the wired topic backend; nil until boot.
var Topic backends.Topic

// Signer is the wired URL signer; nil until boot.
var Signer backends.URLSigner

// Cache is the wired cache backend; nil until boot.
var Cache backends.Cache

// Metrics is the wired metrics sink; nil until boot.
var Metrics backends.Metrics

// Users is the wired accounts backend behind auth.Users(); nil until
// boot.
var Users backends.Users

// StaleSessions and EndSessions are pkg/runtime's, wired at boot.
//
// Sessions belong to the runtime — it mints them, reads them, and knows
// their shape — but the SDK is where a role changes, so it has to be
// able to say "the sessions of this account are out of date" without
// owning the record. StaleSessions marks them for refresh on their next
// request; EndSessions deletes them outright, which is what disabling or
// deleting an account means.
//
// Nil in a bare test, where there are no sessions to be stale.
var (
	StaleSessions func(ctx context.Context, uid string) error
	EndSessions   func(ctx context.Context, uid string) error
)

// Lease is the wired lease backend (spec §4.4) behind tako.Lock and
// cron singleflight; nil until boot.
var Lease backends.Lease

// LeaseOwner identifies this instance in lease records — set at boot,
// unique per process (operators read it off a wedged lease).
var LeaseOwner string

// CronSkipped, when set (pkg/runtime), narrates a singleflight cron
// run that found the lease held — a skip is normal, never an error.
var CronSkipped func(name string)

// remoteCalls maps "<pkgpath> <route>" to a typed remote-transport
// closure (func(tako.Ctx, Req) (Res, error), held as any). Generated
// mains register their REMOTE callees at boot (pkg/runtime); the SDK's
// Endpoint.Call looks up here — nil means direct in-process.
var remoteCalls = map[string]any{}

// RegisterRemoteCall wires one endpoint's remote transport.
func RegisterRemoteCall(key string, transport any) { remoteCalls[key] = transport }

// RemoteCallFor returns the wired transport for the key, or nil.
func RemoteCallFor(key string) any { return remoteCalls[key] }

// RunJob, when set (BootGCP), starts a REAL Cloud Run Job execution;
// nil means the local path — the job rides the queue dispatcher.
var RunJob func(ctx context.Context, name string, params []byte) error

// JobQueue names the internal local queue a job's executions ride —
// one derivation, shared by Run (publish) and the wiring (consume).
func JobQueue(name string) string { return "_job:" + name }

// DeclaredSecrets is set by the SDK at init: it lists every secret name
// declared via tako.Secret, so boot can refuse to start with any unset.
var DeclaredSecrets func() []string

// HandlerInfo is set by the SDK at init: it opens a Handler record for
// the runtime — kind, bound source (queue or topic name), and the
// payload-decoding invoke closure (typed func(tako.Ctx, []byte) error,
// passed as any to avoid an import cycle).
var HandlerInfo func(h any) (kind, source string, invoke any)

// HandlerParallel reads a job handler's RunnerConfig.Parallel fan-out (1 when
// none) — the local runtime runs that many tasks per execution.
var HandlerParallel func(h any) int

// HandlerID is set by the SDK at init: it reads a Handler's
// registration id, which is how the runtime attaches a declared name.
var HandlerID func(h any) int

// handlerIdentity maps a Handler's registration id to the DECLARED name
// — the package-level variable the analyzer read it from, delivered by
// the generated RegisterCron/RegisterSingleton calls.
//
// The SDK cannot see a variable's name: a Go value does not carry the
// name of the var it is assigned to, which is why cron identity used to
// come from reflection on the function pointer and disagreed with every
// other service tako names. The runtime knows, so the runtime tells.
//
// Nothing user-facing reads this. Its one job is the singleflight lease
// key, which must be stable across instances and deploys — so it has to
// be the same string the analyzer used, not one this process invented.
var handlerIdentity sync.Map // int → string

// SetHandlerIdentity records the declared name of a registered handler.
func SetHandlerIdentity(id int, name string) { handlerIdentity.Store(id, name) }

// HandlerIdentity returns the declared name, and whether one was set. A
// handler running under NewTestApp has none — no generated main called
// Register — and callers fall back to a process-local key, which is all
// a test's lease has to distinguish.
func HandlerIdentity(id int) (string, bool) {
	v, ok := handlerIdentity.Load(id)
	if !ok {
		return "", false
	}
	return v.(string), true
}

// RenderSite renders a site template inside the layout — pkg/runtime
// sets it (RegisterSiteTemplates); site.Render calls through. The ctx
// is the request's context.Context (carries the CSRF token).
var RenderSite func(ctx context.Context, name string, data any) ([]byte, error)

// RenderFragment renders a site template WITHOUT the layout — the
// htmx partial path (site.Fragment).
var RenderFragment func(ctx context.Context, name string, data any) ([]byte, error)

// HTMLParts opens a tako.HTML for the runtime's page handler — the SDK
// sets it at init (typed as any to avoid the import cycle). fragment
// marks layoutless partials (they skip the document-only headers).
//
// status is the redirect status a plain request should get, and zero
// means "the default", which is 303. Keeping the default at zero is
// what lets Redirect stay untouched and a bare HTML{} stay not-a-
// redirect.
var HTMLParts func(h any) (markup []byte, redirect string, status int, fragment bool)

// RenderMarkdown renders markdown under the site's declared posture
// (sanitized unless WithTrustedHTML) — site.Markdown calls through.
var RenderMarkdown func(src string) ([]byte, error)

// shutdown holds the cleanup a backend needs before the process ends —
// flushing buffered metrics, mostly. Registered at boot, run once by
// pkg/runtime after the last in-flight request finishes.
//
// The ctx carries what is left of Cloud Run's shutdown window, which is
// short and fixed. A hook that cannot finish inside it must give up, not
// hold the process past the kill.
var shutdown []func(context.Context)

// OnShutdown registers cleanup to run before the process exits.
func OnShutdown(fn func(context.Context)) { shutdown = append(shutdown, fn) }

// ShutdownHooks returns the registered cleanup, in registration order.
func ShutdownHooks() []func(context.Context) { return shutdown }
