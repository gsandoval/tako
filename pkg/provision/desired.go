package provision

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/gsandoval/tako/pkg/analyzer"
	gcpb "github.com/gsandoval/tako/pkg/backends/gcp"
)

// Config identifies one deploy target.
type Config struct {
	Project string
	// ProjectNumber backs deterministic Cloud Run URLs
	// (https://<name>-<number>.<region>.run.app) so push subscriptions
	// are plannable before the service exists, and the Pub/Sub service
	// agent's email. Deploy resolves it once via cloudresourcemanager.
	ProjectNumber string
	Region        string
	App           string
	// Env is the environment; "prod" (the default) deploys bare names,
	// anything else prefixes every resource name (spec §4.6a:
	// environments are disjoint resource sets from the same code).
	Env string
	// AuthAPIKey/AuthDomain are Identity Platform's browser client
	// config (discovered at deploy time when the app declares Auth) —
	// injected so the framework's sign-in page can run the flow. The
	// key is a public browser key, not a secret.
	AuthAPIKey string
	AuthDomain string
}

// SAEmail is the full address of a service's identity.
func (c Config) SAEmail(service string) string {
	return c.SAName(service) + "@" + c.Project + ".iam.gserviceaccount.com"
}

// RunURL is the service's deterministic Cloud Run URL.
func (c Config) RunURL(service string) string {
	return fmt.Sprintf("https://%s-%s.%s.run.app", c.Name(service), c.ProjectNumber, c.Region)
}

// DefaultEnv is the environment deploys target when --env is not given.
const DefaultEnv = "prod"

// RepoName is the app-agnostic Artifact Registry repo every tako deploy
// pushes into (dedicated project doctrine: one app, one project).
const RepoName = "tako"

// Name applies the env prefix to a resource name.
func (c Config) Name(base string) string {
	if c.Env == DefaultEnv {
		return base
	}
	return c.Env + "-" + base
}

// SAName derives the IAM accountId for a service's identity: the
// env-prefixed service name plus a -sa type suffix. IAM requires 6–30
// chars, which service names alone don't guarantee ("hello" is 5): the
// suffix carries realistic names past the minimum, a hash pad covers
// pathological one-char ones, and names past 30 truncate to a stable
// hash so they stay unique. The -sa suffix survives padding and
// truncation — it is the marker the IAM-grant reconciler uses to
// recognize tako-managed members, so every variant must end with it.
func (c Config) SAName(base string) string {
	name := c.Name(base) + "-sa"
	sum := sha256.Sum256([]byte(name))
	if len(name) < 6 {
		return c.Name(base) + "-" + hex.EncodeToString(sum[:2]) + "-sa"
	}
	if len(name) <= 30 {
		return name
	}
	return strings.TrimRight(name[:20], "-") + "-" + hex.EncodeToString(sum[:3]) + "-sa"
}

// ManagedMember reports whether an IAM policy member is one of this
// project's tako service identities — the predicate grant revocation
// keys on. Every tako SA accountId ends in -sa (see SAName).
func ManagedMember(member, project string) bool {
	email, ok := strings.CutPrefix(member, "serviceAccount:")
	if !ok {
		return false
	}
	local, ok := strings.CutSuffix(email, "@"+project+".iam.gserviceaccount.com")
	return ok && strings.HasSuffix(local, "-sa")
}

// Labels is the label set every managed object carries; listing these
// labels IS the observed state.
func (c Config) Labels() map[string]string {
	return map[string]string{"tako-app": c.App, "tako-env": c.Env}
}

// profileKnobs maps a scaling profile to Cloud Run settings (spec §4.6).
var profileKnobs = map[string]map[string]any{
	"default": {"min": 0, "max": 20, "concurrency": 80, "cpu": "1", "memory": "512Mi"},
	"latency": {"min": 1, "max": 100, "concurrency": 40, "cpu": "2", "memory": "1Gi"},
	"batch":   {"min": 0, "max": 10, "concurrency": 10, "cpu": "2", "memory": "2Gi"},
}

