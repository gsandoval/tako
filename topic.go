package tako

import (
	"encoding/json"

	"github.com/gsandoval/tako/internal/hooks"
	"github.com/gsandoval/tako/pkg/backends"
)

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

// Publish broadcasts event to every subscriber of the topic. A topic with
// no subscribers is legal — events broadcast into silence.
func (t *TopicHandle[T]) Publish(ctx Ctx, event T) error {
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	return topicBackend(ctx).Publish(ctx, t.name, raw)
}

func (t *TopicHandle[T]) eventSource(T) {}

func (t *TopicHandle[T]) sourceName() string { return t.name }

// EventSource is a stream of T events that Subscribe binds handlers to:
// topics (TopicHandle) and bucket events (BucketEvent). It is implemented
// only by SDK types.
type EventSource[T any] interface {
	eventSource(T)
	sourceName() string
}

// topicBackend returns the wired topic backend, which boot (generated
// mains, tako dev, and — from F7 — NewTestApp) is responsible for
// installing.
func topicBackend(ctx Ctx) backends.Topic {
	if a := appFrom(ctx); a != nil {
		return a.Dispatcher.Topics()
	}
	if hooks.Topic == nil {
		panic("tako: no topic backend wired — run via tako dev, a generated service, or tako.NewTestApp")
	}
	return hooks.Topic
}
