package conformance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/gsandoval/tako/pkg/backends"
)

// linksIndexed is the seeded kind's queryable set: every field the
// cases below filter or order on. Naming it once, here, is the shape
// an app gets for free — its schema comes off the struct tags — and
// keeps the cases about behaviour rather than about bookkeeping.
var linksIndexed = []string{"slug", "owner", "hits", "created"}

// RunEntity runs the entity-contract suite against a fresh store from
// open. Every backend must pass it unchanged — any local/gcp behavior
// difference becomes a case here first, then a fix.
func RunEntity(t *testing.T, open func(t *testing.T) backends.Entity) {
	ctx := context.Background()
	doc := func(id, owner string, hits int, created string) []byte {
		raw, err := json.Marshal(map[string]any{
			"slug": id, "owner": owner, "hits": hits, "created": created,
		})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	seed := func(t *testing.T) backends.Entity {
		s := open(t)
		for i, d := range []struct {
			id, owner string
			hits      int
			created   string
		}{
			{"a", "memo", 3, "2026-07-01T10:00:00Z"},
			{"b", "memo", 1, "2026-07-03T10:00:00Z"},
			{"c", "alex", 7, "2026-07-02T12:00:00+02:00"}, // == 10:00Z
		} {
			if err := s.Put(ctx, "links", d.id, doc(d.id, d.owner, d.hits, d.created), linksIndexed); err != nil {
				t.Fatalf("seed %d: %v", i, err)
			}
		}
		return s
	}
	ids := func(t *testing.T, docs [][]byte) []string {
		var out []string
		for _, d := range docs {
			var m map[string]any
			if err := json.Unmarshal(d, &m); err != nil {
				t.Fatal(err)
			}
			out = append(out, m["slug"].(string))
		}
		return out
	}
	wantIDs := func(t *testing.T, docs [][]byte, want ...string) {
		t.Helper()
		got := ids(t, docs)
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("ids = %v, want %v", got, want)
		}
	}

	t.Run("get put roundtrip", func(t *testing.T) {
		s := open(t)
		want := doc("x", "memo", 0, "2026-07-01T00:00:00Z")
		if err := s.Put(ctx, "links", "x", want, linksIndexed); err != nil {
			t.Fatal(err)
		}
		got, err := s.Get(ctx, "links", "x")
		if err != nil || string(got) != string(want) {
			t.Fatalf("Get = %s, %v", got, err)
		}
	})

	t.Run("get missing is ErrNotFound", func(t *testing.T) {
		s := open(t)
		if _, err := s.Get(ctx, "links", "nope"); !errors.Is(err, backends.ErrNotFound) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("put is an upsert", func(t *testing.T) {
		s := open(t)
		_ = s.Put(ctx, "links", "x", doc("x", "memo", 0, "2026-07-01T00:00:00Z"), linksIndexed)
		want := doc("x", "alex", 9, "2026-07-02T00:00:00Z")
		if err := s.Put(ctx, "links", "x", want, linksIndexed); err != nil {
			t.Fatal(err)
		}
		got, _ := s.Get(ctx, "links", "x")
		if string(got) != string(want) {
			t.Fatalf("Get after upsert = %s", got)
		}
	})

	t.Run("exists", func(t *testing.T) {
		s := open(t)
		_ = s.Put(ctx, "links", "x", doc("x", "memo", 0, "2026-07-01T00:00:00Z"), linksIndexed)
		if ok, err := s.Exists(ctx, "links", "x"); err != nil || !ok {
			t.Fatalf("Exists(x) = %v, %v", ok, err)
		}
		if ok, err := s.Exists(ctx, "links", "y"); err != nil || ok {
			t.Fatalf("Exists(y) = %v, %v", ok, err)
		}
	})

	t.Run("delete removes and is idempotent", func(t *testing.T) {
		s := open(t)
		_ = s.Put(ctx, "links", "x", doc("x", "memo", 0, "2026-07-01T00:00:00Z"), linksIndexed)
		if err := s.Delete(ctx, "links", "x"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Get(ctx, "links", "x"); !errors.Is(err, backends.ErrNotFound) {
			t.Fatalf("err = %v", err)
		}
		if err := s.Delete(ctx, "links", "x"); err != nil {
			t.Fatalf("second delete: %v", err)
		}
	})

	t.Run("update mutates atomically", func(t *testing.T) {
		s := seed(t)
		err := s.Update(ctx, "links", "a", linksIndexed, func(d []byte) ([]byte, error) {
			var m map[string]any
			_ = json.Unmarshal(d, &m)
			m["hits"] = m["hits"].(float64) + 1
			return json.Marshal(m)
		})
		if err != nil {
			t.Fatal(err)
		}
		got, _ := s.Get(ctx, "links", "a")
		var m map[string]any
		_ = json.Unmarshal(got, &m)
		if m["hits"].(float64) != 4 {
			t.Fatalf("hits = %v", m["hits"])
		}
	})

	t.Run("update missing is ErrNotFound", func(t *testing.T) {
		s := open(t)
		err := s.Update(ctx, "links", "nope", linksIndexed, func(d []byte) ([]byte, error) { return d, nil })
		if !errors.Is(err, backends.ErrNotFound) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("update propagates mutate error without writing", func(t *testing.T) {
		s := seed(t)
		boom := errors.New("boom")
		err := s.Update(ctx, "links", "a", linksIndexed, func(d []byte) ([]byte, error) { return nil, boom })
		if !errors.Is(err, boom) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("query equality filter", func(t *testing.T) {
		s := seed(t)
		docs, err := s.Query(ctx, "links", backends.EntityQuery{
			Filters: []backends.Filter{{Field: "owner", Op: "=", Value: "memo"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		wantIDs(t, docs, "a", "b")
	})

	t.Run("query range filter on numbers", func(t *testing.T) {
		s := seed(t)
		docs, err := s.Query(ctx, "links", backends.EntityQuery{
			Filters: []backends.Filter{{Field: "hits", Op: ">=", Value: 3}},
		})
		if err != nil {
			t.Fatal(err)
		}
		wantIDs(t, docs, "a", "c")
	})

	t.Run("query filters combine with AND", func(t *testing.T) {
		s := seed(t)
		docs, err := s.Query(ctx, "links", backends.EntityQuery{
			Filters: []backends.Filter{
				{Field: "owner", Op: "=", Value: "memo"},
				{Field: "hits", Op: "<", Value: 2},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		wantIDs(t, docs, "b")
	})

	t.Run("query orders ascending and descending", func(t *testing.T) {
		s := seed(t)
		asc, err := s.Query(ctx, "links", backends.EntityQuery{Order: "hits"})
		if err != nil {
			t.Fatal(err)
		}
		wantIDs(t, asc, "b", "a", "c")
		desc, err := s.Query(ctx, "links", backends.EntityQuery{Order: "-hits"})
		if err != nil {
			t.Fatal(err)
		}
		wantIDs(t, desc, "c", "a", "b")
	})

	t.Run("query orders timestamps as instants across zones", func(t *testing.T) {
		s := seed(t)
		// a: 10:00Z, c: 12:00+02:00 (== 10:00Z, ties break by id), b: 03rd
		docs, err := s.Query(ctx, "links", backends.EntityQuery{Order: "created"})
		if err != nil {
			t.Fatal(err)
		}
		wantIDs(t, docs, "a", "c", "b")
	})

	t.Run("query limit", func(t *testing.T) {
		s := seed(t)
		docs, err := s.Query(ctx, "links", backends.EntityQuery{Order: "hits", Limit: 2})
		if err != nil {
			t.Fatal(err)
		}
		wantIDs(t, docs, "b", "a")
	})

	t.Run("query with no matches is empty, not an error", func(t *testing.T) {
		s := seed(t)
		docs, err := s.Query(ctx, "links", backends.EntityQuery{
			Filters: []backends.Filter{{Field: "owner", Op: "=", Value: "nobody"}},
		})
		if err != nil || len(docs) != 0 {
			t.Fatalf("docs = %v, %v", docs, err)
		}
	})

	// Count is a server-side aggregation on gcp (runAggregationQuery) and
	// a walk of what local already filtered. Two entirely different
	// mechanisms answering one question, which is exactly the shape the
	// conformance suite exists for.
	t.Run("count answers the whole match, filters included", func(t *testing.T) {
		s := seed(t)
		for _, c := range []struct {
			name string
			q    backends.EntityQuery
			want int64
		}{
			{"everything", backends.EntityQuery{}, 3},
			{"equality filter", backends.EntityQuery{
				Filters: []backends.Filter{{Field: "owner", Op: "=", Value: "memo"}}}, 2},
			{"range filter", backends.EntityQuery{
				Filters: []backends.Filter{{Field: "hits", Op: ">", Value: 1}}}, 2},
			{"ANDed filters", backends.EntityQuery{Filters: []backends.Filter{
				{Field: "owner", Op: "=", Value: "memo"},
				{Field: "hits", Op: ">", Value: 1}}}, 1},
			{"no matches is zero, not an error", backends.EntityQuery{
				Filters: []backends.Filter{{Field: "owner", Op: "=", Value: "nobody"}}}, 0},
		} {
			got, err := s.Count(ctx, "links", c.q)
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			if got != c.want {
				t.Errorf("%s: count = %d, want %d", c.name, got, c.want)
			}
		}
	})

	// Count IGNORES Limit, and that is a decision rather than an
	// oversight: Limit shapes a page, Count answers the total, and the
	// question a numbered pager asks is "how many are there" — not "how
	// many are there, up to the size of one page", which it already
	// knows.
	t.Run("count ignores limit", func(t *testing.T) {
		s := seed(t)
		got, err := s.Count(ctx, "links", backends.EntityQuery{Limit: 1})
		if err != nil {
			t.Fatal(err)
		}
		if got != 3 {
			t.Errorf("count with Limit 1 = %d, want 3 — Limit shapes a page, Count answers the total", got)
		}
	})

	// Order cannot change a count. On gcp the aggregation is built from
	// the same query object the scan uses, so an order that survives into
	// it is a chance for a sort key's absence to silently drop rows —
	// Datastore excludes entities missing the sort property.
	t.Run("count is unchanged by ordering", func(t *testing.T) {
		s := seed(t)
		plain, err := s.Count(ctx, "links", backends.EntityQuery{})
		if err != nil {
			t.Fatal(err)
		}
		ordered, err := s.Count(ctx, "links", backends.EntityQuery{Order: "-created"})
		if err != nil {
			t.Fatal(err)
		}
		if plain != ordered {
			t.Errorf("count = %d unordered, %d ordered — ordering must not change a total", plain, ordered)
		}
	})

	t.Run("count sees writes and deletes", func(t *testing.T) {
		s := seed(t)
		if err := s.Put(ctx, "links", "d", doc("d", "memo", 9, "2026-07-04T10:00:00Z"), linksIndexed); err != nil {
			t.Fatal(err)
		}
		got, err := s.Count(ctx, "links", backends.EntityQuery{
			Filters: []backends.Filter{{Field: "owner", Op: "=", Value: "memo"}}})
		if err != nil {
			t.Fatal(err)
		}
		if got != 3 {
			t.Errorf("count after put = %d, want 3", got)
		}
		if err := s.Delete(ctx, "links", "a"); err != nil {
			t.Fatal(err)
		}
		got, err = s.Count(ctx, "links", backends.EntityQuery{
			Filters: []backends.Filter{{Field: "owner", Op: "=", Value: "memo"}}})
		if err != nil {
			t.Fatal(err)
		}
		if got != 2 {
			t.Errorf("count after delete = %d, want 2", got)
		}
	})

	// Paging is where the two backends are least alike: gcp hands back
	// Datastore's own cursor, local computes a keyset position from the
	// last row. They must still be indistinguishable from outside.
	t.Run("page walks the whole result in order", func(t *testing.T) {
		s := seed(t)
		q := backends.EntityQuery{Order: "hits", Limit: 2}
		page1, next, err := s.Page(ctx, "links", q, "")
		if err != nil {
			t.Fatal(err)
		}
		wantIDs(t, page1, "b", "a")
		if next == "" {
			t.Fatal("no cursor after a full page — there is a third row")
		}
		page2, next2, err := s.Page(ctx, "links", q, next)
		if err != nil {
			t.Fatal(err)
		}
		wantIDs(t, page2, "c")
		if next2 != "" {
			// A short page is the last page. Returning a cursor here
			// invites one more round trip that can only be empty.
			t.Errorf("cursor %q after the last page, want empty", next2)
		}
	})

	t.Run("page refuses without an explicit order", func(t *testing.T) {
		s := seed(t)
		if _, _, err := s.Page(ctx, "links", backends.EntityQuery{Limit: 2}, ""); err == nil {
			t.Fatal("paging with no order must be refused — the default id order " +
				"cannot always push down, and a cursor in a different order than the " +
				"caller asked for silently skips or repeats rows")
		}
	})

	t.Run("page refuses without a page size", func(t *testing.T) {
		s := seed(t)
		if _, _, err := s.Page(ctx, "links", backends.EntityQuery{Order: "hits"}, ""); err == nil {
			t.Fatal("paging with no limit must be refused — a page of everything " +
				"makes the cursor meaningless")
		}
	})

	// The case the cursor design exists for. Between page 1 and page 2,
	// one row is inserted BEFORE the cursor position and one AFTER, and
	// another is deleted. A keyset cursor names the last row seen, so
	// the write before it changes nothing; an OFFSET cursor would shift
	// and repeat a row. Both backends must observe the same thing.
	t.Run("page is stable across writes between pages", func(t *testing.T) {
		s := seed(t) // hits: b=1, a=3, c=7
		q := backends.EntityQuery{Order: "hits", Limit: 2}
		page1, next, err := s.Page(ctx, "links", q, "")
		if err != nil {
			t.Fatal(err)
		}
		wantIDs(t, page1, "b", "a") // through hits=3

		// Before the cursor: an offset cursor would now push "a" onto
		// page 2 and show it twice.
		if err := s.Put(ctx, "links", "z", doc("z", "memo", 0, "2026-07-01T00:00:00Z"), linksIndexed); err != nil {
			t.Fatal(err)
		}
		// After the cursor: genuinely part of what is still to come.
		if err := s.Put(ctx, "links", "d", doc("d", "memo", 5, "2026-07-05T10:00:00Z"), linksIndexed); err != nil {
			t.Fatal(err)
		}
		page2, _, err := s.Page(ctx, "links", q, next)
		if err != nil {
			t.Fatal(err)
		}
		wantIDs(t, page2, "d", "c")
	})

	t.Run("page handles a delete between pages", func(t *testing.T) {
		s := seed(t)
		q := backends.EntityQuery{Order: "hits", Limit: 1}
		page1, next, err := s.Page(ctx, "links", q, "")
		if err != nil {
			t.Fatal(err)
		}
		wantIDs(t, page1, "b")
		if err := s.Delete(ctx, "links", "a"); err != nil {
			t.Fatal(err)
		}
		page2, _, err := s.Page(ctx, "links", q, next)
		if err != nil {
			t.Fatal(err)
		}
		wantIDs(t, page2, "c")
	})

	t.Run("page applies filters and descending order", func(t *testing.T) {
		s := seed(t)
		q := backends.EntityQuery{
			Filters: []backends.Filter{{Field: "owner", Op: "=", Value: "memo"}},
			Order:   "-hits", Limit: 1,
		}
		page1, next, err := s.Page(ctx, "links", q, "")
		if err != nil {
			t.Fatal(err)
		}
		wantIDs(t, page1, "a") // memo's rows are a=3, b=1; descending
		page2, _, err := s.Page(ctx, "links", q, next)
		if err != nil {
			t.Fatal(err)
		}
		wantIDs(t, page2, "b")
	})

	t.Run("page over an empty result is empty with no cursor", func(t *testing.T) {
		s := seed(t)
		docs, next, err := s.Page(ctx, "links", backends.EntityQuery{
			Filters: []backends.Filter{{Field: "owner", Op: "=", Value: "nobody"}},
			Order:   "hits", Limit: 2,
		}, "")
		if err != nil || len(docs) != 0 || next != "" {
			t.Fatalf("docs = %v, next = %q, err = %v", ids(t, docs), next, err)
		}
	})

	t.Run("tx commits all writes together", func(t *testing.T) {
		s := open(t)
		err := s.Tx(ctx, "links", linksIndexed, func(tx backends.EntityTxOps) error {
			if err := tx.Put("x", doc("x", "memo", 0, "2026-07-01T00:00:00Z")); err != nil {
				return err
			}
			return tx.Put("y", doc("y", "memo", 0, "2026-07-01T00:00:00Z"))
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{"x", "y"} {
			if ok, _ := s.Exists(ctx, "links", id); !ok {
				t.Fatalf("%s missing after tx", id)
			}
		}
	})

	t.Run("tx error rolls everything back", func(t *testing.T) {
		s := seed(t)
		boom := errors.New("boom")
		err := s.Tx(ctx, "links", linksIndexed, func(tx backends.EntityTxOps) error {
			if err := tx.Put("z", doc("z", "memo", 0, "2026-07-01T00:00:00Z")); err != nil {
				return err
			}
			if err := tx.Delete("a"); err != nil {
				return err
			}
			return boom
		})
		if !errors.Is(err, boom) {
			t.Fatalf("err = %v", err)
		}
		if ok, _ := s.Exists(ctx, "links", "z"); ok {
			t.Fatal("z exists after rolled-back tx")
		}
		if ok, _ := s.Exists(ctx, "links", "a"); !ok {
			t.Fatal("a deleted by rolled-back tx")
		}
	})

	t.Run("tx reads see the store state", func(t *testing.T) {
		s := seed(t)
		err := s.Tx(ctx, "links", linksIndexed, func(tx backends.EntityTxOps) error {
			if _, err := tx.Get("a"); err != nil {
				return err
			}
			if _, err := tx.Get("nope"); !errors.Is(err, backends.ErrNotFound) {
				return fmt.Errorf("tx.Get(nope) = %v, want ErrNotFound", err)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	})

	// --- FL20: entity:"index" decides what is indexed -----------------
	//
	// The tag was read into a set whose only consumer was the query
	// layer, and the write path could not see it — so every scalar was
	// mirrored as an indexed Datastore property whether or not the app
	// asked. An app then failed to write a field it never declared
	// queryable, with an error about an index it never asked for. These
	// three cases are the contract that replaced that.

	t.Run("an unindexed field holds more than an index could", func(t *testing.T) {
		s := open(t)
		// A blog post's body: longer than any index takes, and nobody
		// filters on it. This is the case that failed against gcp and
		// passed against local, which is why it lives here.
		body := strings.Repeat("a paragraph. ", 400) // ~5 KB
		raw, err := json.Marshal(map[string]any{"slug": "x", "owner": "memo", "body": body})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Put(ctx, "posts", "x", raw, []string{"slug", "owner"}); err != nil {
			t.Fatalf("a %d-byte unindexed field was refused: %v", len(body), err)
		}
		got, err := s.Get(ctx, "posts", "x")
		if err != nil {
			t.Fatal(err)
		}
		var back map[string]any
		if err := json.Unmarshal(got, &back); err != nil {
			t.Fatal(err)
		}
		if back["body"] != body {
			t.Fatalf("the long field did not survive the round trip (%d bytes back)", len(fmt.Sprint(back["body"])))
		}
	})

	t.Run("a declared field is still queryable after a write", func(t *testing.T) {
		// The other direction, and the dangerous one: excluding too
		// much does not error, it just matches nothing. A backend that
		// indexed nothing would pass the case above and fail here.
		s := open(t)
		raw, _ := json.Marshal(map[string]any{"slug": "x", "owner": "memo", "body": "short"})
		if err := s.Put(ctx, "posts", "x", raw, []string{"slug", "owner"}); err != nil {
			t.Fatal(err)
		}
		docs, err := s.Query(ctx, "posts", backends.EntityQuery{
			Filters: []backends.Filter{{Field: "owner", Op: "=", Value: "memo"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(docs) != 1 {
			t.Fatalf("a filter on a DECLARED field matched %d rows, want 1 — "+
				"excluding a queryable field is silent, and this is the case that hears it", len(docs))
		}
	})

	t.Run("an indexed value past the cap is refused the same way everywhere", func(t *testing.T) {
		s := open(t)
		long := strings.Repeat("x", backends.MaxIndexedBytes+1)
		raw, _ := json.Marshal(map[string]any{"slug": "x", "owner": long})
		err := s.Put(ctx, "posts", "x", raw, []string{"slug", "owner"})
		if err == nil {
			t.Fatal("an indexed value past the cap was accepted — it would be accepted here and " +
				"refused in the cloud, which is the difference this suite exists to catch")
		}
		// The wording is the contract: the app can act on "drop the
		// tag", and cannot act on Datastore's version of this.
		for _, want := range []string{`entity "posts"`, "owner", `entity:"index"`, "1500"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("refusal missing %q:\n%s", want, err)
			}
		}
	})
}
