package tako

import (
	"io"
	"sync"
	"time"

	"github.com/gsandoval/tako/internal/hooks"
	"github.com/gsandoval/tako/internal/trace"
	"github.com/gsandoval/tako/pkg/backends"
)

// Bucket declares an object store. The name is the bucket's stable identity
// and must be a string constant (analyzer-enforced).
func Bucket(name string, cfg ...BucketConfig) *BucketHandle {
	b := &BucketHandle{name: name, cfg: cfg}
	b.Uploaded = BucketEvent{bucket: b}
	return b
}

// BucketHandle is a declared object store. Keyed like every keyed store:
// Get, Put, Delete, Exists, Take.
type BucketHandle struct {
	// Uploaded is the event source that fires when an object finishes
	// uploading; bind a handler with tako.Subscribe(bucket.Uploaded, fn).
	Uploaded BucketEvent

	name string
	cfg  []BucketConfig
}

type deleteOnCloseReader struct {
	r      io.Reader
	once   sync.Once
	onDone func()
}

func (d *deleteOnCloseReader) Read(p []byte) (int, error) {
	n, err := d.r.Read(p)
	if err == io.EOF {
		_ = d.Close()
	}
	return n, err
}

func (d *deleteOnCloseReader) Close() error {
	var closeErr error
	d.once.Do(func() {
		if c, ok := d.r.(io.Closer); ok {
			closeErr = c.Close()
		}
		if d.onDone != nil {
			d.onDone()
		}
	})
	return closeErr
}

// Take returns the object stored under key as a streaming Blob that
// automatically deletes the object from the bucket when the stream reaches
// EOF or is closed by the HTTP server.
func (b *BucketHandle) Take(ctx Ctx, key string) (Blob, error) {
	sctx, finish := trace.StartSpan(ctx, "bucket", b.name+".Take", "bucket", b.name, "op", "Take", "key", key)
	data, err := bucketBackend(ctx).Get(sctx, b.name, key)
	if err != nil {
		err = mapNotFound(err)
		finish(err)
		return Blob{}, err
	}
	finish(nil)
	return Blob{
		Reader: &deleteOnCloseReader{
			r: data.Reader,
			onDone: func() {
				_ = bucketBackend(ctx).Delete(ctx, b.name, key)
			},
		},
		ContentType: data.ContentType,
		Size:        data.Size,
		ETag:        data.ETag,
	}, nil
}

// Get returns the object stored under key. tako.IsNotFound reports absence.
func (b *BucketHandle) Get(ctx Ctx, key string) (Blob, error) {
	sctx, finish := trace.StartSpan(ctx, "bucket", b.name+".Get", "bucket", b.name, "op", "Get", "key", key)
	data, err := bucketBackend(ctx).Get(sctx, b.name, key)
	if err != nil {
		err = mapNotFound(err)
		finish(err)
		return Blob{}, err
	}
	finish(nil)
	// The ETag rides along, so a handler that returns this blob answers
	// If-None-Match without knowing the header exists (FL25).
	return Blob{Reader: data.Reader, ContentType: data.ContentType, Size: data.Size, ETag: data.ETag}, nil
}

// Put stores blob under key, replacing any existing object.
func (b *BucketHandle) Put(ctx Ctx, key string, blob Blob) error {
	sctx, finish := trace.StartSpan(ctx, "bucket", b.name+".Put", "bucket", b.name, "op", "Put", "key", key)
	err := bucketBackend(ctx).Put(sctx, b.name, key, backends.BlobData{
		Reader: blob.Reader, ContentType: blob.ContentType, Size: blob.Size,
	})
	finish(err)
	return err
}

// Delete removes the object stored under key; deleting an absent key is
// not an error.
func (b *BucketHandle) Delete(ctx Ctx, key string) error {
	sctx, finish := trace.StartSpan(ctx, "bucket", b.name+".Delete", "bucket", b.name, "op", "Delete", "key", key)
	err := bucketBackend(ctx).Delete(sctx, b.name, key)
	finish(err)
	return err
}

// Exists reports whether an object is stored under key.
func (b *BucketHandle) Exists(ctx Ctx, key string) (bool, error) {
	sctx, finish := trace.StartSpan(ctx, "bucket", b.name+".Exists", "bucket", b.name, "op", "Exists", "key", key)
	ok, err := bucketBackend(ctx).Exists(sctx, b.name, key)
	finish(err)
	return ok, err
}

// List iterates the objects whose keys start with prefix, in
// lexicographic key order.
func (b *BucketHandle) List(ctx Ctx, prefix string) Iter[ObjectInfo] {
	sctx, finish := trace.StartSpan(ctx, "bucket", b.name+".List", "bucket", b.name, "op", "List", "prefix", prefix)
	metas, err := bucketBackend(ctx).List(sctx, b.name, prefix)
	finish(err)
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
// uploads with a PutPolicy; constraints are enforced when the client
// uploads, not when the URL is minted — and PutPolicy.Headers gives the
// uploader exactly what this URL was signed to expect.
func (b *BucketHandle) SignedPut(ctx Ctx, key string, ttl time.Duration, policy ...PutPolicy) (string, error) {
	sctx, finish := trace.StartSpan(ctx, "bucket", b.name+".SignedPut", "bucket", b.name, "op", "SignedPut", "key", key)
	var c backends.SignConstraints
	for _, p := range policy {
		c.MaxSize = p.MaxSize
		c.ContentType = p.ContentType
	}
	u, err := signerBackend(ctx).SignPut(sctx, b.name, key, ttl, c)
	finish(err)
	return u, err
}

// SignedGet returns a URL that lets a client download key directly for the
// given duration.
func (b *BucketHandle) SignedGet(ctx Ctx, key string, ttl time.Duration) (string, error) {
	sctx, finish := trace.StartSpan(ctx, "bucket", b.name+".SignedGet", "bucket", b.name, "op", "SignedGet", "key", key)
	u, err := signerBackend(ctx).SignGet(sctx, b.name, key, ttl)
	finish(err)
	return u, err
}

// BucketEvent is a bucket's event source, usable as a Subscribe binding.
// Its payload type is ObjectInfo.
type BucketEvent struct {
	bucket *BucketHandle
}

func (BucketEvent) eventSource(ObjectInfo) {}

func (e BucketEvent) sourceName() string { return e.bucket.name + ".Uploaded" }

// signerBackend returns the wired URL signer, which boot (generated
// mains, tako dev, and — from F7 — NewTestApp) is responsible for
// installing.
func signerBackend(ctx Ctx) backends.URLSigner {
	if a := appFrom(ctx); a != nil {
		return a.Signer
	}
	if hooks.Signer == nil {
		panic("tako: no URL signer wired — run via tako dev, a generated service, or tako.NewTestApp")
	}
	return hooks.Signer
}

// bucketBackend returns the wired bucket backend, which boot (generated
// mains, tako dev, and — from F7 — NewTestApp) is responsible for
// installing.
func bucketBackend(ctx Ctx) backends.Bucket {
	if a := appFrom(ctx); a != nil {
		return a.Buckets
	}
	if hooks.Bucket == nil {
		panic(unwiredPanic("bucket"))
	}
	return hooks.Bucket
}
