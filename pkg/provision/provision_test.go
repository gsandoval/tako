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
	want := `  + create  artifact repo tako
  + create  service account hello-sa
  + create  run service hello
  + create  iam grants bucket my-proj-tako
  + create  iam grants run hello
  + create  iam grants serviceAccount hello-sa@my-proj.iam.gserviceaccount.com
  6 creates, 0 updates, 0 skips, 0 orphans
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
	if c != 0 || u != 0 || s != 6 || o != 0 {
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
	if c != 0 || u != 1 || s != 5 {
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
	want := []string{
		"tako", "pr-42-hello-sa", "pr-42-hello",
		"bucket my-proj-tako",
		"run pr-42-hello", "serviceAccount pr-42-hello-sa@my-proj.iam.gserviceaccount.com",
	}
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
	// A one-char service still clears IAM's 6-char minimum via the pad,
	// and every variant keeps the -sa marker ManagedMember keys on.
	if got := prod.SAName("a"); len(got) < 6 || !strings.HasSuffix(got, "-sa") {
		t.Fatalf("SAName(a) = %q, want hash-padded *-sa", got)
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
	if !strings.HasPrefix(a, "pr-421-") || !strings.HasSuffix(a, "-sa") {
		t.Fatalf("SAName %q lost its env prefix or -sa marker", a)
	}
	if !ManagedMember("serviceAccount:"+a+"@my-proj.iam.gserviceaccount.com", "my-proj") {
		t.Fatalf("ManagedMember misses clamped SA %q", a)
	}
	if ManagedMember("serviceAccount:someone-else@my-proj.iam.gserviceaccount.com", "my-proj") ||
		ManagedMember("allUsers", "my-proj") ||
		ManagedMember("serviceAccount:hello-sa@other-proj.iam.gserviceaccount.com", "my-proj") {
		t.Fatal("ManagedMember matches a foreign member")
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

// TestReplaceClassAndRenameSuspects pins C6's plan intelligence: a push
// subscription whose live topic differs is a REPLACE with the
// consequence stated, and an orphan paired with a same-kind create is a
// rename suspect (data does not follow) — both trip NeedsConfirm.
func TestReplaceClassAndRenameSuspects(t *testing.T) {
	desired := []Object{
		{Kind: KindPushSubscription, Name: "app-worker", Spec: map[string]any{"topic": "jobs-v2"}},
		{Kind: KindBucket, Name: "p-app-pics", Spec: map[string]any{"location": "x"}},
	}
	observed := []Observed{
		{Kind: KindPushSubscription, Name: "app-worker",
			SpecHash: "stale", Detail: map[string]string{"topic": "jobs"}},
		{Kind: KindBucket, Name: "p-app-photos", SpecHash: "whatever"},
	}
	plan := Compute("app", "prod", desired, observed)
	if !plan.NeedsConfirm() {
		t.Fatal("replace + rename suspect must need confirmation")
	}
	got := plan.Render()
	for _, want := range []string{
		"! update  push subscription app-worker  ← REPLACE: topic is immutable (jobs → jobs-v2)",
		"in-flight messages on the old subscription are lost",
		"? orphaned (kept) bucket p-app-photos",
		"↳ rename suspect: bucket p-app-pics appeared — if this is a rename, data does not follow automatically",
		"a migrating tako rename is a planned feature",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("plan missing %q:\n%s", want, got)
		}
	}

	// An ordinary in-place update needs no ceremony.
	calm := Compute("app", "prod", desired[:1], []Observed{
		{Kind: KindPushSubscription, Name: "app-worker",
			SpecHash: "stale", Detail: map[string]string{"topic": "jobs-v2"}},
	})
	if calm.NeedsConfirm() {
		t.Fatalf("in-place update must not prompt:\n%s", calm.Render())
	}
}
