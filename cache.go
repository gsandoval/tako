package tako

import (
	"encoding/json"
	"time"

	"github.com/gsandoval/tako/internal/hooks"
	"github.com/gsandoval/tako/pkg/backends"
)

// Cache declares a typed cache with the given entry expiry. v0 is
// per-instance and in-memory — there is no cross-instance invalidation;
// a shared backend becomes a later opt-in without changing call sites.
// The name must be a string constant (analyzer-enforced).
//
// A zero ttl means entries never expire. It is a parameter rather than a
// setting because a cache without an expiry policy is a decision, not a
// default worth making silently.
func Cache[T any](name string, ttl time.Duration) *CacheHandle[T] {
	return &CacheHandle[T]{name: name, ttl: ttl}
}

// CacheHandle is a declared cache for values of type T. Keyed like every
// keyed store: Get, Put, Delete, Exists.
type CacheHandle[T any] struct {
	name string
	ttl  time.Duration
}

// Get returns the cached value under key. tako.IsNotFound reports a value
// that is absent or expired.
func (c *CacheHandle[T]) Get(ctx Ctx, key string) (T, error) {
	var value T
	raw, err := cacheBackend(ctx).Get(ctx, c.name, key)
	if err != nil {
		return value, mapNotFound(err)
	}
	return value, json.Unmarshal(raw, &value)
}

// Put caches value under key with the TTL from the declaration; a zero
// TTL means entries never expire.
func (c *CacheHandle[T]) Put(ctx Ctx, key string, value T) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return cacheBackend(ctx).Put(ctx, c.name, key, raw, c.ttl)
}

// Delete removes the value cached under key.
func (c *CacheHandle[T]) Delete(ctx Ctx, key string) error {
	return cacheBackend(ctx).Delete(ctx, c.name, key)
}

// Exists reports whether an unexpired value is cached under key.
func (c *CacheHandle[T]) Exists(ctx Ctx, key string) (bool, error) {
	return cacheBackend(ctx).Exists(ctx, c.name, key)
}

// GetOrFill returns the cached value under key, calling fill and caching
// its result on a miss. A fill error is returned and nothing is cached;
// concurrent misses may each fill (it's a cache — the last Put wins).
func (c *CacheHandle[T]) GetOrFill(ctx Ctx, key string, fill func(Ctx) (T, error)) (T, error) {
	value, err := c.Get(ctx, key)
	if err == nil {
		return value, nil
	}
	if !IsNotFound(err) {
		return value, err
	}
	value, err = fill(ctx)
	if err != nil {
		return value, err
	}
	return value, c.Put(ctx, key, value)
}

// cacheBackend returns the wired cache backend (see queueBackend).
func cacheBackend(ctx Ctx) backends.Cache {
	if a := appFrom(ctx); a != nil {
		return a.Cache
	}
	if hooks.Cache == nil {
		panic("tako: no cache backend wired — run via tako dev, a generated service, or tako.NewTestApp")
	}
	return hooks.Cache
}
