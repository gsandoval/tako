package local

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/gsandoval/tako/pkg/backends"
)

// EntityStore implements the entity contract on a single bbolt file:
// one bucket per kind, JSON documents as values, queries as in-process
// scans. The file lives at <dir>/entity.db and survives restarts.
type EntityStore struct {
	dir  string
	once sync.Once
	db   *bolt.DB
	err  error
}

// OpenEntity prepares the entity database under dir. The file — and
// the directory holding it — are created on FIRST USE, not here (RC3):
// every generated service now calls Boot so that logging and metrics
// are wired, including services that declare no entity at all, and
// those should not litter a .tako/data they never read.
//
// The cost is that a database another process holds is reported at the
// first query rather than at boot. The message is the same one, and it
// still names the file and how to find the holder.
func OpenEntity(dir string) (*EntityStore, error) {
	return &EntityStore{dir: dir}, nil
}

// handle opens the database once, on demand.
func (s *EntityStore) handle() (*bolt.DB, error) {
	s.once.Do(func() {
		if err := os.MkdirAll(s.dir, 0o755); err != nil {
			s.err = err
			return
		}
		path := filepath.Join(s.dir, "entity.db")
		db, err := bolt.Open(path, 0o644, &bolt.Options{Timeout: time.Second})
		if err != nil {
			if errors.Is(err, bolt.ErrTimeout) {
				// The one way this happens in practice: another process
				// has the file locked — usually an orphaned service from
				// an earlier dev run. Say so, and say how to find it.
				s.err = fmt.Errorf("opening local entity store: another process holds %s — an orphaned service from a previous run? find it with `lsof %s` and kill it (or remove .tako/data if nothing holds it)", path, path)
				return
			}
			s.err = fmt.Errorf("opening local entity store: %w", err)
			return
		}
		s.db = db
	})
	return s.db, s.err
}

// Close releases the underlying database file, if one was ever opened.
func (s *EntityStore) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *EntityStore) Get(ctx context.Context, kind, id string) ([]byte, error) {
	db, err := s.handle()
	if err != nil {
		return nil, err
	}
	var doc []byte
	err = db.View(func(tx *bolt.Tx) error {
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

// checkIndexed enforces the ONE thing about indexes that local can
// enforce: the cap on an indexed value.
//
// Nothing here indexes — this backend scans — so the check buys this
// store nothing at all. It buys the developer the only thing that
// matters: an app that writes a 2 KB indexed title fails on the laptop
// where it was written instead of on the first deploy. A limit visible
// only in production is a limit the conformance suite is supposed to
// have made visible here (decision 9).
func checkIndexed(kind string, doc []byte, indexed []string) error {
	if len(indexed) == 0 {
		return nil
	}
	var fields map[string]any
	if err := json.Unmarshal(doc, &fields); err != nil {
		return nil // Put's own unmarshal reports it; this is not that error
	}
	for _, name := range indexed {
		if s, ok := fields[name].(string); ok && len(s) > backends.MaxIndexedBytes {
			return backends.IndexedTooLong(kind, name, len(s))
		}
	}
	return nil
}

func (s *EntityStore) Put(ctx context.Context, kind, id string, doc []byte, indexed []string) error {
	if err := checkIndexed(kind, doc, indexed); err != nil {
		return err
	}
	db, err := s.handle()
	if err != nil {
		return err
	}
	return db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(kind))
		if err != nil {
			return err
		}
		return b.Put([]byte(id), doc)
	})
}

func (s *EntityStore) Delete(ctx context.Context, kind, id string) error {
	db, err := s.handle()
	if err != nil {
		return err
	}
	return db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(kind))
		if b == nil {
			return nil
		}
		return b.Delete([]byte(id))
	})
}

func (s *EntityStore) Exists(ctx context.Context, kind, id string) (bool, error) {
	db, err := s.handle()
	if err != nil {
		return false, err
	}
	exists := false
	err = db.View(func(tx *bolt.Tx) error {
		if b := tx.Bucket([]byte(kind)); b != nil {
			exists = b.Get([]byte(id)) != nil
		}
		return nil
	})
	return exists, err
}

func (s *EntityStore) Update(ctx context.Context, kind, id string, indexed []string, mutate func(doc []byte) ([]byte, error)) error {
	db, err := s.handle()
	if err != nil {
		return err
	}
	return db.Update(func(tx *bolt.Tx) error {
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
		if err := checkIndexed(kind, next, indexed); err != nil {
			return err
		}
		return b.Put([]byte(id), next)
	})
}

func (s *EntityStore) Tx(ctx context.Context, kind string, indexed []string, fn func(tx backends.EntityTxOps) error) error {
	db, err := s.handle()
	if err != nil {
		return err
	}
	return db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists([]byte(kind))
		if err != nil {
			return err
		}
		return fn(&entityTx{b: b, kind: kind, indexed: indexed})
	})
}

