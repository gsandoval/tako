// Package analyzer loads a Tako module via go/packages and go/types and
// produces its AppGraph: the resources, handlers, usage edges, and service
// partitioning that drive code generation and provisioning.
package analyzer

// AppGraph is the analyzer's output: everything the generators and the
// provisioner need to know about an app, derived from its source.
type AppGraph struct {
	// Module is the Go module path of the analyzed app.
	Module string `json:"module"`
	// Services is the derived service partitioning (package = service).
	Services []Service `json:"services"`
	// Resources are the declared resource vars, in source order.
	Resources []Resource `json:"resources"`
	// Handlers are the declared handler vars, in source order.
	Handlers []Handler `json:"handlers"`
	// Edges are resource-usage and service-call edges.
	Edges []Edge `json:"edges"`
}

// Service is one deployable unit. Within a package, API/Page/Cron handlers
// deploy together as that package's service; each worker and subscriber
// gets its own.
type Service struct {
	// Name is the service's stable identity.
	Name string `json:"name"`
	// Package is the import path of the Go package the service comes from.
	Package string `json:"package"`
	// Handlers lists the vars (package-qualified) that run in this service.
	Handlers []string `json:"handlers"`
	// Profile is the service's scaling profile: "default", "latency", or
	// "batch".
	Profile string `json:"profile"`
	// Job marks a deployable that provisions as a Cloud Run JOB (a
	// tako.JobHandler), not a service: no routes, no ingress, no
	// request timeout.
	Job bool `json:"job,omitempty"`
	// Singleton marks a min=1/max=1 service running a lease-guarded
	// loop (tako.Singleton).
	Singleton bool `json:"singleton,omitempty"`
	// Footprint is the service's explicit compute override
	// (WithFootprint), inherited from its handlers like Profile. Zero
	// fields inherit the profile's value; the provisioner resolves the
	// pair via ProfileKnobs.
	Footprint *Footprint `json:"footprint,omitempty"`
	// Domain is the custom domain the service answers on (WithDomain),
	// inherited from its handlers or its site the way Profile is. Empty
	// means the generated run.app URL is the only address.
	Domain string `json:"domain,omitempty"`
}

