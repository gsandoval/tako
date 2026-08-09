package tako

import (
	"strconv"
	"time"
)

// Configuration is declared as typed structs, one per constructor family.
// A field can only be set where it means something, so the compiler —
// not the analyzer, and never production — rejects a knob applied to the
// wrong thing. Zero values inherit; every field is read at analysis time.
//
// Structs shared by more than one constructor are named for what they
// describe (Route, Compute, Delivery); a struct serving exactly one
// constructor carries its name (CronConfig, SiteConfig).

// Profile selects a scaling profile for the service a handler runs in.
type Profile int

const (
	// ProfileDefault scales to zero: min 0, high concurrency per
	// instance, CPU throttled between requests.
	ProfileDefault Profile = iota
	// ProfileLatency keeps an instance warm (min 1) with CPU always
	// allocated, and deliberately packs FEWER requests per instance than
	// ProfileDefault — less queueing behind a busy instance is how tail
	// latency is bought.
	ProfileLatency
	// ProfileBatch favors throughput per instance for background work.
	ProfileBatch
)

// Toggle is a three-state override for a boolean knob, because a plain
// bool cannot distinguish "leave it alone" from "explicitly off" in a
// struct whose zero value means inherit.
type Toggle int

const (
	// ToggleInherit takes the profile's value.
	ToggleInherit Toggle = iota
	// ToggleOn forces the knob on regardless of profile.
	ToggleOn
	// ToggleOff forces the knob off regardless of profile.
	ToggleOff
)

// Route configures one API or Page endpoint — the two settings that
// belong to a single route rather than to the whole service.
type Route struct {
	// SignIn requires an authenticated user; the handler's Ctx.User is
	// then guaranteed rather than a panic waiting to happen.
	SignIn bool
	// Allow names the declared tako.Policy that decides whether the
	// signed-in user may use this route.
	//
	//	tako.Page("GET /admin", adminHome, tako.Route{Allow: Editors})
	//
	// It implies SignIn — a policy takes a User, so a route carrying one
	// is authenticated by construction, and writing both is redundant
	// rather than wrong. A refusal is a 403 rendered through the app's
	// tako.StatusPage, carrying the policy's name in Status.Policy so a
	// status page can tell YOUR refusal from a stale CSRF token's.
	//
	// It must be a package-level policy var, not a function literal
	// (tako/policy-var): a door with a name is a door the analyzer can
	// print, `tako gen graph` can list, and two routes can share.
	Allow *PolicyHandle
	// Private restricts ingress to IAM-authenticated callers (other
	// services, not the public internet). IAM-only, not
	// network-internal.
	Private bool
	// MaxAge lets shared caches keep this page's response, which they
	// otherwise may not: every rendered page answers no-cache, so a
	// read-heavy site revalidates every visitor's request against the
	// app and the entity store.
	//
	//	tako.Page("GET /{section}/{slug}", article, tako.Route{
	//		MaxAge: 5 * time.Minute,
	//	})
	//
	// Zero keeps no-cache, so nothing changes until you say so. A
	// constant between one second and one year.
	//
	// It is refused on a SignIn route: a shared cache holding an
	// authenticated page would serve one signed-in visitor's page to
	// another, and that is a bug you find from a support ticket.
	MaxAge time.Duration
}

// Compute sizes the service an async handler is the whole of. Workers
// and subscribers get one service each, so sizing the handler and
// sizing the service are the same act — which is why these settings are
// refused on API and Page, where a route would be sizing something it
// only shares (see ServiceConfig).
type Compute struct {
	Profile   Profile
	Footprint Footprint
	Traffic   Traffic
}

// ServiceConfig carries the settings that describe a whole deployable:
// declared once on tako.Service, never on whichever route happened to
// be written first.
type ServiceConfig struct {
	// Domain answers on a custom domain (with managed TLS) instead of
	// only the generated run.app URL. One service, one domain.
	Domain string

	Profile   Profile
	Footprint Footprint
	Traffic   Traffic
}

