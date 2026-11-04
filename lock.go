package tako

import (
	"context"
	"log/slog"

	"github.com/gsandoval/tako/internal/hooks"
	"github.com/gsandoval/tako/internal/trace"
	"github.com/gsandoval/tako/pkg/backends"
)

// Lock declares a distributed mutual-exclusion lock. Most apps never touch
// Lock directly — CronConfig.Singleflight and Singleton are built on the
// same mechanism. The name must be a string constant (analyzer-enforced).
//
// Lock guards critical SECTIONS — cross-entity rebuilds, calls into
// external systems, sequential cursors. It is NOT for single-entity
// atomicity: that is Tx's job, cheaper and contention-free. Locks are
// not reentrant, and a crash needs no cleanup — the lease's TTL (30s)
// expires and the next campaigner takes over.
func Lock(name string) *LockHandle {
	return &LockHandle{name: name}
}

// LockHandle is a declared distributed lock.
type LockHandle struct {
	name string
}

func leaseBackend(ctx context.Context) backends.Lease {
	if a := appFrom(ctx); a != nil && a.Lease != nil {
		return a.Lease
	}
	return hooks.Lease
}

// Do blocks until the lock is held, runs fn while holding it, and releases
// it on return. fn's Ctx is cancelled if the lock is lost mid-flight.
func (l *LockHandle) Do(ctx Ctx, fn func(Ctx) error) error {
	lease := leaseBackend(ctx)
	if lease == nil {
		panic(unwiredPanic("lease"))
	}
	sctx, finish := trace.StartSpan(ctx, "lock", l.name+".Do", "lock", l.name, "op", "Do")
	err := backends.DoLease(sctx, lease, l.name, hooks.LeaseOwner, backends.LeaseTTL, func(c context.Context) error {
		return fn(reroot(ctx, c))
	})
	finish(err)
	return err
}

// TryDo runs fn only if the lock is immediately available, reporting
// whether it ran; it returns (false, nil) when someone else holds the lock.
func (l *LockHandle) TryDo(ctx Ctx, fn func(Ctx) error) (bool, error) {
	lease := leaseBackend(ctx)
	if lease == nil {
		panic(unwiredPanic("lease"))
	}
	sctx, finish := trace.StartSpan(ctx, "lock", l.name+".TryDo", "lock", l.name, "op", "TryDo")
	ok, err := backends.TryDoLease(sctx, lease, l.name, hooks.LeaseOwner, backends.LeaseTTL, func(c context.Context) error {
		return fn(reroot(ctx, c))
	})
	finish(err)
	return ok, err
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
func (c rerootedCtx) Client() Client          { return c.inner.Client() }
func (c rerootedCtx) Task() (int, int)        { return c.inner.Task() }
