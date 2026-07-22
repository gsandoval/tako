package backends

import (
	"context"
	"time"
)

// Cache is the storage contract behind tako.Cache[T]: a keyed store of
// opaque values with per-entry expiry. v0 backends are per-instance and
// in-memory — the contract deliberately promises no cross-instance
// invalidation.
type Cache interface {
	// Get returns the value under (cache, key), or ErrNotFound when the
	// key is absent or its entry has expired.
	Get(ctx context.Context, cache, key string) ([]byte, error)
	// Put stores value under (cache, key). ttl == 0 means the entry
	// never expires.
	Put(ctx context.Context, cache, key string, value []byte, ttl time.Duration) error
	// Delete removes the value under (cache, key); absent keys are not
	// an error.
	Delete(ctx context.Context, cache, key string) error
	// Exists reports whether an unexpired value sits under (cache, key).
	Exists(ctx context.Context, cache, key string) (bool, error)
}
