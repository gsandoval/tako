package tako

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/gsandoval/tako/internal/hooks"
	"github.com/gsandoval/tako/pkg/backends"
)

// Cache declares a typed cache. v0 is per-instance and in-memory — there is
// no cross-instance invalidation; a shared backend becomes a later opt-in
// without changing call sites. The name must be a string constant
// (analyzer-enforced); set expiry with WithTTL.
func Cache[T any](name string, opts ...Option) *CacheHandle[T] {
	return &CacheHandle[T]{name: name, opts: opts}
}

// CacheHandle is a declared cache for values of type T. Keyed like every
// keyed store: Get, Put, Delete, Exists.
type CacheHandle[T any] struct {
	name string
	opts []Option
}

// Get returns the cached value under key. tako.IsNotFound reports a value
// that is absent or expired.
func (c *CacheHandle[T]) Get(ctx Ctx, key string) (T, error) {
	var value T
	if _, err := c.ttl(); err != nil {
		return value, err
	}
	raw, err := cacheBackend(ctx).Get(ctx, c.name, key)
	if err != nil {
		return value, mapNotFound(err)
	}
	return value, json.Unmarshal(raw, &value)
}

// Put caches value under key with the TTL from the declaration; no
// declared TTL means entries never expire.
func (c *CacheHandle[T]) Put(ctx Ctx, key string, value T) error {
	ttl, err := c.ttl()
	if err != nil {
		return err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return cacheBackend(ctx).Put(ctx, c.name, key, raw, ttl)
}

// Delete removes the value cached under key.
func (c *CacheHandle[T]) Delete(ctx Ctx, key string) error {
	if _, err := c.ttl(); err != nil {
		return err
	}
	return cacheBackend(ctx).Delete(ctx, c.name, key)
}

// Exists reports whether an unexpired value is cached under key.
func (c *CacheHandle[T]) Exists(ctx Ctx, key string) (bool, error) {
	if _, err := c.ttl(); err != nil {
		return false, err
	}
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

// ttl reads the declaration's options.
func (c *CacheHandle[T]) ttl() (time.Duration, error) {
	var ttl time.Duration
	for _, o := range c.opts {
		switch o.name {
		case "ttl":
			ttl = o.args[0].(time.Duration)
		default:
			return 0, fmt.Errorf("tako.%s is not a Cache option (Cache accepts WithTTL)", optionDisplayName(o.name))
		}
	}
	return ttl, nil
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
