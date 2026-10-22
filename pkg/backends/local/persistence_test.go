package local

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gsandoval/tako/pkg/backends"
)

func TestQueueDelayedTaskSurvivesReload(t *testing.T) {
	dir := t.TempDir()
	d1 := NewQueueDispatcher(dir, QueueOptions{MaxAttempts: 5, Backoff: 50 * time.Millisecond})
	var d1Delivered atomic.Bool
	d1.Consume("emails", func(ctx context.Context, payload []byte) error {
		d1Delivered.Store(true)
		return nil
	})

	if err := d1.Publish(context.Background(), "emails", []byte(`{"to":"ana@example.com"}`), backends.PublishOptions{
		Delay: 120 * time.Millisecond,
	}); err != nil {
		t.Fatal(err)
	}

	// Simulate hot reload before the delay expires.
	time.Sleep(20 * time.Millisecond)
	_ = d1.Close()
	if d1Delivered.Load() {
		t.Fatal("d1 delivered the delayed message before its delay elapsed")
	}

	// Start a new dispatcher on the same directory and register the consumer.
	d2 := NewQueueDispatcher(dir, QueueOptions{MaxAttempts: 5, Backoff: 50 * time.Millisecond})
	defer d2.Close()

	got := make(chan string, 1)
	d2.Consume("emails", func(ctx context.Context, payload []byte) error {
		got <- string(payload)
		return nil
	})

	select {
	case msg := <-got:
		if msg != `{"to":"ana@example.com"}` {
			t.Fatalf("recovered payload = %q", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("delayed message was lost across reload")
	}

	if err := d2.WaitIdle(time.Second); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "queues", "queue", "emails"))
	if len(entries) != 0 {
		t.Fatalf("persisted queue files remaining after ack: %d", len(entries))
	}
}

func TestQueueRetryBackoffSurvivesReloadAndPreservesAttemptCount(t *testing.T) {
	dir := t.TempDir()
	d1 := NewQueueDispatcher(dir, QueueOptions{MaxAttempts: 3, Backoff: 200 * time.Millisecond})

	attempt1Done := make(chan struct{})
	d1.Consume("webhooks", func(ctx context.Context, payload []byte) error {
		close(attempt1Done)
		return errors.New("temporary outage")
	})

	if err := d1.Publish(context.Background(), "webhooks", []byte(`{"id":1}`), backends.PublishOptions{}); err != nil {
		t.Fatal(err)
	}
	<-attempt1Done
	time.Sleep(30 * time.Millisecond) // now sleeping in backoff with Attempt=1 persisted
	_ = d1.Close()

	d2 := NewQueueDispatcher(dir, QueueOptions{MaxAttempts: 3, Backoff: 10 * time.Millisecond})
	defer d2.Close()

	var d2Attempts atomic.Int32
	d2.Consume("webhooks", func(ctx context.Context, payload []byte) error {
		d2Attempts.Add(1)
		return errors.New("still failing")
	})

	// Wait for d2 to finish remaining attempts (attempt 2 and attempt 3 -> 2 attempts on d2) and dead-letter.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		dlEntries, _ := os.ReadDir(filepath.Join(dir, "deadletter", "webhooks"))
		if len(dlEntries) > 0 {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if err := d2.WaitIdle(time.Second); err != nil {
		t.Fatal(err)
	}
	if got := d2Attempts.Load(); got != 2 {
		t.Fatalf("d2 ran %d attempts after reload (1 already ran on d1, MaxAttempts=3), want 2", got)
	}
}

func TestSetQueueDeliveryHonorsMaxAttemptsAndBackoff(t *testing.T) {
	dir := t.TempDir()
	d := NewQueueDispatcher(dir, QueueOptions{MaxAttempts: 5, Backoff: 200 * time.Millisecond})
	defer d.Close()

	d.SetQueueDelivery("custom", 2, 5*time.Millisecond, 10*time.Millisecond)
	var attempts atomic.Int32
	d.Consume("custom", func(ctx context.Context, payload []byte) error {
		attempts.Add(1)
		return errors.New("fail")
	})

	if err := d.Publish(context.Background(), "custom", []byte(`{}`), backends.PublishOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := d.WaitIdle(time.Second); err != nil {
		t.Fatal(err)
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("attempts = %d, want 2 (from SetQueueDelivery)", got)
	}
}

func TestEntityTxSnapshotReadsBeforeCommit(t *testing.T) {
	store, err := OpenEntity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.Put(ctx, "Account", "a1", []byte(`{"id":"a1","balance":100}`), nil); err != nil {
		t.Fatal(err)
	}

	err = store.Tx(ctx, "Account", nil, func(tx backends.EntityTxOps) error {
		if err := tx.Put("a1", []byte(`{"id":"a1","balance":200}`)); err != nil {
			return err
		}
		if err := tx.Put("a2", []byte(`{"id":"a2","balance":50}`)); err != nil {
			return err
		}
		// Snapshot read inside the transaction must still see the pre-transaction state.
		raw1, err := tx.Get("a1")
		if err != nil {
			return err
		}
		if string(raw1) != `{"id":"a1","balance":100}` {
			t.Fatalf("tx.Get(a1) after tx.Put(a1) returned %s, want pre-tx snapshot", raw1)
		}
		if _, err := tx.Get("a2"); !errors.Is(err, backends.ErrNotFound) {
			t.Fatalf("tx.Get(a2) after tx.Put(a2) returned err=%v, want ErrNotFound", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// After commit, both writes are visible.
	got1, err := store.Get(ctx, "Account", "a1")
	if err != nil || string(got1) != `{"id":"a1","balance":200}` {
		t.Fatalf("post-commit Get(a1) = %s, %v", got1, err)
	}
	got2, err := store.Get(ctx, "Account", "a2")
	if err != nil || string(got2) != `{"id":"a2","balance":50}` {
		t.Fatalf("post-commit Get(a2) = %s, %v", got2, err)
	}
}

func TestMetricsLogIncludesLabels(t *testing.T) {
	m := NewMetricsLog()
	var seenLabels map[string]string
	m.OnMetricWithLabels(func(kind, name string, labels map[string]string, value float64, total int64) {
		seenLabels = labels
	})
	m.Add(context.Background(), "orders", map[string]string{"tier": "pro", "region": "us"}, 1)
	if seenLabels["tier"] != "pro" || seenLabels["region"] != "us" {
		t.Fatalf("seenLabels = %+v", seenLabels)
	}
	if got := FormatMetricLabels(seenLabels); got != "{region=us, tier=pro}" {
		t.Fatalf("FormatMetricLabels = %q, want {region=us, tier=pro}", got)
	}
}
