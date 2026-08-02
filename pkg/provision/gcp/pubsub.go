package gcp

import (
	"context"
	"fmt"
	"net/http"

	"github.com/gsandoval/tako/pkg/provision"
)

// Topics manages Pub/Sub topics (KindTopic); labels carry the markers.
type Topics struct {
	C      *Client
	Labels map[string]string
}

func (t *Topics) Kind() provision.Kind { return provision.KindTopic }

func (t *Topics) name(short string) string {
	return "projects/" + t.C.Project + "/topics/" + short
}

type topicBody struct {
	Name   string            `json:"name,omitempty"`
	Labels map[string]string `json:"labels"`
}

func (t *Topics) List(ctx context.Context) ([]provision.Observed, error) {
	u := t.C.url(pubsubHost, "/v1/projects/"+t.C.Project+"/topics?pageSize=1000")
	var out struct {
		Topics []topicBody `json:"topics"`
	}
	if err := t.C.do(ctx, http.MethodGet, u, nil, &out); err != nil {
		return nil, Classify(err, t.C.Project, "list topics", "roles/pubsub.viewer")
	}
	var observed []provision.Observed
	for _, tp := range out.Topics {
		if !hasLabels(tp.Labels, t.Labels) {
			continue
		}
		observed = append(observed, provision.Observed{
			Kind:     provision.KindTopic,
			Name:     lastSegment(tp.Name),
			SpecHash: tp.Labels["tako-spec-hash"],
		})
	}
	return observed, nil
}

func (t *Topics) Create(ctx context.Context, o provision.Object) error {
	body := topicBody{Labels: withHash(t.Labels, provision.SpecHash(o.Spec))}
	if err := t.C.do(ctx, http.MethodPut, t.C.url(pubsubHost, "/v1/"+t.name(o.Name)), body, nil); err != nil {
		return Classify(err, t.C.Project, "create the topic", "roles/pubsub.admin")
	}
	return nil
}

func (t *Topics) Update(ctx context.Context, o provision.Object) error {
	body := map[string]any{
		"topic":      topicBody{Name: t.name(o.Name), Labels: withHash(t.Labels, provision.SpecHash(o.Spec))},
		"updateMask": "labels",
	}
	if err := t.C.do(ctx, http.MethodPatch, t.C.url(pubsubHost, "/v1/"+t.name(o.Name)), body, nil); err != nil {
		return Classify(err, t.C.Project, "update the topic", "roles/pubsub.admin")
	}
	return nil
}

func (t *Topics) Delete(ctx context.Context, name string) error {
	if err := t.C.do(ctx, http.MethodDelete, t.C.url(pubsubHost, "/v1/"+t.name(name)), nil, nil); err != nil {
		return Classify(err, t.C.Project, "delete the topic", "roles/pubsub.admin")
	}
	return nil
}

// PushSubscriptions manages one OIDC push subscription per worker
// (KindPushSubscription). The subscription authenticates as the
// worker's own SA; Cloud Run IAM (the grants kind) admits it.
type PushSubscriptions struct {
	C      *Client
	Labels map[string]string
	// Say narrates the replace path (topic changes can't patch).
	Say func(format string, args ...any)
}

func (s *PushSubscriptions) Kind() provision.Kind { return provision.KindPushSubscription }

func (s *PushSubscriptions) name(short string) string {
	return "projects/" + s.C.Project + "/subscriptions/" + short
}

type subscriptionBody struct {
	Name               string            `json:"name,omitempty"`
	Topic              string            `json:"topic,omitempty"`
	Labels             map[string]string `json:"labels"`
	AckDeadlineSeconds int               `json:"ackDeadlineSeconds,omitempty"`
	PushConfig         map[string]any    `json:"pushConfig,omitempty"`
	RetryPolicy        map[string]any    `json:"retryPolicy,omitempty"`
	DeadLetterPolicy   map[string]any    `json:"deadLetterPolicy,omitempty"`
}

