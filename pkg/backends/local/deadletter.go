package local

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gsandoval/tako/pkg/backends"
)

func (d *QueueDispatcher) deadLetter(to delivery, payload []byte, attempts int, err error, permanent bool) {
	dl := backends.DeadLetter{
		Queue:      to.source,
		Subscriber: to.consumer,
		Payload:    payload,
		Attempts:   attempts,
		LastError:  err.Error(),
		Permanent:  permanent,
	}
	dir := filepath.Join(d.dir, to.dlDir)
	if mkErr := os.MkdirAll(dir, 0o755); mkErr == nil {
		raw, _ := json.MarshalIndent(deadLetterFile{
			DeadLetter: dl, Payload: payload, Time: time.Now().UTC()}, "", "  ")
		name := fmt.Sprintf("%d.json", time.Now().UnixNano())
		_ = os.WriteFile(filepath.Join(dir, name), raw, 0o644)
	}
	d.mu.Lock()
	subs := append([]func(backends.DeadLetter){}, d.onDeadLetter...)
	d.mu.Unlock()
	for _, fn := range subs {
		fn(dl)
	}
}

// deadLetterFile is the ON-DISK shape of a dead letter, and the only
// place that shape is written down.
//
// It shadows Payload as json.RawMessage on purpose: the file is meant to
// be read by a human, so the message appears as itself rather than as a
// base64 blob. That makes the field incompatible with
// backends.DeadLetter's []byte, which encoding/json fills only from a
// base64 STRING — decoding a file into that type fails on every real
// record. Writer and readers therefore share this type; nobody
// re-declares it.
type deadLetterFile struct {
	backends.DeadLetter
	Payload json.RawMessage `json:"payload"`
	Time    time.Time       `json:"time"`
}

// DecodeDeadLetter reads one on-disk dead-letter record. The CLI and the
// replay drain both go through here so neither can drift from the
// writer.
func DecodeDeadLetter(raw []byte) (backends.DeadLetter, time.Time, error) {
	var f deadLetterFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return backends.DeadLetter{}, time.Time{}, err
	}
	rec := f.DeadLetter
	rec.Payload = f.Payload
	return rec, f.Time, nil
}

// ReplayDir is the inbox a replayed dead letter is MOVED into. The CLI
// and the dev server are different processes with no channel between
// them, and when dev is down there is no broker to publish into at all
// — so replay is a rename, and the dispatcher drains the directory on
// boot and on every watch tick. The rename is also the "leaves the DLQ"
// half of the contract, done atomically by the filesystem.
const ReplayDir = "replay"

// DrainReplays re-delivers every dead letter moved into the replay
// inbox and returns how many it dispatched. A record whose carrier no
// longer exists (the queue was renamed, the subscriber deleted) is left
// in place rather than dropped: the file is the only copy.
func (d *QueueDispatcher) DrainReplays() (int, error) {
	dir := filepath.Join(d.dir, "deadletter", ReplayDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		rec, _, err := DecodeDeadLetter(raw)
		if err != nil || rec.Queue == "" {
			continue
		}
		if !d.replay(rec) {
			continue // carrier gone: leave the file, it is the only copy
		}
		_ = os.Remove(path)
		n++
	}
	return n, nil
}

// replay puts one record back on its carrier. A queue goes to its single
// worker; a topic goes to the ONE subscriber that failed, never the
// whole fan-out — the same addressing the cloud path does with the
// tako-replay-to attribute, which is what keeps the two backends
// behaving alike.
func (d *QueueDispatcher) replay(rec backends.DeadLetter) bool {
	to, deliver, ok := d.routeFor(rec)
	if !ok {
		return false
	}
	// Dispatched SYNCHRONOUSLY, and outside the lock.
	//
	// This used to be `go d.dispatch(…)`, because dispatch takes d.mu
	// (chaosPlanFor) and replay held it. But dispatch is also where the
	// in-flight count is incremented, so deferring it to a goroutine
	// left a window where DrainReplays had returned and the work was
	// not yet counted — WaitIdle answered "idle" for a message that had
	// not been delivered, and DrainQueues would do the same in a user's
	// test. Resolving the route under the lock and dispatching after it
	// closes that window: dispatch counts the work before it returns,
	// and still launches delivery in its own goroutine.
	now := time.Now().UTC()
	var task *persistedTask
	var taskPath string
	if rec.Subscriber == "" {
		task, taskPath = d.persistNewTask(persistedTask{
			Kind:      "queue",
			Queue:     rec.Queue,
			Payload:   append([]byte(nil), rec.Payload...),
			Attempt:   0,
			DueAt:     now,
			CreatedAt: now,
		})
	} else {
		task, taskPath = d.persistNewTask(persistedTask{
			Kind:       "topic",
			Topic:      rec.Queue,
			Subscriber: rec.Subscriber,
			Payload:    append([]byte(nil), rec.Payload...),
			Attempt:    0,
			DueAt:      now,
			CreatedAt:  now,
		})
	}
	d.dispatch(to, deliver, rec.Payload, task, taskPath)
	return true
}

// routeFor resolves a dead letter to its carrier under the lock, so the
// caller can dispatch without holding it.
func (d *QueueDispatcher) routeFor(rec backends.DeadLetter) (delivery, func(context.Context, []byte) error, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if rec.Subscriber == "" {
		deliver, ok := d.consumers[rec.Queue]
		if !ok {
			return delivery{}, nil, false
		}
		return delivery{
			source: rec.Queue,
			dlDir:  filepath.Join("deadletter", rec.Queue),
		}, deliver, true
	}
	for _, sub := range d.subscribers[rec.Queue] {
		if sub.name != rec.Subscriber {
			continue
		}
		return delivery{
			source:   rec.Queue,
			consumer: sub.name,
			dlDir:    filepath.Join("deadletter", rec.Queue, sub.name),
		}, sub.deliver, true
	}
	return delivery{}, nil, false
}
