package gcp

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

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
	// every service tako writes — service-level AND template-level, so
	// each revision permanently names the git SHA it was built from
	// (what rollback prints).
	Annotations map[string]string
	// Canary, when non-zero, holds updates back: public services split
	// Canary% to the new revision and the rest to the one serving now;
	// async services stage the new revision at 0% (spec v0: push
	// traffic doesn't canary) — tako promote completes either.
	Canary int
}

func (r *RunServices) Kind() provision.Kind { return provision.KindRunService }

func (r *RunServices) parent() string {
	return fmt.Sprintf("projects/%s/locations/%s", r.C.Project, r.C.Region)
}

type runVolume struct {
	Name string `json:"name"`
	GCS  *struct {
		Bucket   string `json:"bucket"`
		ReadOnly bool   `json:"readOnly,omitempty"`
	} `json:"gcs,omitempty"`
	EmptyDir *struct {
		Medium    string `json:"medium,omitempty"`
		SizeLimit string `json:"sizeLimit,omitempty"`
	} `json:"emptyDir,omitempty"`
}

type runVolumeMount struct {
	Name      string `json:"name"`
	MountPath string `json:"mountPath"`
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
		MaxInstanceRequestConcurrency int         `json:"maxInstanceRequestConcurrency,omitempty"`
		// Omitted-when-false, like cpuIdle and startupCpuBoost — the third
		// field on this service with that shape, and the third chance to
		// report drift against ourselves if liveHash forgets it.
		SessionAffinity bool        `json:"sessionAffinity,omitempty"`
		Timeout         string      `json:"timeout,omitempty"`
		Volumes         []runVolume `json:"volumes,omitempty"`
		Containers      []struct {
			Image string `json:"image"`
			Env   []struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			} `json:"env,omitempty"`
			Ports []struct {
				Name          string `json:"name,omitempty"`
				ContainerPort int    `json:"containerPort,omitempty"`
			} `json:"ports,omitempty"`
			VolumeMounts []runVolumeMount `json:"volumeMounts,omitempty"`
			Resources    struct {
				Limits  map[string]string `json:"limits,omitempty"`
				CPUIdle bool              `json:"cpuIdle,omitempty"`
				// Like cpuIdle, this reads back only when true — the
				// omitempty is the API's shape, not laziness, and the
				// LiveHash below must mirror it exactly or every deploy
				// reports drift against itself.
				StartupCPUBoost bool `json:"startupCpuBoost,omitempty"`
			} `json:"resources"`
		} `json:"containers"`
	} `json:"template"`
}

func (r *RunServices) List(ctx context.Context) ([]provision.Observed, error) {
	url := r.C.url(runHost, "/v2/%s/services?pageSize=1000", r.parent())
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
			LiveHash: liveHash(svc, r.isPublic(ctx, lastSegment(svc.Name))),
		})
	}
	return observed, nil
}

// The ingress stance, spelled once: public IS the allUsers binding on
// the invoker role. Three places read that fact — drift detection,
// the converge below, and destroy — and a fourth spelling of either
// string is a bug nobody sees until a service is open that should not
// be.
const (
	invokerRole = "roles/run.invoker"
	allUsers    = "allUsers"
)

// isPublic reads the live ingress stance, so drift detection reads the
// policy rather than assuming (a service flipped public/private in the
// console must hash differently).
func (r *RunServices) isPublic(ctx context.Context, name string) bool {
	var p policy
	url := r.C.url(runHost, "/v2/%s/services/%s:getIamPolicy", r.parent(), name)
	if err := r.C.do(ctx, http.MethodGet, url, nil, &p); err != nil {
		return false
	}
	for _, b := range p.Bindings {
		if b.Role == invokerRole && slices.Contains(b.Members, allUsers) {
			return true
		}
	}
	return false
}

// secondsOf parses Cloud Run's duration form ("300s") back to an int,
// the shape the desired spec carries.
func secondsOf(d string) int {
	n, err := strconv.Atoi(strings.TrimSuffix(d, "s"))
	if err != nil {
		return 0
	}
	return n
}

// defaultPort is the port every tako service listens on: pkg/runtime's
// Serve falls back to 8080 when PORT is unset, and Cloud Run sets PORT
// from this number. The two must not drift apart.
const defaultPort = 8080

// portName maps the knob to Cloud Run's two allowed port names. Named
// rather than inlined because liveHash has to reverse it, and the pair
// drifting apart is exactly the bug that reports drift against itself.
func portName(http2 bool) string {
	if http2 {
		return "h2c"
	}
	return "http1"
}

