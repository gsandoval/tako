package local

import (
	"context"
	"testing"
)

// TestPersistenceAcrossReopen is local-specific: the bbolt file must
// survive a close/reopen cycle — the "kill and restart tako dev" story.
func TestPersistenceAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	s, err := OpenEntity(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, "links", "gh", []byte(`{"slug":"gh","hits":1}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s, err = OpenEntity(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	doc, err := s.Get(ctx, "links", "gh")
	if err != nil || string(doc) != `{"slug":"gh","hits":1}` {
		t.Fatalf("after reopen: %s, %v", doc, err)
	}
}
