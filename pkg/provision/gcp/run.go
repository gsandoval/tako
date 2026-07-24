package gcp

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/gsandoval/tako/pkg/provision"
)

// RunServices manages Cloud Run v2 services. Labels carry the tako
// markers, annotations carry the spec hash and version stamps; drift is
// detected by re-deriving the intended-spec map from the live service
// and hashing it the same way Desired does.
type RunServices struct {
	C      *Client
	Labels map[string]string
	// Annotations are version stamps (git sha, graph hash) added to
	// every service tako writes.
	Annotations map[string]string
}

func (r *RunServices) Kind() provision.Kind { return provision.KindRunService }

func (r *RunServices) parent() string {
	return fmt.Sprintf("projects/%s/locations/%s", r.C.Project, r.C.Region)
}

type runService struct {
	Name        string            `json:"name,omitempty"`
	URI         string            `json:"uri,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
	Template    struct {
		ServiceAccount string `json:"serviceAccount,omitempty"`
		Scaling        struct {
			MinInstanceCount int `json:"minInstanceCount"`
			MaxInstanceCount int `json:"maxInstanceCount"`
		} `json:"scaling"`
		MaxInstanceRequestConcurrency int `json:"maxInstanceRequestConcurrency,omitempty"`
		Containers                    []struct {
			Image string `json:"image"`
			Env   []struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			} `json:"env,omitempty"`
			Resources struct {
				Limits map[string]string `json:"limits,omitempty"`
			} `json:"resources"`
		} `json:"containers"`
	} `json:"template"`
}

func (r *RunServices) List(ctx context.Context) ([]provision.Observed, error) {
	url := fmt.Sprintf("https://run.googleapis.com/v2/%s/services?pageSize=1000", r.parent())
	var out struct {
		Services []runService `json:"services"`
	}
	if err := r.C.do(ctx, http.MethodGet, url, nil, &out); err != nil {
		return nil, Classify(err, r.C.Project, "list run services", "roles/run.viewer")
	}
	var observed []provision.Observed
	for _, svc := range out.Services {
		if !hasLabels(svc.Labels, r.Labels) {
			continue
		}
		observed = append(observed, provision.Observed{
			Kind:     provision.KindRunService,
			Name:     lastSegment(svc.Name),
			SpecHash: svc.Annotations["tako-spec-hash"],
			LiveHash: liveHash(svc),
		})
	}
	return observed, nil
}

// liveHash re-derives the intended-spec map from the live service so a
// console edit of any tako-owned field hashes differently. Values GCP
// normalizes are un-normalized (cpu "1000m" → "1"). Ingress is assumed
// public (the C2 constant) — IAM drift detection joins with the derived
// grants in C3.
func liveHash(svc runService) string {
	if len(svc.Template.Containers) != 1 {
		return "live-shape-unexpected"
	}
	c := svc.Template.Containers[0]
	env := map[string]any{}
	for _, e := range c.Env {
		env[e.Name] = e.Value
	}
	cpu := strings.TrimSuffix(c.Resources.Limits["cpu"], "000m")
	return provision.SpecHash(map[string]any{
		"image":          c.Image,
		"serviceAccount": svc.Template.ServiceAccount,
		"env":            env,
		"scaling": map[string]any{
			"min":         svc.Template.Scaling.MinInstanceCount,
			"max":         svc.Template.Scaling.MaxInstanceCount,
			"concurrency": svc.Template.MaxInstanceRequestConcurrency,
			"cpu":         cpu,
			"memory":      c.Resources.Limits["memory"],
		},
		"public": true,
	})
}

func (r *RunServices) body(o provision.Object) map[string]any {
	spec := o.Spec
	scaling := spec["scaling"].(map[string]any)
	env := []map[string]any{}
	for name, value := range spec["env"].(map[string]any) {
		env = append(env, map[string]any{"name": name, "value": value})
	}
	annotations := map[string]string{"tako-spec-hash": provision.SpecHash(o.Spec)}
	for k, v := range r.Annotations {
		annotations[k] = v
	}
	return map[string]any{
		"labels":      r.Labels,
		"annotations": annotations,
		"template": map[string]any{
			"serviceAccount": spec["serviceAccount"],
			"scaling": map[string]any{
				"minInstanceCount": scaling["min"],
				"maxInstanceCount": scaling["max"],
			},
			"maxInstanceRequestConcurrency": scaling["concurrency"],
			"containers": []map[string]any{{
				"image": spec["image"],
				"env":   env,
				"resources": map[string]any{
					"limits": map[string]string{
						"cpu":    fmt.Sprint(scaling["cpu"]),
						"memory": fmt.Sprint(scaling["memory"]),
					},
				},
			}},
		},
	}
}

func (r *RunServices) Create(ctx context.Context, o provision.Object) error {
	url := fmt.Sprintf("https://run.googleapis.com/v2/%s/services?serviceId=%s", r.parent(), o.Name)
	var op struct {
		Name string `json:"name"`
	}
	if err := r.C.do(ctx, http.MethodPost, url, r.body(o), &op); err != nil {
		return Classify(err, r.C.Project, "create the run service", "roles/run.admin")
	}
	if err := r.C.waitOperation(ctx, "https://run.googleapis.com/v2/"+op.Name, waitLong); err != nil {
		return fmt.Errorf("run service %s did not become ready: %w", o.Name, err)
	}
	return r.allowPublic(ctx, o)
}

func (r *RunServices) Update(ctx context.Context, o provision.Object) error {
	url := fmt.Sprintf("https://run.googleapis.com/v2/%s/services/%s", r.parent(), o.Name)
	var op struct {
		Name string `json:"name"`
	}
	if err := r.C.do(ctx, http.MethodPatch, url, r.body(o), &op); err != nil {
		return Classify(err, r.C.Project, "update the run service", "roles/run.admin")
	}
	if err := r.C.waitOperation(ctx, "https://run.googleapis.com/v2/"+op.Name, waitLong); err != nil {
		return fmt.Errorf("run service %s did not become ready: %w", o.Name, err)
	}
	return r.allowPublic(ctx, o)
}

// allowPublic grants unauthenticated invocation — C2's ingress stance
// (WithPrivate and auth arrive with their features).
func (r *RunServices) allowPublic(ctx context.Context, o provision.Object) error {
	if public, _ := o.Spec["public"].(bool); !public {
		return nil
	}
	url := fmt.Sprintf("https://run.googleapis.com/v2/%s/services/%s:setIamPolicy", r.parent(), o.Name)
	body := map[string]any{"policy": map[string]any{
		"bindings": []map[string]any{{"role": "roles/run.invoker", "members": []string{"allUsers"}}},
	}}
	if err := r.C.do(ctx, http.MethodPost, url, body, nil); err != nil {
		return Classify(err, r.C.Project, "allow public access to the run service", "roles/run.admin")
	}
	return nil
}

// URL fetches the service's serving URL.
func (r *RunServices) URL(ctx context.Context, name string) (string, error) {
	var svc runService
	url := fmt.Sprintf("https://run.googleapis.com/v2/%s/services/%s", r.parent(), name)
	if err := r.C.do(ctx, http.MethodGet, url, nil, &svc); err != nil {
		return "", Classify(err, r.C.Project, "read the run service", "roles/run.viewer")
	}
	return svc.URI, nil
}

func (r *RunServices) Delete(ctx context.Context, name string) error {
	url := fmt.Sprintf("https://run.googleapis.com/v2/%s/services/%s", r.parent(), name)
	var op struct {
		Name string `json:"name"`
	}
	if err := r.C.do(ctx, http.MethodDelete, url, nil, &op); err != nil {
		return Classify(err, r.C.Project, "delete the run service", "roles/run.admin")
	}
	return r.C.waitOperation(ctx, "https://run.googleapis.com/v2/"+op.Name, waitLong)
}
