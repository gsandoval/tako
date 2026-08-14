package tako

import (
	"io"
	"time"
)

// Blob is a streamable object: a reader plus content type and size. Small
// blobs can be received and returned directly by API handlers; large ones
// go through signed URLs.
type Blob struct {
	// Reader streams the blob's bytes.
	Reader io.Reader
	// ContentType is the blob's MIME type.
	ContentType string
	// Size is the blob's length in bytes, or -1 when unknown.
	Size int64
	// ETag identifies these bytes, so a reader can revalidate instead of
	// downloading again: tako sends it, and answers 304 Not Modified when
	// the request's If-None-Match matches.
	//
	// bucket.Get fills it in, so a handler that streams a stored object
	// gets this for free. Set it yourself for bytes you build — a resize,
	// a rendered document — and make it change whenever the bytes do.
	// That is the whole contract, and it is the one thing tako cannot
	// check for you: an ETag that outlives its bytes serves the old ones
	// to anyone holding it.
	//
	// Empty sends no validator, which is what every blob did before.
	ETag string
}

// ObjectInfo describes an object in a Bucket. It is the payload of bucket
// event subscriptions and the element type of List iterations.
type ObjectInfo struct {
	// Key is the object's key within its bucket.
	Key string `json:"key"`
	// Size is the object's length in bytes.
	Size int64 `json:"size"`
	// ContentType is the object's MIME type.
	ContentType string `json:"contentType"`
	// Updated is when the object was last written.
	Updated time.Time `json:"updated"`
}

// Iter iterates a result stream. Call Next until it reports false, then
// check Err.
type Iter[T any] struct {
	items []T
	next  int
	err   error
}

// Next returns the next element and whether one was available.
func (it *Iter[T]) Next() (T, bool) {
	if it.err != nil || it.next >= len(it.items) {
		var zero T
		return zero, false
	}
	v := it.items[it.next]
	it.next++
	return v, true
}

// Err returns the error that ended iteration early, if any.
func (it *Iter[T]) Err() error {
	return it.err
}
