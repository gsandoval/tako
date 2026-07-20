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
	// Default scales to zero: min 0, modest concurrency, throttled CPU.
	Default Profile = iota
	// Latency keeps an instance warm: min 1, high concurrency, CPU always on.
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

// WithRateLimit caps a queue's delivery rate at n messages per second.
func WithRateLimit(n int) Option { return option("rate_limit", n) }

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

// WithSingleflight wraps a Cron handler in a lease so overlapping or
// double-fired runs collapse to one execution.
func WithSingleflight() Option { return option("singleflight") }

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

// WithDomain maps a custom domain (with managed TLS) to the site.
func WithDomain(domain string) Option { return option("domain", domain) }

// WithRedirect adds a permanent redirect from one site path to another.
func WithRedirect(from, to string) Option { return option("redirect", from, to) }

// WithCDN serves the site through a load balancer and CDN instead of
// embedding it in the app service. Opt-in: it provisions billable
// infrastructure.
func WithCDN() Option { return option("cdn") }

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
