package backends

import (
	"context"
	"errors"
)

// ErrNotFound marks an absent value. The SDK maps it to tako.IsNotFound.
var ErrNotFound = errors.New("not found")

// The two refusals every Page implementation shares. They live here
// rather than in each backend because the WORDING is the contract as
// much as the behaviour: a user meets this error, and meeting a
// different one on local than in the cloud would be its own bug.
var (
	// ErrPageNeedsOrder is the one that matters. gcp falls back to a
	// client-side sort when it cannot push an order down, and the
	// server cursor it holds is then a position in a different order
	// than the caller asked for — so paging without an explicit order
	// would work locally and silently skip or repeat rows in the cloud.
	// Refusing is the only answer that is the same in both.
	ErrPageNeedsOrder = errors.New("tako: Page needs an explicit OrderBy — a cursor names a position in an order, and there is no stable position without one; add OrderBy on whichever field you mean to page through")
	// ErrPageNeedsLimit refuses a page of everything, which would make
	// the cursor meaningless rather than wrong.
	ErrPageNeedsLimit = errors.New("tako: Page needs a page size — add Limit(n) to say how big a page is")
)

// Entity is the storage contract behind tako.Entity[T]. Values are opaque
// JSON documents keyed by (kind, id); queries filter and order on the
// documents' top-level JSON fields. The conformance suite defines the
// behavior every implementation must share.
type Entity interface {
	// Get returns the document stored under (kind, id), or ErrNotFound.
	Get(ctx context.Context, kind, id string) ([]byte, error)
	// Put stores doc under (kind, id), replacing any existing document.
	Put(ctx context.Context, kind, id string, doc []byte) error
	// Delete removes (kind, id); deleting an absent id is not an error.
	Delete(ctx context.Context, kind, id string) error
	// Exists reports whether (kind, id) holds a document.
	Exists(ctx context.Context, kind, id string) (bool, error)
	// Query returns the documents of kind matching q, in q's order.
	Query(ctx context.Context, kind string, q EntityQuery) ([][]byte, error)
	// Page returns ONE page of documents matching q, resuming after the
	// opaque cursor (empty starts at the beginning), plus the cursor to
	// resume from next. An empty returned cursor means the last page.
	//
	// q.Order and q.Limit are both REQUIRED, and an implementation must
	// refuse rather than guess. Without an explicit order there is no
	// stable position for a cursor to name — gcp falls back to a
	// client-side sort when it cannot push one down, and the server
	// cursor it holds is then a position in a DIFFERENT order than the
	// caller asked for, so handing it back would silently skip or repeat
	// rows.
	//
	// The cursor is KEYSET, not an offset: it names the last row seen,
	// so a row written before that position does not shift the next
	// page. Both backends must agree on that, which is what the
	// conformance suite's write-between-pages case pins.
	Page(ctx context.Context, kind string, q EntityQuery, after string) (docs [][]byte, next string, err error)
	// Count returns how many documents of kind match q's FILTERS.
	//
	// q.Limit and q.Order are ignored: Limit shapes a page and Count
	// answers the total, which is the question a numbered pager asks;
	// and an order can only change a count by excluding rows that lack
	// the sort key, which would be a wrong answer rather than a
	// different one.
	Count(ctx context.Context, kind string, q EntityQuery) (int64, error)
	// Update atomically applies mutate to the document under (kind, id).
	// It returns ErrNotFound when the id is absent, and mutate's error
	// unchanged when mutate fails.
	Update(ctx context.Context, kind, id string, mutate func(doc []byte) ([]byte, error)) error
	// Tx runs fn atomically against one kind: all of fn's writes commit
	// together, or none do when fn returns an error (returned unchanged).
	Tx(ctx context.Context, kind string, fn func(tx EntityTxOps) error) error
}

// EntityTxOps are the operations available inside an entity transaction.
type EntityTxOps interface {
	// Get returns the document under id as visible in the transaction.
	Get(id string) ([]byte, error)
	// Put stages doc under id.
	Put(id string, doc []byte) error
	// Delete stages removal of id.
	Delete(id string) error
}

// EntityQuery is a filtered, ordered, limited scan over one kind.
type EntityQuery struct {
	// Filters are ANDed conditions on top-level JSON fields.
	Filters []Filter
	// Order is a top-level JSON field to sort by; a "-" prefix sorts
	// descending; empty means id order.
	Order string
	// Limit caps the result count; 0 means no limit.
	Limit int
}

// Filter is one query condition.
type Filter struct {
	// Field is the top-level JSON field name.
	Field string
	// Op is one of "=", "<", "<=", ">", ">=".
	Op string
	// Value is compared against the field's JSON value.
	Value any
}
