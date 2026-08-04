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
	url := r.C.url(tasksHost, "/v2/%s/queues?pageSize=500", r.parent())
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

// QueueNameReuseDays is how long Cloud Tasks blocks a deleted queue's
// name. Google's, not tako's, and there is no API to shorten it.
const QueueNameReuseDays = 7

// nameReuseBlocked reports Google's refusal to recreate a queue whose
// name was released too recently.
//
// The match is on the shortest fragment of the message rather than the
// whole of it: the only transcript of this failure is the T3 walk's
// paraphrase (2026-08-01), so a longer match would risk not firing at
// all. A miss falls through to the generic message, which is what
// shipped before — never to a wrong answer.
func nameReuseBlocked(err error) bool {
	ae, ok := asAPIError(err)
	return ok && strings.Contains(strings.ToLower(ae.Message), "existed too recently")
}

func (r *TasksQueues) Create(ctx context.Context, o provision.Object) error {
	url := r.C.url(tasksHost, "/v2/%s/queues", r.parent())
	if err := r.C.do(ctx, http.MethodPost, url, r.body(o), nil); err != nil {
		// Google says only that the name existed too recently. It does
		// not say how long the wait is, when the clock started, or that
		// another environment sidesteps it — and tako knows two of the
		// three. No countdown is offered because the error carries no
		// delete timestamp, and a guessed one would be worse than none.
		if nameReuseBlocked(err) {
			return fmt.Errorf("cannot recreate the tasks queue %q — Cloud Tasks blocks reusing a deleted queue's name for %d days after the delete, and this name is still inside that window. tako cannot work around it: either wait, or deploy under another environment, whose queues get fresh names — tako deploy --env <name>",
				o.Name, QueueNameReuseDays)
		}
		return Classify(err, r.C.Project, "create the tasks queue", "roles/cloudtasks.admin")
	}
	return nil
}

func (r *TasksQueues) Update(ctx context.Context, o provision.Object) error {
	url := r.C.url(tasksHost, "/v2/%s/queues/%s?updateMask=rateLimits.maxDispatchesPerSecond,retryConfig.maxAttempts", r.parent(), o.Name)
	if err := r.C.do(ctx, http.MethodPatch, url, r.body(o), nil); err != nil {
		return Classify(err, r.C.Project, "update the tasks queue", "roles/cloudtasks.admin")
	}
	return nil
}

func (r *TasksQueues) Delete(ctx context.Context, name string) error {
	url := r.C.url(tasksHost, "/v2/%s/queues/%s", r.parent(), name)
	if err := r.C.do(ctx, http.MethodDelete, url, nil, nil); err != nil {
		return Classify(err, r.C.Project, "delete the tasks queue", "roles/cloudtasks.admin")
	}
	return nil
}
