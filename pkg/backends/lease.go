package backends

import (
	"context"
	"fmt"
	"math/rand"
	"sync/atomic"
	"time"
)

// Lease is the storage contract behind tako.Lock and cron singleflight
// (spec §4.4): a named compare-and-swap record {owner, expiry}. The
// backend surface is three single-attempt CAS operations — campaigning,
// heartbeats, and loss-cancellation are the SHARED logic below
// (DoLease / TryDoLease), so semantics cannot diverge per backend.
//
// Crash safety needs no release: the TTL expires and the next
// campaigner takes over. Tokens are fencing generations (the GCS
// object generation, a counter locally): extension and release succeed
// only for the exact holder, so a usurper can never be double-held.
type Lease interface {
	// TryAcquire attempts one acquisition of name: absent or expired →
	// held, with the new fencing token; live under any holder → false.
	// Leases are not reentrant — a holder's own retry is also false.
	TryAcquire(ctx context.Context, name, owner string, ttl time.Duration) (held bool, token int64, err error)
	// Extend renews the expiry iff (name, owner, token) still hold the
	// lease; ok=false means it was lost (expired and taken, or
	// released). A successful extension returns the next token.
	Extend(ctx context.Context, name, owner string, token int64, ttl time.Duration) (ok bool, newToken int64, err error)
	// Release deletes the lease iff (name, owner, token) still hold
	// it; having already lost it is not an error.
	Release(ctx context.Context, name, owner string, token int64) error
}

// LeaseTTL is the framework lease policy (spec §4.4): 30s TTL,
// extended every TTL/3, campaigns polling every ~2s. Not tunable in
// v0 — tako.Lock takes no options.
const (
	LeaseTTL  = 30 * time.Second
	leasePoll = 2 * time.Second
)

// TryDoLease runs fn only if the named lease is immediately
// acquirable, reporting whether it ran. While fn runs the lease is
// heartbeated; if it is lost mid-flight fn's ctx cancels.
func TryDoLease(ctx context.Context, l Lease, name, owner string, ttl time.Duration, fn func(context.Context) error) (bool, error) {
	held, token, err := l.TryAcquire(ctx, name, owner, ttl)
	if err != nil || !held {
		return false, err
	}
	return true, runHeld(ctx, l, name, owner, token, ttl, fn)
}

// DoLease campaigns until the lease is held (or ctx ends), then runs
// fn under it with the same heartbeat/loss semantics as TryDoLease.
func DoLease(ctx context.Context, l Lease, name, owner string, ttl time.Duration, fn func(context.Context) error) error {
	for {
		held, token, err := l.TryAcquire(ctx, name, owner, ttl)
		if err != nil {
			return err
		}
		if held {
			return runHeld(ctx, l, name, owner, token, ttl, fn)
		}
		// Jittered poll: campaigners shouldn't stampede the instant a
		// lease frees.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(leasePoll + time.Duration(rand.Int63n(int64(500*time.Millisecond)))):
		}
	}
}

// runHeld runs fn while holding the lease: extends at ttl/3, cancels
// fn's ctx on loss, releases on return. fn's own error wins; a lost
// lease surfaces only when fn itself succeeded.
func runHeld(ctx context.Context, l Lease, name, owner string, token int64, ttl time.Duration, fn func(context.Context) error) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var current atomic.Int64
	current.Store(token)
	var lost atomic.Bool
	hbDone := make(chan struct{})
	go func() {
		defer close(hbDone)
		tick := time.NewTicker(ttl / 3)
		defer tick.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-tick.C:
				// Transient errors are survivable — the TTL is the
				// backstop; only a definitive CAS miss means loss.
				ok, next, err := l.Extend(ctx, name, owner, current.Load(), ttl)
				if err != nil {
					continue
				}
				if !ok {
					lost.Store(true)
					cancel()
					return
				}
				current.Store(next)
			}
		}
	}()

	err := fn(runCtx)
	cancel()
	<-hbDone

	if lost.Load() {
		if err == nil {
			err = fmt.Errorf("lease %q was lost while held (ttl expired or forcibly taken) — work after the loss point may not have run", name)
		}
		return err // nothing to release; someone else may hold it now
	}
	// Release best-effort on the parent ctx even if fn's ctx ended.
	_ = l.Release(context.WithoutCancel(ctx), name, owner, current.Load())
	return err
}
