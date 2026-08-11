package gcp

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// docs/tako-cloud-permissions.md's Layer 1 table has been the source of
// truth for the deployer's roles since before any code knew them: it is
// what a user copied by hand, and it is what `tako setup` now derives
// and grants.
//
// Two descriptions of one thing, and the moment code owns one of them,
// one starts lying. The doc is not decoration here — it is what an
// operator reads when setup CANNOT grant (the hand-over path), and what
// a platform team approves before anyone runs anything. So it is
// checked.
//
// The check is one-directional on purpose: the table is the MAXIMUM (a
// user may grant once and never think again), and setup grants the
// subset this app's graph needs. So every role tako can ever grant must
// appear in the table; the table may list more.
func TestEveryRoleSetupCanGrantIsInThePermissionsDoc(t *testing.T) {
	raw, err := os.ReadFile("../../../docs/tako-cloud-permissions.md")
	if err != nil {
		t.Fatalf("%v — if the doc moved, move this check with it", err)
	}
	documented := map[string]bool{}
	for _, m := range regexp.MustCompile("`(roles/[a-zA-Z.]+)`").FindAllStringSubmatch(string(raw), -1) {
		documented[m[1]] = true
	}
	if len(documented) < 10 {
		t.Fatalf("only found %d roles in the permissions doc — the table's shape changed and "+
			"this check is no longer reading it", len(documented))
	}

	// Every surface an app can declare, so the union is everything
	// Requirements can ever ask for.
	_, perms := Requirements(Needs{
		Datastore: true, PubSub: true, Scheduler: true, Tasks: true,
		Secrets: []string{"s"}, Metrics: true, SignBlob: true,
		Domains: []string{"example.com"}, AuthProviders: []string{"google"},
	})
	for _, p := range perms {
		if !documented[p.Role] {
			t.Errorf("setup can grant %s (for %s) and the permissions doc's tables never "+
				"mention it — the doc is what an operator reads when setup cannot grant, "+
				"and what a platform team approves", p.Role, p.Name)
		}
	}
}

// The preflight's remediation now names `tako setup` rather than a raw
// gcloud line, because the tighter-set ruling makes re-running setup the
// normal way an app grows into a new permission. A gap that told the
// user to paste a binding would send them around the command that
// exists to do it.
func TestPreflightGapsPointAtTheCommandThatFixesThem(t *testing.T) {
	src, err := os.ReadFile("preflight.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	for _, want := range []string{
		"enable it with the rest: tako setup",
		"grant it with the rest: tako setup",
		"tako datastore create --region",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("preflight no longer points at %q — a user who meets the gap is told to "+
				"do by hand what a command does", want)
		}
	}
	// And the database gap must NOT hand out a location, since that
	// choice is permanent and is the whole reason it has its own command.
	if strings.Contains(text, "gcloud firestore databases create") {
		t.Error("preflight still prints a raw firestore create — it would pick the region for " +
			"the user, and a database's location cannot be changed afterward")
	}
}
