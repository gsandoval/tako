package gcp

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/gsandoval/tako/pkg/provision"
)

// SchedulerJobs manages Cloud Scheduler jobs (T2): the cron triggers.
// Scheduler jobs carry no labels, so the tako identity rides the
// DESCRIPTION ("tako:<app>:<env>") and the spec hash rides after it —
// the same marker discipline, one field over.
type SchedulerJobs struct {
	C      *Client
	App    string
	Env    string
	Labels map[string]string // unused (no labels on Scheduler); kept for applier symmetry
}

func (r *SchedulerJobs) Kind() provision.Kind { return provision.KindSchedulerJob }

func (r *SchedulerJobs) parent() string {
	return fmt.Sprintf("projects/%s/locations/%s", r.C.Project, r.C.Region)
}

func (r *SchedulerJobs) marker() string { return "tako:" + r.App + ":" + r.Env }

type schedulerJob struct {
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Schedule    string `json:"schedule,omitempty"`
	TimeZone    string `json:"timeZone,omitempty"`
	HTTPTarget  struct {
		URI        string `json:"uri"`
		HTTPMethod string `json:"httpMethod,omitempty"`
		OIDCToken  struct {
			ServiceAccountEmail string `json:"serviceAccountEmail,omitempty"`
			Audience            string `json:"audience,omitempty"`
		} `json:"oidcToken"`
	} `json:"httpTarget"`
	AttemptDeadline string `json:"attemptDeadline,omitempty"`
}

func (r *SchedulerJobs) List(ctx context.Context) ([]provision.Observed, error) {
	url := fmt.Sprintf("https://cloudscheduler.googleapis.com/v1/%s/jobs?pageSize=500", r.parent())
	var out struct {
		Jobs []schedulerJob `json:"jobs"`
	}
	if err := r.C.do(ctx, http.MethodGet, url, nil, &out); err != nil {
		if disabledAPI(err) {
			return nil, nil // API off ⇒ nothing of ours can exist
		}
		return nil, Classify(err, r.C.Project, "list scheduler jobs", "roles/cloudscheduler.viewer")
	}
	var observed []provision.Observed
	for _, j := range out.Jobs {
		desc, hash, _ := strings.Cut(j.Description, " spec-hash=")
		if desc != r.marker() {
			continue
		}
		observed = append(observed, provision.Observed{
			Kind:     provision.KindSchedulerJob,
			Name:     lastSegment(j.Name),
			SpecHash: hash,
			LiveHash: provision.SpecHash(map[string]any{
				"schedule":       j.Schedule,
				"uri":            j.HTTPTarget.URI,
				"serviceAccount": j.HTTPTarget.OIDCToken.ServiceAccountEmail,
				"audience":       j.HTTPTarget.OIDCToken.Audience,
			}),
		})
	}
	return observed, nil
}

func (r *SchedulerJobs) body(o provision.Object) map[string]any {
	spec := o.Spec
	return map[string]any{
		"name":        r.parent() + "/jobs/" + o.Name,
		"description": r.marker() + " spec-hash=" + provision.SpecHash(o.Spec),
		"schedule":    spec["schedule"],
		"timeZone":    "Etc/UTC",
		"httpTarget": map[string]any{
			"uri":        spec["uri"],
			"httpMethod": "POST",
			"oidcToken": map[string]any{
				"serviceAccountEmail": spec["serviceAccount"],
				"audience":            spec["audience"],
			},
		},
		// The run's timeout: Scheduler retries a failed or overlong
		// attempt on the next tick (no retryConfig — the schedule IS
		// the retry policy for idempotent crons).
		"attemptDeadline": "180s",
	}
}

func (r *SchedulerJobs) Create(ctx context.Context, o provision.Object) error {
	url := fmt.Sprintf("https://cloudscheduler.googleapis.com/v1/%s/jobs", r.parent())
	if err := r.C.do(ctx, http.MethodPost, url, r.body(o), nil); err != nil {
		return Classify(err, r.C.Project, "create the scheduler job", "roles/cloudscheduler.admin")
	}
	return nil
}

func (r *SchedulerJobs) Update(ctx context.Context, o provision.Object) error {
	url := fmt.Sprintf("https://cloudscheduler.googleapis.com/v1/%s/jobs/%s", r.parent(), o.Name)
	if err := r.C.do(ctx, http.MethodPatch, url, r.body(o), nil); err != nil {
		return Classify(err, r.C.Project, "update the scheduler job", "roles/cloudscheduler.admin")
	}
	return nil
}

func (r *SchedulerJobs) Delete(ctx context.Context, name string) error {
	url := fmt.Sprintf("https://cloudscheduler.googleapis.com/v1/%s/jobs/%s", r.parent(), name)
	if err := r.C.do(ctx, http.MethodDelete, url, nil, nil); err != nil {
		return Classify(err, r.C.Project, "delete the scheduler job", "roles/cloudscheduler.admin")
	}
	return nil
}
