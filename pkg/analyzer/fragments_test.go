package analyzer

import (
	"sort"
	"strings"
	"testing"
)

// composites reads one entity's derived tuples as "a,b,-c" strings,
// paired with the function each was credited to.
func composites(t *testing.T, dir, entity string) map[string]string {
	t.Helper()
	g, err := Analyze(Config{Dir: dir, Env: []string{"GOWORK=off"}})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, r := range g.Resources {
		if r.Kind != "entity" || r.Name != entity {
			continue
		}
		for i, tuple := range r.Composites {
			src := ""
			if i < len(r.CompositeSources) {
				src = r.CompositeSources[i]
			}
			if j := strings.LastIndex(src, " "); j >= 0 {
				src = src[j+1:]
			}
			out[strings.Join(tuple, ",")] = src
		}
	}
	return out
}

// A query assembled across a function boundary derives the same set as
// the same query written inline. Before FL10 the caller's chain stopped
// at the call, so the shape that actually runs had no index — RC1's
// production 500, one call frame away.
func TestFragmentsDeriveTheCallersShapes(t *testing.T) {
	got := composites(t, "testdata/queryfragments", "articles")
	want := map[string]string{
		// list: the fragment's State, plus a conditional Section.
		"state,-publishAt":         "list",
		"section,state,-publishAt": "list",
		// byAuthor: two fragments deep. Filters take STRUCT order
		// (State is declared before Author), which is the rule for
		// every tuple; only the OrderBy suffix follows call order.
		"state,author,-publishAt": "byAuthor",
		// recent: two OrderBys, in the order they are written.
		"state,section,-publishAt": "recent",
		// maybePublished: the fragment is called inside an if, so
		// everything it carries is optional at that site.
		"section,-publishAt": "maybePublished",
	}
	for tuple, fn := range want {
		src, ok := got[tuple]
		if !ok {
			t.Errorf("no composite (%s) — derived: %s", tuple, keysOf(got))
			continue
		}
		if src != fn {
			t.Errorf("composite (%s) credited to %s, want %s", tuple, src, fn)
		}
	}
}

// A fragment is not a query. Deriving an index for a shape that never
// runs costs a real index — minutes to build, one of a project's 200.
func TestAnUncalledFragmentAsksForNothing(t *testing.T) {
	got := composites(t, "testdata/queryfragments", "articles")
	for tuple, src := range got {
		if src == "neverCalled" || src == "published" || src == "authored" {
			t.Errorf("composite (%s) was derived from the fragment %s", tuple, src)
		}
	}
	// (author, section) is what neverCalled would have produced.
	if _, ok := got["author,section"]; ok {
		t.Error("the uncalled fragment's shape survived")
	}
}

// Datastore uses a composite only when its sort properties are in the
// query's sort order, so a reversed suffix is as unusable as a missing
// index. ast.Inspect visits a chained call outermost-first, which
// recorded the LAST OrderBy written as the FIRST one derived.
func TestOrderBysKeepTheOrderTheyAreWrittenIn(t *testing.T) {
	got := composites(t, "testdata/queryfragments", "articles")
	if _, ok := got["state,section,-publishAt"]; !ok {
		t.Errorf("OrderBy(\"Section\").OrderBy(\"-PublishAt\") did not derive (state, section, -publishAt) — derived: %s", keysOf(got))
	}
	if _, ok := got["state,-publishAt,section"]; ok {
		t.Error("the two OrderBys are still recorded in reverse")
	}
}

func keysOf(m map[string]string) string {
	var ks []string
	for k := range m {
		ks = append(ks, "("+k+")")
	}
	sort.Strings(ks)
	return strings.Join(ks, " ")
}
