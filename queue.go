package tako

import (
	"encoding/json"
	"time"

	"github.com/gsandoval/tako/internal/hooks"
	"github.com/gsandoval/tako/pkg/backends"
)

// Queue declares a work queue for payloads of type T: competing consumers,
// exactly one Worker. The name must be a string constant
// (analyzer-enforced).
func Queue[T any](name string, cfg ...Delivery) *QueueHandle[T] {
	return &QueueHandle[T]{name: name, delivery: cfg}
}

// QueueHandle is a declared work queue. Publish enqueues work; exactly one
// tako.Worker consumes it (two workers on one queue is an analysis error).
type QueueHandle[T any] struct {
	name     string
	delivery []Delivery
}

// Publish enqueues msg for the queue's worker.
func (q *QueueHandle[T]) Publish(ctx Ctx, msg T) error {
	return q.publish(ctx, msg, backends.PublishOptions{})
}

// PublishAfter enqueues msg for delivery no earlier than delay from now.
// Using it selects a scheduling-capable backend in the cloud — same API,
// provisioner's choice.
func (q *QueueHandle[T]) PublishAfter(ctx Ctx, msg T, delay time.Duration) error {
	return q.publish(ctx, msg, backends.PublishOptions{Delay: delay})
}

func (q *QueueHandle[T]) publish(ctx Ctx, msg T, po backends.PublishOptions) error {
	raw, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return queueBackend(ctx).Publish(ctx, q.name, raw, po)
}

// queueBackend returns the wired queue backend, which boot (generated
// mains, tako dev, and — from F7 — NewTestApp) is responsible for
// installing.
func queueBackend(ctx Ctx) backends.Queue {
	if a := appFrom(ctx); a != nil {
		return a.Dispatcher
	}
	if hooks.Queue == nil {
		panic(unwiredPanic("queue"))
	}
	return hooks.Queue
}
