package conformance

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gsandoval/tako/pkg/backends"
)

// RunLease runs the lease-contract suite (spec §4.4) — the mechanism
// behind tako.Lock and cron singleflight. open returns a fresh backend
// plus a breaker that force-releases a lease regardless of holder (the
// loss-injection hook: MemoryLease.Break locally, an unconditional
// object delete on GCS). Chapters use short TTLs — expiry is
// wall-clock behavior, tested for real on both backends.
func RunLease(t *testing.T, open func(t *testing.T) (backends.Lease, func(name string))) {
	ctx := context.Background()

	t.Run("acquire excludes and release frees", func(t *testing.T) {
		l, _ := open(t)
		held, tok, err := l.TryAcquire(ctx, "excl", "a", 5*time.Second)
		if err != nil || !held {
			t.Fatalf("first acquire = %v, %v", held, err)
		}
		if again, _, err := l.TryAcquire(ctx, "excl", "b", 5*time.Second); err != nil || again {
			t.Fatalf("second acquire while held = %v, %v", again, err)
		}
		// Not reentrant: the holder's own retry is also refused.
		if self, _, err := l.TryAcquire(ctx, "excl", "a", 5*time.Second); err != nil || self {
			t.Fatalf("holder's own re-acquire = %v, %v", self, err)
		}
		if err := l.Release(ctx, "excl", "a", tok); err != nil {
			t.Fatal(err)
		}
		if free, _, err := l.TryAcquire(ctx, "excl", "b", 5*time.Second); err != nil || !free {
			t.Fatalf("acquire after release = %v, %v", free, err)
		}
	})

	t.Run("ttl expiry lets the next campaigner in", func(t *testing.T) {
		l, _ := open(t)
		held, _, err := l.TryAcquire(ctx, "ttl", "crashed", 700*time.Millisecond)
		if err != nil || !held {
			t.Fatalf("acquire = %v, %v", held, err)
		}
		// No heartbeat, no release — the crash story.
		time.Sleep(900 * time.Millisecond)
		took, _, err := l.TryAcquire(ctx, "ttl", "next", 5*time.Second)
		if err != nil || !took {
			t.Fatalf("acquire after expiry = %v, %v", took, err)
		}
	})

	t.Run("stale tokens cannot extend or release", func(t *testing.T) {
		l, _ := open(t)
		_, tok1, err := l.TryAcquire(ctx, "fence", "a", 700*time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(900 * time.Millisecond)
		_, tok2, err := l.TryAcquire(ctx, "fence", "b", 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		// The usurped holder's token is dead: extension refuses…
		if ok, _, err := l.Extend(ctx, "fence", "a", tok1, 5*time.Second); err != nil || ok {
			t.Fatalf("stale extend = %v, %v", ok, err)
		}
		// …and its release cannot free b's lease.
		if err := l.Release(ctx, "fence", "a", tok1); err != nil {
			t.Fatal(err)
		}
		if stolen, _, err := l.TryAcquire(ctx, "fence", "c", 5*time.Second); err != nil || stolen {
			t.Fatalf("acquire after stale release = %v, %v (b's lease vanished)", stolen, err)
		}
		_ = tok2
	})

	t.Run("DoLease serializes contenders", func(t *testing.T) {
		l, _ := open(t)
		var mu sync.Mutex
		var ledger []string
		var inside int
		var wg sync.WaitGroup
		for _, who := range []string{"a", "b", "c"} {
			wg.Add(1)
			go func(who string) {
				defer wg.Done()
				err := backends.DoLease(ctx, l, "ser", who, 5*time.Second, func(context.Context) error {
					mu.Lock()
					inside++
					if inside > 1 {
						t.Errorf("two holders inside the critical section")
					}
					ledger = append(ledger, "start-"+who)
					mu.Unlock()
					time.Sleep(150 * time.Millisecond)
					mu.Lock()
					inside--
					ledger = append(ledger, "end-"+who)
					mu.Unlock()
					return nil
				})
				if err != nil {
					t.Errorf("DoLease(%s): %v", who, err)
				}
			}(who)
		}
		wg.Wait()
		// The ledger must read start-x, end-x, start-y, end-y, … —
		// disjoint windows, never interleaved.
		for i := 0; i+1 < len(ledger); i += 2 {
			s, e := ledger[i], ledger[i+1]
			if !strings.HasPrefix(s, "start-") || e != "end-"+strings.TrimPrefix(s, "start-") {
				t.Fatalf("interleaved ledger: %v", ledger)
			}
		}
	})

	t.Run("TryDoLease refuses while held and runs when free", func(t *testing.T) {
		l, _ := open(t)
		holding := make(chan struct{})
		done := make(chan error, 1)
		go func() {
			_, err := backends.TryDoLease(ctx, l, "try", "holder", 5*time.Second, func(context.Context) error {
				close(holding)
				time.Sleep(400 * time.Millisecond)
				return nil
			})
			done <- err
		}()
		<-holding
		ran, err := backends.TryDoLease(ctx, l, "try", "second", 5*time.Second, func(context.Context) error { return nil })
		if err != nil || ran {
			t.Fatalf("TryDoLease while held = %v, %v", ran, err)
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		ran2, err := backends.TryDoLease(ctx, l, "try", "second", 5*time.Second, func(context.Context) error { return nil })
		if err != nil || !ran2 {
			t.Fatalf("TryDoLease after release = %v, %v", ran2, err)
		}
	})

	t.Run("heartbeats keep a long run alive past its ttl", func(t *testing.T) {
		l, _ := open(t)
		var cancelled bool
		err := backends.DoLease(ctx, l, "long", "worker", 1500*time.Millisecond, func(runCtx context.Context) error {
			// Runs for 3× the ttl: only the ttl/3 extensions keep it held.
			select {
			case <-runCtx.Done():
				cancelled = true
			case <-time.After(4500 * time.Millisecond):
			}
			return nil
		})
		if err != nil || cancelled {
			t.Fatalf("long run: err=%v cancelled=%v (heartbeat failed to extend)", err, cancelled)
		}
	})

	t.Run("a lost lease cancels the run", func(t *testing.T) {
		l, brk := open(t)
		started := make(chan struct{})
		done := make(chan error, 1)
		go func() {
			done <- backends.DoLease(ctx, l, "lost", "victim", 1500*time.Millisecond, func(runCtx context.Context) error {
				close(started)
				select {
				case <-runCtx.Done():
					return nil // cancelled, as the contract promises
				case <-time.After(10 * time.Second):
					t.Error("run never cancelled after the lease was taken")
					return nil
				}
			})
		}()
		<-started
		brk("lost") // the operator (or expiry+usurper) takes the lease away
		err := <-done
		if err == nil || !strings.Contains(err.Error(), "lost") {
			t.Fatalf("DoLease after loss = %v, want the lost-lease error", err)
		}
	})
}
