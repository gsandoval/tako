package local

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gsandoval/tako/pkg/backends"
)

// QueueOptions tune the in-process dispatcher. The defaults are the
// stated local policy: 5 attempts, fixed 200ms backoff.
type QueueOptions struct {
	// MaxAttempts caps deliveries before dead-lettering; 0 means 5.
	MaxAttempts int
	// Backoff is the fixed delay between attempts; 0 means 200ms.
	Backoff time.Duration
}

// QueueDispatcher implements the queue contract in-process: Publish
// dispatches to the registered consumer on a goroutine, redelivers on
// error with a fixed backoff, and dead-letters to
// <dir>/deadletter/<queue>/<id>.json.
type QueueDispatcher struct {
	dir  string
	opts QueueOptions

	mu           sync.Mutex
	consumers    map[string]func(ctx context.Context, payload []byte) error
	onDeadLetter []func(backends.DeadLetter)
	onAttempt    func(queue string, attempt, max int, err error, retryIn time.Duration)
	onAck        func(queue string, took time.Duration)
	inflight     sync.WaitGroup
}

// NewQueueDispatcher roots a dispatcher's dead letters at dir.
func NewQueueDispatcher(dir string, opts QueueOptions) *QueueDispatcher {
	if opts.MaxAttempts <= 0 {
		opts.MaxAttempts = 5
	}
	if opts.Backoff <= 0 {
		opts.Backoff = 200 * time.Millisecond
	}
	return &QueueDispatcher{dir: dir, opts: opts, consumers: map[string]func(context.Context, []byte) error{}}
}

// OnDeadLetter subscribes to dead letters (the dev console printer).
func (d *QueueDispatcher) OnDeadLetter(fn func(backends.DeadLetter)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.onDeadLetter = append(d.onDeadLetter, fn)
}

// OnAttempt subscribes to failed delivery attempts that will retry.
func (d *QueueDispatcher) OnAttempt(fn func(queue string, attempt, max int, err error, retryIn time.Duration)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.onAttempt = fn
}

// OnAck subscribes to successful deliveries.
func (d *QueueDispatcher) OnAck(fn func(queue string, took time.Duration)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.onAck = fn
}

// MaxAttempts reports the attempt cap (conformance reads it).
func (d *QueueDispatcher) MaxAttempts() int { return d.opts.MaxAttempts }

func (d *QueueDispatcher) Consume(queue string, deliver func(ctx context.Context, payload []byte) error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.consumers[queue] = deliver
}

func (d *QueueDispatcher) Publish(ctx context.Context, queue string, payload []byte) error {
	d.mu.Lock()
	deliver, ok := d.consumers[queue]
	d.mu.Unlock()
	if !ok {
		return fmt.Errorf("queue %q has no worker registered", queue)
	}
	d.inflight.Add(1)
	// Delivery is detached from the publishing request's context: async
	// work outlives the request that enqueued it.
	go d.deliverLoop(queue, deliver, payload)
	return nil
}

func (d *QueueDispatcher) deliverLoop(queue string, deliver func(context.Context, []byte) error, payload []byte) {
	defer d.inflight.Done()
	start := time.Now()
	var lastErr error
	for attempt := 1; attempt <= d.opts.MaxAttempts; attempt++ {
		lastErr = deliver(context.Background(), payload)
		if lastErr == nil {
			d.mu.Lock()
			ack := d.onAck
			d.mu.Unlock()
			if ack != nil {
				ack(queue, time.Since(start))
			}
			return
		}
		if backends.IsPermanent(lastErr) {
			d.deadLetter(queue, payload, attempt, lastErr, true)
			return
		}
		if attempt < d.opts.MaxAttempts {
			d.mu.Lock()
			onAttempt := d.onAttempt
			d.mu.Unlock()
			if onAttempt != nil {
				onAttempt(queue, attempt, d.opts.MaxAttempts, lastErr, d.opts.Backoff)
			}
			time.Sleep(d.opts.Backoff)
		}
	}
	d.deadLetter(queue, payload, d.opts.MaxAttempts, lastErr, false)
}

func (d *QueueDispatcher) deadLetter(queue string, payload []byte, attempts int, err error, permanent bool) {
	dl := backends.DeadLetter{
		Queue:     queue,
		Payload:   payload,
		Attempts:  attempts,
		LastError: err.Error(),
		Permanent: permanent,
	}
	dir := filepath.Join(d.dir, "deadletter", queue)
	if mkErr := os.MkdirAll(dir, 0o755); mkErr == nil {
		raw, _ := json.MarshalIndent(struct {
			backends.DeadLetter
			Payload json.RawMessage `json:"payload"`
			Time    time.Time       `json:"time"`
		}{DeadLetter: dl, Payload: payload, Time: time.Now().UTC()}, "", "  ")
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

// WaitIdle blocks until every published message has acked or
// dead-lettered, or the timeout passes.
func (d *QueueDispatcher) WaitIdle(timeout time.Duration) error {
	done := make(chan struct{})
	go func() {
		d.inflight.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("queues still busy after %s", timeout)
	}
}