func reconstructMounts(volumes []runVolume, vmounts []runVolumeMount) []any {
	if len(volumes) == 0 {
		return nil
	}
	mountPaths := map[string]string{}
	for _, vm := range vmounts {
		mountPaths[vm.Name] = vm.MountPath
	}
	var out []any
	for _, v := range volumes {
		if v.EmptyDir != nil {
			out = append(out, map[string]any{
				"name":      v.Name,
				"mountPath": mountPaths[v.Name],
				"ephemeral": true,
				"sizeLimit": v.EmptyDir.SizeLimit,
			})
		} else if v.GCS != nil {
			out = append(out, map[string]any{
				"name":      v.Name,
				"mountPath": mountPaths[v.Name],
				"ephemeral": false,
				"bucket":    v.GCS.Bucket,
				"readOnly":  v.GCS.ReadOnly,
			})
		}
	}
	slices.SortFunc(out, func(a, b any) int {
		na, _ := a.(map[string]any)["name"].(string)
		nb, _ := b.(map[string]any)["name"].(string)
		return strings.Compare(na, nb)
	})
	return out
}

func buildVolumesAndMounts(spec map[string]any) ([]map[string]any, []map[string]any) {
	raw, ok := spec["mounts"].([]any)
	if !ok || len(raw) == 0 {
		return nil, nil
	}
	var volumes []map[string]any
	var vmounts []map[string]any
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name, _ := m["name"].(string)
		mountPath, _ := m["mountPath"].(string)
		ephemeral, _ := m["ephemeral"].(bool)
		if ephemeral {
			sizeLimit, _ := m["sizeLimit"].(string)
			volumes = append(volumes, map[string]any{
				"name": name,
				"emptyDir": map[string]any{
					"medium":    "MEMORY",
					"sizeLimit": sizeLimit,
				},
			})
		} else {
			bucket, _ := m["bucket"].(string)
			readOnly, _ := m["readOnly"].(bool)
			volumes = append(volumes, map[string]any{
				"name": name,
				"gcs": map[string]any{
					"bucket":   bucket,
					"readOnly": readOnly,
				},
			})
		}
		vmounts = append(vmounts, map[string]any{
			"name":      name,
			"mountPath": mountPath,
		})
	}
	return volumes, vmounts
}

// liveHash re-derives the intended-spec map from the live service so a
// console edit of any tako-owned field hashes differently. Values GCP
// normalizes are un-normalized (cpu "1000m" → "1").
func liveHash(svc runService, public bool) string {
	if len(svc.Template.Containers) != 1 {
		return "live-shape-unexpected"
	}
	c := svc.Template.Containers[0]
	env := map[string]any{}
	for _, e := range c.Env {
		env[e.Name] = e.Value
	}
	cpu := strings.TrimSuffix(c.Resources.Limits["cpu"], "000m")
	spec := map[string]any{
		"image":          c.Image,
		"serviceAccount": svc.Template.ServiceAccount,
		"env":            env,
		"scaling": map[string]any{
			"min":         svc.Template.Scaling.MinInstanceCount,
			"max":         svc.Template.Scaling.MaxInstanceCount,
			"concurrency": svc.Template.MaxInstanceRequestConcurrency,
			"cpu":         cpu,
			"memory":      c.Resources.Limits["memory"],
			// cpuIdle is the API's inverse of the knob tako names.
			"cpuAlwaysOn":  !c.Resources.CPUIdle,
			"startupBoost": c.Resources.StartupCPUBoost,
			"affinity":     svc.Template.SessionAffinity,
			"http2":        len(c.Ports) > 0 && c.Ports[0].Name == "h2c",
			"timeout":      secondsOf(svc.Template.Timeout),
		},
		"public": public,
	}
	if mounts := reconstructMounts(svc.Template.Volumes, c.VolumeMounts); len(mounts) > 0 {
		spec["mounts"] = mounts
	}
	return provision.SpecHash(spec)
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
	volumes, vmounts := buildVolumesAndMounts(spec)
	container := map[string]any{
		"image": spec["image"],
		"env":   env,
		// The port NAME is how Cloud Run's v2 API expresses the
		// protocol for the last hop: "h2c" keeps HTTP/2 all the way
		// to the instance, "http1" lets the edge downgrade. TLS is
		// terminated at the frontend either way — a container never
		// sees it — so h2c is the only shape HTTP/2 can take here.
		//
		// containerPort is NOT optional alongside it. Naming a port
		// without a number fails the create outright ("must be
		// 0 < port_number < 65536" — the zero value is not a
		// default, it is a violation). It is also what Cloud Run
		// injects as PORT, so it has to match what Serve listens
		// on when PORT is unset.
		"ports": []map[string]any{{
			"name":          portName(scaling["http2"] == true),
			"containerPort": defaultPort,
		}},
		"resources": map[string]any{
			"limits": map[string]string{
				"cpu":    fmt.Sprint(scaling["cpu"]),
				"memory": fmt.Sprint(scaling["memory"]),
			},
			// Cloud Run throttles CPU between requests unless told
			// otherwise; Latency is the profile that says otherwise.
			"cpuIdle": scaling["cpuAlwaysOn"] != true,
			// Extra CPU until the instance is serving. A warm floor
			// cannot prevent every cold start, so this is the other
			// half of what Latency promises.
			"startupCpuBoost": scaling["startupBoost"] == true,
		},
	}
	if len(vmounts) > 0 {
		container["volumeMounts"] = vmounts
	}
	tmpl := map[string]any{
		"annotations":    r.Annotations,
		"serviceAccount": spec["serviceAccount"],
		"scaling": map[string]any{
			"minInstanceCount": scaling["min"],
			"maxInstanceCount": scaling["max"],
		},
		"maxInstanceRequestConcurrency": scaling["concurrency"],
		// Best-effort stickiness, and a template field rather than a
		// container one — Cloud Run routes, the container never knows.
		"sessionAffinity": scaling["affinity"] == true,
		"timeout":         fmt.Sprintf("%ds", scaling["timeout"]),
		"containers":      []map[string]any{container},
	}
	if len(volumes) > 0 {
		tmpl["volumes"] = volumes
	}
	return map[string]any{
		"labels":      r.Labels,
		"annotations": annotations,
		"template":    tmpl,
	}
}

