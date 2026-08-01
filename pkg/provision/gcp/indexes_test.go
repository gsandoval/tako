package gcp

import (
	"testing"

	"github.com/gsandoval/tako/pkg/provision"
)

func idxObj(kind string, props ...string) provision.Object {
	raw := make([]any, 0, len(props))
	for _, p := range props {
		dir := "ASCENDING"
		if p[0] == '-' {
			p, dir = p[1:], "DESCENDING"
		}
		raw = append(raw, map[string]any{"name": p, "direction": dir})
	}
	return provision.Object{
		Kind: provision.KindDatastoreIndex,
		Name: kind + " (" + props[0] + ")",
		Spec: map[string]any{"entityKind": kind, "properties": raw},
	}
}

// Wait keys on the index's own state, not the create operation. The two
// disagree by ~1m42s (measured), which was dead time at the end of every
// deploy that built an index.
func TestWaitReadsIndexStateNotOperation(t *testing.T) {
	o := idxObj("comments", "post", "-created")

	ready := liveIndex{IndexID: "x", Kind: "comments", State: "READY", Properties: []indexProp{
		{Name: "post", Direction: "ASCENDING"}, {Name: "created", Direction: "DESCENDING"}}}
	if !sameIndex(o, ready) {
		t.Fatal("a live index matching the desired shape must be recognized as that index")
	}

	// Property ORDER is part of the identity — a composite index on
	// (post, created) is not the one on (created, post).
	swapped := ready
	swapped.Properties = []indexProp{
		{Name: "created", Direction: "DESCENDING"}, {Name: "post", Direction: "ASCENDING"}}
	if sameIndex(o, swapped) {
		t.Error("property order must distinguish composite indexes")
	}

	// Direction too.
	flipped := ready
	flipped.Properties = []indexProp{
		{Name: "post", Direction: "ASCENDING"}, {Name: "created", Direction: "ASCENDING"}}
	if sameIndex(o, flipped) {
		t.Error("property direction must distinguish composite indexes")
	}
}

// A deploy with no indexes must not pay a single API call for the wait.
func TestWaitIsFreeWithoutIndexes(t *testing.T) {
	// A nil client would panic on any HTTP call; reaching the return
	// without one is the assertion.
	d := &DatastoreIndexes{C: nil, Desired: []provision.Object{
		{Kind: provision.KindRunService, Name: "api"},
	}}
	if err := d.Wait(nil); err != nil {
		t.Fatalf("Wait with no index objects should be a no-op, got %v", err)
	}
	empty := &DatastoreIndexes{C: nil}
	if err := empty.Wait(nil); err != nil {
		t.Fatalf("Wait with no desired objects should be a no-op, got %v", err)
	}
}
