package tako

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"sync"

	"github.com/gsandoval/tako/internal/entitytag"
	"github.com/gsandoval/tako/internal/hooks"
	"github.com/gsandoval/tako/pkg/backends"
)

// Entity declares a typed entity store — an entity store, not "the
// database". Schema comes from the struct: `entity:"id"` marks the key
// field, `entity:"index"` marks queryable fields. The name is the store's
// stable identity and must be a string constant (analyzer-enforced).
func Entity[T any](name string) *EntityHandle[T] {
	return &EntityHandle[T]{name: name}
}

// EntityHandle is a declared entity store for values of type T. Keyed like
// every keyed store: Get, Put, Delete, Exists. Transactions and queries are
// methods on the store, never framework-level globals.
type EntityHandle[T any] struct {
	name string

	schemaOnce sync.Once
	schema     *entitySchema
	schemaErr  error
}

// entitySchema is T's declared schema, computed once per handle at first
// use (never per request): the id field and the indexed fields, with
// their JSON document names.
type entitySchema struct {
	idField string            // Go name of the entity:"id" field
	idJSON  string            // its JSON document name
	indexed map[string]string // Go field name → JSON name, entity:"index" fields
	// indexedJSON is the same set as a sorted slice — the shape every
	// write hands to the backend. Built here, once, rather than
	// assembled and sorted on each Put, Update and Tx.
	indexedJSON []string
}

// Get returns the entity stored under id. tako.IsNotFound reports absence.
func (e *EntityHandle[T]) Get(ctx Ctx, id string) (T, error) {
	var v T
	doc, err := entityBackend(ctx).Get(ctx, e.name, id)
	if err != nil {
		return v, mapNotFound(err)
	}
	return v, json.Unmarshal(doc, &v)
}

// Put stores the entity under the id carried by its `entity:"id"` field.
func (e *EntityHandle[T]) Put(ctx Ctx, entity T) error {
	id, doc, err := e.encode(entity)
	if err != nil {
		return err
	}
	indexed, err := e.indexedFields()
	if err != nil {
		return err
	}
	return entityBackend(ctx).Put(ctx, e.name, id, doc, indexed)
}

// indexedFields is the entity:"index" set, as JSON names, for the store.
//
// This is the whole of FL20 from the SDK's side: the tag used to be
// read for the query layer's benefit and never reached a write, so the
// backend mirrored every scalar into an index and a long untagged field
// was refused for an index nobody declared. One handle, one schema, one
// place — an app cannot get this wrong, because it never sees it.
func (e *EntityHandle[T]) indexedFields() ([]string, error) {
	s, err := e.loadSchema()
	if err != nil {
		return nil, err
	}
	// Built once with the schema, not per write: Put, Update and Tx all
	// ask for this, and it cannot change for the life of a handle.
	return s.indexedJSON, nil
}

// Delete removes the entity stored under id; deleting an absent id is not
// an error.
func (e *EntityHandle[T]) Delete(ctx Ctx, id string) error {
	return entityBackend(ctx).Delete(ctx, e.name, id)
}

// Exists reports whether an entity is stored under id.
func (e *EntityHandle[T]) Exists(ctx Ctx, id string) (bool, error) {
	return entityBackend(ctx).Exists(ctx, e.name, id)
}

// Query starts a query against the store. Only fields tagged
// `entity:"index"` are queryable; querying anything else is an
// analysis-time error, and the analyzer derives indexes from the actual
// call sites.
func (e *EntityHandle[T]) Query(ctx Ctx) *EntityQuery[T] {
	return &EntityQuery[T]{h: e, ctx: ctx}
}

// Update performs a transactional read-modify-write of the entity stored
// under id. tako.IsNotFound reports an absent id.
func (e *EntityHandle[T]) Update(ctx Ctx, id string, mutate func(*T)) error {
	indexed, err := e.indexedFields()
	if err != nil {
		return err
	}
	err = entityBackend(ctx).Update(ctx, e.name, id, indexed, func(doc []byte) ([]byte, error) {
		var v T
		if err := json.Unmarshal(doc, &v); err != nil {
			return nil, err
		}
		mutate(&v)
		return json.Marshal(v)
	})
	return mapNotFound(err)
}

// Tx runs fn in a transaction scoped to this store. If fn returns an
// error the transaction is rolled back and the error is returned
// unchanged, so status errors (tako.Errorf) compose with transactions.
func (e *EntityHandle[T]) Tx(ctx Ctx, fn func(tx EntityTx[T]) error) error {
	indexed, err := e.indexedFields()
	if err != nil {
		return err
	}
	return entityBackend(ctx).Tx(ctx, e.name, indexed, func(ops backends.EntityTxOps) error {
		return fn(&entityTx[T]{h: e, ops: ops})
	})
}

