package backends

import (
	"context"
	"errors"
	"fmt"
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

// MaxIndexedBytes is Datastore's cap on the value of an INDEXED string
// property. Untagged fields are not indexed (FL20) and may hold as much
// as a document holds; a field carrying entity:"index" is subject to it.
const MaxIndexedBytes = 1500

// IndexedTooLong is the refusal both backends give for an indexed value
// past the cap, and it is deliberately shared rather than written twice.
//
// local does not index anything — it scans — so nothing about its own
// storage requires this check. It makes it anyway, because the
// alternative is an app that writes a 2 KB title happily on a laptop
// and fails the first time it is deployed. That difference is exactly
// what the conformance suite exists to prevent, so the limit is part of
// the contract rather than a property of one backend.
//
// The message names the tag, because the tag is the thing the app can
// change: the value is long because somebody asked for it to be
// queryable.
func IndexedTooLong(kind, field string, n int) error {
	return fmt.Errorf("tako: entity %q: %s is declared entity:%q and this value is %d bytes — "+
		"an indexed string is capped at %d. Drop the tag if you do not query on %s; "+
		"keep it and put the long text in another field, which is not indexed and has no such limit",
		kind, field, "index", n, MaxIndexedBytes, field)
}

// Entity is the storage contract behind tako.Entity[T]. Values are opaque
// JSON documents keyed by (kind, id); queries filter and order on the
// documents' top-level JSON fields. The conformance suite defines the
// behavior every implementation must share.
type Entity interface {
	// Get returns the document stored under (kind, id), or ErrNotFound.
	Get(ctx context.Context, kind, id string) ([]byte, error)
	// Put stores doc under (kind, id), replacing any existing document.
	//
	// indexed names the document's queryable fields, by their JSON
	// names — the fields carrying entity:"index". Everything else is
	// stored and never indexed, which is what lets a document hold a
	// value no index could (FL20).
	//
	// It is a parameter rather than something a store remembers per
	// kind because the two ways of getting this wrong are not equally
	// bad. Indexing too much fails the WRITE, loudly, at the cap.
	// Indexing too little fails the READ, SILENTLY: a Datastore filter
	// on an excluded property matches nothing and reports no error, so
	// a page of posts simply comes back empty. A store that had to be
	// told once, somewhere else, would fail that way whenever the
	// telling was skipped — in a bare test, say. Travelling with the
	// document, it cannot be skipped.
	Put(ctx context.Context, kind, id string, doc []byte, indexed []string) error
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
	// unchanged when mutate fails. indexed is Put's.
	Update(ctx context.Context, kind, id string, indexed []string, mutate func(doc []byte) ([]byte, error)) error
	// Tx runs fn atomically against one kind: all of fn's writes commit
	// together, or none do when fn returns an error (returned unchanged).
	//
	// indexed is declared once here, not per write, because a
	// transaction is scoped to ONE kind and a kind has one schema —
	// so EntityTxOps.Put has nothing left to get wrong.
	Tx(ctx context.Context, kind string, indexed []string, fn func(tx EntityTxOps) error) error
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
