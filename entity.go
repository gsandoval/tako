package tako

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"time"

	"github.com/gsandoval/tako/internal/entitytag"
	"github.com/gsandoval/tako/internal/hooks"
	"github.com/gsandoval/tako/internal/trace"
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

type blobField struct {
	fieldIndex int
	bucket     string
}

// entitySchema is T's declared schema, computed once per handle at first
// use (never per request): the id field, optional ttl field, linked blob
// fields, and the indexed fields, with their JSON document names.
type entitySchema struct {
	idField  string            // Go name of the entity:"id" field
	idIndex  int               // struct field index of idField
	idJSON   string            // its JSON document name
	ttlField string            // Go name of the entity:"ttl" field, if any
	ttlIndex int               // struct field index of ttlField (-1 when none)
	ttlJSON  string            // its JSON document name
	blobs    []blobField       // fields tagged entity:"blob:<bucket>"
	indexed  map[string]string // Go field name → JSON name, entity:"index" fields
	// indexedJSON is the same set as a sorted slice — the shape every
	// write hands to the backend. Built here, once, rather than
	// assembled and sorted on each Put, Update and Tx.
	indexedJSON []string
}

// Get returns the entity stored under id. tako.IsNotFound reports absence.
func (e *EntityHandle[T]) Get(ctx Ctx, id string) (T, error) {
	var v T
	var retErr error
	sctx, finish := trace.StartSpan(ctx, "entity", e.name+".Get", "entity", e.name, "op", "Get", "id", id)
	defer func() { finish(retErr) }()
	if _, err := e.loadSchema(); err != nil {
		retErr = err
		return v, retErr
	}
	doc, err := entityBackend(ctx).Get(sctx, e.name, id)
	if err != nil {
		retErr = mapNotFound(err)
		return v, retErr
	}
	if err := json.Unmarshal(doc, &v); err != nil {
		retErr = err
		return v, retErr
	}
	if e.isExpiredValue(v) {
		var zero T
		retErr = errNotFound
		return zero, retErr
	}
	return v, nil
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
	sctx, finish := trace.StartSpan(ctx, "entity", e.name+".Put", "entity", e.name, "op", "Put", "id", id)
	err = entityBackend(ctx).Put(sctx, e.name, id, doc, indexed)
	finish(err)
	return err
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

// Delete removes the entity stored under id (and any bucket objects linked
// via `entity:"blob:<bucket>"` struct tags); deleting an absent id is not
// an error.
func (e *EntityHandle[T]) Delete(ctx Ctx, id string) error {
	sctx, finish := trace.StartSpan(ctx, "entity", e.name+".Delete", "entity", e.name, "op", "Delete", "id", id)
	if s, err := e.loadSchema(); err == nil && len(s.blobs) > 0 {
		if doc, gerr := entityBackend(ctx).Get(sctx, e.name, id); gerr == nil {
			var v T
			if json.Unmarshal(doc, &v) == nil {
				e.deleteBlobsOf(ctx, v)
			}
		}
	}
	err := entityBackend(ctx).Delete(sctx, e.name, id)
	finish(err)
	return err
}

// ReapExpired scans the store for entities whose `entity:"ttl"` timestamp has
// passed, deletes any bucket objects linked via `entity:"blob:<bucket>"`, and
// removes the expired entity records. It returns the number of reaped entities.
func (e *EntityHandle[T]) ReapExpired(ctx Ctx) (int, error) {
	s, err := e.loadSchema()
	if err != nil {
		return 0, err
	}
	if s.ttlIndex < 0 {
		return 0, nil
	}
	var retErr error
	sctx, finish := trace.StartSpan(ctx, "entity", e.name+".ReapExpired", "entity", e.name, "op", "ReapExpired")
	defer func() { finish(retErr) }()
	docs, err := entityBackend(ctx).Query(sctx, e.name, backends.EntityQuery{})
	if err != nil {
		retErr = err
		return 0, err
	}
	reaped := 0
	for _, doc := range docs {
		var item T
		if err := json.Unmarshal(doc, &item); err != nil {
			continue
		}
		if !e.isExpiredValue(item) {
			continue
		}
		e.deleteBlobsOf(ctx, item)
		if id := e.idOfValue(item); id != "" {
			_ = entityBackend(ctx).Delete(sctx, e.name, id)
		}
		reaped++
	}
	return reaped, nil
}

// Exists reports whether an entity is stored under id.
func (e *EntityHandle[T]) Exists(ctx Ctx, id string) (bool, error) {
	s, err := e.loadSchema()
	if err != nil {
		return false, err
	}
	if s.ttlIndex >= 0 {
		_, err := e.Get(ctx, id)
		if err == nil {
			return true, nil
		}
		if IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	sctx, finish := trace.StartSpan(ctx, "entity", e.name+".Exists", "entity", e.name, "op", "Exists", "id", id)
	ok, err := entityBackend(ctx).Exists(sctx, e.name, id)
	finish(err)
	return ok, err
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
	sctx, finish := trace.StartSpan(ctx, "entity", e.name+".Update", "entity", e.name, "op", "Update", "id", id)
	err = entityBackend(ctx).Update(sctx, e.name, id, indexed, func(doc []byte) ([]byte, error) {
		var v T
		if err := json.Unmarshal(doc, &v); err != nil {
			return nil, err
		}
		if e.isExpiredValue(v) {
			return nil, backends.ErrNotFound
		}
		mutate(&v)
		return json.Marshal(v)
	})
	err = mapNotFound(err)
	finish(err)
	return err
}

// Upsert performs a transactional create-or-update of the entity stored
// under id. When id is absent (or its TTL has expired), mutate is called
// on a zero-initialized T with its `entity:"id"` field populated to id;
// otherwise mutate is called on the existing entity.
func (e *EntityHandle[T]) Upsert(ctx Ctx, id string, mutate func(*T)) error {
	s, err := e.loadSchema()
	if err != nil {
		return err
	}
	sctx, finish := trace.StartSpan(ctx, "entity", e.name+".Upsert", "entity", e.name, "op", "Upsert", "id", id)
	err = entityBackend(ctx).Tx(sctx, e.name, s.indexedJSON, func(ops backends.EntityTxOps) error {
		var v T
		doc, getErr := ops.Get(id)
		if getErr != nil && getErr != backends.ErrNotFound {
			return getErr
		}
		if getErr == nil {
			if err := json.Unmarshal(doc, &v); err != nil {
				return err
			}
			if e.isExpiredValue(v) {
				e.deleteBlobsOf(ctx, v)
				var zero T
				v = zero
			}
		}
		e.populateID(&v, id)
		mutate(&v)
		e.populateID(&v, id)
		_, nextDoc, err := e.encode(v)
		if err != nil {
			return err
		}
		return ops.Put(id, nextDoc)
	})
	finish(err)
	return err
}

func (e *EntityHandle[T]) populateID(v *T, id string) {
	s, err := e.loadSchema()
	if err != nil || s.idIndex < 0 {
		return
	}
	rv := reflect.ValueOf(v).Elem()
	f := rv.Field(s.idIndex)
	if f.Kind() == reflect.String && f.CanSet() && f.String() == "" {
		f.SetString(id)
	}
}

func (e *EntityHandle[T]) idOfValue(v T) string {
	s, err := e.loadSchema()
	if err != nil || s.idIndex < 0 {
		return ""
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Struct {
		return ""
	}
	f := rv.Field(s.idIndex)
	if f.Kind() == reflect.String {
		return f.String()
	}
	return ""
}

func (e *EntityHandle[T]) deleteBlobsOf(ctx Ctx, v T) {
	s, err := e.loadSchema()
	if err != nil || len(s.blobs) == 0 {
		return
	}
	var bb backends.Bucket
	if a := appFrom(ctx); a != nil && a.Buckets != nil {
		bb = a.Buckets
	} else if hooks.Bucket != nil {
		bb = hooks.Bucket
	}
	if bb == nil {
		return
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Struct {
		return
	}
	for _, bf := range s.blobs {
		f := rv.Field(bf.fieldIndex)
		if f.Kind() == reflect.String {
			if key := f.String(); key != "" {
				_ = bb.Delete(ctx, bf.bucket, key)
			}
		}
	}
}

func (e *EntityHandle[T]) isExpiredValue(v T) bool {
	s, err := e.loadSchema()
	if err != nil || s.ttlIndex < 0 {
		return false
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Struct {
		return false
	}
	exp, ok := rv.Field(s.ttlIndex).Interface().(time.Time)
	return ok && !exp.IsZero() && exp.Before(time.Now())
}

// Tx runs fn in a transaction scoped to this store. If fn returns an
// error the transaction is rolled back and the error is returned
// unchanged, so status errors (tako.Errorf) compose with transactions.
func (e *EntityHandle[T]) Tx(ctx Ctx, fn func(tx EntityTx[T]) error) error {
	indexed, err := e.indexedFields()
	if err != nil {
		return err
	}
	sctx, finish := trace.StartSpan(ctx, "entity", e.name+".Tx", "entity", e.name, "op", "Tx")
	err = entityBackend(ctx).Tx(sctx, e.name, indexed, func(ops backends.EntityTxOps) error {
		return fn(&entityTx[T]{h: e, ctx: ctx, ops: ops})
	})
	finish(err)
	return err
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
		s := &entitySchema{indexed: map[string]string{}, idIndex: -1, ttlIndex: -1}
		for i := range t.NumField() {
			f := t.Field(i)
			switch entitytag.Kind(string(f.Tag)) {
			case "id":
				s.idField = f.Name
				s.idIndex = i
				s.idJSON = entitytag.Name(string(f.Tag), f.Name)
			case "ttl":
				if entitytag.Omitted(string(f.Tag)) {
					e.schemaErr = fmt.Errorf("tako: entity %q: field %s is tagged entity:\"ttl\" "+
						"but json:\"-\" keeps it out of the stored document", e.name, f.Name)
					return
				}
				if f.Type != reflect.TypeFor[time.Time]() {
					e.schemaErr = fmt.Errorf("tako: entity %q: field %s is tagged entity:\"ttl\" with type %s — TTL fields must be time.Time",
						e.name, f.Name, f.Type)
					return
				}
				if s.ttlField != "" {
					e.schemaErr = fmt.Errorf("tako: entity %q: both %s and %s are tagged entity:\"ttl\" — an entity may have at most one TTL field",
						e.name, s.ttlField, f.Name)
					return
				}
				s.ttlField = f.Name
				s.ttlIndex = i
				s.ttlJSON = entitytag.Name(string(f.Tag), f.Name)
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
			if bucket := entitytag.BlobBucket(string(f.Tag)); bucket != "" {
				if f.Type.Kind() != reflect.String {
					e.schemaErr = fmt.Errorf("tako: entity %q: field %s is tagged entity:\"blob:%s\" with type %s — blob key fields must be string",
						e.name, f.Name, bucket, f.Type)
					return
				}
				s.blobs = append(s.blobs, blobField{fieldIndex: i, bucket: bucket})
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
	if _, err := q.h.loadSchema(); err != nil {
		return nil, err
	}
	var retErr error
	sctx, finish := trace.StartSpan(q.ctx, "entity", q.h.name+".Query", "entity", q.h.name, "op", "Query")
	defer func() { finish(retErr) }()
	docs, err := entityBackend(q.ctx).Query(sctx, q.h.name, q.q)
	if err != nil {
		retErr = err
		return nil, err
	}
	out := make([]T, 0, len(docs))
	for _, doc := range docs {
		var item T
		if err := json.Unmarshal(doc, &item); err != nil {
			retErr = err
			return nil, err
		}
		if q.h.isExpiredValue(item) {
			continue
		}
		out = append(out, item)
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
	var retErr error
	sctx, finish := trace.StartSpan(q.ctx, "entity", q.h.name+".Page", "entity", q.h.name, "op", "Page")
	defer func() { finish(retErr) }()
	docs, next, err := entityBackend(q.ctx).Page(sctx, q.h.name, q.q, cursor)
	if err != nil {
		retErr = err
		return page, err
	}
	page.Items = make([]T, 0, len(docs))
	for _, doc := range docs {
		var item T
		if err := json.Unmarshal(doc, &item); err != nil {
			retErr = err
			return EntityPage[T]{}, err
		}
		if q.h.isExpiredValue(item) {
			continue
		}
		page.Items = append(page.Items, item)
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
	s, err := q.h.loadSchema()
	if err != nil {
		return 0, err
	}
	if s.ttlIndex >= 0 {
		noLimit := *q
		noLimit.q.Limit = 0
		noLimit.q.Order = ""
		all, err := noLimit.All()
		return int64(len(all)), err
	}
	sctx, finish := trace.StartSpan(q.ctx, "entity", q.h.name+".Count", "entity", q.h.name, "op", "Count")
	n, err := entityBackend(q.ctx).Count(sctx, q.h.name, q.q)
	finish(err)
	return n, err
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
	ctx Ctx
	ops backends.EntityTxOps
}

func (t *entityTx[T]) Get(id string) (T, error) {
	var v T
	doc, err := t.ops.Get(id)
	if err != nil {
		return v, mapNotFound(err)
	}
	if err := json.Unmarshal(doc, &v); err != nil {
		return v, err
	}
	if t.h.isExpiredValue(v) {
		var zero T
		return zero, errNotFound
	}
	return v, nil
}

func (t *entityTx[T]) Put(entity T) error {
	id, doc, err := t.h.encode(entity)
	if err != nil {
		return err
	}
	return t.ops.Put(id, doc)
}

func (t *entityTx[T]) Delete(id string) error {
	if s, err := t.h.loadSchema(); err == nil && len(s.blobs) > 0 {
		if doc, gerr := t.ops.Get(id); gerr == nil {
			var v T
			if json.Unmarshal(doc, &v) == nil {
				t.h.deleteBlobsOf(t.ctx, v)
			}
		}
	}
	return t.ops.Delete(id)
}

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
