package tako

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
	panic("tako: not implemented")
}

// Put caches value under key with the TTL from the declaration.
func (c *CacheHandle[T]) Put(ctx Ctx, key string, value T) error {
	panic("tako: not implemented")
}

// Delete removes the value cached under key.
func (c *CacheHandle[T]) Delete(ctx Ctx, key string) error {
	panic("tako: not implemented")
}

// Exists reports whether an unexpired value is cached under key.
func (c *CacheHandle[T]) Exists(ctx Ctx, key string) (bool, error) {
	panic("tako: not implemented")
}

// GetOrFill returns the cached value under key, calling fill and caching
// its result on a miss.
func (c *CacheHandle[T]) GetOrFill(ctx Ctx, key string, fill func(Ctx) (T, error)) (T, error) {
	panic("tako: not implemented")
}
