package gcp

import (
	"context"
	"net/http"
	"time"
)

// DeadMessage is one message retained on a dead-letter keep
// subscription, as tako dlq shows it.
type DeadMessage struct {
	AckID     string
	Payload   []byte
	Reason    string // tako-dead-reason attribute (Permanent short-circuits set it)
	Service   string // tako-service attribute
	Published time.Time
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
			AckID:     m.AckID,
			Payload:   m.Message.Data,
			Reason:    m.Message.Attributes["tako-dead-reason"],
			Service:   m.Message.Attributes["tako-service"],
			Published: m.Message.PublishTime,
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
