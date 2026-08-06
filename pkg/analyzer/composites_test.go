package analyzer

import (
	"strings"
	"testing"
)

// A derived index and the note naming the query it came from must stay
// together. They did not: Composites was sorted and CompositeSources
// was not, so every entity whose walk order differed from its sorted
// order had its notes shifted — and the deploy plan then named a
// confidently wrong file:line at exactly the moment someone is
// deciding whether a query shape is right.
//
// No golden covered CompositeSources at all when RC1 shipped it, which
// is the actual defect this test closes; the sort was only where it
// showed.
func TestEachCompositeKeepsItsOwnSource(t *testing.T) {
	g, err := Analyze(Config{Dir: "testdata/indexnotes", Env: []string{"GOWORK=off"}})
	if err != nil {
		t.Fatal(err)
	}
	var articles *Resource
	for i, r := range g.Resources {
		if r.Kind == "entity" && r.Name == "articles" {
			articles = &g.Resources[i]
		}
	}
	if articles == nil {
		t.Fatal("no articles entity in the graph")
	}
	// The full array, not its length: a length check passes on swapped
	// notes, which is how this survived.
	want := map[string]string{
		"section,state,-publishAt": "publishedInSection",
		"state,-publishAt":         "publishedArticles",
	}
	if len(articles.Composites) != len(want) {
		t.Fatalf("composites = %v, want %d", articles.Composites, len(want))
	}
	if len(articles.CompositeSources) != len(articles.Composites) {
		t.Fatalf("%d composites, %d sources", len(articles.Composites), len(articles.CompositeSources))
	}
	for i, tuple := range articles.Composites {
		key := strings.Join(tuple, ",")
		fn, known := want[key]
		if !known {
			t.Errorf("unexpected composite %q", key)
			continue
		}
		if got := articles.CompositeSources[i]; !strings.HasSuffix(got, " "+fn) {
			t.Errorf("composite %q is credited to %q, want the note for %s", key, got, fn)
		}
	}
}

// Sorting is what breaks the pairing, so the order itself is pinned
// too: the plan's index list is stable across runs.
func TestCompositesStaySorted(t *testing.T) {
	g, err := Analyze(Config{Dir: "testdata/indexnotes", Env: []string{"GOWORK=off"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range g.Resources {
		if r.Kind != "entity" {
			continue
		}
		for i := 1; i < len(r.Composites); i++ {
			prev := strings.Join(r.Composites[i-1], ",")
			cur := strings.Join(r.Composites[i], ",")
			if prev >= cur {
				t.Errorf("%s composites out of order: %q then %q", r.Name, prev, cur)
			}
		}
	}
}
