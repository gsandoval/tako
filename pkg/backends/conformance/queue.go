package conformance

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gsandoval/tako/pkg/backends"
)

// permanentErr mimics tako.Permanent without importing the SDK.
type permanentErr struct{ error }

func (permanentErr) PermanentDelivery() bool { return true }

// QueueParams tells the suite what policy the store under test runs.
type QueueParams struct {
	// MaxAttempts is the delivery cap before dead-lettering.
	MaxAttempts int
}

// RunQueue runs the queue-contract suite against fresh stores from open:
// at-least-once delivery, ack stops redelivery, errors redeliver,
// permanent errors and attempt exhaustion dead-letter.
func RunQueue(t *testing.T, open func(t *testing.T) backends.Queue, params QueueParams) {
	ctx := context.Background()
	const wait = 10 * time.Second

	deadLetters := func(t *testing.T, q backends.Queue) <-chan backends.DeadLetter {
		t.Helper()
		obs, ok := q.(backends.DeadLetterObserver)
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

	t.Run("delivers payload and ack stops redelivery", func(t *testing.T) {
		q := open(t)
		got := make(chan []byte, 16)
		q.Consume("jobs", func(ctx context.Context, payload []byte) error {
			got <- payload
			return nil
		})
		if err := q.Publish(ctx, "jobs", []byte(`{"n":1}`)); err != nil {
			t.Fatal(err)
		}
		select {
		case p := <-got:
			if string(p) != `{"n":1}` {
				t.Fatalf("payload = %s", p)
			}
		case <-time.After(wait):
			t.Fatal("no delivery")
		}
		select {
		case p := <-got:
			t.Fatalf("redelivered after ack: %s", p)
		case <-time.After(300 * time.Millisecond):
		}
	})

	t.Run("errors redeliver until success", func(t *testing.T) {
		q := open(t)
		var attempts atomic.Int32
		done := make(chan struct{})
		q.Consume("jobs", func(ctx context.Context, payload []byte) error {
			if attempts.Add(1) < 3 {
				return errors.New("transient")
			}
			close(done)
			return nil
		})
		if err := q.Publish(ctx, "jobs", []byte(`{}`)); err != nil {
			t.Fatal(err)
		}
		select {
		case <-done:
			if got := attempts.Load(); got != 3 {
				t.Fatalf("attempts = %d", got)
			}
		case <-time.After(wait):
			t.Fatalf("never succeeded (attempts=%d)", attempts.Load())
		}
	})

	t.Run("permanent errors dead-letter without retry", func(t *testing.T) {
		q := open(t)
		dls := deadLetters(t, q)
		var attempts atomic.Int32
		q.Consume("jobs", func(ctx context.Context, payload []byte) error {
			attempts.Add(1)
			return permanentErr{errors.New("bad payload")}
		})
		if err := q.Publish(ctx, "jobs", []byte(`{"bad":true}`)); err != nil {
			t.Fatal(err)
		}
		dl := recv(t, dls)
		if attempts.Load() != 1 || dl.Attempts != 1 || !dl.Permanent {
			t.Fatalf("attempts = %d, dl = %+v", attempts.Load(), dl)
		}
		if dl.Queue != "jobs" || dl.LastError != "bad payload" || string(dl.Payload) != `{"bad":true}` {
			t.Fatalf("dl = %+v", dl)
		}
	})

	t.Run("attempt exhaustion dead-letters", func(t *testing.T) {
		q := open(t)
		dls := deadLetters(t, q)
		var attempts atomic.Int32
		q.Consume("jobs", func(ctx context.Context, payload []byte) error {
			attempts.Add(1)
			return errors.New("always failing")
		})
		if err := q.Publish(ctx, "jobs", []byte(`{}`)); err != nil {
			t.Fatal(err)
		}
		dl := recv(t, dls)
		if int(attempts.Load()) != params.MaxAttempts || dl.Attempts != params.MaxAttempts {
			t.Fatalf("attempts = %d, dl.Attempts = %d, want %d", attempts.Load(), dl.Attempts, params.MaxAttempts)
		}
		if dl.Permanent {
			t.Fatal("exhaustion marked permanent")
		}
	})

	t.Run("queues are isolated", func(t *testing.T) {
		q := open(t)
		a := make(chan []byte, 1)
		b := make(chan []byte, 1)
		q.Consume("a", func(ctx context.Context, p []byte) error { a <- p; return nil })
		q.Consume("b", func(ctx context.Context, p []byte) error { b <- p; return nil })
		if err := q.Publish(ctx, "a", []byte(`"for a"`)); err != nil {
			t.Fatal(err)
		}
		select {
		case <-a:
		case <-time.After(wait):
			t.Fatal("a never delivered")
		}
		select {
		case p := <-b:
			t.Fatalf("b received a's message: %s", p)
		case <-time.After(300 * time.Millisecond):
		}
	})

	t.Run("publish without a worker is an error", func(t *testing.T) {
		q := open(t)
		if err := q.Publish(ctx, "ghost", []byte(`{}`)); err == nil {
			t.Fatal("publish to consumerless queue succeeded")
		}
	})
}