// listSubscriptions lists tako-labeled subscriptions of one delivery
// shape: Pub/Sub keeps push and pull subscriptions in the same
// collection, so each kind filters by pushConfig presence.
func listSubscriptions(ctx context.Context, c *Client, labels map[string]string, kind provision.Kind, push bool) ([]provision.Observed, error) {
	u := c.url(pubsubHost, "/v1/projects/"+c.Project+"/subscriptions?pageSize=1000")
	var out struct {
		Subscriptions []subscriptionBody `json:"subscriptions"`
	}
	if err := c.do(ctx, http.MethodGet, u, nil, &out); err != nil {
		return nil, Classify(err, c.Project, "list subscriptions", "roles/pubsub.viewer")
	}
	var observed []provision.Observed
	for _, sub := range out.Subscriptions {
		if !hasLabels(sub.Labels, labels) || (len(sub.PushConfig) > 0) != push {
			continue
		}
		observed = append(observed, provision.Observed{
			Kind:     kind,
			Name:     lastSegment(sub.Name),
			SpecHash: sub.Labels["tako-spec-hash"],
			// The topic is the subscription's one immutable field — the
			// plan classifies a topic change as a REPLACE from this.
			Detail: map[string]string{"topic": lastSegment(sub.Topic)},
		})
	}
	return observed, nil
}

func (s *PushSubscriptions) List(ctx context.Context) ([]provision.Observed, error) {
	return listSubscriptions(ctx, s.C, s.Labels, provision.KindPushSubscription, true)
}

func (s *PushSubscriptions) body(o provision.Object) subscriptionBody {
	b := subscriptionBody{
		Topic:  "projects/" + s.C.Project + "/topics/" + fmt.Sprint(o.Spec["topic"]),
		Labels: withHash(s.Labels, provision.SpecHash(o.Spec)),
		// Cloud Run request timeout is the real ceiling; 600s is
		// Pub/Sub's maximum ack deadline.
		AckDeadlineSeconds: 600,
		PushConfig: map[string]any{
			"pushEndpoint": o.Spec["endpoint"],
			"oidcToken": map[string]any{
				"serviceAccountEmail": o.Spec["serviceAccount"],
				"audience":            o.Spec["audience"],
			},
		},
	}
	// Local failure semantics on the broker: bounded retries with
	// backoff, then the service's dead topic (C4).
	if r, ok := o.Spec["retry"].(map[string]any); ok {
		b.RetryPolicy = map[string]any{
			"minimumBackoff": fmt.Sprint(r["minimumBackoff"]),
			"maximumBackoff": fmt.Sprint(r["maximumBackoff"]),
		}
	}
	if d, ok := o.Spec["deadLetter"].(map[string]any); ok {
		b.DeadLetterPolicy = map[string]any{
			"deadLetterTopic":     "projects/" + s.C.Project + "/topics/" + fmt.Sprint(d["topic"]),
			"maxDeliveryAttempts": d["maxAttempts"],
		}
	}
	return b
}

func (s *PushSubscriptions) Create(ctx context.Context, o provision.Object) error {
	if err := s.C.do(ctx, http.MethodPut, s.C.url(pubsubHost, "/v1/"+s.name(o.Name)), s.body(o), nil); err != nil {
		return Classify(err, s.C.Project, "create the push subscription", "roles/pubsub.admin")
	}
	return nil
}

