package tako

import (
	"strings"
	"time"
)

// Option configures a resource or handler declaration, or a single operation
// such as Publish or SignedPut. All options live in one namespace; which
// options are valid for which constructor is enforced by the analyzer at
// build time, so an invalid combination fails analysis, not production.
type Option struct {
	name string
	args []any
}

func option(name string, args ...any) Option { return Option{name: name, args: args} }

// Profile selects a scaling profile for the service a handler runs in.
type Profile int

const (
	// Default scales to zero: min 0, high concurrency per instance,
	// CPU throttled between requests.
	Default Profile = iota
	// Latency keeps an instance warm (min 1) with CPU always allocated,
	// and deliberately packs FEWER requests per instance than Default —
	// less queueing behind a busy instance is how tail latency is bought.
	Latency
	// Batch favors throughput per instance for background work.
	Batch
)

// WithTTL sets the expiry for entries in a Cache.
func WithTTL(d time.Duration) Option { return option("ttl", d) }

// WithGoogle enables Google sign-in on an Auth declaration.
func WithGoogle() Option { return option("google") }

// WithEmail enables email/password sign-in on an Auth declaration.
func WithEmail() Option { return option("email") }

// WithDelay delivers a queue message after the given delay. Declaring or
// publishing with a delay selects a backend that supports scheduling
// (Cloud Tasks) — same API, provisioner's choice.
func WithDelay(d time.Duration) Option { return option("delay", d) }

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

// WithDelivery overrides a queue's or topic's delivery contract
// field-by-field.
func WithDelivery(d Delivery) Option { return option("delivery", d) }

// WithMaxSize limits the object size accepted by a SignedPut URL, in bytes.
func WithMaxSize(bytes int64) Option { return option("max_size", bytes) }

// WithContentType restricts the content type accepted by a SignedPut URL;
// a trailing wildcard is allowed ("image/*").
func WithContentType(pattern string) Option { return option("content_type", pattern) }

// WithAuth requires an authenticated user on an API or Page route; the
// handler's Ctx.User is then guaranteed.
func WithAuth() Option { return option("auth") }

// WithPrivate restricts an endpoint's ingress to IAM-authenticated callers
// only (other services, not the public internet). This is IAM-only, not
// network-internal.
func WithPrivate() Option { return option("private") }

// WithProfile sets the scaling profile of the service a handler runs in.
func WithProfile(p Profile) Option { return option("profile", p) }

// Footprint pins explicit compute values for the service a handler runs
// in — the escape hatch under the named profiles. Zero fields inherit
// from the scaling profile (WithProfile, or the default profile); set
// fields override it. Every field is read and validated at analysis
// time against Cloud Run's limits, so an impossible combination fails
// the build, not the deploy.
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
	// Timeout caps how long ONE unit of work may run: a request for a
	// service, a task for a job. Zero inherits the profile's.
	Timeout time.Duration
	// CPUAlwaysOn keeps CPU allocated between requests instead of
	// throttling it. Inherit (the zero value) takes the profile's
	// choice — only Latency allocates.
	CPUAlwaysOn Toggle
	// Drain is how long a shutting-down instance may spend finishing the
	// requests already in flight before it stops for good. Zero inherits
	// the profile's.
	//
	// It divides a window it does not control: Cloud Run sends SIGTERM
	// and kills the instance 10 seconds later, and that 10 seconds is
	// fixed — no API field, no flag. Drain buys the requests; what is
	// left over pays for flushing metrics and closing cleanly. Values
	// above 9s are refused rather than clamped, because the kill lands
	// first and a longer promise would simply be false.
	Drain time.Duration
	// StartupBoost gives a starting instance extra CPU above CPU until
	// it is serving, which is the one knob that moves cold-start
	// latency. Inherit takes the profile's choice — only Latency boosts.
	StartupBoost Toggle
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

// Toggle is a three-state override for a boolean knob, because a plain
// bool cannot distinguish "leave it alone" from "explicitly off" in a
// struct whose zero value means inherit.
type Toggle int

const (
	// Inherit takes the profile's value.
	Inherit Toggle = iota
	// On forces the knob on regardless of profile.
	On
	// Off forces the knob off regardless of profile.
	Off
)

// WithFootprint overrides the scaling profile field-by-field with
// explicit compute values. Prefer a profile (named intent, framework
// values); reach for a footprint when a workload's needs are known and
// specific. On job handlers and singletons only CPU, Memory and Timeout
// apply — the instance shape is what makes those primitives what they
// are.
func WithFootprint(f Footprint) Option { return option("footprint", f) }

// WithRetries sets how many times a failed job task is retried before
// the execution fails. Jobs are compute, not messaging: there is no
// dead-letter to catch what runs out of attempts.
func WithRetries(n int) Option { return option("retries", n) }

// WithSingleflight wraps a Cron handler in a lease so overlapping or
// double-fired runs collapse to one execution.
func WithSingleflight() Option { return option("singleflight") }

// WithTimezone runs a cron on its schedule in the given IANA zone
// ("America/Mexico_City") instead of UTC. Nine o'clock means nine
// o'clock somewhere.
func WithTimezone(zone string) Option { return option("timezone", zone) }

// WithDeadline caps how long one cron run may take before the scheduler
// abandons the attempt (15s–30m; the schedule is the retry).
func WithDeadline(d time.Duration) Option { return option("deadline", d) }

// WithParallel fans a Job out across n parallel tasks.
func WithParallel(n int) Option { return option("parallel", n) }

// SPA serves index.html for unknown paths on a Site, the single-page-app
// convention. Omit it for plain static serving.
func SPA() Option { return option("spa") }

// WithBuild runs the given command at deploy time (and on changes in dev)
// and serves outDir as the site's content. Any static-output generator works.
func WithBuild(command, outDir string) Option { return option("build", command, outDir) }

// WithDevProxy proxies the site to a local dev server (e.g. Vite or Hugo)
// during tako dev instead of rebuilding on change.
func WithDevProxy(url string) Option { return option("dev_proxy", url) }

// WithDomain answers on a custom domain (with managed TLS) instead of only
// the generated run.app URL. It is a SERVICE-level option: declare it on any
// one of a service's handlers — Site, API, or Page — and the whole service
// answers there. One service, one domain.
func WithDomain(domain string) Option { return option("domain", domain) }

// WithRedirect adds a permanent redirect from one site path to another.
func WithRedirect(from, to string) Option { return option("redirect", from, to) }

// WithTrustedHTML disables markdown sanitization for content that is fully
// trusted (never user-supplied). Sanitized output is the default because
// user-supplied markdown is an XSS vector.
func WithTrustedHTML() Option { return option("trusted_html") }

// optionDisplayName maps an option's internal name back to its
// constructor's exported name for error texts: "max_size" → "WithMaxSize".
func optionDisplayName(name string) string {
	acronyms := map[string]string{"ttl": "TTL", "cdn": "CDN", "html": "HTML"}
	var b strings.Builder
	b.WriteString("With")
	for _, part := range strings.Split(name, "_") {
		if a, ok := acronyms[part]; ok {
			b.WriteString(a)
			continue
		}
		b.WriteString(strings.ToUpper(part[:1]) + part[1:])
	}
	return b.String()
}