// Desired derives the managed-object set from the AppGraph. images maps
// service name → digest-pinned image reference (built before planning:
// the digest is part of the spec, so image changes are hash changes).
func Desired(g *analyzer.AppGraph, cfg Config, images map[string]string) ([]Object, error) {
	objs := []Object{{
		Kind: KindRepo,
		Name: RepoName,
		Spec: map[string]any{"location": cfg.Region, "format": "DOCKER"},
	}}
	// A service whose handlers are all async receives work over OIDC
	// push, never public traffic (mirrors codegen's asyncOnly).
	async := map[string]bool{}
	for _, h := range g.Handlers {
		if _, seen := async[h.Service]; !seen {
			async[h.Service] = true
		}
		if h.Kind != "worker" && h.Kind != "subscribe" {
			async[h.Service] = false
		}
	}
	// WithPrivate closes the front door (§2.12): IAM-only ingress, like
	// async services. The analyzer rejects mixed packages, so one
	// private route means the whole service is private.
	privateSvc := map[string]bool{}
	for _, h := range g.Handlers {
		if (h.Kind == "api" || h.Kind == "page") && h.Private {
			privateSvc[h.Service] = true
		}
	}
	// Callees per service, from call edges: the deploy injects each
	// callee's DETERMINISTIC URL (the same host form auth registers) so
	// generated mains can wire remote transports without discovery.
	svcOf := map[string]string{}
	for _, h := range g.Handlers {
		svcOf[h.Var] = h.Service
	}
	callees := map[string][]string{}
	for _, e := range g.Edges {
		if e.Kind != "call" || svcOf[e.From] == svcOf[e.To] {
			continue
		}
		callees[svcOf[e.From]] = append(callees[svcOf[e.From]], svcOf[e.To])
	}

	// Buckets with Uploaded subscribers get an events topic, fed by
	// GCS's own notifications (C5): every write — through-service or
	// signed direct — lands there without the runtime's involvement.
	eventBuckets := map[string]bool{}
	for _, h := range g.Handlers {
		if h.Kind == "subscribe" && h.BindingKind == "bucket" {
			eventBuckets[strings.TrimSuffix(h.Binding, ".Uploaded")] = true
		}
	}
	var eventList []string
	for b := range eventBuckets {
		eventList = append(eventList, b)
	}
	sort.Strings(eventList)

	for _, svc := range g.Services {
		image, ok := images[svc.Name]
		if !ok {
			return nil, fmt.Errorf("no image built for service %s", svc.Name)
		}
		knobs := profileKnobs[svc.Profile]
		if knobs == nil {
			return nil, fmt.Errorf("service %s has unknown profile %q", svc.Name, svc.Profile)
		}
		env := map[string]any{
			"TAKO_BACKEND": "gcp",
			"TAKO_PROJECT": cfg.Project,
			"TAKO_APP":     cfg.App,
			"TAKO_ENV":     cfg.Env,
			// The service's own base name: dead-topic derivation
			// (Permanent publishes there directly) and log labeling.
			"TAKO_SERVICE": svc.Name,
		}
		if cfg.AuthAPIKey != "" {
			env["TAKO_AUTH_APIKEY"] = cfg.AuthAPIKey
			env["TAKO_AUTH_DOMAIN"] = cfg.AuthDomain
		}
		seen := map[string]bool{}
		for _, callee := range callees[svc.Name] {
			if seen[callee] {
				continue
			}
			seen[callee] = true
			env["TAKO_CALLEE_"+strings.ToUpper(strings.ReplaceAll(callee, "-", "_"))] = cfg.RunURL(callee)
		}
		objs = append(objs,
			Object{
				Kind: KindServiceAccount,
				Name: cfg.SAName(svc.Name),
				Spec: map[string]any{
					"displayName": fmt.Sprintf("tako: service %s of app %s (%s)", svc.Name, cfg.App, cfg.Env),
				},
			},
			Object{
				Kind: KindRunService,
				Name: cfg.Name(svc.Name),
				Spec: map[string]any{
					"image":          image,
					"serviceAccount": cfg.SAEmail(svc.Name),
					"env":            env,
					"scaling":        knobs,
					// Async services admit only the OIDC push identity;
					// WithPrivate services admit only granted callers.
					"public": !async[svc.Name] && !privateSvc[svc.Name],
				},
			},
		)
	}

	for _, r := range g.Resources {
		switch r.Kind {
		case "bucket":
			objs = append(objs, Object{
				Kind: KindBucket,
				Name: gcpb.BucketName(cfg.Project, cfg.App, cfg.Env, r.Name),
				// cors: browsers execute signed URLs directly against
				// the bucket (SignedPut's whole point).
				Spec: map[string]any{"location": cfg.Region, "uniform": true, "cors": true},
			})
		case "entity":
			if len(r.Composite) > 0 {
				props := make([]any, 0, len(r.Composite))
				for _, f := range r.Composite {
					dir := "ASCENDING"
					if strings.HasPrefix(f, "-") {
						f, dir = f[1:], "DESCENDING"
					}
					props = append(props, map[string]any{"name": f, "direction": dir})
				}
				objs = append(objs, Object{
					Kind: KindDatastoreIndex,
					// Indexes serve every namespace, so envs share them:
					// no env prefix in the identity.
					Name: r.Name + " (" + strings.Join(r.Composite, ", ") + ")",
					Spec: map[string]any{"entityKind": r.Name, "properties": props},
				})
			}
		case "queue", "topic":
			objs = append(objs, Object{
				Kind: KindTopic,
				Name: cfg.Name(r.Name),
				Spec: map[string]any{},
			})
		}
	}

	// Events topics for buckets with subscribers (created with the other
	// topics so reconcile order holds), and the GCS notification config
	// feeding each — created only after the GCS service agent may
	// publish (GCS validates that at config-creation time; the deploy
	// sequences it).
	for _, b := range eventList {
		physical := gcpb.BucketName(cfg.Project, cfg.App, cfg.Env, b)
		objs = append(objs,
			Object{
				Kind: KindTopic,
				Name: gcpb.EventTopicName(cfg.Env, b),
				Spec: map[string]any{},
			},
			Object{
				Kind: KindBucketNotification,
				Name: physical,
				Spec: map[string]any{
					"bucket":        physical,
					"topic":         gcpb.EventTopicName(cfg.Env, b),
					"eventTypes":    []any{"OBJECT_FINALIZE"},
					"payloadFormat": "JSON_API_V1",
				},
			},
		)
	}

	// One push subscription per async handler — the partition rule makes
	// that per async SERVICE (each worker and subscriber is alone in its
	// own). Pub/Sub authenticates as the service's own SA and the
	// service's Run IAM admits exactly that (grants below). Every
	// subscription carries local's failure semantics: 5 delivery
	// attempts with exponential backoff, then the service's dead-letter
	// topic. The dead topic needs a subscription BEFORE messages route
	// to it (Pub/Sub drops dead-letters otherwise): the pull "keep"
	// subscription is that retention, and what tako dlq reads.
	for _, h := range g.Handlers {
		if h.Kind != "worker" && h.Kind != "subscribe" {
			continue
		}
		dead := gcpb.DeadTopicName(cfg.Env, h.Service)
		url := cfg.RunURL(h.Service)
		objs = append(objs,
			Object{Kind: KindTopic, Name: dead, Spec: map[string]any{}},
			Object{
				Kind: KindPullSubscription,
				Name: dead,
				Spec: map[string]any{"topic": dead},
			},
			Object{
				Kind: KindPushSubscription,
				Name: cfg.Name(h.Service),
				Spec: map[string]any{
					"topic":          sourceTopic(cfg, h),
					"endpoint":       url + "/_tako/push",
					"serviceAccount": cfg.SAEmail(h.Service),
					"audience":       url,
					"retry":          map[string]any{"minimumBackoff": "10s", "maximumBackoff": "300s"},
					"deadLetter":     map[string]any{"topic": dead, "maxAttempts": 5},
				},
			},
		)
	}

	objs = append(objs, deriveGrants(g, cfg)...)
	return objs, nil
}

// sourceTopic is the physical topic one async handler's subscription
// consumes: the queue's topic for a worker, the declared topic for a
// topic subscriber, and the bucket's events topic for an Uploaded
// subscriber.
func sourceTopic(cfg Config, h analyzer.Handler) string {
	if h.Kind == "subscribe" && h.BindingKind == "bucket" {
		return gcpb.EventTopicName(cfg.Env, strings.TrimSuffix(h.Binding, ".Uploaded"))
	}
	return cfg.Name(h.Binding)
}
