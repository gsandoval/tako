package analyzer

import (
	"sort"
	"strings"
	"testing"
)

// The enumeration behind RC1. The end-to-end proof is
// testdata/gallery_appgraph.golden.json (the real query that shipped a
// 500) and testdata/errors/queryshapes (the refusal); these pin the
// combinations that would otherwise need a fixture module each.

func shapeKey(c queryChain) string {
	var fs []string
	for f := range c.filters {
		fs = append(fs, f)
	}
	sort.Strings(fs)
	var os []string
	for _, o := range c.orders {
		s := o.field
		if o.dir == "desc" {
			s = "-" + s
		}
		os = append(os, s)
	}
	return strings.Join(fs, ",") + "|" + strings.Join(os, ",")
}

func shapeKeys(cs []queryChain) []string {
	var out []string
	for _, c := range cs {
		out = append(out, shapeKey(c))
	}
	sort.Strings(out)
	return out
}

func eqShapes(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, " · ") != strings.Join(want, " · ") {
		t.Errorf("shapes:\n got: %v\nwant: %v", got, want)
	}
}

// A function with no branches is what it always was: one shape. This is
// the regression guard for every app that never hit the bug.
func TestUnconditionalFiltersAreOneShape(t *testing.T) {
	got := shapesOf([]queryMod{
		{field: "Ready"},
		{field: "Owner"},
		{field: "Created", dir: "desc"},
	})
	eqShapes(t, shapeKeys(got), []string{"Owner,Ready|-Created"})
}

// THE regression. gallery's readyPhotos: a mandatory filter, an
// optional one, and a sort — and the shape that runs when the branch is
// skipped must be derived, because that is the one an anonymous request
// takes and the one Datastore refused.
func TestTheMandatoryOnlyShapeIsAlwaysDerived(t *testing.T) {
	got := shapesOf([]queryMod{
		{field: "Ready"},
		{field: "Owner", optional: true},
		{field: "Created", dir: "desc"},
	})
	eqShapes(t, shapeKeys(got), []string{"Owner,Ready|-Created", "Ready|-Created"})
}

// Two independent branches are four shapes, including the empty one.
func TestEachSubsetOfTheOptionalFiltersIsAShape(t *testing.T) {
	got := shapesOf([]queryMod{
		{field: "Ready"},
		{field: "Owner", optional: true},
		{field: "Album", optional: true},
	})
	eqShapes(t, shapeKeys(got), []string{
		"Album,Owner,Ready|", "Album,Ready|", "Owner,Ready|", "Ready|",
	})
}

// if/else arms are independently optional, so the combination that
// cannot occur together is still derived. Over-indexing is the safe
// direction — this pins it as deliberate, not accidental.
func TestExclusiveBranchesAreDeliberatelyOverIndexed(t *testing.T) {
	got := shapesOf([]queryMod{
		{field: "Ready"},
		{field: "Owner", optional: true}, // if
		{field: "Album", optional: true}, // else
	})
	var both bool
	for _, k := range shapeKeys(got) {
		if k == "Album,Owner,Ready|" {
			both = true
		}
	}
	if !both {
		t.Error("the mutually exclusive combination was not derived — over-indexing is the safe direction here")
	}
}

// A conditional OrderBy changes the index a shape needs just as a
// filter does, so it is subsetted too — and call order survives.
func TestAConditionalOrderByIsItsOwnShape(t *testing.T) {
	got := shapesOf([]queryMod{
		{field: "Ready"},
		{field: "Owner"},
		{field: "Created", dir: "desc", optional: true},
	})
	eqShapes(t, shapeKeys(got), []string{"Owner,Ready|", "Owner,Ready|-Created"})
}

// Orders keep the order they were called in — the OrderBy suffix of a
// derived tuple is positional, so swapping them is a different index.
func TestOrderByCallOrderIsPreserved(t *testing.T) {
	got := shapesOf([]queryMod{
		{field: "Ready"},
		{field: "Album", dir: "asc"},
		{field: "Created", dir: "desc"},
	})
	eqShapes(t, shapeKeys(got), []string{"Ready|Album,-Created"})
}

// The powerset is bounded by what maxOptionalMods admits, so the
// derived index count for one function cannot run away silently. Four
// optional modifiers is 16 shapes; the fifth is refused with
// tako/query-shapes (testdata/errors/queryshapes).
func TestTheShapeCountStaysWithinTheCap(t *testing.T) {
	mods := []queryMod{{field: "Ready"}}
	for _, f := range []string{"Owner", "Album", "Kind", "Camera"} {
		mods = append(mods, queryMod{field: f, optional: true})
	}
	if got := len(shapesOf(mods)); got != 1<<maxOptionalMods {
		t.Errorf("got %d shapes for %d optional modifiers, want %d", got, maxOptionalMods, 1<<maxOptionalMods)
	}
}
