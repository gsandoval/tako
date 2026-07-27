package tako

import (
	"context"
	"errors"
	"log/slog"

	"github.com/gsandoval/tako/internal/hooks"
	"github.com/gsandoval/tako/pkg/backends"
)

// Lock declares a distributed mutual-exclusion lock. Most apps never touch
// Lock directly — Cron's WithSingleflight and Singleton are built on the
// same mechanism. The name must be a string constant (analyzer-enforced).
//
// Lock guards critical SECTIONS — cross-entity rebuilds, calls into
// external systems, sequential cursors. It is NOT for single-entity
// atomicity: that is Tx's job, cheaper and contention-free. Locks are
// not reentrant, and a crash needs no cleanup — the lease's TTL (30s)
// expires and the next campaigner takes over.
func Lock(name string, opts ...Option) *LockHandle {
	return &LockHandle{name: name, opts: opts}
}

// LockHandle is a declared distributed lock.
type LockHandle struct {
	name string
	opts []Option
}

// Do blocks until the lock is held, runs fn while holding it, and releases
// it on return. fn's Ctx is cancelled if the lock is lost mid-flight.
func (l *LockHandle) Do(ctx Ctx, fn func(Ctx) error) error {
	if hooks.Lease == nil {
		return errors.New("tako: locks are not wired — Do works in booted services (tako dev / deploy) and NewTestApp tests, not bare tests")
	}
	return backends.DoLease(ctx, hooks.Lease, l.name, hooks.LeaseOwner, backends.LeaseTTL, func(c context.Context) error {
		return fn(reroot(ctx, c))
	})
}

// TryDo runs fn only if the lock is immediately available, reporting
// whether it ran; it returns (false, nil) when someone else holds the lock.
func (l *LockHandle) TryDo(ctx Ctx, fn func(Ctx) error) (bool, error) {
	if hooks.Lease == nil {
		return false, errors.New("tako: locks are not wired — TryDo works in booted services (tako dev / deploy) and NewTestApp tests, not bare tests")
	}
	return backends.TryDoLease(ctx, hooks.Lease, l.name, hooks.LeaseOwner, backends.LeaseTTL, func(c context.Context) error {
		return fn(reroot(ctx, c))
	})
}

// reroot re-bases a Ctx on a cancellable context — the lease machinery
// cancels fn when the lock is lost mid-flight.
func reroot(inner Ctx, c context.Context) Ctx { return rerootedCtx{Context: c, inner: inner} }

type rerootedCtx struct {
	context.Context
	inner Ctx
}

func (c rerootedCtx) User() User              { return c.inner.User() }
func (c rerootedCtx) MaybeUser() (User, bool) { return c.inner.MaybeUser() }
func (c rerootedCtx) Log() *slog.Logger       { return c.inner.Log() }
func (c rerootedCtx) IsHTMX() bool            { return c.inner.IsHTMX() }
