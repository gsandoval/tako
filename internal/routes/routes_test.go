package routes

import (
	"flag"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files")

// The URL contract, pinned.
//
// These strings are in deployed Cloud Scheduler jobs, in Pub/Sub push
// subscriptions, in Cloud Tasks targets, in alert queries that match on
// a route label, and in whatever sign-in code a user wrote against the
// session endpoint. Renaming one does not break a build — it makes a
// scheduler POST into a 404 and an alert match nothing, neither of
// which looks like a failure from the outside. This test is what makes
// such an edit announce itself.
func TestRouteLockIsUnchanged(t *testing.T) {
	path := filepath.Join("testdata", "lock.golden.txt")
	got := Lock()
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("the reserved URL surface changed.\n\ngot:\n%s\nwant:\n%s\n"+
			"If this is deliberate: every deployed Scheduler job, push subscription and\n"+
			"alert query targeting the old path stops working at the next deploy.\n"+
			"Re-run with -update once you have said so in the task log.", got, want)
	}
}

// Every mount is inside the namespace it claims to be inside. A mount
// that escapes the prefix is one the dispatch never routes to, and it
// would be dead on arrival rather than merely misplaced.
func TestEveryMountIsInsideTheNamespace(t *testing.T) {
	for _, m := range All {
		path := m.Pattern
		if i := strings.IndexByte(path, ' '); i >= 0 {
			path = path[i+1:]
		}
		if !IsReserved(path) {
			t.Errorf("%s mounts %q, which is outside %s", m.Name, path, Prefix)
		}
	}
}

// The mounts must be registerable together. A duplicate or a pair Go
// cannot order would panic in a generated main, which is the failure
// mode this whole package exists to remove.
func TestTheMountsCoexistOnOneMux(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("tako's own mounts conflict with each other: %v", r)
		}
	}()
	mux := http.NewServeMux()
	for _, m := range All {
		mux.Handle(m.Pattern, http.NotFoundHandler())
	}
}

func TestIsReservedCoversTheRootAndRefusesLookalikes(t *testing.T) {
	for _, yes := range []string{"/_tako", "/_tako/", "/_tako/api/session", "/_tako/z/", "/_tako/anything"} {
		if !IsReserved(yes) {
			t.Errorf("IsReserved(%q) = false, want true", yes)
		}
	}
	// A path that merely starts with the same letters is the app's.
	for _, no := range []string{"/", "/_takoish/x", "/_tak", "/api/_session", "/x/_tako/y", ""} {
		if IsReserved(no) {
			t.Errorf("IsReserved(%q) = true, want false", no)
		}
	}
}

func TestSignedTokenIsThePathAfterTheMount(t *testing.T) {
	if got := SignedToken(Signed + "abc.def"); got != "abc.def" {
		t.Errorf("SignedToken = %q, want %q", got, "abc.def")
	}
}
