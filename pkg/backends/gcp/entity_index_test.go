package gcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gsandoval/tako/pkg/backends"
)

// FL20, at the wire.
//
// The conformance suite states the CONTRACT — a long unindexed field
// round-trips, a declared field stays queryable — but it can only prove
// it against a live project, and against `local` it passed both before
// and after this change because local indexes nothing. The defect lived
// in exactly one place: the properties this store hands Datastore. So
// this is where it is pinned.
//
// What was wrong: every mirrored scalar went up indexed, because the
// contract carried bytes alone and the app's entity:"index" tags could
// not reach the write. Datastore caps an indexed string at 1500 bytes,
// so an app was refused a paragraph it had never declared queryable.
func TestOnlyDeclaredFieldsGoUpIndexed(t *testing.T) {
	props := putAndCaptureProperties(t,
		map[string]any{
			"slug":  "hello-world",
			"owner": "memo",
			"body":  strings.Repeat("a paragraph. ", 400), // ~5 KB
			"hits":  float64(3),
		},
		[]string{"slug", "owner"},
	)

	for _, name := range []string{"slug", "owner"} {
		if excluded(props[name]) {
			t.Errorf("%s is declared entity:\"index\" and went up EXCLUDED — a filter on it "+
				"would match nothing, and Datastore reports no error for that", name)
		}
	}
	// The two that carry the fix: untagged fields are stored, not indexed.
	for _, name := range []string{"body", "hits"} {
		if !excluded(props[name]) {
			t.Errorf("%s is not declared entity:\"index\" and went up INDEXED — this is the "+
				"defect: a 5 KB body is refused for an index nobody asked for", name)
		}
	}
	// The document itself was always excluded, and still is.
	if !excluded(props["_doc"]) {
		t.Error("the document blob must stay out of the index")
	}
	// Excluded does not mean absent: the mirror is still written, so
	// nothing about reads changes.
	if props["body"] == nil {
		t.Error("an unindexed field must still be stored")
	}
}

// An app that declares nothing queryable indexes nothing — the case a
// store which quietly indexed "everything not listed" would get wrong.
func TestAnEntityWithNoDeclaredFieldsIndexesNothing(t *testing.T) {
	props := putAndCaptureProperties(t, map[string]any{"slug": "x", "note": "y"}, nil)
	for name := range props {
		if !excluded(props[name]) {
			t.Errorf("%s went up indexed for an entity that declares no queryable field", name)
		}
	}
}

// The cap applies to what the app DID declare, and the refusal is
// tako's: Datastore's own names a property and a byte count and leaves
// the reader to discover that a struct tag put it there.
func TestAnIndexedValuePastTheCapIsRefusedBeforeItIsSent(t *testing.T) {
	sent := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent = true
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	s := NewEntityStore(&Client{HC: srv.Client(), Opts: Options{Project: "p", Env: "prod"}, Base: srv.URL})

	doc, _ := json.Marshal(map[string]any{
		"slug":  "x",
		"title": strings.Repeat("x", backends.MaxIndexedBytes+1),
	})
	err := s.Put(context.Background(), "posts", "x", doc, []string{"slug", "title"})
	if err == nil {
		t.Fatal("an indexed value past the cap must be refused")
	}
	if sent {
		t.Error("the write was sent anyway — the point of checking here is that the app " +
			"gets tako's message instead of Datastore's")
	}
	for _, want := range []string{`entity "posts"`, "title", `entity:"index"`, "1500"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal missing %q:\n%s", want, err)
		}
	}
}

// A field the app never declared may be as long as the document allows,
// which is the whole point.
func TestAnUndeclaredValuePastTheCapIsFine(t *testing.T) {
	props := putAndCaptureProperties(t,
		map[string]any{"slug": "x", "body": strings.Repeat("x", backends.MaxIndexedBytes*4)},
		[]string{"slug"},
	)
	if !excluded(props["body"]) {
		t.Error("a 6 KB undeclared field went up indexed — Datastore would refuse the write")
	}
}

// putAndCaptureProperties runs one Put against a fake Datastore and
// returns the properties it was asked to upsert.
func putAndCaptureProperties(t *testing.T, fields map[string]any, indexed []string) map[string]any {
	t.Helper()
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	s := NewEntityStore(&Client{HC: srv.Client(), Opts: Options{Project: "p", Env: "prod"}, Base: srv.URL})

	doc, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Put(context.Background(), "posts", "x", doc, indexed); err != nil {
		t.Fatal(err)
	}
	mutations, ok := body["mutations"].([]any)
	if !ok || len(mutations) != 1 {
		t.Fatalf("expected one mutation, got %v", body["mutations"])
	}
	upsert, ok := mutations[0].(map[string]any)["upsert"].(map[string]any)
	if !ok {
		t.Fatalf("expected an upsert, got %v", mutations[0])
	}
	props, ok := upsert["properties"].(map[string]any)
	if !ok {
		t.Fatalf("expected properties, got %v", upsert)
	}
	return props
}

// excluded reports whether a property went up with indexing turned off.
func excluded(prop any) bool {
	m, ok := prop.(map[string]any)
	if !ok {
		return false
	}
	return m["excludeFromIndexes"] == true
}
