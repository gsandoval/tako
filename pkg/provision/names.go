package provision

import (
	"fmt"
	"strings"

	"github.com/gsandoval/tako/pkg/analyzer"
	gcpb "github.com/gsandoval/tako/pkg/backends/gcp"
)

// maxBucketName is Google's cap on a GCS bucket name.
const maxBucketName = 63

// CheckNames refuses a deploy whose derived physical names Google will
// not accept, before anything is created and before any network call.
//
// Only lengths are checked, and only where tako assembles a name out
// of parts the user did not choose together: AppID guarantees the
// characters are legal, but not that a project id, an app id, an env
// prefix and a declared bucket name fit in 63 together. The failure
// this replaces is an HTTP 400 in the middle of an apply, naming a
// string that appears nowhere in the user's code.
func CheckNames(g *analyzer.AppGraph, cfg Config) error {
	for _, r := range g.Resources {
		var name, kindLabel string
		switch r.Kind {
		case "bucket":
			name = gcpb.BucketName(cfg.Project, cfg.AppID(), cfg.Env, r.Name)
			kindLabel = "bucket"
		case "disk":
			if r.Config["ephemeral"] == "true" {
				continue
			}
			name = gcpb.DiskBucketName(cfg.Project, cfg.AppID(), cfg.Env, r.Name)
			kindLabel = "disk"
		default:
			continue
		}
		if len(name) <= maxBucketName {
			continue
		}
		return fmt.Errorf("%s %q would be created as\n  %s (%d characters)\n\n"+
			"    Google caps bucket names at %d. The name is\n"+
			"    <project>-<app id>-<env>-<declared name>; shorten the declared\n"+
			"    name, the env, or the project id.",
			kindLabel, r.Name, name, len(name), maxBucketName)
	}
	return checkCollisions(g)
}

// checkCollisions refuses two declarations that become one physical
// name in some environment (CR7).
//
// Env isolation is a string prefix — Prefixed(env, base) is
// env + "-" + base — with nothing separating the two parts, so the
// mapping is not injective. Declare a topic "orders" and a topic
// "staging-orders" and they are THE SAME Pub/Sub topic the moment
// anyone deploys --env staging: staging's messages arrive at prod's
// subscriber. §4.6a promises environments do not touch each other, and
// this is the one shape where they do.
//
// It is decidable from a single graph because the declared set is the
// same in every environment: if the app declares both M and P-M, then
// P is the environment where they meet. The same applies to the names
// tako DERIVES — <bucket>-uploaded, <service>-dead — which are equally
// spellable by hand.
//
// The proper fix is a separator a declared name cannot contain, and
// that is a rename of every physical resource in every deployed app,
// so it is its own task. This refuses the collision instead of
// silently shipping it.
func checkCollisions(g *analyzer.AppGraph) error {
	// Every physical name this app can produce, and the declaration it
	// came from, per environment-independent form.
	declared := map[string]string{} // logical name → "kind name"
	for _, r := range g.Resources {
		declared[r.Name] = r.Kind + " " + r.Name
	}
	for _, r := range g.Resources {
		if r.Kind == "bucket" {
			if owner, ok := declared[r.Name+"-uploaded"]; ok {
				return fmt.Errorf("%s is also the event topic of bucket %q\n\n"+
					"    tako publishes a bucket's writes on <bucket>-uploaded, so those two\n"+
					"    are one Pub/Sub topic. Rename the declaration.",
					owner, r.Name)
			}
		}
		// M and P-M: P is the env in which they are the same resource.
		for logical := range declared {
			if logical == r.Name || !strings.HasSuffix(r.Name, "-"+logical) {
				continue
			}
			env := strings.TrimSuffix(r.Name, "-"+logical)
			return fmt.Errorf("%s and %s become the same physical name in environment %q\n\n"+
				"    Environments are a name prefix (%s-%s), so in that environment\n"+
				"    %q addresses what %q addresses in prod — one resource, two\n"+
				"    declarations, and messages crossing between them. Rename one.",
				declared[logical], declared[r.Name], env, env, logical, logical, r.Name)
		}
	}
	return nil
}
