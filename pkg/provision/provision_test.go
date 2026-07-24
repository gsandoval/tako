package provision

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/gsandoval/tako/pkg/analyzer"
)

// helloGraph is the miniature graph the scenarios reconcile.
func helloGraph() *analyzer.AppGraph {
	return &analyzer.AppGraph{
		Module:   "hello",
		Services: []analyzer.Service{{Name: "hello", Package: "hello", Profile: "default"}},
	}
}

func helloDesired(t *testing.T) []Object {
	t.Helper()
	cfg := Config{Project: "my-proj", Region: "us-central1", App: "hello", Env: DefaultEnv}
	desired, err := Desired(helloGraph(), cfg, map[string]string{
		"hello": "us-central1-docker.pkg.dev/my-proj/tako/hello@sha256:abc123",
	})
	if err != nil {
		t.Fatal(err)
	}
	return desired
}

// fakeCloud remembers applied objects and plays them back as observed —
// the reconcile loop closed without GCP.
type fakeCloud struct {
	live map[string]string // kind/name → spec hash
}

func newFakeCloud() *fakeCloud { return &fakeCloud{live: map[string]string{}} }

func (f *fakeCloud) Create(_ context.Context, o Object) error {
	f.live[string(o.Kind)+"/"+o.Name] = SpecHash(o.Spec)
	return nil
}
func (f *fakeCloud) Update(ctx context.Context, o Object) error { return f.Create(ctx, o) }

func (f *fakeCloud) observed() []Observed {
	var out []Observed
	for key, hash := range f.live {
		kind, name, _ := strings.Cut(key, "/")
		out = append(out, Observed{Kind: Kind(kind), Name: name, SpecHash: hash})
	}
	return out
}

func TestFreshProjectPlansAllCreates(t *testing.T) {
	desired := helloDesired(t)
	plan := Compute("hello", "prod", desired, nil)
	want := `  create  artifact repo tako
  create  service account hello-sa
  create  run service hello
  3 creates, 0 updates, 0 skips, 0 orphans
`
	if got := plan.Render(); got != want {
		t.Fatalf("plan:\n%s\nwant:\n%s", got, want)
	}
	if !plan.Changed() {
		t.Fatal("all-creates plan reports unchanged")
	}
}

func TestSecondDeployIsAllSkips(t *testing.T) {
	desired := helloDesired(t)
	cloud := newFakeCloud()
	if err := Apply(context.Background(), Compute("hello", "prod", desired, nil), desired, cloud); err != nil {
		t.Fatal(err)
	}
	plan := Compute("hello", "prod", desired, cloud.observed())
	c, u, s, o := plan.Counts()
	if c != 0 || u != 0 || s != 3 || o != 0 {
		t.Fatalf("second plan = %d/%d/%d/%d:\n%s", c, u, s, o, plan.Render())
	}
	if plan.Changed() {
		t.Fatal("no-op plan reports changed")
	}
}

func TestNewImageDigestPlansUpdate(t *testing.T) {
	desired := helloDesired(t)
	cloud := newFakeCloud()
	if err := Apply(context.Background(), Compute("hello", "prod", desired, nil), desired, cloud); err != nil {
		t.Fatal(err)
	}
	// A code change: the built digest moves, nothing else does.
	cfg := Config{Project: "my-proj", Region: "us-central1", App: "hello", Env: DefaultEnv}
	next, err := Desired(helloGraph(), cfg, map[string]string{
		"hello": "us-central1-docker.pkg.dev/my-proj/tako/hello@sha256:def456",
	})
	if err != nil {
		t.Fatal(err)
	}
	plan := Compute("hello", "prod", next, cloud.observed())
	c, u, s, _ := plan.Counts()
	if c != 0 || u != 1 || s != 2 {
		t.Fatalf("plan after digest change:\n%s", plan.Render())
	}
}

func TestHandEditRendersDrift(t *testing.T) {
	desired := helloDesired(t)
	cloud := newFakeCloud()
	if err := Apply(context.Background(), Compute("hello", "prod", desired, nil), desired, cloud); err != nil {
		t.Fatal(err)
	}
	observed := cloud.observed()
	for i := range observed {
		if observed[i].Kind == KindRunService {
			// The console edit: live fields no longer hash to the record.
			observed[i].LiveHash = "feedfacefeedface"
		}
	}
	plan := Compute("hello", "prod", desired, observed)
	if !strings.Contains(plan.Render(), "DRIFT: modified outside tako") {
		t.Fatalf("no drift warning:\n%s", plan.Render())
	}
}