// Footprint mirrors tako.Footprint into the graph: the fields a handler
// pinned explicitly. Zero means "inherit from the profile".
type Footprint struct {
	CPU          int    `json:"cpu,omitempty"`
	Memory       string `json:"memory,omitempty"`
	Concurrency  int    `json:"concurrency,omitempty"`
	MinInstances int    `json:"min_instances,omitempty"`
	MaxInstances int    `json:"max_instances,omitempty"`
	// TimeoutSeconds caps one unit of work: a request for a service, a
	// task for a job. Zero inherits the profile's.
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`
	// CPUAlwaysOn mirrors tako.Toggle: 0 inherit, 1 on, 2 off.
	CPUAlwaysOn int `json:"cpu_always_on,omitempty"`
	// StartupBoost mirrors tako.Toggle: extra CPU until the instance is
	// serving (Cloud Run v2 startupCpuBoost).
	StartupBoost int `json:"startup_boost,omitempty"`
	// SessionAffinity mirrors tako.Toggle: route a returning client back
	// to the instance it reached last (Cloud Run v2 sessionAffinity).
	SessionAffinity int `json:"session_affinity,omitempty"`
	// HTTP2 mirrors tako.Toggle: keep HTTP/2 to the instance instead of
	// letting the edge downgrade it (Cloud Run v2 port name "h2c").
	HTTP2 int `json:"http2,omitempty"`
}

// Resource is one declared resource var.
type Resource struct {
	// Kind is the constructor: "bucket", "entity", "cache", "queue",
	// "topic", "lock", "secret", "auth", "site", "job", "counter",
	// "histogram". ("job" is the declared tako.Job carrier — its
	// deployable is the Service marked Job.)
	Kind string `json:"kind"`
	// Name is the constant name passed to the constructor — the resource's
	// stable identity.
	Name string `json:"name"`
	// Var is the declaring var, package-qualified.
	Var string `json:"var"`
	// Package is the import path of the declaring package.
	Package string `json:"package"`
	// Type is the payload type for generic resources (Entity, Cache,
	// Queue, Topic, Job), empty otherwise.
	Type string `json:"type,omitempty"`
	// Indexes are the entity fields the app actually queries
	// (Where/OrderBy call sites), in struct declaration order — the
	// derived index set.
	Indexes []string `json:"indexes,omitempty"`
	// Composites are the derived Datastore composite indexes, one
	// tuple PER QUERY SHAPE (grouped per function): filter fields
	// first (struct order, ascending), then OrderBy fields in call
	// order with a "-" prefix for descending. Empty when
	// single-property indexes suffice. Grouping per function can
	// over-index a function running two separate queries; it never
	// under-indexes a shape (the aggregate-per-entity scheme this
	// replaced did — shop, R0).
	Composites [][]string `json:"composites,omitempty"`
	// Delivery is a queue's or topic's WithDelivery contract; zero
	// fields inherit the framework defaults. RateLimit above zero
	// routes the queue through Cloud Tasks (only a broker with a cap
	// can enforce one).
	RateLimit         int `json:"rate_limit,omitempty"`
	MaxAttempts       int `json:"max_attempts,omitempty"`
	MinBackoffSeconds int `json:"min_backoff_seconds,omitempty"`
	MaxBackoffSeconds int `json:"max_backoff_seconds,omitempty"`
	// Delayed marks a queue with at least one WithDelay publish site —
	// delayed delivery routes through Cloud Tasks in the cloud.
	Delayed bool `json:"delayed,omitempty"`
	// Site configuration (Kind "site"): the served directory, optional
	// frontend build (command + output dir), the dev-proxy URL, the SPA
	// fallback, and permanent redirects.
	Dir      string `json:"dir,omitempty"`
	Build    string `json:"build,omitempty"`
	BuildOut string `json:"build_out,omitempty"`
	DevProxy string `json:"dev_proxy,omitempty"`
	SPA      bool   `json:"spa,omitempty"`
	// TrustedHTML is the markdown posture opt-out (WithTrustedHTML):
	// raw HTML in markdown passes through unsanitized.
	TrustedHTML bool `json:"trusted_html,omitempty"`
	// Domain is a WithDomain claim (Kind "service"); applyDomains
	// projects it onto the declaring package's app service.
	Domain string `json:"domain,omitempty"`
	// Profile and Footprint are a service declaration's scaling claims
	// (Kind "service"), projected like the handler-level ones.
	Profile   string     `json:"profile,omitempty"`
	Footprint *Footprint `json:"footprint,omitempty"`
	// Providers are the sign-in providers of an auth declaration
	// ("google", "email").
	Providers []string       `json:"providers,omitempty"`
	Redirects []SiteRedirect `json:"redirects,omitempty"`
	// Pos is the declaration position as path:line:col.
	Pos string `json:"pos"`
}

// SiteRedirect is one permanent site redirect (WithRedirect).
type SiteRedirect struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Handler is one declared handler var.
type Handler struct {
	// Kind is the constructor: "api", "page", "worker", "subscribe",
	// "job", "cron", "singleton".
	Kind string `json:"kind"`
	// Var is the declaring var, package-qualified.
	Var string `json:"var"`
	// Package is the import path of the declaring package.
	Package string `json:"package"`
	// Service is the name of the Service the handler runs in.
	Service string `json:"service"`
	// Route is the method-and-pattern route for API/Page handlers.
	Route string `json:"route,omitempty"`
	// Schedule is the schedule for Cron handlers.
	Schedule string `json:"schedule,omitempty"`
	// Parallel is a JobHandler's WithParallel task fan-out (0 = one task).
	Parallel int `json:"parallel,omitempty"`
	// Retries is a JobHandler's WithRetries claim (0 = Cloud Run's 3).
	Retries int `json:"retries,omitempty"`
	// Profile is the handler's WithProfile claim ("latency", "batch";
	// empty = default). The service inherits it — mixed claims within
	// one service are an analysis error.
	Profile string `json:"profile,omitempty"`
	// Footprint is the handler's WithFootprint claim; the service
	// inherits it — differing claims within one service are an
	// analysis error (tako/mixed-footprint).
	Footprint *Footprint `json:"footprint,omitempty"`
	// Timezone is a Cron's WithTimezone claim (IANA name); empty = UTC.
	Timezone string `json:"timezone,omitempty"`
	// DeadlineSeconds is a Cron's WithDeadline claim; 0 = 180s.
	DeadlineSeconds int `json:"deadline_seconds,omitempty"`
	// Singleflight marks a Cron declared with tako.WithSingleflight:
	// the handler runs under the §4.4 lease, collapsing overlapping
	// runs across instances.
	Singleflight bool `json:"singleflight,omitempty"`
	// Binding is the bound source's name for Worker/Subscribe/Job
	// handlers: the queue or topic name, or "<bucket>.Uploaded" for
	// bucket-event subscribers.
	Binding string `json:"binding,omitempty"`
	// RequiresAuth marks a route declared with tako.WithAuth: the shim
	// guarantees ctx.User past a 401 gate.
	RequiresAuth bool `json:"requires_auth,omitempty"`
	// Private marks a route declared with tako.WithPrivate: IAM-only
	// ingress, unreachable by browsers (excluded from generated
	// browser clients; enforced at deploy from Phase H's service
	// slice).
	Private bool `json:"private,omitempty"`
	// BindingKind distinguishes a Subscribe handler's source: "topic" or
	// "bucket". Empty for workers (always a queue) and sync handlers.
	BindingKind string `json:"binding_kind,omitempty"`
	// ReqType names the request struct as generated code references it
	// within the handler's package; empty for struct{}.
	ReqType string `json:"req_type,omitempty"`
	// ReqRef and ResRef are the request/response types as OTHER
	// packages' generated code references them (package-name
	// qualified, e.g. "billing.ChargeReq") — remote call stubs (§2.12)
	// name both. ResRef may be a basic or slice expression.
	ReqRef string `json:"req_ref,omitempty"`
	ResRef string `json:"res_ref,omitempty"`
	// Bindings are the request struct's tagged fields, in source order.
	Bindings []Binding `json:"bindings,omitempty"`
	// HasBody reports whether untagged request fields bind from the JSON
	// body.
	HasBody bool `json:"has_body,omitempty"`
	// ResBlob reports whether the result type is tako.Blob, streamed
	// instead of JSON-encoded.
	ResBlob bool `json:"res_blob,omitempty"`
	// Pos is the declaration position as path:line:col.
	Pos string `json:"pos"`
}

// Binding is one tagged request-struct field: where it binds from and the
// static type the generated decode uses.
type Binding struct {
	// Field is the request struct field name.
	Field string `json:"field"`
	// Kind is "path", "query", "form", or "blob" (multipart file field).
	Kind string `json:"kind"`
	// Name is the parameter name bound from (tag value).
	Name string `json:"name"`
	// Type is the field's Go type: string, bool, int, int64, or float64.
	Type string `json:"type"`
}

// Edge records that a handler uses a resource or calls another service's
// endpoint; edges drive derived IAM.
type Edge struct {
	// From is the using handler's Var.
	From string `json:"from"`
	// To is the used Resource's Name, or the callee Handler's Var for call
	// edges.
	To string `json:"to"`
	// ToKind is the target Resource's Kind ("entity", "bucket", …).
	// Resource names are only unique per kind — gallery has both a
	// bucket and an entity named "photos" — and grant derivation needs
	// the pair. Empty for call edges.
	ToKind string `json:"to_kind,omitempty"`
	// Kind is "resource" or "call".
	Kind string `json:"kind"`
	// Verb is the access class: "read", "write", "publish", "sign",
	// "run", or "call".
	Verb string `json:"verb"`
}
