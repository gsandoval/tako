package local

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gsandoval/tako/pkg/backends"
)

// writeReplay drops a record into the inbox in the EXACT shape the
// dispatcher writes to disk — deadLetterFile, with payload as raw JSON.
//
// It used to marshal backends.DeadLetter directly, whose []byte Payload
// round-trips through base64. That shape never occurs in production, and
// building the fixture out of it meant these tests passed against a file
// format nothing writes: the real records failed to decode and were
// silently skipped, by the drain here AND by tako dlq --local. A test
// fixture that constructs its own input is only as good as its fidelity
// to the writer.
func writeReplay(t *testing.T, dir string, rec backends.DeadLetter) {
	t.Helper()
	inbox := filepath.Join(dir, "deadletter", ReplayDir)
	if err := os.MkdirAll(inbox, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(deadLetterFile{
		DeadLetter: rec, Payload: rec.Payload, Time: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inbox, "1.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

// The regression the fixture was hiding: a record written by the
// dispatcher itself must decode. This produces a REAL dead letter
// through a real failed delivery, then reads the file back the way the
// CLI does.
func TestOnDiskDeadLetterDecodes(t *testing.T) {
	dir := t.TempDir()
	d := NewQueueDispatcher(dir, QueueOptions{MaxAttempts: 1, Backoff: time.Millisecond})
	dead := make(chan backends.DeadLetter, 1)
	d.OnDeadLetter(func(dl backends.DeadLetter) { dead <- dl })
	d.Consume("thumbnails", func(ctx context.Context, p []byte) error {
		return context.DeadlineExceeded
	})
	if err := d.Publish(context.Background(), "thumbnails", []byte(`{"photoID":"x"}`), backends.PublishOptions{}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-dead:
	case <-time.After(3 * time.Second):
		t.Fatal("no dead letter produced")
	}

	matches, _ := filepath.Glob(filepath.Join(dir, "deadletter", "thumbnails", "*.json"))
	if len(matches) != 1 {
		t.Fatalf("%d files on disk, want 1", len(matches))
	}
	raw, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	rec, when, err := DecodeDeadLetter(raw)
	if err != nil {
		t.Fatalf("a record the dispatcher wrote did not decode: %v\n%s", err, raw)
	}
	// The payload comes back RE-INDENTED: the file is written with
	// MarshalIndent so a human can read it, which reformats the embedded
	// JSON. Handlers unmarshal, so whitespace is invisible to them —
	// compare semantically rather than byte-for-byte.
	var compact bytes.Buffer
	if err := json.Compact(&compact, rec.Payload); err != nil {
		t.Fatalf("payload is not JSON: %v", err)
	}
	if rec.Queue != "thumbnails" || compact.String() != `{"photoID":"x"}` {
		t.Errorf("decoded %+v, payload %s", rec, compact.String())
	}
	if when.IsZero() {
		t.Error("the record carries no time")
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

// The ordering bug behind a full-suite flake: DrainReplays used to hand
// the message to `go d.dispatch(…)`, and the in-flight count is
// incremented INSIDE dispatch — so it could return before the work was
// counted, and WaitIdle answered "idle" for a message that had not been
// delivered. In a user's suite that is app.DrainQueues() returning early.
//
// Honest about what this test is: a CONTRACT test, not a deterministic
// regression test. The window is a few instructions wide — reintroduce
// `go d.dispatch` and this still passes, because the stray goroutine
// almost always runs before the assertion. It could not be forced from
// a test without instrumenting the scheduler.
//
// What it does buy: the invariant is now written down and checked, so
// removing the counting outright fails loudly, and the blocked-consumer
// half pins that WaitIdle and DrainReplays agree at all.
func TestDrainReplaysCountsTheWorkBeforeItReturns(t *testing.T) {
	dir := t.TempDir()
	d := NewQueueDispatcher(dir, QueueOptions{MaxAttempts: 1, Backoff: time.Millisecond})
	release := make(chan struct{})
	arrived := make(chan struct{})
	d.Consume("thumbnails", func(ctx context.Context, p []byte) error {
		close(arrived)
		<-release
		return nil
	})

	writeReplay(t, dir, backends.DeadLetter{Queue: "thumbnails", Payload: []byte(`{"photo":"x"}`)})
	if n, err := d.DrainReplays(); err != nil || n != 1 {
		t.Fatalf("DrainReplays = %d, %v", n, err)
	}

	// The invariant, checked directly rather than raced: the work is
	// counted by the time DrainReplays returns. Asserting this through
	// WaitIdle alone is not deterministic — the stray goroutine usually
	// wins — which is exactly why the bug survived as a rare flake.
	if n := d.inflight.Len(); n == 0 {
		t.Fatal("DrainReplays returned with nothing in flight — WaitIdle (and app.DrainQueues) will report idle for a message that has not been delivered")
	}
	// And the consequence: idle is a LIE while the consumer is blocked.
	if err := d.WaitIdle(150 * time.Millisecond); err == nil {
		t.Fatal("WaitIdle reported idle while a replayed message was still undelivered")
	}
	select {
	case <-arrived:
	case <-time.After(2 * time.Second):
		t.Fatal("the replayed message never reached the consumer")
	}

	close(release)
	if err := d.WaitIdle(2 * time.Second); err != nil {
		t.Fatalf("WaitIdle after release: %v", err)
	}
}
