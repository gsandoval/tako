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

// QueueDispatcher implements the queue and topic contracts in-process:
// Publish dispatches to the registered consumer(s) on goroutines,
// redelivers on error with a fixed backoff, and dead-letters to
// <dir>/deadletter/<queue>/<id>.json (queues) or
// <dir>/deadletter/<topic>/<subscriber>/<id>.json (topics).
type QueueDispatcher struct {
	dir  string
	opts QueueOptions

	mu           sync.Mutex
	consumers    map[string]func(ctx context.Context, payload []byte) error
	subscribers  map[string][]topicSub
	onDeadLetter []func(backends.DeadLetter)
	onAttempt    func(source, consumer string, attempt, max int, err error, retryIn time.Duration)
	onAck        func(source, consumer string, took time.Duration)
	onChaos      func(source, consumer, action string)
	chaos        *chaosState
	inflight     sync.WaitGroup
}

// topicSub is one named subscriber of one topic.
type topicSub struct {
	name    string
	deliver func(ctx context.Context, payload []byte) error
}

// delivery identifies one message's destination for narration and
// dead-lettering.
type delivery struct {
	source   string // queue or topic name
	consumer string // subscriber name; "" for queue workers
	dlDir    string // dead-letter dir, relative to the dispatcher root
}

// NewQueueDispatcher roots a dispatcher's dead letters at dir.
func NewQueueDispatcher(dir string, opts QueueOptions) *QueueDispatcher {
	if opts.MaxAttempts <= 0 {
		opts.MaxAttempts = 5
	}
	if opts.Backoff <= 0 {
		opts.Backoff = 200 * time.Millisecond
	}
	return &QueueDispatcher{
		dir:         dir,
		opts:        opts,
		consumers:   map[string]func(context.Context, []byte) error{},
		subscribers: map[string][]topicSub{},
	}
}

// OnDeadLetter subscribes to dead letters (the dev console printer).
func (d *QueueDispatcher) OnDeadLetter(fn func(backends.DeadLetter)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.onDeadLetter = append(d.onDeadLetter, fn)
}

// OnAttempt subscribes to failed delivery attempts that will retry.
// consumer is the subscriber name for topic deliveries, "" for queues.
func (d *QueueDispatcher) OnAttempt(fn func(source, consumer string, attempt, max int, err error, retryIn time.Duration)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.onAttempt = fn
}

// OnAck subscribes to successful deliveries. consumer is the subscriber
// name for topic deliveries, "" for queues.
func (d *QueueDispatcher) OnAck(fn func(source, consumer string, took time.Duration)) {
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
	d.dispatch(delivery{source: queue, dlDir: filepath.Join("deadletter", queue)}, deliver, payload)
	return nil
}

// dispatch launches the delivery loop (plus a chaos duplicate when chaos
// says so), detached from the publishing request's context: async work
// outlives the request that enqueued it.
func (d *QueueDispatcher) dispatch(to delivery, deliver func(context.Context, []byte) error, payload []byte) {
	plan := d.chaosPlanFor(to)
	d.inflight.Add(1)
	go d.deliverLoop(to, deliver, payload, plan)
	if plan.duplicate {
		d.notifyChaos(to, "duplicate")
		d.inflight.Add(1)
		go d.deliverLoop(to, deliver, payload, chaosPlan{delay: plan.delay * 2})
	}
}

// Topics adapts the dispatcher to the topic contract: fan-out with an
// independent delivery loop — attempts, backoff, dead letter — per
// (topic, subscriber) pair.
func (d *QueueDispatcher) Topics() backends.Topic { return topicView{d} }

type topicView struct{ d *QueueDispatcher }

func (v topicView) Subscribe(topic, subscriber string, deliver func(ctx context.Context, payload []byte) error) {
	v.d.mu.Lock()
	defer v.d.mu.Unlock()
	v.d.subscribers[topic] = append(v.d.subscribers[topic], topicSub{name: subscriber, deliver: deliver})
}

func (v topicView) Publish(ctx context.Context, topic string, payload []byte) error {
	v.d.mu.Lock()
	subs := append([]topicSub{}, v.d.subscribers[topic]...)
	v.d.mu.Unlock()
	// Zero subscribers is legal: events broadcast into silence.
	for _, sub := range subs {
		v.d.dispatch(delivery{
			source:   topic,
			consumer: sub.name,
			dlDir:    filepath.Join("deadletter", topic, sub.name),
		}, sub.deliver, payload)
	}
	return nil
}

// OnDeadLetter lets conformance observe topic dead letters through the
// same interface queues use.
func (v topicView) OnDeadLetter(fn func(backends.DeadLetter)) { v.d.OnDeadLetter(fn) }

func (d *QueueDispatcher) deliverLoop(to delivery, deliver func(context.Context, []byte) error, payload []byte, plan chaosPlan) {
	defer d.inflight.Done()
	if plan.delay > 0 {
		time.Sleep(plan.delay) // chaos: shuffle delivery order
	}
	start := time.Now()
	var lastErr error
	for attempt := 1; attempt <= d.opts.MaxAttempts; attempt++ {
		lastErr = deliver(context.Background(), payload)
		if lastErr == nil {
			d.mu.Lock()
			ack := d.onAck
			d.mu.Unlock()
			if ack != nil {
				ack(to.source, to.consumer, time.Since(start))
			}
			// Chaos: at-least-once means even an acked message may come
			// back. The respawn carries no plan of its own, so chains stop.
			if plan.redeliver {
				d.notifyChaos(to, "redeliver")
				d.inflight.Add(1)
				go d.deliverLoop(to, deliver, payload, chaosPlan{})
			}
			return
		}
		if backends.IsPermanent(lastErr) {
			d.deadLetter(to, payload, attempt, lastErr, true)
			return
		}
		if attempt < d.opts.MaxAttempts {
			d.mu.Lock()
			onAttempt := d.onAttempt
			d.mu.Unlock()
			if onAttempt != nil {
				onAttempt(to.source, to.consumer, attempt, d.opts.MaxAttempts, lastErr, d.opts.Backoff)
			}
			time.Sleep(d.opts.Backoff)
		}
	}
	d.deadLetter(to, payload, d.opts.MaxAttempts, lastErr, false)
}

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
