package gcp

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/gsandoval/tako/pkg/provision"
)

// TasksQueues manages Cloud Tasks queues (T3): the delivery front for
// rate-limited and delayed tako queues. Tasks queues carry no labels
// and no descriptions, so the managed marker is the NAME: every tako
// tasks queue ends in "-tako" (the env-prefixed logical name plus the
// suffix), and the spec is small enough to compare live.
type TasksQueues struct {
	C *Client
}

func (r *TasksQueues) Kind() provision.Kind { return provision.KindTasksQueue }

func (r *TasksQueues) parent() string {
	return fmt.Sprintf("projects/%s/locations/%s", r.C.Project, r.C.Region)
}

type tasksQueue struct {
	Name       string `json:"name,omitempty"`
	RateLimits struct {
		MaxDispatchesPerSecond float64 `json:"maxDispatchesPerSecond,omitempty"`
	} `json:"rateLimits"`
	RetryConfig struct {
		MaxAttempts int `json:"maxAttempts,omitempty"`
	} `json:"retryConfig"`
	State string `json:"state,omitempty"`
}

func (r *TasksQueues) List(ctx context.Context) ([]provision.Observed, error) {
	url := fmt.Sprintf("https://cloudtasks.googleapis.com/v2/%s/queues?pageSize=500", r.parent())
	var out struct {
		Queues []tasksQueue `json:"queues"`
	}
	if err := r.C.do(ctx, http.MethodGet, url, nil, &out); err != nil {
		if disabledAPI(err) {
			return nil, nil // API off ⇒ nothing of ours can exist
		}
		return nil, Classify(err, r.C.Project, "list tasks queues", "roles/cloudtasks.viewer")
	}
	var observed []provision.Observed
	for _, q := range out.Queues {
		id := lastSegment(q.Name)
		if !strings.HasSuffix(id, "-tako") {
			continue
		}
		hash := provision.SpecHash(map[string]any{
			"rate":     int(q.RateLimits.MaxDispatchesPerSecond),
			"attempts": q.RetryConfig.MaxAttempts,
		})
		observed = append(observed, provision.Observed{
			Kind:     provision.KindTasksQueue,
			Name:     id,
			SpecHash: hash,
			LiveHash: hash,
		})
	}
	return observed, nil
}

func (r *TasksQueues) body(o provision.Object) map[string]any {
	return map[string]any{
		"name": r.parent() + "/queues/" + o.Name,
		"rateLimits": map[string]any{
			"maxDispatchesPerSecond": o.Spec["rate"],
		},
		// Mirror the queue's declared redelivery contract. Tasks has NO
		// dead letter — an exhausted task is gone (queue.md's Not yet).
		"retryConfig": map[string]any{"maxAttempts": o.Spec["attempts"]},
	}
}

func (r *TasksQueues) Create(ctx context.Context, o provision.Object) error {
	url := fmt.Sprintf("https://cloudtasks.googleapis.com/v2/%s/queues", r.parent())
	if err := r.C.do(ctx, http.MethodPost, url, r.body(o), nil); err != nil {
		return Classify(err, r.C.Project, "create the tasks queue", "roles/cloudtasks.admin")
	}
	return nil
}

func (r *TasksQueues) Update(ctx context.Context, o provision.Object) error {
	url := fmt.Sprintf("https://cloudtasks.googleapis.com/v2/%s/queues/%s?updateMask=rateLimits.maxDispatchesPerSecond,retryConfig.maxAttempts", r.parent(), o.Name)
	if err := r.C.do(ctx, http.MethodPatch, url, r.body(o), nil); err != nil {
		return Classify(err, r.C.Project, "update the tasks queue", "roles/cloudtasks.admin")
	}
	return nil
}

func (r *TasksQueues) Delete(ctx context.Context, name string) error {
	url := fmt.Sprintf("https://cloudtasks.googleapis.com/v2/%s/queues/%s", r.parent(), name)
	if err := r.C.do(ctx, http.MethodDelete, url, nil, nil); err != nil {
		return Classify(err, r.C.Project, "delete the tasks queue", "roles/cloudtasks.admin")
	}
	return nil
}
