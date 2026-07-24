package provision

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/gsandoval/tako/pkg/analyzer"
)

// Config identifies one deploy target.
type Config struct {
	Project string
	Region  string
	App     string
	// Env is the environment; "prod" (the default) deploys bare names,
	// anything else prefixes every resource name (spec §4.6a:
	// environments are disjoint resource sets from the same code).
	Env string
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

// SAName derives the IAM accountId for a service's identity. IAM
// requires 6–30 chars, which service names alone don't guarantee
// ("hello" is 5): the tako- prefix secures the minimum, and names past
// 30 are truncated with a hash suffix so they stay unique and stable.
func (c Config) SAName(base string) string {
	name := "tako-" + c.Name(base)
	if len(name) <= 30 {
		return name
	}
	sum := sha256.Sum256([]byte(name))
	return strings.TrimRight(name[:23], "-") + "-" + hex.EncodeToString(sum[:3])
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
	for _, svc := range g.Services {
		image, ok := images[svc.Name]
		if !ok {
			return nil, fmt.Errorf("no image built for service %s", svc.Name)
		}
		knobs := profileKnobs[svc.Profile]
		if knobs == nil {
			return nil, fmt.Errorf("service %s has unknown profile %q", svc.Name, svc.Profile)
		}
		name := cfg.Name(svc.Name)
		sa := cfg.SAName(svc.Name)
		objs = append(objs,
			Object{
				Kind: KindServiceAccount,
				Name: sa,
				Spec: map[string]any{
					"displayName": fmt.Sprintf("tako: service %s of app %s (%s)", svc.Name, cfg.App, cfg.Env),
				},
			},
			Object{
				Kind: KindRunService,
				Name: name,
				Spec: map[string]any{
					"image":          image,
					"serviceAccount": sa + "@" + cfg.Project + ".iam.gserviceaccount.com",
					"env":            map[string]any{"TAKO_BACKEND": "gcp"},
					"scaling":        knobs,
					// Public ingress until auth/private features land.
					"public": true,
				},
			},
		)
	}
	return objs, nil
}
