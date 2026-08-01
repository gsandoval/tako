package gcp

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/gsandoval/tako/pkg/provision"
)

// RunJobs manages Cloud Run v2 JOBS (§2.7): compute without a request
// timeout. Same marker discipline as services — labels carry the tako
// identity, the spec hash rides an annotation, drift re-derives the
// intended spec from the live job.
type RunJobs struct {
	C      *Client
	Labels map[string]string
}

func (r *RunJobs) Kind() provision.Kind { return provision.KindRunJob }

func (r *RunJobs) parent() string {
	return fmt.Sprintf("projects/%s/locations/%s", r.C.Project, r.C.Region)
}

type runJob struct {
	Name        string            `json:"name,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
	Template    struct {
		TaskCount int `json:"taskCount,omitempty"`
		Template  struct {
			ServiceAccount string `json:"serviceAccount,omitempty"`
			MaxRetries     int    `json:"maxRetries,omitempty"`
			Timeout        string `json:"timeout,omitempty"`
			Containers     []struct {
				Image string `json:"image"`
				Env   []struct {
					Name  string `json:"name"`
					Value string `json:"value"`
				} `json:"env,omitempty"`
				Resources struct {
					Limits map[string]string `json:"limits"`
				} `json:"resources"`
			} `json:"containers"`
		} `json:"template"`
	} `json:"template"`
}

func (r *RunJobs) List(ctx context.Context) ([]provision.Observed, error) {
	url := fmt.Sprintf("https://run.googleapis.com/v2/%s/jobs?pageSize=1000", r.parent())
	var out struct {
		Jobs []runJob `json:"jobs"`
	}
	if err := r.C.do(ctx, http.MethodGet, url, nil, &out); err != nil {
		return nil, Classify(err, r.C.Project, "list run jobs", "roles/run.viewer")
	}
	var observed []provision.Observed
	for _, j := range out.Jobs {
		if !hasLabels(j.Labels, r.Labels) {
			continue
		}
		observed = append(observed, provision.Observed{
			Kind:     provision.KindRunJob,
			Name:     lastSegment(j.Name),
			SpecHash: j.Annotations["tako-spec-hash"],
			LiveHash: jobLiveHash(j),
		})
	}
	return observed, nil
}

func jobLiveHash(j runJob) string {
	if len(j.Template.Template.Containers) != 1 {
		return "live-shape-unexpected"
	}
	c := j.Template.Template.Containers[0]
	env := map[string]any{}
	for _, e := range c.Env {
		env[e.Name] = e.Value
	}
	taskCount := j.Template.TaskCount
	if taskCount == 0 {
		taskCount = 1
	}
	// Same normalization the services applier does: the API reports
	// cpu "1000m"-style even when set as "1".
	cpu := strings.TrimSuffix(c.Resources.Limits["cpu"], "000m")
	return provision.SpecHash(map[string]any{
		"image":          c.Image,
		"serviceAccount": j.Template.Template.ServiceAccount,
		"env":            env,
		"taskCount":      taskCount,
		"cpu":            cpu,
		"memory":         c.Resources.Limits["memory"],
	})
}

func (r *RunJobs) body(o provision.Object) map[string]any {
	spec := o.Spec
	env := []map[string]any{}
	for name, value := range spec["env"].(map[string]any) {
		env = append(env, map[string]any{"name": name, "value": value})
	}
	return map[string]any{
		"labels":      r.Labels,
		"annotations": map[string]string{"tako-spec-hash": provision.SpecHash(o.Spec)},
		"template": map[string]any{
			"taskCount": spec["taskCount"],
			"template": map[string]any{
				"serviceAccount": spec["serviceAccount"],
				// Compute, not messaging: Cloud Run retries a failed
				// task up to maxRetries; there is no dead-letter here.
				"maxRetries": spec["retries"],
				"timeout":    fmt.Sprintf("%ds", spec["timeout"]),
				"containers": []map[string]any{{
					"image": spec["image"],
					"env":   env,
					"resources": map[string]any{
						"limits": map[string]any{
							"cpu":    fmt.Sprint(spec["cpu"]),
							"memory": fmt.Sprint(spec["memory"]),
						},
					},
				}},
			},
		},
	}
}

func (r *RunJobs) Create(ctx context.Context, o provision.Object) error {
	url := fmt.Sprintf("https://run.googleapis.com/v2/%s/jobs?jobId=%s", r.parent(), o.Name)
	var op struct {
		Name string `json:"name"`
	}
	if err := r.C.do(ctx, http.MethodPost, url, r.body(o), &op); err != nil {
		return Classify(err, r.C.Project, "create the run job", "roles/run.admin")
	}
	return r.C.waitOperation(ctx, "https://run.googleapis.com/v2/"+op.Name, waitLong)
}

func (r *RunJobs) Update(ctx context.Context, o provision.Object) error {
	url := fmt.Sprintf("https://run.googleapis.com/v2/%s/jobs/%s", r.parent(), o.Name)
	var op struct {
		Name string `json:"name"`
	}
	if err := r.C.do(ctx, http.MethodPatch, url, r.body(o), &op); err != nil {
		return Classify(err, r.C.Project, "update the run job", "roles/run.admin")
	}
	return r.C.waitOperation(ctx, "https://run.googleapis.com/v2/"+op.Name, waitLong)
}

func (r *RunJobs) Delete(ctx context.Context, name string) error {
	url := fmt.Sprintf("https://run.googleapis.com/v2/%s/jobs/%s", r.parent(), name)
	var op struct {
		Name string `json:"name"`
	}
	if err := r.C.do(ctx, http.MethodDelete, url, nil, &op); err != nil {
		return Classify(err, r.C.Project, "delete the run job", "roles/run.admin")
	}
	return r.C.waitOperation(ctx, "https://run.googleapis.com/v2/"+op.Name, waitLong)
}
