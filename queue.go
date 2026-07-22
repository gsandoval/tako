package tako

import (
	"encoding/json"
	"fmt"
	"time"

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

// Publish enqueues msg for the queue's worker. WithDelay defers the
// first delivery; using it selects a scheduling-capable backend in the
// cloud — same API, provisioner's choice.
func (q *QueueHandle[T]) Publish(ctx Ctx, msg T, opts ...Option) error {
	var po backends.PublishOptions
	for _, o := range opts {
		switch o.name {
		case "delay":
			po.Delay = o.args[0].(time.Duration)
		default:
			return fmt.Errorf("tako.%s is not a Publish option (Publish accepts WithDelay)", optionDisplayName(o.name))
		}
	}
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
		panic("tako: no queue backend wired — run via tako dev, a generated service, or tako.NewTestApp")
	}
	return hooks.Queue
}
