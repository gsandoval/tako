package backends

import (
	"context"
	"time"
)

// SignConstraints are the upload limits a SignedPut URL carries. They
// are enforced at upload time by whatever serves the URL — the local
// endpoint in dev, GCS itself in the cloud.
type SignConstraints struct {
	// MaxSize caps the upload in bytes; 0 means unlimited.
	MaxSize int64
	// ContentType restricts the upload's content type; "image/*" matches
	// the type's prefix, a pattern without '*' must match exactly, ""
	// accepts anything.
	ContentType string
}

// URLSigner is the contract behind SignedPut/SignedGet: mint URLs that
// let a client transfer object bytes directly, bypassing the service.
type URLSigner interface {
	// SignPut returns a URL accepting one PUT of the object's bytes for
	// ttl, subject to c.
	SignPut(ctx context.Context, bucket, key string, ttl time.Duration, c SignConstraints) (string, error)
	// SignGet returns a URL streaming the object for ttl.
	SignGet(ctx context.Context, bucket, key string, ttl time.Duration) (string, error)
}
