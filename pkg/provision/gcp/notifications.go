package gcp

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gsandoval/tako/pkg/provision"
)

// BucketNotifications manages GCS notification configs
// (KindBucketNotification): one per events bucket, publishing
// OBJECT_FINALIZE to the bucket's events topic. Configs carry no
// labels; ownership rides custom_attributes (tako-spec-hash), and
// observation lists the DESIRED buckets' configs — content-addressed
// like Datastore indexes.
type BucketNotifications struct {
	C       *Client
	Desired []provision.Object
}

func (n *BucketNotifications) Kind() provision.Kind { return provision.KindBucketNotification }

type notificationConfig struct {
	ID               string            `json:"id,omitempty"`
	Topic            string            `json:"topic,omitempty"`
	PayloadFormat    string            `json:"payload_format,omitempty"`
	EventTypes       []string          `json:"event_types,omitempty"`
	CustomAttributes map[string]string `json:"custom_attributes,omitempty"`
}

func (n *BucketNotifications) configsURL(bucket string) string {
	return "https://storage.googleapis.com/storage/v1/b/" + bucket + "/notificationConfigs"
}

func (n *BucketNotifications) List(ctx context.Context) ([]provision.Observed, error) {
	var observed []provision.Observed
	for _, o := range n.Desired {
		if o.Kind != provision.KindBucketNotification {
			continue
		}
		var out struct {
			Items []notificationConfig `json:"items"`
		}
		err := n.C.do(ctx, http.MethodGet, n.configsURL(o.Name), nil, &out)
		if err != nil {
			if ae, ok := err.(*apiError); ok && ae.Status == http.StatusNotFound {
				continue // bucket not created yet: no configs to observe
			}
			return nil, Classify(err, n.C.Project, "list bucket notifications", "roles/storage.admin")
		}
		for _, cfg := range out.Items {
			if hash, ok := cfg.CustomAttributes["tako-spec-hash"]; ok {
				observed = append(observed, provision.Observed{
					Kind:     provision.KindBucketNotification,
					Name:     o.Name,
					SpecHash: hash,
				})
			}
		}
	}
	return observed, nil
}

func (n *BucketNotifications) Create(ctx context.Context, o provision.Object) error {
	body := notificationConfig{
		Topic:            "//pubsub.googleapis.com/projects/" + n.C.Project + "/topics/" + fmt.Sprint(o.Spec["topic"]),
		PayloadFormat:    fmt.Sprint(o.Spec["payloadFormat"]),
		EventTypes:       []string{"OBJECT_FINALIZE"},
		CustomAttributes: map[string]string{"tako-spec-hash": provision.SpecHash(o.Spec)},
	}
	// GCS validates the service agent's pubsub.publisher AT CREATION
	// time; the deploy grants it just before, so a brief IAM-propagation
	// retry is honest rather than defensive.
	deadline := time.Now().Add(90 * time.Second)
	for {
		err := n.C.do(ctx, http.MethodPost, n.configsURL(fmt.Sprint(o.Spec["bucket"])), body, nil)
		if err == nil {
			return nil
		}
		ae, ok := err.(*apiError)
		retriable := ok && (ae.Status == http.StatusBadRequest || ae.Status == http.StatusForbidden) &&
			strings.Contains(strings.ToLower(ae.Message), "permission")
		if !retriable || time.Now().After(deadline) {
			return Classify(err, n.C.Project, "create the bucket notification", "roles/storage.admin")
		}
		time.Sleep(5 * time.Second)
	}
}

// Update replaces: notification configs are immutable, so a spec change
// deletes the tako-owned config and recreates it (a momentary event gap
// on a config change — the honest cost of the platform shape).
func (n *BucketNotifications) Update(ctx context.Context, o provision.Object) error {
	if err := n.Delete(ctx, o.Name); err != nil {
		return err
	}
	return n.Create(ctx, o)
}

// Delete removes every tako-owned config on the bucket (name IS the
// bucket). A missing bucket means nothing to delete.
func (n *BucketNotifications) Delete(ctx context.Context, name string) error {
	var out struct {
		Items []notificationConfig `json:"items"`
	}
	err := n.C.do(ctx, http.MethodGet, n.configsURL(name), nil, &out)
	if err != nil {
		if ae, ok := err.(*apiError); ok && ae.Status == http.StatusNotFound {
			return nil
		}
		return Classify(err, n.C.Project, "list bucket notifications", "roles/storage.admin")
	}
	for _, cfg := range out.Items {
		if _, ok := cfg.CustomAttributes["tako-spec-hash"]; !ok {
			continue // foreign config: never touched
		}
		if err := n.C.do(ctx, http.MethodDelete, n.configsURL(name)+"/"+cfg.ID, nil, nil); err != nil {
			return Classify(err, n.C.Project, "delete the bucket notification", "roles/storage.admin")
		}
	}
	return nil
}
