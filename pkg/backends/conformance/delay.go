package conformance

import (
	"context"
	"testing"
	"time"

	"github.com/gsandoval/tako/pkg/backends"
)

// ManualClock is implemented by backends whose delayed deliveries can
// ride a fake clock (the local dispatcher; test apps use it so a
// WithDelay never sleeps for real).
type ManualClock interface {
	UseManualClock()
	Advance(time.Duration)
}

// RunQueueDelay runs the delayed-publish suite: a delayed message is
// held — invisible to consumers and idle to WaitIdle — until its delay
// has fully elapsed, then delivers with normal semantics.
func RunQueueDelay(t *testing.T, open func(t *testing.T) backends.Queue) {
	ctx := context.Background()
	const wait = 10 * time.Second

	setup := func(t *testing.T) (backends.Queue, ManualClock, <-chan []byte) {
		t.Helper()
		q := open(t)
		clock, ok := q.(ManualClock)
		if !ok {
			t.Skip("backend has no manual clock to test delays against")
		}
		clock.UseManualClock()
		got := make(chan []byte, 16)
		q.Consume("jobs", func(ctx context.Context, p []byte) error {
			got <- p
			return nil
		})
		return q, clock, got
	}
	none := func(t *testing.T, got <-chan []byte, why string) {
		t.Helper()
		select {
		case p := <-got:
			t.Fatalf("%s: delivered %s", why, p)
		case <-time.After(150 * time.Millisecond):
		}
	}
	one := func(t *testing.T, got <-chan []byte) []byte {
		t.Helper()
		select {
		case p := <-got:
			return p
		case <-time.After(wait):
			t.Fatal("no delivery")
			return nil
		}
	}

	t.Run("delayed message waits for the full delay", func(t *testing.T) {
		q, clock, got := setup(t)
		if err := q.Publish(ctx, "jobs", []byte(`{}`), backends.PublishOptions{Delay: 4 * time.Hour}); err != nil {
			t.Fatal(err)
		}
		none(t, got, "before any advance")
		clock.Advance(3 * time.Hour)
		none(t, got, "3h into a 4h delay")
		clock.Advance(time.Hour)
		one(t, got)
		none(t, got, "after the single delivery")
	})

	t.Run("undelayed messages are unaffected by the clock", func(t *testing.T) {
		q, _, got := setup(t)
		if err := q.Publish(ctx, "jobs", []byte(`{}`), backends.PublishOptions{}); err != nil {
			t.Fatal(err)
		}
		one(t, got)
	})

	t.Run("delays release in due order across messages", func(t *testing.T) {
		q, clock, got := setup(t)
		if err := q.Publish(ctx, "jobs", []byte(`"late"`), backends.PublishOptions{Delay: 2 * time.Hour}); err != nil {
			t.Fatal(err)
		}
		if err := q.Publish(ctx, "jobs", []byte(`"soon"`), backends.PublishOptions{Delay: time.Hour}); err != nil {
			t.Fatal(err)
		}
		clock.Advance(time.Hour)
		if p := one(t, got); string(p) != `"soon"` {
			t.Fatalf("first release = %s", p)
		}
		none(t, got, "late message at 1h of 2h")
		clock.Advance(time.Hour)
		if p := one(t, got); string(p) != `"late"` {
			t.Fatalf("second release = %s", p)
		}
	})
}
