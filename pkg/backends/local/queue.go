package local

import (
	"context"
	"fmt"
	"path/filepath"
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

	mu            sync.Mutex
	consumers     map[string]func(ctx context.Context, payload []byte) error
	subscribers   map[string][]topicSub
	onDeadLetter  []func(backends.DeadLetter)
	onAttempt     func(source, consumer string, attempt, max int, err error, retryIn time.Duration)
	onAck         func(source, consumer string, took time.Duration)
	onChaos       func(source, consumer, action string)
	onDelayed     func(queue string, delay time.Duration)
	rateNext      map[string]time.Time
	rateEvery     map[string]time.Duration
	queueDelivery map[string]queueDeliveryConfig
	activeTasks   map[string]bool
	stopCh        chan struct{}
	closed        bool
	taskSeq       atomic.Uint64
	chaos         *chaosState
	inflight      inflightCount

	// Delayed publishes. Real mode arms a timer per message; manual mode
	// (test apps) parks messages on a virtual clock that only Advance
	// moves — a parked message is idle, not in flight.
	manualClock bool
	virtualNow  time.Duration
	parked      []parkedDelivery
}

// parkedDelivery is one delayed message waiting on the manual clock.
type parkedDelivery struct {
	due      time.Duration
	to       delivery
	deliver  func(ctx context.Context, payload []byte) error
	payload  []byte
	task     *persistedTask
	taskPath string
}

type queueDeliveryConfig struct {
	maxAttempts int
	minBackoff  time.Duration
	maxBackoff  time.Duration
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
		dir:           dir,
		opts:          opts,
		consumers:     map[string]func(context.Context, []byte) error{},
		subscribers:   map[string][]topicSub{},
		queueDelivery: map[string]queueDeliveryConfig{},
		activeTasks:   map[string]bool{},
		stopCh:        make(chan struct{}),
	}
}

// Close stops in-flight retry/delay sleeps cleanly without removing
// persisted queue files, so a subsequent dispatcher on the same
// directory resumes them.
func (d *QueueDispatcher) Close() error {
	d.mu.Lock()
	if !d.closed {
		d.closed = true
		close(d.stopCh)
	}
	d.mu.Unlock()
	return nil
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
	d.consumers[queue] = deliver
	manual := d.manualClock
	d.mu.Unlock()
	if d.dir != "" && !manual {
		d.recoverQueueTasks(queue, deliver)
	}
}

// RegisterConsumer is an alias for Consume.
func (d *QueueDispatcher) RegisterConsumer(queue string, deliver func(ctx context.Context, payload []byte) error) {
	d.Consume(queue, deliver)
}

// SetQueueDelivery configures per-queue retry limits and exponential
// backoff bounds on the local dispatcher.
func (d *QueueDispatcher) SetQueueDelivery(queue string, maxAttempts int, minBackoff, maxBackoff time.Duration) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.queueDelivery == nil {
		d.queueDelivery = map[string]queueDeliveryConfig{}
	}
	d.queueDelivery[queue] = queueDeliveryConfig{
		maxAttempts: maxAttempts,
		minBackoff:  minBackoff,
		maxBackoff:  maxBackoff,
	}
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
	now := time.Now().UTC()
	task, taskPath := d.persistNewTask(persistedTask{
		Kind:      "queue",
		Queue:     queue,
		Payload:   append([]byte(nil), payload...),
		Attempt:   0,
		DueAt:     now.Add(opts.Delay),
		CreatedAt: now,
	})
	if opts.Delay > 0 {
		d.deferDelivery(to, deliver, payload, opts.Delay, task, taskPath)
		return nil
	}
	d.dispatch(to, deliver, payload, task, taskPath)
	return nil
}

