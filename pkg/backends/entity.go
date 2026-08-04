package backends

import (
	"context"
	"errors"
)

// ErrNotFound marks an absent value. The SDK maps it to tako.IsNotFound.
var ErrNotFound = errors.New("not found")

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
