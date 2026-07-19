package tako

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
	panic("tako: not implemented")
}
