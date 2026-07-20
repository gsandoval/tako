package backends

import (
	"context"
	"errors"
)

// Queue is the delivery contract behind tako.Queue[T]: at-least-once
// delivery of opaque payloads to a queue's single consumer, with
// redelivery on error and dead-lettering on permanent failure or attempt
// exhaustion.
type Queue interface {
	// Publish enqueues payload for the queue's consumer.
	Publish(ctx context.Context, queue string, payload []byte) error
	// Consume registers the queue's single consumer. deliver returns nil
	// to ack, an error to redeliver; a permanent error (IsPermanent)
	// short-circuits to the dead letter.
	Consume(queue string, deliver func(ctx context.Context, payload []byte) error)
}

// DeadLetter records a message that gave up delivery.
type DeadLetter struct {
	// Queue is the queue or topic the message failed on.
	Queue string `json:"queue"`
	// Subscriber names the failing subscriber for topic deliveries; empty
	// for queue deliveries (a queue has exactly one consumer).
	Subscriber string `json:"subscriber,omitempty"`
	// Payload is the message's raw JSON payload.
	Payload []byte `json:"payload"`
	// Attempts is how many deliveries were tried.
	Attempts int `json:"attempts"`
	// LastError is the final delivery error's text.
	LastError string `json:"lastError"`
	// Permanent reports whether the handler refused retries.
	Permanent bool `json:"permanent"`
}

// DeadLetterObserver is implemented by backends that can surface dead
// letters in-process (the local backend; the dev console subscribes).
type DeadLetterObserver interface {
	OnDeadLetter(fn func(DeadLetter))
}

// permanentError is the duck interface tako.Permanent's errors satisfy;
// defined here so backends never import the SDK.
type permanentError interface {
	PermanentDelivery() bool
}

// IsPermanent reports whether err refuses redelivery.
func IsPermanent(err error) bool {
	var pe permanentError
	return errors.As(err, &pe) && pe.PermanentDelivery()
}
