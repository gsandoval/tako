package tako

import (
	"encoding/json"

	"github.com/gsandoval/tako/internal/hooks"
	"github.com/gsandoval/tako/pkg/backends"
)

// Queue declares a work queue for payloads of type T: competing consumers,
// exactly one Worker. The name must be a string constant
// (analyzer-enforced).
func Queue[T any](name string, opts ...Option) *QueueHandle[T] {
	return &QueueHandle[T]{name: name, opts: opts}
}

// QueueHandle is a declared work queue. Publish enqueues work; exactly one
// tako.Worker consumes it (two workers on one queue is an analysis error).
type QueueHandle[T any] struct {
	name string
	opts []Option
}

// Publish enqueues msg for the queue's worker. WithDelay defers delivery
// and WithRateLimit caps it; using either selects a scheduling-capable
// backend — same API, provisioner's choice.
func (q *QueueHandle[T]) Publish(ctx Ctx, msg T, opts ...Option) error {
	raw, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return queueBackend().Publish(ctx, q.name, raw)
}

// queueBackend returns the wired queue backend, which boot (generated
// mains, tako dev, and — from F7 — NewTestApp) is responsible for
// installing.
func queueBackend() backends.Queue {
	b := hooks.Queue
	if b == nil {
		panic("tako: no queue backend wired — run via tako dev or a generated service (tests boot one with runtime.BootLocal until NewTestApp lands in F7)")
	}
	return b
}
