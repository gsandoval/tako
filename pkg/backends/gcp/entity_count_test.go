package gcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gsandoval/tako/pkg/backends"
)

// The wire shape of a count, which conformance cannot reach without a
// live project.
//
// It matters because every part of it is silently wrong-able: the wrong
// endpoint 404s, a missing alias reads back nil, and an order or limit
// that survives into the aggregation makes the count answer LOW — a
// number that looks like an answer.
func TestCountAsksDatastoreToAggregate(t *testing.T) {
	var body map[string]any
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"batch":{"aggregationResults":[{"aggregateProperties":{"total":{"integerValue":"42"}}}]}}`))
	}))
	t.Cleanup(srv.Close)
	c := &Client{HC: srv.Client(), Opts: Options{Project: "p", App: "gallery", Env: "prod"}, Base: srv.URL}
	s := NewEntityStore(c)

	got, err := s.Count(context.Background(), "links", backends.EntityQuery{
		Filters: []backends.Filter{{Field: "owner", Op: "=", Value: "memo"}},
		Order:   "-created",
		Limit:   10,
	})
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if got != 42 {
		t.Errorf("count = %d, want 42 — int64 crosses Datastore's JSON as a string", got)
	}
	if !strings.HasSuffix(path, ":runAggregationQuery") {
		t.Errorf("count posted to %s, want the :runAggregationQuery sibling of :runQuery", path)
	}

	agg, ok := body["aggregationQuery"].(map[string]any)
	if !ok {
		t.Fatalf("body carries no aggregationQuery: %v", body)
	}
	aggs, _ := agg["aggregations"].([]any)
	if len(aggs) != 1 {
		t.Fatalf("aggregations = %v, want exactly one count", aggs)
	}
	first, _ := aggs[0].(map[string]any)
	if _, hasCount := first["count"]; !hasCount {
		t.Errorf("aggregation is not a count: %v", first)
	}
	if first["alias"] != "total" {
		t.Errorf("alias = %v, want %q — an unaliased aggregation reads back under a name we did not choose",
			first["alias"], "total")
	}

	nested, _ := agg["nestedQuery"].(map[string]any)
	if nested == nil {
		t.Fatal("aggregationQuery carries no nestedQuery")
	}
	if nested["filter"] == nil {
		t.Error("the filter did not survive into the aggregation — a count that ignores the filter is not a count of anything")
	}
	// The two that would answer LOW rather than error.
	if _, has := nested["limit"]; has {
		t.Error("a limit reached the aggregation: the total would be capped at one page")
	}
	if _, has := nested["order"]; has {
		t.Error("an order reached the aggregation: Datastore's index-backed sort drops entities missing the sort property, so the count would silently answer low")
	}
}

// An unrecognised response is an error, not a zero. Reporting "nothing
// matched" for "we did not understand the answer" is the quiet kind of
// wrong this stage has spent itself on.
func TestCountRefusesToGuessOnAnUnreadableAnswer(t *testing.T) {
	for name, reply := range map[string]string{
		"no results":   `{"batch":{"aggregationResults":[]}}`,
		"wrong alias":  `{"batch":{"aggregationResults":[{"aggregateProperties":{"property_1":{"integerValue":"3"}}}]}}`,
		"not a number": `{"batch":{"aggregationResults":[{"aggregateProperties":{"total":{"integerValue":"many"}}}]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(reply))
			}))
			t.Cleanup(srv.Close)
			c := &Client{HC: srv.Client(), Opts: Options{Project: "p", App: "gallery", Env: "prod"}, Base: srv.URL}
			n, err := NewEntityStore(c).Count(context.Background(), "links", backends.EntityQuery{})
			if err == nil {
				t.Fatalf("count = %d with no error — an unreadable answer must not become zero", n)
			}
		})
	}
}

