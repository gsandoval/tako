package local

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gsandoval/tako/pkg/backends"
)

// writeReplay drops a record straight into the inbox, which is what the
// CLI's rename produces.
func writeReplay(t *testing.T, dir string, rec backends.DeadLetter) {
	t.Helper()
	inbox := filepath.Join(dir, "deadletter", ReplayDir)
	if err := os.MkdirAll(inbox, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(rec)
	if err := os.WriteFile(filepath.Join(inbox, "1.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReplayRedeliversAQueueMessage(t *testing.T) {
	dir := t.TempDir()
	d := NewQueueDispatcher(dir, QueueOptions{MaxAttempts: 1, Backoff: time.Millisecond})
	var got atomic.Value
	d.Consume("thumbnails", func(ctx context.Context, payload []byte) error {
		got.Store(string(payload))
		return nil
	})

	writeReplay(t, dir, backends.DeadLetter{Queue: "thumbnails", Payload: []byte(`{"photo":"x"}`)})
	n, err := d.DrainReplays()
	if err != nil || n != 1 {
		t.Fatalf("DrainReplays = %d, %v; want 1", n, err)
	}
	if err := d.WaitIdle(2 * time.Second); err != nil {
		t.Fatal(err)
	}
	if got.Load() != `{"photo":"x"}` {
		t.Errorf("worker saw %v, want the replayed payload", got.Load())
	}
	// The inbox is emptied: a replay is a MOVE, so draining it twice must
	// not deliver twice.
	entries, _ := os.ReadDir(filepath.Join(dir, "deadletter", ReplayDir))
	if len(entries) != 0 {
		t.Errorf("%d file(s) left in the inbox — a drained replay must not be re-delivered", len(entries))
	}
}

// The heart of the topic story: a dead letter belongs to ONE subscriber,
// and a replay must not re-run the ones that already succeeded.
func TestReplayReachesOnlyTheFailedSubscriber(t *testing.T) {
	dir := t.TempDir()
	d := NewQueueDispatcher(dir, QueueOptions{MaxAttempts: 1, Backoff: time.Millisecond})
	var failed, healthy atomic.Int64
	topics := d.Topics()
	topics.Subscribe("photo-ready", "NotifyOwner", func(ctx context.Context, p []byte) error {
		failed.Add(1)
		return nil
	})
	topics.Subscribe("photo-ready", "WarmFeed", func(ctx context.Context, p []byte) error {
		healthy.Add(1)
		return nil
	})

	writeReplay(t, dir, backends.DeadLetter{
		Queue: "photo-ready", Subscriber: "NotifyOwner", Payload: []byte(`{"photo":"x"}`)})
	if n, err := d.DrainReplays(); err != nil || n != 1 {
		t.Fatalf("DrainReplays = %d, %v", n, err)
	}
	if err := d.WaitIdle(2 * time.Second); err != nil {
		t.Fatal(err)
	}
	if failed.Load() != 1 {
		t.Errorf("the failed subscriber ran %d times, want 1", failed.Load())
	}
	if healthy.Load() != 0 {
		t.Errorf("a healthy subscriber re-ran %d times — replaying through the fan-out duplicates work that already succeeded", healthy.Load())
	}
}

// A replay that fails again lands back on the DLQ. That is the whole
// reason replay goes through the real delivery path instead of being a
// one-shot call.
func TestReplayThatFailsAgainReturnsToTheDLQ(t *testing.T) {
	dir := t.TempDir()
	d := NewQueueDispatcher(dir, QueueOptions{MaxAttempts: 1, Backoff: time.Millisecond})
	dead := make(chan backends.DeadLetter, 1)
	d.OnDeadLetter(func(dl backends.DeadLetter) { dead <- dl })
	d.Consume("thumbnails", func(ctx context.Context, payload []byte) error {
		return context.DeadlineExceeded // still broken
	})

	writeReplay(t, dir, backends.DeadLetter{Queue: "thumbnails", Payload: []byte(`{"photo":"x"}`)})
	if _, err := d.DrainReplays(); err != nil {
		t.Fatal(err)
	}
	select {
	case dl := <-dead:
		if dl.Queue != "thumbnails" {
			t.Errorf("re-dead-lettered onto %q", dl.Queue)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a replay that failed again never came back to the DLQ — replay must ride the real delivery path")
	}
}

// A carrier that no longer exists must not swallow the message: the file
// is the only copy there is.
func TestReplayKeepsTheFileWhenTheCarrierIsGone(t *testing.T) {
	dir := t.TempDir()
	d := NewQueueDispatcher(dir, QueueOptions{MaxAttempts: 1, Backoff: time.Millisecond})

	writeReplay(t, dir, backends.DeadLetter{Queue: "renamed-away", Payload: []byte(`{}`)})
	n, err := d.DrainReplays()
	if err != nil || n != 0 {
		t.Fatalf("DrainReplays = %d, %v; want 0 for a missing carrier", n, err)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "deadletter", ReplayDir))
	if len(entries) != 1 {
		t.Errorf("the record was deleted with nowhere to deliver it — it was the only copy")
	}
}
