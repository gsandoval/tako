package conformance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/gsandoval/tako/pkg/backends"
)

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
			if err := s.Put(ctx, "links", d.id, doc(d.id, d.owner, d.hits, d.created)); err != nil {
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
		if err := s.Put(ctx, "links", "x", want); err != nil {
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
		_ = s.Put(ctx, "links", "x", doc("x", "memo", 0, "2026-07-01T00:00:00Z"))
		want := doc("x", "alex", 9, "2026-07-02T00:00:00Z")
		if err := s.Put(ctx, "links", "x", want); err != nil {
			t.Fatal(err)
		}
		got, _ := s.Get(ctx, "links", "x")
		if string(got) != string(want) {
			t.Fatalf("Get after upsert = %s", got)
		}
	})

	t.Run("exists", func(t *testing.T) {
		s := open(t)
		_ = s.Put(ctx, "links", "x", doc("x", "memo", 0, "2026-07-01T00:00:00Z"))
		if ok, err := s.Exists(ctx, "links", "x"); err != nil || !ok {
			t.Fatalf("Exists(x) = %v, %v", ok, err)
		}
		if ok, err := s.Exists(ctx, "links", "y"); err != nil || ok {
			t.Fatalf("Exists(y) = %v, %v", ok, err)
		}
	})

	t.Run("delete removes and is idempotent", func(t *testing.T) {
		s := open(t)
		_ = s.Put(ctx, "links", "x", doc("x", "memo", 0, "2026-07-01T00:00:00Z"))
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
		err := s.Update(ctx, "links", "a", func(d []byte) ([]byte, error) {
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
		err := s.Update(ctx, "links", "nope", func(d []byte) ([]byte, error) { return d, nil })
		if !errors.Is(err, backends.ErrNotFound) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("update propagates mutate error without writing", func(t *testing.T) {
		s := seed(t)
		boom := errors.New("boom")
		err := s.Update(ctx, "links", "a", func(d []byte) ([]byte, error) { return nil, boom })
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

	t.Run("tx commits all writes together", func(t *testing.T) {
		s := open(t)
		err := s.Tx(ctx, "links", func(tx backends.EntityTxOps) error {
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
		err := s.Tx(ctx, "links", func(tx backends.EntityTxOps) error {
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
		err := s.Tx(ctx, "links", func(tx backends.EntityTxOps) error {
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
}
