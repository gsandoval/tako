package provision

import (
	"fmt"

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
		if r.Kind != "bucket" {
			continue
		}
		name := gcpb.BucketName(cfg.Project, cfg.AppID(), cfg.Env, r.Name)
		if len(name) <= maxBucketName {
			continue
		}
		return fmt.Errorf("bucket %q would be created as\n  %s (%d characters)\n\n"+
			"    Google caps bucket names at %d. The name is\n"+
			"    <project>-<app id>-<env>-<declared name>; shorten the declared\n"+
			"    name, the env, or the project id.",
			r.Name, name, len(name), maxBucketName)
	}
	return nil
}