func (r *RunServices) Create(ctx context.Context, o provision.Object) error {
	url := r.C.url(runHost, "/v2/%s/services?serviceId=%s", r.parent(), o.Name)
	var op struct {
		Name string `json:"name"`
	}
	if err := r.C.do(ctx, http.MethodPost, url, r.body(o), &op); err != nil {
		return Classify(err, r.C.Project, "create the run service", "roles/run.admin")
	}
	err := r.C.waitOperation(ctx, r.C.url(runHost, "/v2/"+op.Name), waitLong)
	// A repo created moments ago is eventually consistent: the first
	// pull can answer "Image … not found" for an image the push just
	// confirmed (hit live in the W11 walk). The service now exists
	// with a failed revision, so the retry is an update — same spec,
	// fresh revision — paced like the C4 SA-429 fix.
	for attempt := 0; attempt < 3 && err != nil && strings.Contains(err.Error(), "not found"); attempt++ {
		time.Sleep(15 * time.Second)
		err = r.Update(ctx, o)
	}
	if err != nil {
		return fmt.Errorf("run service %s did not become ready: %w", o.Name, err)
	}
	return r.allowPublic(ctx, o)
}

func (r *RunServices) Update(ctx context.Context, o provision.Object) error {
	url := r.C.url(runHost, "/v2/%s/services/%s", r.parent(), o.Name)
	body := r.body(o)
	// Traffic is explicit on every update: the default rollout is 100%
	// to latest (which also means a plain deploy supersedes an
	// in-progress canary — new rollout, new default). Under --canary
	// the new revision is held back instead: public services split,
	// async services stage at 0% until tako promote.
	public, _ := o.Spec["public"].(bool)
	traffic := []TrafficTarget{{Latest: true, Percent: 100}}
	if r.Canary > 0 {
		prev, err := r.serving(ctx, o.Name)
		if err != nil {
			return err
		}
		switch {
		case prev == "":
			// Never served (first rollout): nothing to hold back.
		case public:
			traffic = []TrafficTarget{
				{Revision: prev, Percent: 100 - r.Canary},
				{Latest: true, Percent: r.Canary},
			}
		default:
			traffic = []TrafficTarget{{Revision: prev, Percent: 100}}
		}
	}
	body["traffic"] = trafficBody(traffic)
	var op struct {
		Name string `json:"name"`
	}
	if err := r.C.do(ctx, http.MethodPatch, url, body, &op); err != nil {
		return Classify(err, r.C.Project, "update the run service", "roles/run.admin")
	}
	if err := r.C.waitOperation(ctx, r.C.url(runHost, "/v2/"+op.Name), waitLong); err != nil {
		return fmt.Errorf("run service %s did not become ready: %w", o.Name, err)
	}
	return r.allowPublic(ctx, o)
}