// Paging's wire shape. The cursor discipline is invisible to the
// conformance suite without a live project, and each piece of it fails
// quietly: a missing order means Datastore sorts one way and the caller
// meant another; a drained batch returns the whole result as "one page";
// a cursor handed back on the last page costs a round trip that can only
// be empty.
func TestPagePushesOrderLimitAndCursor(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"batch":{"entityResults":[],"moreResults":"MORE_RESULTS_AFTER_LIMIT","endCursor":"NEXT"}}`))
	}))
	t.Cleanup(srv.Close)
	c := &Client{HC: srv.Client(), Opts: Options{Project: "p", App: "gallery", Env: "prod"}, Base: srv.URL}

	_, _, err := NewEntityStore(c).Page(context.Background(), "links", backends.EntityQuery{
		Order: "-created", Limit: 2,
	}, "HERE")
	if err != nil {
		t.Fatalf("page: %v", err)
	}
	q, _ := body["query"].(map[string]any)
	if q == nil {
		t.Fatalf("no query in body: %v", body)
	}
	if q["startCursor"] != "HERE" {
		t.Errorf("startCursor = %v, want the cursor passed in", q["startCursor"])
	}
	if fmt.Sprint(q["limit"]) != "2" {
		t.Errorf("limit = %v, want 2 — a page must be one page", q["limit"])
	}
	order, _ := q["order"].([]any)
	if len(order) != 2 {
		t.Fatalf("order = %v, want the field plus the __key__ tiebreak", order)
	}
	first, _ := order[0].(map[string]any)
	if first["direction"] != "DESCENDING" {
		t.Errorf("direction = %v, want DESCENDING for a \"-\" order", first["direction"])
	}
	second, _ := order[1].(map[string]any)
	prop, _ := second["property"].(map[string]any)
	if prop["name"] != "__key__" || second["direction"] != "ASCENDING" {
		t.Errorf("tiebreak = %v — must be __key__ ASCENDING, the same one local applies, "+
			"or a page boundary inside equal sort values resumes in two different orders", second)
	}
}

// The last page returns no cursor. Datastore says which it was; reading
// that wrong turns every walk into one extra empty round trip, and worse,
// makes "am I done" unanswerable from the outside.
func TestPageReturnsNoCursorOnTheLastPage(t *testing.T) {
	for name, reply := range map[string]struct {
		json string
		want string
	}{
		"more to read": {`{"batch":{"entityResults":[{"entity":{"key":{"path":[{"name":"a"}]},"properties":{"_doc":{"blobValue":"eyJzbHVnIjoiYSJ9"}}}}],"moreResults":"MORE_RESULTS_AFTER_LIMIT","endCursor":"NEXT"}}`, "NEXT"},
		"no more":      {`{"batch":{"entityResults":[{"entity":{"key":{"path":[{"name":"a"}]},"properties":{"_doc":{"blobValue":"eyJzbHVnIjoiYSJ9"}}}}],"moreResults":"NO_MORE_RESULTS","endCursor":"NEXT"}}`, ""},
		"empty page":   {`{"batch":{"entityResults":[],"moreResults":"MORE_RESULTS_AFTER_LIMIT","endCursor":"NEXT"}}`, ""},
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(reply.json))
			}))
			t.Cleanup(srv.Close)
			c := &Client{HC: srv.Client(), Opts: Options{Project: "p", App: "gallery", Env: "prod"}, Base: srv.URL}
			_, next, err := NewEntityStore(c).Page(context.Background(), "links",
				backends.EntityQuery{Order: "created", Limit: 1}, "")
			if err != nil {
				t.Fatal(err)
			}
			if next != reply.want {
				t.Errorf("cursor = %q, want %q", next, reply.want)
			}
		})
	}
}

// Both refusals are the SHARED errors, not a gcp-flavoured copy. A user
// who meets a different message on local than in the cloud has met two
// bugs, not one rule.
func TestPageRefusalsAreTheSharedOnes(t *testing.T) {
	c := &Client{HC: http.DefaultClient, Opts: Options{Project: "p", App: "gallery", Env: "prod"}, Base: "http://127.0.0.1:1"}
	s := NewEntityStore(c)
	if _, _, err := s.Page(context.Background(), "links", backends.EntityQuery{Limit: 1}, ""); err != backends.ErrPageNeedsOrder {
		t.Errorf("no-order error = %v, want backends.ErrPageNeedsOrder", err)
	}
	if _, _, err := s.Page(context.Background(), "links", backends.EntityQuery{Order: "created"}, ""); err != backends.ErrPageNeedsLimit {
		t.Errorf("no-limit error = %v, want backends.ErrPageNeedsLimit", err)
	}
}
