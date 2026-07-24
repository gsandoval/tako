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
	u := "https://pubsub.googleapis.com/v1/projects/" + t.C.Project + "/topics?pageSize=1000"
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
	if err := t.C.do(ctx, http.MethodPut, "https://pubsub.googleapis.com/v1/"+t.name(o.Name), body, nil); err != nil {
		return Classify(err, t.C.Project, "create the topic", "roles/pubsub.admin")
	}
	return nil
}

func (t *Topics) Update(ctx context.Context, o provision.Object) error {
	body := map[string]any{
		"topic":      topicBody{Name: t.name(o.Name), Labels: withHash(t.Labels, provision.SpecHash(o.Spec))},
		"updateMask": "labels",
	}
	if err := t.C.do(ctx, http.MethodPatch, "https://pubsub.googleapis.com/v1/"+t.name(o.Name), body, nil); err != nil {
		return Classify(err, t.C.Project, "update the topic", "roles/pubsub.admin")
	}
	return nil
}

func (t *Topics) Delete(ctx context.Context, name string) error {
	if err := t.C.do(ctx, http.MethodDelete, "https://pubsub.googleapis.com/v1/"+t.name(name), nil, nil); err != nil {
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
}

func (s *PushSubscriptions) List(ctx context.Context) ([]provision.Observed, error) {
	u := "https://pubsub.googleapis.com/v1/projects/" + s.C.Project + "/subscriptions?pageSize=1000"
	var out struct {
		Subscriptions []subscriptionBody `json:"subscriptions"`
	}
	if err := s.C.do(ctx, http.MethodGet, u, nil, &out); err != nil {
		return nil, Classify(err, s.C.Project, "list subscriptions", "roles/pubsub.viewer")
	}
	var observed []provision.Observed
	for _, sub := range out.Subscriptions {
		if !hasLabels(sub.Labels, s.Labels) {
			continue
		}
		observed = append(observed, provision.Observed{
			Kind:     provision.KindPushSubscription,
			Name:     lastSegment(sub.Name),
			SpecHash: sub.Labels["tako-spec-hash"],
		})
	}
	return observed, nil
}

func (s *PushSubscriptions) body(o provision.Object) subscriptionBody {
	return subscriptionBody{
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
}

func (s *PushSubscriptions) Create(ctx context.Context, o provision.Object) error {
	if err := s.C.do(ctx, http.MethodPut, "https://pubsub.googleapis.com/v1/"+s.name(o.Name), s.body(o), nil); err != nil {
		return Classify(err, s.C.Project, "create the push subscription", "roles/pubsub.admin")
	}
	return nil
}

// Update patches pushConfig and labels. The topic is immutable on a
// Pub/Sub subscription — a queue rebind would need delete + create,
// which the plan surfaces as an update that fails honestly rather than
// silently recreating (rebinding a live queue loses in-flight work).
func (s *PushSubscriptions) Update(ctx context.Context, o provision.Object) error {
	body := map[string]any{
		"subscription": subscriptionBody{
			Name:       s.name(o.Name),
			Labels:     withHash(s.Labels, provision.SpecHash(o.Spec)),
			PushConfig: s.body(o).PushConfig,
		},
		"updateMask": "pushConfig,labels",
	}
	if err := s.C.do(ctx, http.MethodPatch, "https://pubsub.googleapis.com/v1/"+s.name(o.Name), body, nil); err != nil {
		return Classify(err, s.C.Project, "update the push subscription", "roles/pubsub.admin")
	}
	return nil
}

func (s *PushSubscriptions) Delete(ctx context.Context, name string) error {
	if err := s.C.do(ctx, http.MethodDelete, "https://pubsub.googleapis.com/v1/"+s.name(name), nil, nil); err != nil {
		return Classify(err, s.C.Project, "delete the push subscription", "roles/pubsub.admin")
	}
	return nil
}