// CronConfig configures a scheduled handler.
type CronConfig struct {
	// Singleflight wraps the handler in a lease so overlapping or
	// double-fired runs collapse to one execution.
	Singleflight bool
	// Timezone runs the schedule in the given IANA zone
	// ("America/Mexico_City") instead of UTC. Nine o'clock means nine
	// o'clock somewhere.
	Timezone string
	// Deadline caps how long one run may take before the scheduler
	// abandons the attempt (15s–30m; the schedule is the retry).
	Deadline time.Duration

	Profile   Profile
	Footprint Footprint
}

// RunnerConfig configures a job's task handler.
type RunnerConfig struct {
	// Parallel fans the job out across n parallel tasks.
	Parallel int
	// Attempts is how many times a failed task is retried before the
	// execution fails (0–10). Jobs are compute, not messaging: there is
	// no dead letter to catch what runs out of attempts, which is why
	// this range is far tighter than Delivery.MaxAttempts.
	Attempts int
	// Timeout caps how long ONE task may run (up to 24h).
	//
	// A job's only traffic setting is its timeout, which is why this is
	// a field here rather than a whole tako.Traffic: nothing connects to
	// an execution, so there is nobody to keep session affinity with and
	// no connection to negotiate HTTP/2 over.
	Timeout time.Duration

	Profile   Profile
	Footprint Footprint
}

// SingletonConfig configures an exactly-one loop. Its shape is most of
// the contract: min=1, max=1, concurrency=1 and the lease are what make
// a singleton what it is, so there is no scaling to configure and — with
// one instance serving nothing but its own health check — no clients to
// keep affinity with or negotiate HTTP/2 over.
type SingletonConfig struct {
	// Timeout caps how long one turn of the loop may run.
	Timeout time.Duration
	// Footprint sizes the instance. CPU, Memory and StartupBoost only.
	Footprint Footprint
}

// Build runs a command at deploy time (and on changes in dev) and
// serves its output directory as the site's content. Any static-output
// generator works.
type Build struct {
	Command string
	Out     string
}

// SiteConfig configures a static or server-rendered site.
type SiteConfig struct {
	// SPA serves index.html for unknown paths, the single-page-app
	// convention. Leave it false for plain static serving.
	SPA bool
	// Build generates the content before it is served.
	Build Build
	// DevProxy proxies to a local dev server (Vite, Hugo) during
	// tako dev instead of rebuilding on change.
	DevProxy string
	// Redirects maps a site path to its permanent destination.
	Redirects map[string]string
	// TrustedHTML disables markdown sanitization for content that is
	// fully trusted (never user-supplied). Sanitized output is the
	// default because user-supplied markdown is an XSS vector.
	TrustedHTML bool
	// NotFound is the page a 404 renders, for BOTH ways a request can
	// miss: a mistyped URL that matches no static file, and a handler
	// returning tako.NotFound(). One field covers both because they are
	// one experience to a visitor, and because a handler-side result
	// type could never have covered the static miss at all.
	//
	//	tako.SiteConfig{NotFound: "404.html"}
	//
	// It resolves as a TEMPLATE first (templates/404.html, rendered
	// inside layout.html like any page) and falls back to a static file
	// in the site directory — so a generator that already emits
	// 404.html just works.
	//
	// A missing or broken 404 page falls back to today's plain text. A
	// 404 never becomes a 500: the visitor already could not find what
	// they wanted, and an error page that errors helps nobody.
	NotFound string
}

// AuthConfig declares which sign-in providers the app offers. At least
// one is required — an auth declaration with no provider cannot
// authenticate anyone.
type AuthConfig struct {
	// Google enables Google sign-in.
	Google bool
	// Password enables email-and-password sign-in.
	Password bool
}

// Delivery shapes how a queue's or topic's messages reach their
// handlers: how many attempts, how long between them, and how fast.
// Zero fields inherit the framework's defaults — 5 attempts, 10s backing
// off to 300s, no rate cap.
type Delivery struct {
	// MaxAttempts is how many deliveries a message gets before it is
	// dead-lettered (5–100).
	MaxAttempts int
	// MinBackoff and MaxBackoff bound the exponential wait between
	// attempts (1s–10m each).
	MinBackoff time.Duration
	MaxBackoff time.Duration
	// RateLimit caps deliveries per second (1–500). Setting it routes
	// the queue through a broker that can enforce a cap; it is a queue
	// setting, since a topic's subscribers have independent fates.
	RateLimit int
}