// Update patches pushConfig, retry/dead-letter policy, and labels. The
// topic is immutable on a Pub/Sub subscription, so a topic change is
// the REPLACE class: the plan announced it (and the deploy confirmed
// it), and the apply is delete + create — in-flight messages on the
// old subscription are lost, which is exactly what the plan said.
func (s *PushSubscriptions) Update(ctx context.Context, o provision.Object) error {
	var live subscriptionBody
	if err := s.C.do(ctx, http.MethodGet, s.C.url(pubsubHost, "/v1/"+s.name(o.Name)), nil, &live); err == nil &&
		live.Topic != "" && lastSegment(live.Topic) != fmt.Sprint(o.Spec["topic"]) {
		if s.Say != nil {
			s.Say("⚠ replacing subscription %s (topic %s → %s): delete + create", o.Name, lastSegment(live.Topic), o.Spec["topic"])
		}
		if err := s.Delete(ctx, o.Name); err != nil {
			return err
		}
		return s.Create(ctx, o)
	}
	full := s.body(o)
	body := map[string]any{
		"subscription": subscriptionBody{
			Name:             s.name(o.Name),
			Labels:           full.Labels,
			PushConfig:       full.PushConfig,
			RetryPolicy:      full.RetryPolicy,
			DeadLetterPolicy: full.DeadLetterPolicy,
		},
		"updateMask": "pushConfig,retryPolicy,deadLetterPolicy,labels",
	}
	if err := s.C.do(ctx, http.MethodPatch, s.C.url(pubsubHost, "/v1/"+s.name(o.Name)), body, nil); err != nil {
		return Classify(err, s.C.Project, "update the push subscription", "roles/pubsub.admin")
	}
	return nil
}

func (s *PushSubscriptions) Delete(ctx context.Context, name string) error {
	if err := s.C.do(ctx, http.MethodDelete, s.C.url(pubsubHost, "/v1/"+s.name(name)), nil, nil); err != nil {
		return Classify(err, s.C.Project, "delete the push subscription", "roles/pubsub.admin")
	}
	return nil
}

// PullSubscriptions manages the dead-letter "keep" subscriptions
// (KindPullSubscription): a dead topic with no subscription DROPS
// forwarded messages, so each async service's dead topic carries one
// pull subscription from birth — retention (Pub/Sub's 7-day default)
// and what tako dlq reads.
type PullSubscriptions struct {
	C      *Client
	Labels map[string]string
}

func (s *PullSubscriptions) Kind() provision.Kind { return provision.KindPullSubscription }

func (s *PullSubscriptions) name(short string) string {
	return "projects/" + s.C.Project + "/subscriptions/" + short
}

func (s *PullSubscriptions) List(ctx context.Context) ([]provision.Observed, error) {
	return listSubscriptions(ctx, s.C, s.Labels, provision.KindPullSubscription, false)
}

func (s *PullSubscriptions) Create(ctx context.Context, o provision.Object) error {
	body := subscriptionBody{
		Topic:  "projects/" + s.C.Project + "/topics/" + fmt.Sprint(o.Spec["topic"]),
		Labels: withHash(s.Labels, provision.SpecHash(o.Spec)),
	}
	if err := s.C.do(ctx, http.MethodPut, s.C.url(pubsubHost, "/v1/"+s.name(o.Name)), body, nil); err != nil {
		return Classify(err, s.C.Project, "create the dead-letter keep subscription", "roles/pubsub.admin")
	}
	return nil
}

func (s *PullSubscriptions) Update(ctx context.Context, o provision.Object) error {
	body := map[string]any{
		"subscription": subscriptionBody{
			Name:   s.name(o.Name),
			Labels: withHash(s.Labels, provision.SpecHash(o.Spec)),
		},
		"updateMask": "labels",
	}
	if err := s.C.do(ctx, http.MethodPatch, s.C.url(pubsubHost, "/v1/"+s.name(o.Name)), body, nil); err != nil {
		return Classify(err, s.C.Project, "update the dead-letter keep subscription", "roles/pubsub.admin")
	}
	return nil
}

func (s *PullSubscriptions) Delete(ctx context.Context, name string) error {
	if err := s.C.do(ctx, http.MethodDelete, s.C.url(pubsubHost, "/v1/"+s.name(name)), nil, nil); err != nil {
		return Classify(err, s.C.Project, "delete the dead-letter keep subscription", "roles/pubsub.admin")
	}
	return nil
}
