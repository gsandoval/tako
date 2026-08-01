package provision

import (
	"strings"
	"testing"

	"github.com/gsandoval/tako/pkg/analyzer"
)

// blogDomainGraph mirrors a site service that declared WithDomain: one
// public service, a site, and a worker carved out of the same package
// (so the plan proves the domain rides ONE service, not the package).
func blogDomainGraph() *analyzer.AppGraph {
	return &analyzer.AppGraph{
		Module: "blog",
		Services: []analyzer.Service{
			{Name: "blog", Package: "blog", Profile: "default", Domain: "blog.example.com",
				Handlers: []string{"blog.Home", "blog.AddComment"}},
			{Name: "blog-notify", Package: "blog", Profile: "default",
				Handlers: []string{"blog.Notify"}},
		},
		Resources: []analyzer.Resource{
			{Kind: "site", Name: "./site/public", Package: "blog", Domain: "blog.example.com"},
			{Kind: "queue", Name: "notices", Type: "Notice"},
		},
		Handlers: []analyzer.Handler{
			{Kind: "page", Var: "blog.Home", Service: "blog"},
			{Kind: "page", Var: "blog.AddComment", Service: "blog"},
			{Kind: "worker", Var: "blog.Notify", Service: "blog-notify", Binding: "notices"},
		},
	}
}

// The domain mapping plans LAST — after the run service it routes to —
// and exactly once, for the service that declared it.
func TestDomainMappingPlansAfterItsService(t *testing.T) {
	cfg := Config{Project: "my-proj", ProjectNumber: "123456", Region: "us-central1", App: "blog", Env: DefaultEnv}
	desired, err := Desired(blogDomainGraph(), cfg, map[string]string{
		"blog":        "img@sha256:aaa",
		"blog-notify": "img@sha256:bbb",
	})
	if err != nil {
		t.Fatal(err)
	}
	plan := Compute("blog", "prod", desired, nil)
	rendered := plan.Render()

	if n := strings.Count(rendered, "domain mapping"); n != 1 {
		t.Fatalf("want exactly one domain mapping line, got %d:\n%s", n, rendered)
	}
	if !strings.Contains(rendered, "+ create  domain mapping blog.example.com") {
		t.Errorf("plan does not name the domain:\n%s", rendered)
	}
	mapping := strings.Index(rendered, "domain mapping")
	service := strings.Index(rendered, "run service blog\n")
	if service < 0 || mapping < service {
		t.Errorf("domain mapping must plan after its run service:\n%s", rendered)
	}
	// Destroy reverses reconcile order, so the mapping comes off first —
	// before the service it points at stops existing.
	order := DeleteOrder()
	if order[0] != KindDomainMapping {
		t.Errorf("destroy should remove the domain mapping first, got %s", order[0])
	}
}

// Re-pointing a domain at another service is a REPLACE: it cannot be
// patched, and the plan must say the domain goes dark before it does.
func TestDomainMappingRepointIsAReplace(t *testing.T) {
	cfg := Config{Project: "my-proj", ProjectNumber: "123456", Region: "us-central1", App: "blog", Env: DefaultEnv}
	desired, err := Desired(blogDomainGraph(), cfg, map[string]string{
		"blog":        "img@sha256:aaa",
		"blog-notify": "img@sha256:bbb",
	})
	if err != nil {
		t.Fatal(err)
	}
	observed := []Observed{{
		Kind:     KindDomainMapping,
		Name:     "blog.example.com",
		SpecHash: "stale",
		LiveHash: "stale",
		Detail:   map[string]string{"routeName": "oldblog"},
	}}
	plan := Compute("blog", "prod", desired, observed)

	var entry *Entry
	for i := range plan.Entries {
		if plan.Entries[i].Kind == KindDomainMapping {
			entry = &plan.Entries[i]
		}
	}
	if entry == nil {
		t.Fatal("no domain mapping entry")
	}
	if entry.Class != ClassReplace {
		t.Errorf("class = %s, want %s", entry.Class, ClassReplace)
	}
	for _, want := range []string{"oldblog", "blog", "stops answering", "24 hours"} {
		if !strings.Contains(entry.Reason, want) {
			t.Errorf("replace reason missing %q: %s", want, entry.Reason)
		}
	}
	if !plan.NeedsConfirm() {
		t.Error("a replace must stop for confirmation before it runs")
	}
}

// A service with no WithDomain provisions no mapping — the option is the
// only thing that creates one.
func TestNoDomainNoMapping(t *testing.T) {
	g := blogDomainGraph()
	g.Services[0].Domain = ""
	g.Resources[0].Domain = ""
	cfg := Config{Project: "my-proj", ProjectNumber: "123456", Region: "us-central1", App: "blog", Env: DefaultEnv}
	desired, err := Desired(g, cfg, map[string]string{"blog": "img@sha256:aaa", "blog-notify": "img@sha256:bbb"})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range desired {
		if o.Kind == KindDomainMapping {
			t.Fatalf("unexpected domain mapping %s", o.Name)
		}
	}
}