func TestRemovedServiceIsOrphanedKept(t *testing.T) {
	desired := helloDesired(t)
	cloud := newFakeCloud()
	if err := Apply(context.Background(), Compute("hello", "prod", desired, nil), desired, cloud); err != nil {
		t.Fatal(err)
	}
	// The repo alone remains desired; service + SA disappeared from code.
	plan := Compute("hello", "prod", desired[:1], cloud.observed())
	got := plan.Render()
	if !strings.Contains(got, "orphaned (kept) service account hello-sa") ||
		!strings.Contains(got, "orphaned (kept) run service hello") {
		t.Fatalf("orphans missing:\n%s", got)
	}
	if plan.Changed() {
		t.Fatal("orphan-only plan must not report changes (nothing is deleted automatically)")
	}
}

func TestEnvPrefixesNamesAndSpecs(t *testing.T) {
	cfg := Config{Project: "my-proj", Region: "us-central1", App: "hello", Env: "pr-42"}
	desired, err := Desired(helloGraph(), cfg, map[string]string{"hello": "img@sha256:abc"})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, d := range desired {
		names = append(names, d.Name)
	}
	want := []string{"tako", "pr-42-hello-sa", "pr-42-hello"}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("names = %v, want %v", names, want)
		}
	}
}

func TestSANameSatisfiesIAMLimits(t *testing.T) {
	prod := Config{App: "hello", Env: DefaultEnv}
	if got := prod.SAName("hello"); got != "hello-sa" {
		t.Fatalf("SAName(hello) = %q, want hello-sa", got)
	}
	// A one-char service still clears IAM's 6-char minimum via the pad.
	if got := prod.SAName("a"); len(got) < 6 || !strings.HasPrefix(got, "a-sa-") {
		t.Fatalf("SAName(a) = %q, want hash-padded a-sa-*", got)
	}
	// The long case truncates deterministically and stays within 6–30.
	long := Config{App: "hello", Env: "pr-421-feature-branch"}
	a := long.SAName("checkout-orchestrator")
	b := long.SAName("checkout-orchestrator")
	if a != b {
		t.Fatalf("truncated SAName not stable: %q vs %q", a, b)
	}
	if len(a) < 6 || len(a) > 30 {
		t.Fatalf("SAName %q length %d outside IAM's 6–30", a, len(a))
	}
	if !strings.HasPrefix(a, "pr-421-") {
		t.Fatalf("SAName %q lost its env prefix", a)
	}
	// Distinct long names must not collide after truncation.
	if c := long.SAName("checkout-orchestrator2"); c == a {
		t.Fatalf("truncation collided: %q", c)
	}
	// The SA the run service references is the SA that gets created.
	cfg := Config{Project: "my-proj", Region: "us-central1", App: "hello", Env: DefaultEnv}
	desired, err := Desired(helloGraph(), cfg, map[string]string{"hello": "img@sha256:abc"})
	if err != nil {
		t.Fatal(err)
	}
	var saName, runRef string
	for _, d := range desired {
		switch d.Kind {
		case KindServiceAccount:
			saName = d.Name
		case KindRunService:
			runRef = fmt.Sprint(d.Spec["serviceAccount"])
		}
	}
	if runRef != saName+"@my-proj.iam.gserviceaccount.com" {
		t.Fatalf("run service references %q, SA created is %q", runRef, saName)
	}
}

func TestSpecHashIsCanonical(t *testing.T) {
	a := SpecHash(map[string]any{"b": 1, "a": map[string]any{"y": "z", "x": []any{1, 2}}})
	b := SpecHash(map[string]any{"a": map[string]any{"x": []any{1, 2}, "y": "z"}, "b": 1})
	if a != b {
		t.Fatalf("hash depends on key order: %s vs %s", a, b)
	}
	if strings.ContainsAny(a, ":/+= ") || len(a) != 16 {
		t.Fatalf("hash %q is not label-safe", a)
	}
}
