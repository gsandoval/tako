package tako

import "time"

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
	panic("tako: not implemented")
}

// Put stores blob under key, replacing any existing object.
func (b *BucketHandle) Put(ctx Ctx, key string, blob Blob) error {
	panic("tako: not implemented")
}

// Delete removes the object stored under key.
func (b *BucketHandle) Delete(ctx Ctx, key string) error {
	panic("tako: not implemented")
}

// Exists reports whether an object is stored under key.
func (b *BucketHandle) Exists(ctx Ctx, key string) (bool, error) {
	panic("tako: not implemented")
}

// List iterates the objects whose keys start with prefix.
func (b *BucketHandle) List(ctx Ctx, prefix string) Iter[ObjectInfo] {
	panic("tako: not implemented")
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
