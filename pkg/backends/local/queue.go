package local

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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
	onDelayed    func(queue string, delay time.Duration)
	rateNext     map[string]time.Time
	rateEvery    map[string]time.Duration
	chaos        *chaosState
	inflight     inflightCount

	// Delayed publishes. Real mode arms a timer per message; manual mode
	// (test apps) parks messages on a virtual clock that only Advance
	// moves — a parked message is idle, not in flight.
	manualClock bool
	virtualNow  time.Duration
	parked      []parkedDelivery
}

// parkedDelivery is one delayed message waiting on the manual clock.
type parkedDelivery struct {
	due     time.Duration
	to      delivery
	deliver func(ctx context.Context, payload []byte) error
	payload []byte
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

// SetRate caps a queue's local delivery at perSec messages per second —
// the dev-loop mirror of a WithRateLimit queue's Cloud Tasks dispatch
// rate. Pacing is a simple next-slot schedule: each dispatch claims the
// next 1/perSec slot and waits for it. TestApp does not set rates
// (tests assert semantics, not pacing).
func (d *QueueDispatcher) SetRate(queue string, perSec int) {
	if perSec <= 0 {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.rateNext == nil {
		d.rateNext = map[string]time.Time{}
		d.rateEvery = map[string]time.Duration{}
	}
	d.rateEvery[queue] = time.Second / time.Duration(perSec)
}

// ratePace blocks until the queue's next dispatch slot, if it is rated.
func (d *QueueDispatcher) ratePace(queue string) {
	d.mu.Lock()
	every, ok := d.rateEvery[queue]
	if !ok {
		d.mu.Unlock()
		return
	}
	now := time.Now()
	slot := d.rateNext[queue]
	if slot.Before(now) {
		slot = now
	}
	d.rateNext[queue] = slot.Add(every)
	d.mu.Unlock()
	time.Sleep(time.Until(slot))
}

func (d *QueueDispatcher) Publish(ctx context.Context, queue string, payload []byte, opts backends.PublishOptions) error {
	d.mu.Lock()
	deliver, ok := d.consumers[queue]
	d.mu.Unlock()
	if !ok {
		return fmt.Errorf("queue %q has no worker registered", queue)
	}
	to := delivery{source: queue, dlDir: filepath.Join("deadletter", queue)}
	if opts.Delay > 0 {
		d.deferDelivery(to, deliver, payload, opts.Delay)
		return nil
	}
	d.dispatch(to, deliver, payload)
	return nil
}

// deferDelivery holds a delayed message until it comes due: a real timer in dev
// and generated services, the manual clock in test apps.
func (d *QueueDispatcher) deferDelivery(to delivery, deliver func(context.Context, []byte) error, payload []byte, delay time.Duration) {
	d.mu.Lock()
	notify := d.onDelayed
	if d.manualClock {
		d.parked = append(d.parked, parkedDelivery{due: d.virtualNow + delay, to: to, deliver: deliver, payload: payload})
		d.mu.Unlock()
	} else {
		d.mu.Unlock()
		time.AfterFunc(delay, func() { d.dispatch(to, deliver, payload) })
	}
	if notify != nil {
		notify(to.source, delay)
	}
}

// UseManualClock switches delayed publishes onto a virtual clock that
// only Advance moves. Test apps call this at boot; dev uses real timers.
func (d *QueueDispatcher) UseManualClock() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.manualClock = true
}

// Advance moves the manual clock forward and releases every parked
// message that has come due.
func (d *QueueDispatcher) Advance(by time.Duration) {
	d.mu.Lock()
	d.virtualNow += by
	var due, still []parkedDelivery
	for _, p := range d.parked {
		if p.due <= d.virtualNow {
			due = append(due, p)
		} else {
			still = append(still, p)
		}
	}
	d.parked = still
	d.mu.Unlock()
	for _, p := range due {
		d.dispatch(p.to, p.deliver, p.payload)
	}
}

// OnDelayed subscribes to delayed publishes (the dev console's note).
func (d *QueueDispatcher) OnDelayed(fn func(queue string, delay time.Duration)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.onDelayed = fn
}

// dispatch launches the delivery loop (plus a chaos duplicate when chaos
// says so), detached from the publishing request's context: async work
// outlives the request that enqueued it.
func (d *QueueDispatcher) dispatch(to delivery, deliver func(context.Context, []byte) error, payload []byte) {
	plan := d.chaosPlanFor(to)
	d.inflight.Add(1)
	go func() {
		d.ratePace(to.source)
		d.deliverLoop(to, deliver, payload, plan)
	}()
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

// WaitIdle blocks until every published message has acked or
// dead-lettered, or the timeout passes.
// inflightCount is a WaitGroup whose count can also be READ. The
// readable half exists for one reason: the invariant that a call which
// hands work to the dispatcher must count it BEFORE returning is not
// observable through Wait alone — a test can only race it. DrainReplays
// violated that invariant for months and surfaced as an occasional
// full-suite flake.
type inflightCount struct {
	wg sync.WaitGroup
	n  atomic.Int64
}

func (c *inflightCount) Add(n int) {
	c.n.Add(int64(n))
	c.wg.Add(n)
}

func (c *inflightCount) Done() {
	c.n.Add(-1)
	c.wg.Done()
}

func (c *inflightCount) Wait() { c.wg.Wait() }

// Len is the number of deliveries in flight right now.
func (c *inflightCount) Len() int64 { return c.n.Load() }

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
	d.dispatch(to, deliver, rec.Payload)
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
