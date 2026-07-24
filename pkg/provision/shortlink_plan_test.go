package provision

import (
	"testing"

	"github.com/gsandoval/tako/pkg/analyzer"
)

// shortlinkGraph mirrors examples/shortlink's AppGraph: two services
// (api + worker), two entities (one with a composite index), a queue,
// and the usage edges the analyzer derives.
func shortlinkGraph() *analyzer.AppGraph {
	return &analyzer.AppGraph{
		Module: "shortlink",
		Services: []analyzer.Service{
			{Name: "shortlink", Package: "shortlink", Profile: "default",
				Handlers: []string{"shortlink.Create", "shortlink.Resolve", "shortlink.List"}},
			{Name: "shortlink-countclick", Package: "shortlink", Profile: "default",
				Handlers: []string{"shortlink.CountClick"}},
		},
		Resources: []analyzer.Resource{
			{Kind: "entity", Name: "links", Type: "Link",
				Indexes: []string{"Owner", "Created"}, Composite: []string{"owner", "-created"}},
			{Kind: "queue", Name: "clicks", Type: "Click"},
			{Kind: "entity", Name: "clicks-seen", Type: "Click"},
		},
		Handlers: []analyzer.Handler{
			{Kind: "api", Var: "shortlink.Create", Service: "shortlink"},
			{Kind: "api", Var: "shortlink.Resolve", Service: "shortlink"},
			{Kind: "api", Var: "shortlink.List", Service: "shortlink"},
			{Kind: "worker", Var: "shortlink.CountClick", Service: "shortlink-countclick", Binding: "clicks"},
		},
		Edges: []analyzer.Edge{
			{From: "shortlink.Create", To: "links", ToKind: "entity", Kind: "resource", Verb: "write"},
			{From: "shortlink.Resolve", To: "clicks", ToKind: "queue", Kind: "resource", Verb: "publish"},
			{From: "shortlink.Resolve", To: "links", ToKind: "entity", Kind: "resource", Verb: "read"},
			{From: "shortlink.List", To: "links", ToKind: "entity", Kind: "resource", Verb: "read"},
			{From: "shortlink.CountClick", To: "clicks-seen", ToKind: "entity", Kind: "resource", Verb: "write"},
			{From: "shortlink.CountClick", To: "links", ToKind: "entity", Kind: "resource", Verb: "write"},
		},
	}
}

// TestShortlinkPlanRender goldens the whole C3 surface in one plan:
// SAs, composite index, topic, run services (worker private), push
// subscription, and per-target grants.
func TestShortlinkPlanRender(t *testing.T) {
	cfg := Config{Project: "my-proj", ProjectNumber: "123456", Region: "us-central1", App: "shortlink", Env: DefaultEnv}
	desired, err := Desired(shortlinkGraph(), cfg, map[string]string{
		"shortlink":            "img@sha256:aaa",
		"shortlink-countclick": "img@sha256:bbb",
	})
	if err != nil {
		t.Fatal(err)
	}
	plan := Compute("shortlink", "prod", desired, nil)
	want := `  create  artifact repo tako
  create  service account shortlink-sa
  create  service account shortlink-countclick-sa
  create  datastore index links (owner, -created)
  create  topic clicks
  create  run service shortlink
  create  run service shortlink-countclick
  create  iam grants project
  create  iam grants run shortlink
  create  iam grants run shortlink-countclick
  create  iam grants serviceAccount shortlink-countclick-sa@my-proj.iam.gserviceaccount.com
  create  iam grants serviceAccount shortlink-sa@my-proj.iam.gserviceaccount.com
  create  iam grants topic clicks
  create  push subscription shortlink-countclick
  14 creates, 0 updates, 0 skips, 0 orphans
`
	if got := plan.Render(); got != want {
		t.Fatalf("plan:\n%s\nwant:\n%s", got, want)
	}
}

// TestShortlinkGrantContents pins the binding sets grant objects carry.
func TestShortlinkGrantContents(t *testing.T) {
	cfg := Config{Project: "my-proj", ProjectNumber: "123456", Region: "us-central1", App: "shortlink", Env: DefaultEnv}
	desired, err := Desired(shortlinkGraph(), cfg, map[string]string{
		"shortlink":            "img@sha256:aaa",
		"shortlink-countclick": "img@sha256:bbb",
	})
	if err != nil {
		t.Fatal(err)
	}
	grants := map[string][]string{}
	var subSpec map[string]any
	for _, o := range desired {
		switch o.Kind {
		case KindIamGrants:
			for _, b := range o.Spec["bindings"].([]any) {
				m := b.(map[string]any)
				grants[o.Name] = append(grants[o.Name], m["role"].(string)+" → "+m["member"].(string))
			}
		case KindPushSubscription:
			subSpec = o.Spec
		}
	}
	apiSA := "serviceAccount:shortlink-sa@my-proj.iam.gserviceaccount.com"
	workerSA := "serviceAccount:shortlink-countclick-sa@my-proj.iam.gserviceaccount.com"
	checks := map[string][]string{
		"project":                        {"roles/datastore.user → " + workerSA, "roles/datastore.user → " + apiSA},
		"topic clicks":                   {"roles/pubsub.publisher → " + apiSA},
		"run service grants for worker":  nil, // checked below by name
		"run shortlink":                  {},  // public API service: no tako-member bindings
		"run shortlink-countclick":       {"roles/run.invoker → " + workerSA},
		"serviceAccount " + workerSA[15:]: {"roles/iam.serviceAccountTokenCreator → serviceAccount:service-123456@gcp-sa-pubsub.iam.gserviceaccount.com"},
	}
	delete(checks, "run service grants for worker")
	for name, want := range checks {
		got := grants[name]
		if len(got) != len(want) {
			t.Errorf("%s bindings = %v, want %v", name, got, want)
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%s binding[%d] = %q, want %q", name, i, got[i], want[i])
			}
		}
	}
	if subSpec["topic"] != "clicks" ||
		subSpec["endpoint"] != "https://shortlink-countclick-123456.us-central1.run.app/_tako/push" ||
		subSpec["serviceAccount"] != workerSA[15:] {
		t.Errorf("push subscription spec = %v", subSpec)
	}
	// The worker's Run service must be private; the API's public.
	for _, o := range desired {
		if o.Kind == KindRunService {
			public := o.Spec["public"].(bool)
			if o.Name == "shortlink" && !public {
				t.Error("api service must be public")
			}
			if o.Name == "shortlink-countclick" && public {
				t.Error("worker service must not be public")
			}
		}
	}
}
