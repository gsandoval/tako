package gcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

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
	// Tasks-routed queues (T3): the deploy injects TAKO_TASKS_<QUEUE>
	// for every queue that is rate-limited or delay-published — ALL its
	// traffic enqueues through Cloud Tasks (the rate cap must see every
	// message). Everything else rides Pub/Sub push, as before.
	if route := os.Getenv("TAKO_TASKS_" + strings.ToUpper(strings.ReplaceAll(queue, "-", "_"))); route != "" {
		return s.c.enqueueTask(ctx, route, queue, payload, opts.Delay)
	}
	if opts.Delay > 0 {
		return fmt.Errorf("queue %q has no Cloud Tasks route — a WithDelay publish site the analyzer did not see? redeploy so the deploy can provision it", queue)
	}
	return s.c.publish(ctx, TopicName(s.c.Opts.Env, queue), payload, map[string]string{"tako-queue": queue})
}

// enqueueTask creates one Cloud Tasks task that POSTs the standard push
// envelope to the worker's push endpoint with the worker's own OIDC
// identity — the same trust edge as a Pub/Sub push, one broker over.
func (c *Client) enqueueTask(ctx context.Context, route, queue string, payload []byte, delay time.Duration) error {
	parts := strings.SplitN(route, "|", 3)
	if len(parts) != 3 {
		return fmt.Errorf("malformed TAKO_TASKS route for queue %q", queue)
	}
	queueResource, pushURL, workerSA := parts[0], parts[1], parts[2]
	envelope, err := json.Marshal(map[string]any{
		"message": map[string]any{
			"data":       payload, // encoding/json base64-encodes []byte
			"attributes": map[string]string{"tako-queue": queue},
		},
		"subscription": "cloudtasks",
	})
	if err != nil {
		return err
	}
	task := map[string]any{
		"httpRequest": map[string]any{
			"url":        pushURL,
			"httpMethod": "POST",
			"headers":    map[string]string{"Content-Type": "application/json"},
			"body":       base64.StdEncoding.EncodeToString(envelope),
			"oidcToken": map[string]any{
				"serviceAccountEmail": workerSA,
				"audience":            pushURL,
			},
		},
	}
	if delay > 0 {
		task["scheduleTime"] = time.Now().Add(delay).UTC().Format(time.RFC3339)
	}
	u := "https://cloudtasks.googleapis.com/v2/" + queueResource + "/tasks"
	return c.do(ctx, "POST", u, map[string]any{"task": task}, nil)
}

// Consume is a no-op on gcp: delivery arrives over HTTP push, not an
// in-process loop. Registration still matters locally and for tests.
func (s *QueueStore) Consume(queue string, deliver func(ctx context.Context, payload []byte) error) {}

// publish sends one message to a physical Pub/Sub topic — the shared
// path under queues, topics, bucket-event emission, and the Permanent
// dead-letter short-circuit.
func (c *Client) publish(ctx context.Context, topic string, payload []byte, attrs map[string]string) error {
	u := "https://pubsub.googleapis.com/v1/projects/" + c.Opts.Project + "/topics/" + topic + ":publish"
	msg := map[string]any{"data": base64.StdEncoding.EncodeToString(payload)}
	if len(attrs) > 0 {
		msg["attributes"] = attrs
	}
	return c.do(ctx, "POST", u, map[string]any{"messages": []map[string]any{msg}}, nil)
}

// TopicStore implements the publish half of backends.Topic on Pub/Sub.
// Fan-out is the provisioner's: one push subscription per subscriber
// service, each with its own retries and dead letter — publishing here
// reaches all of them.
type TopicStore struct {
	c *Client
}

func NewTopicStore(c *Client) *TopicStore { return &TopicStore{c: c} }

func (s *TopicStore) Publish(ctx context.Context, topic string, payload []byte) error {
	return s.c.publish(ctx, TopicName(s.c.Opts.Env, topic), payload, map[string]string{"tako-topic": topic})
}

// Subscribe is a no-op on gcp, like Consume: delivery arrives over
// HTTP push into each subscriber's own service.
func (s *TopicStore) Subscribe(topic, subscriber string, deliver func(ctx context.Context, payload []byte) error) {
}

// DeadLetter publishes one exhausted payload onto this service's own
// dead-letter topic — the runtime's tako.Permanent short-circuit
// (Pub/Sub cannot express "nack without retry", so the runtime routes
// the message itself and acks). Attributes carry what local's
// dead-letter JSON records.
func (c *Client) DeadLetter(ctx context.Context, payload []byte, reason string) error {
	if c.Opts.Service == "" {
		return fmt.Errorf("dead-letter publish without TAKO_SERVICE — tako deploy injects it, so this is a tako bug")
	}
	return c.publish(ctx, DeadTopicName(c.Opts.Env, c.Opts.Service), payload, map[string]string{
		"tako-dead-reason": reason,
		"tako-service":     c.Opts.Service,
	})
}
