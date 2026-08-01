package conformance

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gsandoval/tako/pkg/backends"
)

// RunMetrics runs the metrics-contract suite against fresh sinks from
// open.
//
// The contract is unusually small — two methods, no error returns, no
// readback — so this chapter tests the PROMISES rather than the values:
// recording never fails, never blocks the caller on I/O, and never
// depends on the request context being alive. Those are exactly the
// properties a handler relies on when it calls Add in the middle of
// serving, and exactly the ones an export backend is tempted to break.
//
// What each backend does with a recording is its own business and is
// covered where it belongs: local's totals in its own tests, gcp's wire
// format against a fake Cloud Monitoring in pkg/backends/gcp.
func RunMetrics(t *testing.T, open func(t *testing.T) backends.Metrics) {
	ctx := context.Background()

	t.Run("recording accepts what handlers actually pass", func(t *testing.T) {
		m := open(t)
		m.Add(ctx, "uploads", 1)
		m.Add(ctx, "uploads", 0)      // a no-op increment is not an error
		m.Add(ctx, "corrections", -1) // counters may be corrected downward
		m.Observe(ctx, "thumb_ms", 0)
		m.Observe(ctx, "thumb_ms", 0.5) // below any sane bucket floor
		m.Observe(ctx, "thumb_ms", 1e9) // above any sane bucket ceiling
		m.Observe(ctx, "ratio", -1)     // negative samples are legal floats
		m.Add(ctx, strings.Repeat("n", 200), 1)
	})

	// Fire-and-forget means the ctx is not a gate. A handler that records
	// after its deadline blew — the failure path most worth measuring —
	// must still be counted.
	t.Run("a dead context still records", func(t *testing.T) {
		m := open(t)
		dead, cancel := context.WithCancel(ctx)
		cancel()
		done := make(chan struct{})
		go func() {
			defer close(done)
			m.Add(dead, "failures", 1)
			m.Observe(dead, "failed_ms", 12)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("recording blocked on a cancelled context — the metrics that matter most are the ones recorded on the failure path")
		}
	})

	// The request path must never wait on a metrics backend, so recording
	// has to be cheap and concurrent. Under -race this also pins that the
	// sink is safe to share across handlers, which every backend is.
	t.Run("concurrent recording never blocks", func(t *testing.T) {
		m := open(t)
		var wg sync.WaitGroup
		done := make(chan struct{})
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 500; j++ {
					m.Add(ctx, "uploads", 1)
					m.Observe(ctx, "thumb_ms", float64(j))
				}
			}()
		}
		go func() { wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("4000 recordings did not finish in 10s — recording is doing I/O on the caller's goroutine")
		}
	})
}
