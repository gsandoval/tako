package backends

import (
	"context"
	"io"
	"time"
)

// Bucket is the storage contract behind tako.Bucket. Objects are byte
// streams keyed by (bucket, key) with a content type. The conformance
// suite defines the behavior every implementation must share.
type Bucket interface {
	// Get returns the object under (bucket, key), or ErrNotFound. The
	// returned reader may implement io.Closer; the caller closes it.
	Get(ctx context.Context, bucket, key string) (BlobData, error)
	// Put stores the stream under (bucket, key), replacing any existing
	// object.
	Put(ctx context.Context, bucket, key string, data BlobData) error
	// Delete removes (bucket, key); deleting an absent key is not an
	// error.
	Delete(ctx context.Context, bucket, key string) error
	// Exists reports whether (bucket, key) holds an object.
	Exists(ctx context.Context, bucket, key string) (bool, error)
	// List returns the objects whose keys start with prefix, in
	// lexicographic key order.
	List(ctx context.Context, bucket, prefix string) ([]ObjectMeta, error)
}

// BlobData is an object stream with its metadata.
type BlobData struct {
	// Reader streams the object's bytes; close it if it is a Closer.
	Reader io.Reader
	// ContentType is the object's MIME type.
	ContentType string
	// Size is the stream length in bytes, or -1 when unknown.
	Size int64
}

// ObjectMeta describes one stored object.
type ObjectMeta struct {
	// Key is the object's key within its bucket.
	Key string
	// Size is the object's length in bytes.
	Size int64
	// ContentType is the object's MIME type.
	ContentType string
	// Updated is when the object was last written.
	Updated time.Time
}
