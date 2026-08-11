package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/gsandoval/tako/pkg/analyzer"
	"github.com/gsandoval/tako/pkg/provision/gcp"
)

// handOver exists for ONE failure: the caller cannot change IAM, so
// somebody else has to run the commands. Every other failure has to pass
// through untouched.
//
// It used to match any error mentioning enabling APIs, and a brand new
// project's first error is "no billing account" — so setup answered a
// billing problem with a block of commands that would fail identically,
// telling the user to go ask a colleague for a permission they already
// had. Found by running against a fresh project (2026-08-10).
func TestTheHandOverBlockIsOnlyForAPermissionDenial(t *testing.T) {
	plan := gcp.SetupPlan{
		Principal:  "user:a@b.c",
		EnableAPIs: []string{"run.googleapis.com"},
		GrantRoles: []string{"roles/run.admin"},
	}

	denied := errors.New("you lack permission to change the project's IAM policy on p — grant: …")
	got := handOver(denied, "p", "user:a@b.c", plan)
	for _, want := range []string{
		"Granting yourself the permission to grant is the one step nobody can",
		"gcloud projects add-iam-policy-binding p",
		"--role roles/run.admin",
		"Then re-run tako setup to finish",
	} {
		if !strings.Contains(got.Error(), want) {
			t.Errorf("the hand-over block is missing %q:\n%s", want, got)
		}
	}

	// Everything else, unchanged. A wrong hand-over is worse than none:
	// it sends the reader to another person for a problem that person
	// cannot fix either.
	for _, other := range []string{
		"tako-test-04 has no billing account — Cloud Run, Artifact Registry and …",
		"project \"tako-test-04\" not found (or you have no access) — check the id",
		"enable APIs: some other 400",
	} {
		err := errors.New(other)
		if out := handOver(err, "p", "user:a@b.c", plan); out.Error() != other {
			t.Errorf("a non-permission failure was dressed as a hand-over:\n%s", out)
		}
	}
}

// CR10: HasSuffix made the answer depend on declaration order, so a gap
// in "api-key" was reported as a gap in "key" — and the printed command
// then wrote a value nothing reads.
func TestTheSuggestedSecretCommandNamesTheRightDeclaration(t *testing.T) {
	g := &analyzer.AppGraph{Resources: []analyzer.Resource{
		{Kind: "secret", Name: "key"},
		{Kind: "secret", Name: "api-key"},
	}}
	for _, c := range []struct{ env, physical, want string }{
		{"prod", "api-key", "api-key"},
		{"prod", "key", "key"},
		{"staging", "staging-api-key", "api-key"},
		{"staging", "staging-key", "key"},
	} {
		if got := declaredName(g, c.env, c.physical); got != c.want {
			t.Errorf("declaredName(%s, %q) = %q, want %q", c.env, c.physical, got, c.want)
		}
	}
}
