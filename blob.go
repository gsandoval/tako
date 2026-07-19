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
}

// ObjectInfo describes an object in a Bucket. It is the payload of bucket
// event subscriptions and the element type of List iterations.
type ObjectInfo struct {
	// Key is the object's key within its bucket.
	Key string
	// Size is the object's length in bytes.
	Size int64
	// ContentType is the object's MIME type.
	ContentType string
	// Updated is when the object was last written.
	Updated time.Time
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
