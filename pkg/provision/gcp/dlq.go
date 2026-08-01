package gcp

import (
	"context"
	"net/http"
	"time"
)

// DeadMessage is one message retained on a dead-letter keep
// subscription, as tako dlq shows it.
type DeadMessage struct {
	// ID is Pub/Sub's messageId: stable for the message's life, unlike
	// AckID which is minted per pull. It is what `--replay <id>` names,
	// because an id that expires between reading and typing is not an
	// id.
	ID        string
	AckID     string
	Payload   []byte
	Reason    string // tako-dead-reason attribute (Permanent short-circuits set it)
	Service   string // tako-service attribute
	Published time.Time
	// Attributes is the message's full attribute set, carried forward
	// verbatim on replay. Pub/Sub's dead-letter forwarding preserves the
	// ORIGINAL attributes, so tako-queue / tako-topic survive the trip
	// and are how a replay knows where the message came from.
	Attributes map[string]string
}

// Source reports where a dead letter came from: a queue (one worker) or
// a topic (fan-out), with the carrier's logical name. ok is false for a
// message carrying neither attribute — a Permanent short-circuit
// published straight to the dead topic before C4 stamped the source,
// or something foreign on the subscription.
func (m DeadMessage) Source() (kind, name string, ok bool) {
	if q := m.Attributes["tako-queue"]; q != "" {
		return "queue", q, true
	}
	if t := m.Attributes["tako-topic"]; t != "" {
		return "topic", t, true
	}
	return "", "", false
}

// PullDead peeks up to max messages from a dead-letter keep
// subscription WITHOUT acking — repeated peeks keep seeing them until
// AckDead drains.
func (c *Client) PullDead(ctx context.Context, sub string, max int) ([]DeadMessage, error) {
	u := "https://pubsub.googleapis.com/v1/projects/" + c.Project + "/subscriptions/" + sub + ":pull"
	var out struct {
		ReceivedMessages []struct {
			AckID   string `json:"ackId"`
			Message struct {
				MessageID   string            `json:"messageId"`
				Data        []byte            `json:"data"` // encoding/json base64-decodes
				Attributes  map[string]string `json:"attributes"`
				PublishTime time.Time         `json:"publishTime"`
			} `json:"message"`
		} `json:"receivedMessages"`
	}
	if err := c.do(ctx, http.MethodPost, u, map[string]any{"maxMessages": max}, &out); err != nil {
		return nil, Classify(err, c.Project, "pull dead-lettered messages", "roles/pubsub.subscriber")
	}
	msgs := make([]DeadMessage, 0, len(out.ReceivedMessages))
	for _, m := range out.ReceivedMessages {
		msgs = append(msgs, DeadMessage{
			ID:         m.Message.MessageID,
			AckID:      m.AckID,
			Payload:    m.Message.Data,
			Reason:     m.Message.Attributes["tako-dead-reason"],
			Service:    m.Message.Attributes["tako-service"],
			Published:  m.Message.PublishTime,
			Attributes: m.Message.Attributes,
		})
	}
	return msgs, nil
}

// AckDead acknowledges peeked messages off a keep subscription — tako
// dlq --ack's drain.
func (c *Client) AckDead(ctx context.Context, sub string, ackIDs []string) error {
	if len(ackIDs) == 0 {
		return nil
	}
	u := "https://pubsub.googleapis.com/v1/projects/" + c.Project + "/subscriptions/" + sub + ":acknowledge"
	if err := c.do(ctx, http.MethodPost, u, map[string]any{"ackIds": ackIDs}, nil); err != nil {
		return Classify(err, c.Project, "acknowledge dead-lettered messages", "roles/pubsub.subscriber")
	}
	return nil
}

// ReplayAttr addresses a replayed message at ONE subscriber. A topic
// fans out to every subscriber, but a dead letter belongs to exactly
// one of them, and Pub/Sub has no inject-into-one-subscription API — so
// the message goes back through the topic carrying this attribute and
// the runtime drops it everywhere else.
//
// Riding the real delivery path is the point: retries, the attempt
// counter and re-dead-lettering all behave exactly as they do for a
// first delivery, so a replay that fails again lands back on the DLQ
// instead of vanishing into the CLI.
const ReplayAttr = "tako-replay-to"

// Republish puts a message back on a topic with the attributes it
// carried, plus whatever the replay adds.
func (c *Client) Republish(ctx context.Context, topic string, payload []byte, attrs map[string]string) error {
	u := "https://pubsub.googleapis.com/v1/projects/" + c.Project + "/topics/" + topic + ":publish"
	msg := map[string]any{"data": payload} // []byte marshals as base64, which is the wire form
	if len(attrs) > 0 {
		msg["attributes"] = attrs
	}
	body := map[string]any{"messages": []any{msg}}
	if err := c.do(ctx, http.MethodPost, u, body, nil); err != nil {
		return Classify(err, c.Project, "republish the dead-lettered message", "roles/pubsub.publisher")
	}
	return nil
}
