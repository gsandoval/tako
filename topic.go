package tako

// Topic declares an event topic for payloads of type T: broadcast fan-out,
// any number of Subscribe handlers, each with independent retries. The name
// must be a string constant (analyzer-enforced).
func Topic[T any](name string, opts ...Option) *TopicHandle[T] {
	return &TopicHandle[T]{name: name, opts: opts}
}

// TopicHandle is a declared event topic, usable as a Subscribe binding.
type TopicHandle[T any] struct {
	name string
	opts []Option
}

// Publish broadcasts event to every subscriber of the topic.
func (t *TopicHandle[T]) Publish(ctx Ctx, event T) error {
	panic("tako: not implemented")
}

func (t *TopicHandle[T]) eventSource(T) {}

// EventSource is a stream of T events that Subscribe binds handlers to:
// topics (TopicHandle) and bucket events (BucketEvent). It is implemented
// only by SDK types.
type EventSource[T any] interface {
	eventSource(T)
}
