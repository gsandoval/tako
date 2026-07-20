package backends

import "context"

// Topic is the event-broadcast contract behind tako.Topic and bucket
// events. Every subscriber receives every published payload, with
// per-subscriber delivery: independent attempt counters, independent
// retries, independent dead letters. The conformance suite defines the
// behavior every implementation must share.
type Topic interface {
	// Publish broadcasts payload to every subscriber of topic. A topic
	// with no subscribers is legal; the publish is a silent no-op.
	Publish(ctx context.Context, topic string, payload []byte) error
	// Subscribe registers deliver as the named subscriber of topic. The
	// subscriber name is the handler var's identity — it keys the
	// subscriber's dead letters and console lines.
	Subscribe(topic, subscriber string, deliver func(ctx context.Context, payload []byte) error)
}