// encode marshals the entity and extracts its id via the JSON document —
// the same encoding path every response takes.
func (e *EntityHandle[T]) encode(entity T) (string, []byte, error) {
	s, err := e.loadSchema()
	if err != nil {
		return "", nil, err
	}
	doc, err := json.Marshal(entity)
	if err != nil {
		return "", nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(doc, &m); err != nil {
		return "", nil, err
	}
	id, _ := m[s.idJSON].(string)
	if id == "" {
		return "", nil, fmt.Errorf("tako: entity %q: id field %s is empty", e.name, s.idField)
	}
	return id, doc, nil
}

// loadSchema reads T's entity tags once per handle. The one-time
// reflection here is init-shaped work, never per-request.
func (e *EntityHandle[T]) loadSchema() (*entitySchema, error) {
	e.schemaOnce.Do(func() {
		t := reflect.TypeFor[T]()
		if t.Kind() != reflect.Struct {
			e.schemaErr = fmt.Errorf("tako: entity %q: type %s is not a struct", e.name, t)
			return
		}
		s := &entitySchema{indexed: map[string]string{}}
		for i := range t.NumField() {
			f := t.Field(i)
			switch entitytag.Kind(string(f.Tag)) {
			case "id":
				s.idField = f.Name
				s.idJSON = entitytag.Name(string(f.Tag), f.Name)
			case "index":
				// A field the document does not carry cannot be
				// indexed, and the failure is silent: the write declares
				// an index on a key no document has, the query filters
				// on it, and Datastore answers nothing without erroring.
				// That is the exact shape FL20 exists to remove, so it
				// is refused where the tags are read.
				if entitytag.Omitted(string(f.Tag)) {
					e.schemaErr = fmt.Errorf("tako: entity %q: field %s is tagged entity:\"index\" "+
						"but json:\"-\" keeps it out of the stored document — an index on a field "+
						"nothing writes matches nothing, silently. Drop one of the two tags.",
						e.name, f.Name)
					return
				}
				s.indexed[f.Name] = entitytag.Name(string(f.Tag), f.Name)
			}
		}
		if s.idField == "" {
			e.schemaErr = fmt.Errorf("tako: entity %q: type %s has no entity:\"id\" field", e.name, t)
			return
		}
		s.indexedJSON = make([]string, 0, len(s.indexed))
		for _, name := range s.indexed {
			s.indexedJSON = append(s.indexedJSON, name)
		}
		sort.Strings(s.indexedJSON) // stable, so a plan or a log reads the same twice
		e.schema = s
	})
	return e.schema, e.schemaErr
}

// EntityQuery builds a query against one entity store. Builder methods
// return the query for chaining; All, First, or Iter execute it.
type EntityQuery[T any] struct {
	h   *EntityHandle[T]
	ctx Ctx
	q   backends.EntityQuery
	err error
}

// Where adds a filter on an indexed field. Op is one of "=", "<", "<=",
// ">", ">=".
func (q *EntityQuery[T]) Where(field, op string, value any) *EntityQuery[T] {
	jsonField, err := q.indexedField(field)
	if err != nil {
		return q.fail(err)
	}
	switch op {
	case "=", "<", "<=", ">", ">=":
	default:
		return q.fail(fmt.Errorf("tako: entity %q: unknown query op %q", q.h.name, op))
	}
	q.q.Filters = append(q.q.Filters, backends.Filter{Field: jsonField, Op: op, Value: value})
	return q
}

// OrderBy sorts results by the named indexed field; prefix with "-" for
// descending order.
func (q *EntityQuery[T]) OrderBy(field string) *EntityQuery[T] {
	name := field
	desc := false
	if len(name) > 0 && name[0] == '-' {
		name, desc = name[1:], true
	}
	jsonField, err := q.indexedField(name)
	if err != nil {
		return q.fail(err)
	}
	if desc {
		jsonField = "-" + jsonField
	}
	q.q.Order = jsonField
	return q
}

// Limit caps the number of results.
func (q *EntityQuery[T]) Limit(n int) *EntityQuery[T] {
	q.q.Limit = n
	return q
}

// All executes the query and returns every matching entity.
func (q *EntityQuery[T]) All() ([]T, error) {
	if q.err != nil {
		return nil, q.err
	}
	docs, err := entityBackend(q.ctx).Query(q.ctx, q.h.name, q.q)
	if err != nil {
		return nil, err
	}
	out := make([]T, len(docs))
	for i, doc := range docs {
		if err := json.Unmarshal(doc, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// First executes the query and returns the first matching entity.
// tako.IsNotFound reports an empty result.
func (q *EntityQuery[T]) First() (T, error) {
	var v T
	if q.err != nil {
		return v, q.err
	}
	q.q.Limit = 1
	all, err := q.All()
	if err != nil {
		return v, err
	}
	if len(all) == 0 {
		return v, errNotFound
	}
	return all[0], nil
}

// EntityPage is one page of results and where to resume.
type EntityPage[T any] struct {
	// Items are this page's entities, in the query's order.
	Items []T
	// Cursor resumes after this page. It is EMPTY on the last page, so
	// `for cursor := ""; ; ` terminates on it rather than on an empty
	// Items — a page can be short without being last.
	Cursor string
	// More reports whether another page exists. It is Cursor != "",
	// named because `if page.More` reads as the question being asked.
	More bool
}

// Page executes the query and returns ONE page, resuming after cursor.
// The empty string starts at the beginning.
//
//	page, err := photos.Query(ctx).OrderBy("-Created").Limit(20).Page(cursor)
//	for _, p := range page.Items { … }
//	// page.Cursor is the next ?cursor=, empty when done
//
// **OrderBy and Limit are both required**, and Page refuses without
// them rather than guessing. A cursor names a position in an order, and
// the cloud backend cannot always push tako's default id order down
// beside an inequality filter — it sorts client-side instead, and the
// server cursor it holds is then a position in a DIFFERENT order than
// you asked for. Handed back, that silently skips or repeats rows. The
// refusal is identical on both backends, so a query cannot page
// correctly in development and lie in production.
//
// The cursor is opaque and belongs to the query that produced it.
// Changing the filters or the order between pages is a different walk,
// not a continuation of this one.
func (q *EntityQuery[T]) Page(cursor string) (EntityPage[T], error) {
	var page EntityPage[T]
	if q.err != nil {
		return page, q.err
	}
	docs, next, err := entityBackend(q.ctx).Page(q.ctx, q.h.name, q.q, cursor)
	if err != nil {
		return page, err
	}
	page.Items = make([]T, len(docs))
	for i, doc := range docs {
		if err := json.Unmarshal(doc, &page.Items[i]); err != nil {
			return EntityPage[T]{}, err
		}
	}
	page.Cursor, page.More = next, next != ""
	return page, nil
}

// Count executes the query and returns how many entities match.
//
//	total, err := posts.Query(ctx).Where("Published", "=", true).Count()
//
// It answers the question a numbered pager asks — "how many are there" —
// and so it IGNORES Limit. Limit shapes a page; the total is not a
// property of the page. `Limit(50).Count()` returns every match, not 50.
//
// On the cloud backend this is a Datastore aggregation, billed per index
// entry scanned rather than per entity returned, so counting a large
// match does not read it.
func (q *EntityQuery[T]) Count() (int64, error) {
	if q.err != nil {
		return 0, q.err
	}
	return entityBackend(q.ctx).Count(q.ctx, q.h.name, q.q)
}

// Iter executes the query and iterates matching entities.
func (q *EntityQuery[T]) Iter() Iter[T] {
	all, err := q.All()
	return Iter[T]{items: all, err: err}
}

func (q *EntityQuery[T]) fail(err error) *EntityQuery[T] {
	if q.err == nil {
		q.err = err
	}
	return q
}

func (q *EntityQuery[T]) indexedField(field string) (string, error) {
	s, err := q.h.loadSchema()
	if err != nil {
		return "", err
	}
	jsonField, ok := s.indexed[field]
	if !ok {
		return "", fmt.Errorf("tako: entity %q: %s is not an indexed field", q.h.name, field)
	}
	return jsonField, nil
}

// EntityTx is a multi-entity transaction scoped to one store, passed to
// EntityHandle.Tx.
type EntityTx[T any] interface {
	// Get returns the entity stored under id within the transaction.
	Get(id string) (T, error)
	// Put stages the entity for write when the transaction commits.
	Put(entity T) error
	// Delete stages removal of the entity stored under id.
	Delete(id string) error
}

type entityTx[T any] struct {
	h   *EntityHandle[T]
	ops backends.EntityTxOps
}

func (t *entityTx[T]) Get(id string) (T, error) {
	var v T
	doc, err := t.ops.Get(id)
	if err != nil {
		return v, mapNotFound(err)
	}
	return v, json.Unmarshal(doc, &v)
}

func (t *entityTx[T]) Put(entity T) error {
	id, doc, err := t.h.encode(entity)
	if err != nil {
		return err
	}
	return t.ops.Put(id, doc)
}

func (t *entityTx[T]) Delete(id string) error { return t.ops.Delete(id) }

// entityBackend returns the wired entity backend, which boot (generated
// mains, tako dev, and — from F7 — NewTestApp) is responsible for
// installing.
func entityBackend(ctx Ctx) backends.Entity {
	if a := appFrom(ctx); a != nil {
		return a.Entity
	}
	if hooks.Entity == nil {
		panic(unwiredPanic("entity"))
	}
	return hooks.Entity
}

// mapNotFound translates the backend's absence marker to the SDK's.
func mapNotFound(err error) error {
	if err == nil {
		return nil
	}
	if err == backends.ErrNotFound {
		return errNotFound
	}
	return err
}
