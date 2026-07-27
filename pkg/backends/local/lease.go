package local

import (
	"context"
	"sync"
	"time"
)

// MemoryLease is the local lease backend: an in-process CAS table with
// the same {owner, expiry, generation} semantics the GCS object lease
// has — mutual exclusion is real across goroutines (and dev's whole
// single process IS one instance).
type MemoryLease struct {
	mu     sync.Mutex
	leases map[string]leaseRec
	gen    int64
}

type leaseRec struct {
	owner   string
	token   int64
	expires time.Time
}

// NewMemoryLease builds an empty lease table.
func NewMemoryLease() *MemoryLease {
	return &MemoryLease{leases: map[string]leaseRec{}}
}

func (m *MemoryLease) TryAcquire(_ context.Context, name, owner string, ttl time.Duration) (bool, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if rec, ok := m.leases[name]; ok && rec.expires.After(time.Now()) {
		return false, 0, nil // live — not reentrant, even for its holder
	}
	m.gen++
	m.leases[name] = leaseRec{owner: owner, token: m.gen, expires: time.Now().Add(ttl)}
	return true, m.gen, nil
}

func (m *MemoryLease) Extend(_ context.Context, name, owner string, token int64, ttl time.Duration) (bool, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.leases[name]
	if !ok || rec.owner != owner || rec.token != token || !rec.expires.After(time.Now()) {
		return false, 0, nil
	}
	m.gen++
	m.leases[name] = leaseRec{owner: owner, token: m.gen, expires: time.Now().Add(ttl)}
	return true, m.gen, nil
}

func (m *MemoryLease) Release(_ context.Context, name, owner string, token int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if rec, ok := m.leases[name]; ok && rec.owner == owner && rec.token == token {
		delete(m.leases, name)
	}
	return nil
}

// Break force-releases a lease regardless of holder — the test hook
// behind conformance's loss-cancellation case (in the cloud the
// equivalent is deleting the lease object).
func (m *MemoryLease) Break(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.leases, name)
}
