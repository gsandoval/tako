package provision

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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

	for _, svc := range g.Services {
		image, ok := images[svc.Name]
		if !ok {
			return nil, fmt.Errorf("no image built for service %s", svc.Name)
		}
		knobs := profileKnobs[svc.Profile]
		if knobs == nil {
			return nil, fmt.Errorf("service %s has unknown profile %q", svc.Name, svc.Profile)
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
					"env": map[string]any{
						"TAKO_BACKEND": "gcp",
						"TAKO_PROJECT": cfg.Project,
						"TAKO_APP":     cfg.App,
						"TAKO_ENV":     cfg.Env,
					},
					"scaling": knobs,
					// Async services admit only the OIDC push identity;
					// everything else stays public until auth lands.
					"public": !async[svc.Name],
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
				Spec: map[string]any{"location": cfg.Region, "uniform": true},
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

	// One push subscription per worker: Pub/Sub authenticates as the
	// worker's own SA, and the worker's Run IAM admits exactly that
	// (grants below). Subscribers join in C4 with per-handler fan-out.
	for _, h := range g.Handlers {
		if h.Kind != "worker" {
			continue
		}
		url := cfg.RunURL(h.Service)
		objs = append(objs, Object{
			Kind: KindPushSubscription,
			Name: cfg.Name(h.Service),
			Spec: map[string]any{
				"topic":          cfg.Name(h.Binding),
				"endpoint":       url + "/_tako/push",
				"serviceAccount": cfg.SAEmail(h.Service),
				"audience":       url,
			},
		})
	}

	objs = append(objs, deriveGrants(g, cfg)...)
	return objs, nil
}
