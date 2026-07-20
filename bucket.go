package tako

import (
	"time"

	"github.com/gsandoval/tako/internal/hooks"
	"github.com/gsandoval/tako/pkg/backends"
)

// Bucket declares an object store. The name is the bucket's stable identity
// and must be a string constant (analyzer-enforced).
func Bucket(name string, opts ...Option) *BucketHandle {
	b := &BucketHandle{name: name, opts: opts}
	b.Uploaded = BucketEvent{bucket: b}
	return b
}

// BucketHandle is a declared object store. Keyed like every keyed store:
// Get, Put, Delete, Exists.
type BucketHandle struct {
	// Uploaded is the event source that fires when an object finishes
	// uploading; bind a handler with tako.Subscribe(bucket.Uploaded, fn).
	Uploaded BucketEvent

	name string
	opts []Option
}

// Get returns the object stored under key. tako.IsNotFound reports absence.
func (b *BucketHandle) Get(ctx Ctx, key string) (Blob, error) {
	data, err := bucketBackend().Get(ctx, b.name, key)
	if err != nil {
		return Blob{}, mapNotFound(err)
	}
	return Blob{Reader: data.Reader, ContentType: data.ContentType, Size: data.Size}, nil
}

// Put stores blob under key, replacing any existing object.
func (b *BucketHandle) Put(ctx Ctx, key string, blob Blob) error {
	return bucketBackend().Put(ctx, b.name, key, backends.BlobData{
		Reader: blob.Reader, ContentType: blob.ContentType, Size: blob.Size,
	})
}

// Delete removes the object stored under key; deleting an absent key is
// not an error.
func (b *BucketHandle) Delete(ctx Ctx, key string) error {
	return bucketBackend().Delete(ctx, b.name, key)
}

// Exists reports whether an object is stored under key.
func (b *BucketHandle) Exists(ctx Ctx, key string) (bool, error) {
	return bucketBackend().Exists(ctx, b.name, key)
}

// List iterates the objects whose keys start with prefix, in
// lexicographic key order.
func (b *BucketHandle) List(ctx Ctx, prefix string) Iter[ObjectInfo] {
	metas, err := bucketBackend().List(ctx, b.name, prefix)
	if err != nil {
		return Iter[ObjectInfo]{err: err}
	}
	items := make([]ObjectInfo, len(metas))
	for i, m := range metas {
		items[i] = ObjectInfo{Key: m.Key, Size: m.Size, ContentType: m.ContentType, Updated: m.Updated}
	}
	return Iter[ObjectInfo]{items: items}
}

// SignedPut returns a URL that lets a client upload directly to key for the
// given duration, bypassing the service's request-size cap. Constrain
// uploads with WithMaxSize and WithContentType.
func (b *BucketHandle) SignedPut(ctx Ctx, key string, ttl time.Duration, opts ...Option) (string, error) {
	panic("tako: not implemented")
}

// SignedGet returns a URL that lets a client download key directly for the
// given duration.
func (b *BucketHandle) SignedGet(ctx Ctx, key string, ttl time.Duration) (string, error) {
	panic("tako: not implemented")
}

// BucketEvent is a bucket's event source, usable as a Subscribe binding.
// Its payload type is ObjectInfo.
type BucketEvent struct {
	bucket *BucketHandle
}

func (BucketEvent) eventSource(ObjectInfo) {}

func (e BucketEvent) sourceName() string { return e.bucket.name + ".Uploaded" }

// bucketBackend returns the wired bucket backend, which boot (generated
// mains, tako dev, and — from F7 — NewTestApp) is responsible for
// installing.
func bucketBackend() backends.Bucket {
	b := hooks.Bucket
	if b == nil {
		panic("tako: no bucket backend wired — run via tako dev or a generated service (tests boot one with runtime.BootLocal until NewTestApp lands in F7)")
	}
	return b
}
