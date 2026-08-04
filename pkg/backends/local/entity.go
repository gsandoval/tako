package local

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/gsandoval/tako/pkg/backends"
)

// EntityStore implements the entity contract on a single bbolt file:
// one bucket per kind, JSON documents as values, queries as in-process
// scans. The file lives at <dir>/entity.db and survives restarts.
type EntityStore struct {
	db *bolt.DB
}

// OpenEntity opens (creating if needed) the entity database under dir.
func OpenEntity(dir string) (*EntityStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "entity.db")
	db, err := bolt.Open(path, 0o644, &bolt.Options{Timeout: time.Second})
	if err != nil {
		if errors.Is(err, bolt.ErrTimeout) {
			// The one way this happens in practice: another process has
			// the file locked — usually an orphaned service from an
			// earlier dev run. Say so, and say how to find it.
			return nil, fmt.Errorf("opening local entity store: another process holds %s — an orphaned service from a previous run? find it with `lsof %s` and kill it (or remove .tako/data if nothing holds it)", path, path)
		}
		return nil, fmt.Errorf("opening local entity store: %w", err)
	}
	return &EntityStore{db: db}, nil
}

// Close releases the underlying database file.
func (s *EntityStore) Close() error { return s.db.Close() }

func (s *EntityStore) Get(ctx context.Context, kind, id string) ([]byte, error) {
	var doc []byte
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(kind))
		if b == nil {
			return backends.ErrNotFound
		}
		v := b.Get([]byte(id))
		if v == nil {
			return backends.ErrNotFound
		}
		doc = bytes.Clone(v)
		return nil
	})
	return doc, err
}

func (s *EntityStore) Put(ctx context.Context, kind, id string, doc []byte) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(kind))
		if err != nil {
			return err
		}
		return b.Put([]byte(id), doc)
	})
}

func (s *EntityStore) Delete(ctx context.Context, kind, id string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(kind))
		if b == nil {
			return nil
		}
		return b.Delete([]byte(id))
	})
}

func (s *EntityStore) Exists(ctx context.Context, kind, id string) (bool, error) {
	exists := false
	err := s.db.View(func(tx *bolt.Tx) error {
		if b := tx.Bucket([]byte(kind)); b != nil {
			exists = b.Get([]byte(id)) != nil
		}
		return nil
	})
	return exists, err
}

func (s *EntityStore) Update(ctx context.Context, kind, id string, mutate func(doc []byte) ([]byte, error)) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(kind))
		if b == nil {
			return backends.ErrNotFound
		}
		v := b.Get([]byte(id))
		if v == nil {
			return backends.ErrNotFound
		}
		next, err := mutate(bytes.Clone(v))
		if err != nil {
			return err
		}
		return b.Put([]byte(id), next)
	})
}

func (s *EntityStore) Tx(ctx context.Context, kind string, fn func(tx backends.EntityTxOps) error) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(kind))
		if err != nil {
			return err
		}
		return fn(&entityTx{b: b})
	})
}

type entityTx struct {
	b *bolt.Bucket
}

func (t *entityTx) Get(id string) ([]byte, error) {
	v := t.b.Get([]byte(id))
	if v == nil {
		return nil, backends.ErrNotFound
	}
	return bytes.Clone(v), nil
}

func (t *entityTx) Put(id string, doc []byte) error { return t.b.Put([]byte(id), doc) }

func (t *entityTx) Delete(id string) error { return t.b.Delete([]byte(id)) }

// Query scans the kind's documents, filters and orders on top-level JSON
// fields, and applies the limit. Functionally equivalent to an indexed
// query — index *derivation* exists for the Datastore backend's sake.
func (s *EntityStore) Query(ctx context.Context, kind string, q backends.EntityQuery) ([][]byte, error) {
	type row struct {
		id     string
		doc    []byte
		fields map[string]any
	}
	var rows []row
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(kind))
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, v []byte) error {
			var fields map[string]any
			if err := json.Unmarshal(v, &fields); err != nil {
				return fmt.Errorf("stored document %s/%s is not valid JSON: %w", kind, k, err)
			}
			for _, f := range q.Filters {
				if !matches(fields[f.Field], f.Op, normalize(f.Value)) {
					return nil
				}
			}
			rows = append(rows, row{id: string(k), doc: bytes.Clone(v), fields: fields})
			return nil
		})
	})
	if err != nil {
		return nil, err
	}

	orderField, desc := strings.CutPrefix(q.Order, "-")
	sort.SliceStable(rows, func(i, j int) bool {
		if orderField != "" {
			if c := compare(rows[i].fields[orderField], rows[j].fields[orderField]); c != 0 {
				if desc {
					return c > 0
				}
				return c < 0
			}
		}
		return rows[i].id < rows[j].id // deterministic tiebreak
	})

	if q.Limit > 0 && len(rows) > q.Limit {
		rows = rows[:q.Limit]
	}
	docs := make([][]byte, len(rows))
	for i, r := range rows {
		docs[i] = r.doc
	}
	return docs, nil
}

// Count walks the same filter the scan uses and counts what survives.
//
// It shares matches() with Query rather than re-deriving the predicate,
// because a count that disagrees with the page it describes is worse
// than no count: a pager would render "page 3 of 2".
//
// Limit and Order are deliberately not consulted — see the contract on
// backends.Entity.
func (s *EntityStore) Count(ctx context.Context, kind string, q backends.EntityQuery) (int64, error) {
	var n int64
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(kind))
		if b == nil {
			return nil
		}
		return b.ForEach(func(k, v []byte) error {
			var fields map[string]any
			if err := json.Unmarshal(v, &fields); err != nil {
				return fmt.Errorf("stored document %s/%s is not valid JSON: %w", kind, k, err)
			}
			for _, f := range q.Filters {
				if !matches(fields[f.Field], f.Op, normalize(f.Value)) {
					return nil
				}
			}
			n++
			return nil
		})
	})
	if err != nil {
		return 0, err
	}
	return n, nil
}

func matches(fieldVal any, op string, want any) bool {
	c := compare(fieldVal, want)
	if c == incomparable {
		return false
	}
	switch op {
	case "=":
		return c == 0
	case "<":
		return c < 0
	case "<=":
		return c <= 0
	case ">":
		return c > 0
	case ">=":
		return c >= 0
	}
	return false
}

const incomparable = 2

// compare orders two JSON values of the same kind; strings that both
// parse as RFC 3339 timestamps compare as instants, so time.Time fields
// order correctly across zones.
func compare(a, b any) int {
	switch av := a.(type) {
	case string:
		bv, ok := b.(string)
		if !ok {
			return incomparable
		}
		if at, err := time.Parse(time.RFC3339Nano, av); err == nil {
			if bt, err := time.Parse(time.RFC3339Nano, bv); err == nil {
				return at.Compare(bt)
			}
		}
		return strings.Compare(av, bv)
	case float64:
		bv, ok := b.(float64)
		if !ok {
			return incomparable
		}
		switch {
		case av < bv:
			return -1
		case av > bv:
			return 1
		}
		return 0
	case bool:
		bv, ok := b.(bool)
		if !ok {
			return incomparable
		}
		switch {
		case !av && bv:
			return -1
		case av && !bv:
			return 1
		}
		return 0
	case nil:
		if b == nil {
			return 0
		}
		return incomparable
	}
	return incomparable
}

// normalize round-trips a filter value through JSON so it compares
// against document values on equal terms (ints become float64, time.Time
// becomes an RFC 3339 string).
func normalize(v any) any {
	raw, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return v
	}
	return out
}