// allowPublic converges the ingress stance — C2's public/private —
// onto the service's IAM policy.
//
// It reconciles BOTH ways, which is the whole of CR2. It used to only
// add: declaring a deployed public route Private left allUsers bound, so
// the door the developer just closed stayed open, and because isPublic
// feeds liveHash the plan reported that service as changed on every
// deploy from then on without ever being able to fix it.
//
// And it is a read-modify-write now. The old write was a setIamPolicy
// carrying one binding, and setIamPolicy REPLACES the policy: everything
// else on a public service — the invoker grant a calling service holds,
// a teammate's run.viewer, another tool's binding — was destroyed on
// every deploy. grants.go states that discipline as this package's rule
// and honours it carefully; this was the one place that did not. The
// etag rides along for the same reason it does there: a policy changed
// between the read and the write is refused rather than clobbered.
//
// A policy that already says the right thing is not rewritten at all.
func (r *RunServices) allowPublic(ctx context.Context, o provision.Object) error {
	public, _ := o.Spec["public"].(bool)
	get := r.C.url(runHost, "/v2/%s/services/%s:getIamPolicy", r.parent(), o.Name)
	var live policy
	if err := r.C.do(ctx, http.MethodGet, get, nil, &live); err != nil {
		return Classify(err, r.C.Project, "read the run service's IAM policy", "roles/run.admin")
	}
	next, changed := withPublicInvoker(live, public)
	if !changed {
		return nil
	}
	set := r.C.url(runHost, "/v2/%s/services/%s:setIamPolicy", r.parent(), o.Name)
	if err := r.C.do(ctx, http.MethodPost, set, map[string]any{"policy": next}, nil); err != nil {
		what := "allow public access to the run service"
		if !public {
			what = "close public access to the run service"
		}
		return Classify(err, r.C.Project, what, "roles/run.admin")
	}
	return nil
}

// withPublicInvoker adds or removes allUsers from roles/run.invoker,
// leaving every other member and every other role exactly as found.
// Reports whether anything actually differs.
func withPublicInvoker(live policy, public bool) (map[string]any, bool) {
	changed := false
	bindings := make([]map[string]any, 0, len(live.Bindings)+1)
	found := false
	for _, b := range live.Bindings {
		if b.Role != invokerRole {
			bindings = append(bindings, map[string]any{"role": b.Role, "members": b.Members})
			continue
		}
		found = true
		members := make([]string, 0, len(b.Members)+1)
		held := false
		for _, m := range b.Members {
			if m == allUsers {
				held = true
				if !public {
					changed = true // dropped
					continue
				}
			}
			members = append(members, m)
		}
		if public && !held {
			members = append(members, allUsers)
			changed = true
		}
		// A binding with no members left is not a binding Google keeps.
		if len(members) > 0 {
			bindings = append(bindings, map[string]any{"role": b.Role, "members": members})
		}
	}
	if public && !found {
		bindings = append(bindings, map[string]any{"role": invokerRole, "members": []string{allUsers}})
		changed = true
	}
	out := map[string]any{"bindings": bindings}
	if live.Etag != "" {
		out["etag"] = live.Etag
	}
	return out, changed
}

// URL fetches the service's serving URL.
func (r *RunServices) URL(ctx context.Context, name string) (string, error) {
	var svc runService
	url := r.C.url(runHost, "/v2/%s/services/%s", r.parent(), name)
	if err := r.C.do(ctx, http.MethodGet, url, nil, &svc); err != nil {
		return "", Classify(err, r.C.Project, "read the run service", "roles/run.viewer")
	}
	return svc.URI, nil
}

func (r *RunServices) Delete(ctx context.Context, name string) error {
	url := r.C.url(runHost, "/v2/%s/services/%s", r.parent(), name)
	var op struct {
		Name string `json:"name"`
	}
	if err := r.C.do(ctx, http.MethodDelete, url, nil, &op); err != nil {
		return Classify(err, r.C.Project, "delete the run service", "roles/run.admin")
	}
	return r.C.waitOperation(ctx, r.C.url(runHost, "/v2/"+op.Name), waitLong)
}
