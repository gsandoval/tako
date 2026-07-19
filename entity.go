package tako

// Entity declares a typed entity store — an entity store, not "the
// database". Schema comes from the struct: `entity:"id"` marks the key
// field, `entity:"index"` marks queryable fields. The name is the store's
// stable identity and must be a string constant (analyzer-enforced).
func Entity[T any](name string, opts ...Option) *EntityHandle[T] {
	return &EntityHandle[T]{name: name, opts: opts}
}

// EntityHandle is a declared entity store for values of type T. Keyed like
// every keyed store: Get, Put, Delete, Exists. Transactions and queries are
// methods on the store, never framework-level globals.
type EntityHandle[T any] struct {
	name string
	opts []Option
}

// Get returns the entity stored under id. tako.IsNotFound reports absence.
func (e *EntityHandle[T]) Get(ctx Ctx, id string) (T, error) {
	panic("tako: not implemented")
}

// Put stores the entity under the id carried by its `entity:"id"` field.
func (e *EntityHandle[T]) Put(ctx Ctx, entity T) error {
	panic("tako: not implemented")
}

// Delete removes the entity stored under id.
func (e *EntityHandle[T]) Delete(ctx Ctx, id string) error {
	panic("tako: not implemented")
}

// Exists reports whether an entity is stored under id.
func (e *EntityHandle[T]) Exists(ctx Ctx, id string) (bool, error) {
	panic("tako: not implemented")
}

// Query starts a query against the store. Only fields tagged
// `entity:"index"` are queryable; querying anything else is an
// analysis-time error, and the analyzer derives composite indexes from the
// actual call sites.
func (e *EntityHandle[T]) Query(ctx Ctx) *EntityQuery[T] {
	panic("tako: not implemented")
}

// Update performs a transactional read-modify-write of the entity stored
// under id.
func (e *EntityHandle[T]) Update(ctx Ctx, id string, mutate func(*T)) error {
	panic("tako: not implemented")
}

// Tx runs fn in a transaction scoped to this store. If fn returns an error
// the transaction is rolled back.
func (e *EntityHandle[T]) Tx(ctx Ctx, fn func(tx EntityTx[T]) error) error {
	panic("tako: not implemented")
}

// EntityQuery builds a query against one entity store. Builder methods
// return the query for chaining; All, First, or Iter execute it.
type EntityQuery[T any] struct {
	err error
}

// Where adds a filter on an indexed field. Op is one of "=", "<", "<=",
// ">", ">=".
func (q *EntityQuery[T]) Where(field, op string, value any) *EntityQuery[T] {
	panic("tako: not implemented")
}

// OrderBy sorts results by the named indexed field; prefix with "-" for
// descending order.
func (q *EntityQuery[T]) OrderBy(field string) *EntityQuery[T] {
	panic("tako: not implemented")
}

// Limit caps the number of results.
func (q *EntityQuery[T]) Limit(n int) *EntityQuery[T] {
	panic("tako: not implemented")
}

// All executes the query and returns every matching entity.
func (q *EntityQuery[T]) All() ([]T, error) {
	panic("tako: not implemented")
}

// First executes the query and returns the first matching entity.
// tako.IsNotFound reports an empty result.
func (q *EntityQuery[T]) First() (T, error) {
	panic("tako: not implemented")
}

// Iter executes the query and iterates matching entities.
func (q *EntityQuery[T]) Iter() Iter[T] {
	panic("tako: not implemented")
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