type entityTx struct {
	b       *bolt.Bucket
	kind    string
	indexed []string
}

func (t *entityTx) Get(id string) ([]byte, error) {
	v := t.b.Get([]byte(id))
	if v == nil {
		return nil, backends.ErrNotFound
	}
	return bytes.Clone(v), nil
}

func (t *entityTx) Put(id string, doc []byte) error {
	if err := checkIndexed(t.kind, doc, t.indexed); err != nil {
		return err
	}
	return t.b.Put([]byte(id), doc)
}

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
	db, err := s.handle()
	if err != nil {
		return nil, err
	}
	var rows []row
	err = db.View(func(tx *bolt.Tx) error {
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

// pageCursor is local's keyset position: the last row seen, as its sort
// value and its id.
//
// Keyset and not an offset, because only keyset survives writes between
// pages the way Datastore does — an offset would shift when a row is
// inserted before the cursor and show one row twice. The id is carried
// because the sort value is not unique; it is the same tiebreak the
// scan applies, so a page boundary cannot land in the middle of a group
// of equal values and lose the rest of it.
type pageCursor struct {
	V  any    `json:"v"`
	ID string `json:"id"`
}

// encodeCursor makes the position opaque. It is opaque on purpose: a
// cursor belongs to the query that produced it, and one that looks
// parseable invites being built by hand for a different query — which
// gcp's cursors would refuse and local's would silently mis-page.
func encodeCursor(c pageCursor) (string, error) {
	raw, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeCursor(s string) (pageCursor, error) {
	var c pageCursor
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return c, fmt.Errorf("tako: page cursor is not readable — pass back the cursor a previous Page returned, unchanged")
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, fmt.Errorf("tako: page cursor is not readable — pass back the cursor a previous Page returned, unchanged")
	}
	return c, nil
}

// Page returns one page and the position to resume from.
//
// It sorts the whole matching set and then seeks, which is honest for an
// in-process store over bbolt and wrong for nothing the local backend is
// for. What it must get right is the SEMANTICS, because those are what
// the gcp backend is held to.
func (s *EntityStore) Page(ctx context.Context, kind string, q backends.EntityQuery, after string) ([][]byte, string, error) {
	if q.Order == "" {
		return nil, "", backends.ErrPageNeedsOrder
	}
	if q.Limit <= 0 {
		return nil, "", backends.ErrPageNeedsLimit
	}
	var start *pageCursor
	if after != "" {
		c, err := decodeCursor(after)
		if err != nil {
			return nil, "", err
		}
		start = &c
	}

	orderField, desc := strings.CutPrefix(q.Order, "-")
	type row struct {
		id     string
		doc    []byte
		fields map[string]any
	}
	db, err := s.handle()
	if err != nil {
		return nil, "", err
	}
	var rows []row
	err = db.View(func(tx *bolt.Tx) error {
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
		return nil, "", err
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if c := compare(rows[i].fields[orderField], rows[j].fields[orderField]); c != 0 {
			if desc {
				return c > 0
			}
			return c < 0
		}
		return rows[i].id < rows[j].id
	})

	// Seek past the cursor by VALUE rather than by index — that is the
	// whole difference between keyset and offset paging.
	if start != nil {
		kept := rows[:0]
		for _, r := range rows {
			if afterCursor(r.fields[orderField], r.id, *start, desc) {
				kept = append(kept, r)
			}
		}
		rows = kept
	}

	more := len(rows) > q.Limit
	if more {
		rows = rows[:q.Limit]
	}
	docs := make([][]byte, len(rows))
	for i, r := range rows {
		docs[i] = r.doc
	}
	next := ""
	if more && len(rows) > 0 {
		last := rows[len(rows)-1]
		if next, err = encodeCursor(pageCursor{V: last.fields[orderField], ID: last.id}); err != nil {
			return nil, "", err
		}
	}
	return docs, next, nil
}

// afterCursor reports whether (v, id) sorts strictly after the cursor,
// in the direction the query asked for. The id tiebreak is ALWAYS
// ascending, matching the scan's tiebreak and Datastore's __key__ —
// reversing it with the sort direction would make a descending page
// skip rows whenever two share a sort value.
func afterCursor(v any, id string, c pageCursor, desc bool) bool {
	switch cmp := compare(v, normalize(c.V)); {
	case cmp == incomparable:
		return false
	case cmp == 0:
		return id > c.ID
	case desc:
		return cmp < 0
	default:
		return cmp > 0
	}
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
	db, err := s.handle()
	if err != nil {
		return 0, err
	}
	var n int64
	err = db.View(func(tx *bolt.Tx) error {
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
