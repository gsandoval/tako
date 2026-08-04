package metrics

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files")

// The metric contract, pinned.
//
// These names are in the queries of every policy tako has ever deployed
// and in every dashboard anyone has built by hand. Renaming one does not
// break a build — it makes queries match nothing, which is
// indistinguishable from a healthy system until someone needs the alert.
// This test is the only thing that makes such an edit announce itself.
func TestMetricLockIsUnchanged(t *testing.T) {
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
		t.Fatalf("reading %s (run with -update to create it): %v", path, err)
	}
	if string(want) != got {
		t.Errorf("the metric contract changed.\n\nEvery deployed alert policy queries these names,\n"+
			"and a rename makes those queries match nothing rather than fail. If this is\n"+
			"deliberate, rerun with -update and treat it as a breaking change.\n\n--- want\n%s\n--- got\n%s",
			want, got)
	}
}

// A label's closed value set has to be closed. StatusClasses is the one
// vocabulary a query can get wrong by inventing a plausible member —
// `status="500"` and `status="error"` both look right and both match
// nothing.
func TestStatusClassOnlyProducesDeclaredValues(t *testing.T) {
	declared := map[string]bool{}
	for _, v := range StatusClasses {
		declared[v] = true
	}
	// Every status a Go handler can set, plus the boundaries.
	for code := 100; code <= 599; code++ {
		if got := StatusClass(code); !declared[got] {
			t.Fatalf("StatusClass(%d) = %q, which is not in StatusClasses %v", code, got, StatusClasses)
		}
	}
	// And nothing declared is unreachable: a value in the list that no
	// status produces is a promise to a query writer that cannot be kept.
	produced := map[string]bool{}
	for code := 100; code <= 599; code++ {
		produced[StatusClass(code)] = true
	}
	for _, v := range StatusClasses {
		if !produced[v] {
			t.Errorf("StatusClasses declares %q, which StatusClass never returns", v)
		}
	}
}

// The lock file is meant to be READ, which is the lesson this stage
// learned four times over. A rendering nobody can follow is a diff, not
// a contract.
func TestLockReadsAsTheContract(t *testing.T) {
	got := Lock()
	for _, want := range []string{
		"custom.googleapis.com/tako/requests",
		"custom.googleapis.com/tako/request_ms",
		"resource generic_task",
		"label status = 1xx | 2xx | 3xx | 4xx | 5xx",
		"CUMULATIVE DISTRIBUTION",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("lock is missing %q:\n%s", want, got)
		}
	}
}
