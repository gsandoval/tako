package local

import (
	"context"
	"sync"
	"time"

	"github.com/gsandoval/tako/pkg/backends"
)

// MemoryCache is the local cache backend: per-process, in-memory, entries
// expire on read past their deadline. This is also v0's honest cloud
// story — per-instance, no cross-instance invalidation.
type MemoryCache struct {
	mu     sync.Mutex
	caches map[string]map[string]cacheEntry
	now    func() time.Time
}

type cacheEntry struct {
	value   []byte
	expires time.Time // zero = never
}

// NewMemoryCache builds an empty cache set.
func NewMemoryCache() *MemoryCache {
	return &MemoryCache{caches: map[string]map[string]cacheEntry{}, now: time.Now}
}

func (m *MemoryCache) Get(_ context.Context, cache, key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.caches[cache][key]
	if !ok || m.expired(e) {
		delete(m.caches[cache], key)
		return nil, backends.ErrNotFound
	}
	return append([]byte(nil), e.value...), nil
}

func (m *MemoryCache) Put(_ context.Context, cache, key string, value []byte, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.caches[cache] == nil {
		m.caches[cache] = map[string]cacheEntry{}
	}
	e := cacheEntry{value: append([]byte(nil), value...)}
	if ttl > 0 {
		e.expires = m.now().Add(ttl)
	}
	m.caches[cache][key] = e
	return nil
}

func (m *MemoryCache) Delete(_ context.Context, cache, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.caches[cache], key)
	return nil
}

func (m *MemoryCache) Exists(_ context.Context, cache, key string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.caches[cache][key]
	if !ok || m.expired(e) {
		delete(m.caches[cache], key)
		return false, nil
	}
	return true, nil
}

func (m *MemoryCache) expired(e cacheEntry) bool {
	return !e.expires.IsZero() && m.now().After(e.expires)
}