// Footprint pins explicit compute values for the service a handler runs
// in — the escape hatch under the named profiles. Zero fields inherit
// from the scaling profile; set fields override it. Every field is read
// and validated at analysis time against Cloud Run's limits, so an
// impossible combination fails the build, not the deploy.
//
// Prefer a profile (named intent, framework values); reach for a
// footprint when a workload's needs are known and specific. On job
// runners and singletons only CPU and Memory apply — the instance shape
// is what makes those primitives what they are.
type Footprint struct {
	// CPU is the vCPU count per instance: 1, 2, 4, or 8.
	CPU int
	// Memory is the per-instance memory limit, like "512Mi" or "4Gi".
	Memory string
	// Concurrency caps concurrent requests per instance (1–1000).
	Concurrency int
	// MinInstances keeps that many instances always warm — warm
	// instances bill continuously. Zero inherits the profile's minimum;
	// an explicit always-scale-to-zero floor is expressed by choosing a
	// min=0 profile, not by this field.
	MinInstances int
	// MaxInstances caps the scale-out (1–1000).
	MaxInstances int
	// CPUAlwaysOn keeps CPU allocated between requests instead of
	// throttling it. ToggleInherit takes the profile's choice — only
	// ProfileLatency allocates.
	CPUAlwaysOn Toggle
	// StartupBoost gives a starting instance extra CPU above CPU until
	// it is serving, which is the one knob that moves cold-start
	// latency. ToggleInherit takes the profile's choice — only
	// ProfileLatency boosts.
	StartupBoost Toggle
}

// Traffic pins how requests REACH a service and how long they may stay,
// as opposed to how much machine runs them (Footprint). Zero fields
// inherit from the scaling profile; set fields override it, and every
// field is validated at analysis time.
//
// The two are separate because they answer separate questions and are
// chosen at different moments: a footprint is sized against a
// workload's appetite, a traffic setting against how clients talk to
// it.
type Traffic struct {
	// Timeout caps how long ONE unit of work may run: a request for a
	// service, a task for a job. Zero inherits the profile's.
	//
	// It is not the shutdown budget — those are different clocks. A
	// service may hold a request for minutes and still get only seconds
	// to finish it when the instance is replaced.
	Timeout time.Duration
	// SessionAffinity asks Cloud Run to send a returning client back to
	// the instance it reached last. No profile turns it on: it trades
	// away even load spreading, and an instance can vanish at any time,
	// so it is a best-effort optimization for warm per-client state —
	// never somewhere to keep state you cannot lose.
	SessionAffinity Toggle
	// HTTP2 keeps HTTP/2 all the way to the instance instead of letting
	// Cloud Run downgrade it to HTTP/1.1 at the edge. Browsers already
	// reach Cloud Run over HTTP/2 either way — this is about the last
	// hop, and it is what buys request multiplexing and streaming
	// between the frontend and your handler.
	//
	// A tako server always answers HTTP/2, so turning this on cannot
	// arrive at a container that refuses it. Off by default because
	// Cloud Run's own default is to downgrade, and for ordinary
	// request/response JSON the last hop gains little.
	HTTP2 Toggle
}

// PutPolicy constrains what a SignedPut URL will accept. The same value
// mints the URL and produces the headers the uploader must send, so the
// two cannot drift apart.
type PutPolicy struct {
	// MaxSize limits the object size accepted by the URL, in bytes.
	MaxSize int64
	// ContentType restricts the accepted content type; a trailing
	// wildcard is allowed ("image/*").
	ContentType string
}

// Headers returns the headers a client MUST send with its PUT to a URL
// minted with this policy. A policy carrying MaxSize signs a size-range
// condition into the request — a PUT without the header (or exceeding
// the range) is refused by the storage backend itself, local and cloud
// alike. Hand the map to the uploader alongside the URL; it is empty
// when the policy needs none.
func (p PutPolicy) Headers() map[string]string {
	h := map[string]string{}
	if p.MaxSize > 0 {
		h["x-goog-content-length-range"] = "0," + strconv.FormatInt(p.MaxSize, 10)
	}
	return h
}
