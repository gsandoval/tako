package gcp

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/gsandoval/tako/pkg/backends"
)

// QueueStore implements the publish half of backends.Queue on Pub/Sub.
// Delivery is push: the provisioner points an OIDC push subscription at
// each worker's /_tako/push endpoint, so Consume has nothing to run
// in-process — Cloud Run IAM admits the push, PushHandler invokes the
// worker.
type QueueStore struct {
	c *Client
}

func NewQueueStore(c *Client) *QueueStore { return &QueueStore{c: c} }

func (s *QueueStore) Publish(ctx context.Context, queue string, payload []byte, opts backends.PublishOptions) error {
	if opts.Delay > 0 {
		return fmt.Errorf("tako.WithDelay on the gcp backend needs Cloud Tasks — that arrives with C5; remove the delay or run locally")
	}
	topic := TopicName(s.c.Opts.Env, queue)
	u := "https://pubsub.googleapis.com/v1/projects/" + s.c.Opts.Project + "/topics/" + topic + ":publish"
	body := map[string]any{"messages": []map[string]any{{
		"data":       base64.StdEncoding.EncodeToString(payload),
		"attributes": map[string]string{"tako-queue": queue},
	}}}
	return s.c.do(ctx, "POST", u, body, nil)
}

// Consume is a no-op on gcp: delivery arrives over HTTP push, not an
// in-process loop. Registration still matters locally and for tests.
func (s *QueueStore) Consume(queue string, deliver func(ctx context.Context, payload []byte) error) {}

// TopicStore is the not-yet stub for tako.Topic on gcp: per-handler
// fan-out subscriptions and dead-letter wiring arrive with C4.
type TopicStore struct{}

func (TopicStore) Publish(ctx context.Context, topic string, payload []byte) error {
	return fmt.Errorf("tako.Topic on the gcp backend arrives with C4 — topics deploy but publishes fail until then")
}

func (TopicStore) Subscribe(topic, subscriber string, deliver func(ctx context.Context, payload []byte) error) {
}
