package conformance

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gsandoval/tako/pkg/backends"
)

// TopicParams tells the suite what policy the store under test runs.
type TopicParams struct {
	// MaxAttempts is the per-subscriber delivery cap before dead-lettering.
	MaxAttempts int
}

// RunTopic runs the topic-contract suite against fresh stores from open:
// broadcast fan-out with per-subscriber independence — every subscriber
// gets every event, and attempts, retries, and dead letters never cross
// between subscribers.
func RunTopic(t *testing.T, open func(t *testing.T) backends.Topic, params TopicParams) {
	ctx := context.Background()
	const wait = 10 * time.Second

	deadLetters := func(t *testing.T, tp backends.Topic) <-chan backends.DeadLetter {
		t.Helper()
		obs, ok := tp.(backends.DeadLetterObserver)
		if !ok {
			t.Skip("backend cannot observe dead letters in-process")
		}
		ch := make(chan backends.DeadLetter, 16)
		obs.OnDeadLetter(func(dl backends.DeadLetter) { ch <- dl })
		return ch
	}
	recv := func(t *testing.T, ch <-chan backends.DeadLetter) backends.DeadLetter {
		t.Helper()
		select {
		case dl := <-ch:
			return dl
		case <-time.After(wait):
			t.Fatal("no dead letter arrived")
			return backends.DeadLetter{}
		}
	}

	t.Run("every subscriber receives every event", func(t *testing.T) {
		tp := open(t)
		a := make(chan []byte, 1)
		b := make(chan []byte, 1)
		tp.Subscribe("events", "A", func(ctx context.Context, p []byte) error { a <- p; return nil })
		tp.Subscribe("events", "B", func(ctx context.Context, p []byte) error { b <- p; return nil })
		if err := tp.Publish(ctx, "events", []byte(`{"n":1}`)); err != nil {
			t.Fatal(err)
		}
		for name, ch := range map[string]chan []byte{"A": a, "B": b} {
			select {
			case p := <-ch:
				if string(p) != `{"n":1}` {
					t.Fatalf("%s payload = %s", name, p)
				}
			case <-time.After(wait):
				t.Fatalf("%s never delivered", name)
			}
		}
	})

	t.Run("subscribers retry independently", func(t *testing.T) {
		tp := open(t)
		var flakyAttempts, steadyAttempts atomic.Int32
		flakyDone := make(chan struct{})
		tp.Subscribe("events", "flaky", func(ctx context.Context, p []byte) error {
			if flakyAttempts.Add(1) < 3 {
				return errors.New("transient")
			}
			close(flakyDone)
			return nil
		})
		tp.Subscribe("events", "steady", func(ctx context.Context, p []byte) error {
			steadyAttempts.Add(1)
			return nil
		})
		if err := tp.Publish(ctx, "events", []byte(`{}`)); err != nil {
			t.Fatal(err)
		}
		select {
		case <-flakyDone:
		case <-time.After(wait):
			t.Fatalf("flaky never succeeded (attempts=%d)", flakyAttempts.Load())
		}
		if got := flakyAttempts.Load(); got != 3 {
			t.Fatalf("flaky attempts = %d", got)
		}
		// The steady subscriber is untouched by its sibling's failures.
		time.Sleep(100 * time.Millisecond)
		if got := steadyAttempts.Load(); got != 1 {
			t.Fatalf("steady attempts = %d, want 1", got)
		}
	})

	t.Run("dead letters are per subscriber", func(t *testing.T) {
		tp := open(t)
		dls := deadLetters(t, tp)
		var okAttempts atomic.Int32
		tp.Subscribe("events", "doomed", func(ctx context.Context, p []byte) error {
			return errors.New("always failing")
		})
		tp.Subscribe("events", "fine", func(ctx context.Context, p []byte) error {
			okAttempts.Add(1)
			return nil
		})
		if err := tp.Publish(ctx, "events", []byte(`{"x":true}`)); err != nil {
			t.Fatal(err)
		}
		dl := recv(t, dls)
		if dl.Queue != "events" || dl.Subscriber != "doomed" || dl.Attempts != params.MaxAttempts || dl.Permanent {
			t.Fatalf("dl = %+v", dl)
		}
		if string(dl.Payload) != `{"x":true}` {
			t.Fatalf("payload = %s", dl.Payload)
		}
		if got := okAttempts.Load(); got != 1 {
			t.Fatalf("fine attempts = %d, want 1", got)
		}
		select {
		case extra := <-dls:
			t.Fatalf("second dead letter: %+v", extra)
		case <-time.After(200 * time.Millisecond):
		}
	})

	t.Run("permanent errors dead-letter without retry", func(t *testing.T) {
		tp := open(t)
		dls := deadLetters(t, tp)
		var attempts atomic.Int32
		tp.Subscribe("events", "strict", func(ctx context.Context, p []byte) error {
			attempts.Add(1)
			return permanentErr{errors.New("bad event")}
		})
		if err := tp.Publish(ctx, "events", []byte(`{}`)); err != nil {
			t.Fatal(err)
		}
		dl := recv(t, dls)
		if attempts.Load() != 1 || dl.Attempts != 1 || !dl.Permanent || dl.Subscriber != "strict" {
			t.Fatalf("attempts = %d, dl = %+v", attempts.Load(), dl)
		}
	})

	t.Run("publish without subscribers is a no-op", func(t *testing.T) {
		tp := open(t)
		if err := tp.Publish(ctx, "silence", []byte(`{}`)); err != nil {
			t.Fatalf("publish to subscriberless topic: %v", err)
		}
	})

	t.Run("topics are isolated", func(t *testing.T) {
		tp := open(t)
		a := make(chan []byte, 1)
		b := make(chan []byte, 1)
		tp.Subscribe("a", "subA", func(ctx context.Context, p []byte) error { a <- p; return nil })
		tp.Subscribe("b", "subB", func(ctx context.Context, p []byte) error { b <- p; return nil })
		if err := tp.Publish(ctx, "a", []byte(`"for a"`)); err != nil {
			t.Fatal(err)
		}
		select {
		case <-a:
		case <-time.After(wait):
			t.Fatal("subA never delivered")
		}
		select {
		case p := <-b:
			t.Fatalf("subB received a's event: %s", p)
		case <-time.After(300 * time.Millisecond):
		}
	})
}