// deferDelivery holds a delayed message until it comes due: a real timer in dev
// and generated services, the manual clock in test apps.
func (d *QueueDispatcher) deferDelivery(to delivery, deliver func(context.Context, []byte) error, payload []byte, delay time.Duration, task *persistedTask, taskPath string) {
	d.mu.Lock()
	notify := d.onDelayed
	if d.manualClock {
		d.parked = append(d.parked, parkedDelivery{
			due: d.virtualNow + delay, to: to, deliver: deliver,
			payload: payload, task: task, taskPath: taskPath,
		})
		d.mu.Unlock()
	} else {
		stopCh := d.stopCh
		d.mu.Unlock()
		go func() {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-timer.C:
				d.dispatch(to, deliver, payload, task, taskPath)
			case <-stopCh:
			}
		}()
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
		d.dispatch(p.to, p.deliver, p.payload, p.task, p.taskPath)
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
func (d *QueueDispatcher) dispatch(to delivery, deliver func(context.Context, []byte) error, payload []byte, task *persistedTask, taskPath string) {
	plan := d.chaosPlanFor(to)
	d.inflight.Add(1)
	go func() {
		d.ratePace(to.source)
		d.deliverLoop(to, deliver, payload, plan, task, taskPath)
	}()
	if plan.duplicate {
		d.notifyChaos(to, "duplicate")
		d.inflight.Add(1)
		go d.deliverLoop(to, deliver, payload, chaosPlan{delay: plan.delay * 2}, nil, "")
	}
}

// Topics adapts the dispatcher to the topic contract: fan-out with an
// independent delivery loop — attempts, backoff, dead letter — per
// (topic, subscriber) pair.
func (d *QueueDispatcher) Topics() backends.Topic { return topicView{d} }

type topicView struct{ d *QueueDispatcher }

func (v topicView) Subscribe(topic, subscriber string, deliver func(ctx context.Context, payload []byte) error) {
	v.d.mu.Lock()
	v.d.subscribers[topic] = append(v.d.subscribers[topic], topicSub{name: subscriber, deliver: deliver})
	manual := v.d.manualClock
	v.d.mu.Unlock()
	if v.d.dir != "" && !manual {
		v.d.recoverTopicTasks(topic, subscriber, deliver)
	}
}

// RegisterSubscriber is an alias for Topics().Subscribe.
func (d *QueueDispatcher) RegisterSubscriber(topic, subscriber string, deliver func(ctx context.Context, payload []byte) error) {
	d.Topics().Subscribe(topic, subscriber, deliver)
}

func (v topicView) Publish(ctx context.Context, topic string, payload []byte) error {
	v.d.mu.Lock()
	subs := append([]topicSub{}, v.d.subscribers[topic]...)
	v.d.mu.Unlock()
	now := time.Now().UTC()
	// Zero subscribers is legal: events broadcast into silence.
	for _, sub := range subs {
		task, taskPath := v.d.persistNewTask(persistedTask{
			Kind:       "topic",
			Topic:      topic,
			Subscriber: sub.name,
			Payload:    append([]byte(nil), payload...),
			Attempt:    0,
			DueAt:      now,
			CreatedAt:  now,
		})
		v.d.dispatch(delivery{
			source:   topic,
			consumer: sub.name,
			dlDir:    filepath.Join("deadletter", topic, sub.name),
		}, sub.deliver, payload, task, taskPath)
	}
	return nil
}

// OnDeadLetter lets conformance observe topic dead letters through the
// same interface queues use.
func (v topicView) OnDeadLetter(fn func(backends.DeadLetter)) { v.d.OnDeadLetter(fn) }

func (d *QueueDispatcher) deliveryPolicy(to delivery) (int, func(attempt int) time.Duration) {
	d.mu.Lock()
	cfg, hasCfg := d.queueDelivery[to.source]
	defaultMax := d.opts.MaxAttempts
	defaultBackoff := d.opts.Backoff
	d.mu.Unlock()

	maxAttempts := defaultMax
	if to.consumer == "" && hasCfg && cfg.maxAttempts > 0 {
		maxAttempts = cfg.maxAttempts
	}
	backoffFor := func(attempt int) time.Duration {
		if to.consumer == "" && hasCfg && (cfg.minBackoff > 0 || cfg.maxBackoff > 0) {
			base := cfg.minBackoff
			if base <= 0 {
				base = defaultBackoff
			}
			shift := attempt - 1
			if shift < 0 {
				shift = 0
			}
			if shift > 30 {
				shift = 30
			}
			b := base * time.Duration(1<<shift)
			if cfg.maxBackoff > 0 && b > cfg.maxBackoff {
				b = cfg.maxBackoff
			}
			return b
		}
		return defaultBackoff
	}
	return maxAttempts, backoffFor
}

func (d *QueueDispatcher) deliverLoop(to delivery, deliver func(context.Context, []byte) error, payload []byte, plan chaosPlan, task *persistedTask, taskPath string) {
	defer d.inflight.Done()
	if plan.delay > 0 {
		if !d.sleepOrStop(plan.delay) {
			return
		}
	}
	maxAttempts, backoffFor := d.deliveryPolicy(to)
	startAttempt := 1
	if task != nil && task.Attempt > 0 {
		startAttempt = task.Attempt + 1
	}
	start := time.Now()
	var lastErr error
	for attempt := startAttempt; attempt <= maxAttempts; attempt++ {
		d.mu.Lock()
		closed := d.closed
		d.mu.Unlock()
		if closed {
			return
		}
		lastErr = deliver(context.Background(), payload)
		if lastErr == nil {
			d.removeTask(taskPath)
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
				go d.deliverLoop(to, deliver, payload, chaosPlan{}, nil, "")
			}
			return
		}
		if backends.IsPermanent(lastErr) {
			d.removeTask(taskPath)
			d.deadLetter(to, payload, attempt, lastErr, true)
			return
		}
		if attempt < maxAttempts {
			retryIn := backoffFor(attempt)
			if task != nil && taskPath != "" {
				task.Attempt = attempt
				task.DueAt = time.Now().UTC().Add(retryIn)
				d.writeTaskFile(taskPath, *task)
			}
			d.mu.Lock()
			onAttempt := d.onAttempt
			d.mu.Unlock()
			if onAttempt != nil {
				onAttempt(to.source, to.consumer, attempt, maxAttempts, lastErr, retryIn)
			}
			if !d.sleepOrStop(retryIn) {
				return
			}
		}
	}
	d.removeTask(taskPath)
	if lastErr == nil {
		lastErr = fmt.Errorf("exhausted %d attempts", maxAttempts)
	}
	d.deadLetter(to, payload, maxAttempts, lastErr, false)
}

func (d *QueueDispatcher) sleepOrStop(dur time.Duration) bool {
	if dur <= 0 {
		return true
	}
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return false
	}
	stopCh := d.stopCh
	d.mu.Unlock()
	timer := time.NewTimer(dur)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-stopCh:
		return false
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
