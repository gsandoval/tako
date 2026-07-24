package analyzer

import (
	"fmt"
	"strings"
	"testing"
)

// TestShortlinkEdges pins the derived usage edges — the input to C3's
// IAM grants. resolve publishes clicks and reads links; countClick
// reaches links and the clicks-seen ledger transitively; list/create
// touch links only.
func TestShortlinkEdges(t *testing.T) {
	g, err := Analyze(Config{Dir: repoPath(t, "examples/shortlink")})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	got := map[string]bool{}
	for _, e := range g.Edges {
		if e.Kind != "resource" {
			t.Fatalf("unexpected edge kind %q", e.Kind)
		}
		got[fmt.Sprintf("%s %s %s", e.From, e.Verb, e.To)] = true
	}
	want := []string{
		"shortlink.Create write links",
		"shortlink.Resolve read links",
		"shortlink.Resolve publish clicks",
		"shortlink.List read links",
		"shortlink.CountClick write clicks-seen",
		"shortlink.CountClick write links",
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("missing edge %q\ngot: %v", w, keys(got))
		}
	}
	// The scan must not invent publishes for handlers that never publish.
	if got["shortlink.Create publish clicks"] || got["shortlink.List publish clicks"] {
		t.Error("over-derived publish edge for a non-publishing handler")
	}
}

// TestShortlinkComposite pins the aggregate composite index:
// Where(Owner) + OrderBy(-Created) needs (owner asc, created desc) in
// Datastore mode — property names are the MARSHALED (json tag) names,
// which is what the wire writes and Datastore indexes.
func TestShortlinkComposite(t *testing.T) {
	g, err := Analyze(Config{Dir: repoPath(t, "examples/shortlink")})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	for _, r := range g.Resources {
		switch r.Name {
		case "links":
			if want := "owner,-created"; strings.Join(r.Composite, ",") != want {
				t.Errorf("links composite = %v, want %s", r.Composite, want)
			}
		case "clicks-seen":
			if len(r.Composite) != 0 {
				t.Errorf("clicks-seen composite = %v, want none (never queried)", r.Composite)
			}
		}
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
