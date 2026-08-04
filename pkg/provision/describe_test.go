package provision

import (
	"strings"
	"testing"

	"github.com/gsandoval/tako/pkg/analyzer"
)

func describeGraph() *analyzer.AppGraph {
	g := alertGraph()
	g.Resources = []analyzer.Resource{
		{Kind: "alert", Name: "Slowing", Package: "gallery", Channels: []string{"gallery.oncall"},
			Alert: &analyzer.AlertCondition{Op: "above", Reading: "latency", Ctor: "RequestsSlow",
				SubjectKind: "route", SubjectName: "GET /api/photos", SubjectVar: "gallery.List",
				Threshold: 2000, Quantile: 0.99, ForSeconds: 300}},
		{Kind: "alert", Name: "Gone", Package: "gallery", Channels: []string{"gallery.oncall"},
			Alert: &analyzer.AlertCondition{Op: "absent", Reading: "cron_missed", Ctor: "CronMissed",
				SubjectKind: "cron", SubjectVar: "gallery.Cleanup"}},
	}
	return g
}

// DescribeAlerts must not need a project, a region or credentials. An
// alert is a property of the code; only the env-prefixed names inside it
// depend on where it would deploy. Demanding a gcloud config to read
// your own source would be friction with nothing behind it.
func TestDescribeNeedsNoCloudTarget(t *testing.T) {
	got, err := DescribeAlerts(Config{App: "gallery", Env: "prod"}, describeGraph())
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("described %d alerts, want 2", len(got))
	}
	for _, d := range got {
		if d.Query == "" {
			t.Errorf("%s has no query", d.Name)
		}
	}
}

// The constructor is what makes a preset forkable: you cannot rewrite by
// hand what you cannot identify. It comes from the analyzer rather than
// being reconstructed from the reading, so a preset and its label cannot
// drift apart.
func TestDescribeNamesTheConstructorThatProducedIt(t *testing.T) {
	got, err := DescribeAlerts(Config{App: "gallery", Env: "prod"}, describeGraph())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"Slowing": "RequestsSlow", "Gone": "CronMissed"}
	for _, d := range got {
		if d.Ctor != want[d.Name] {
			t.Errorf("%s came from %q, want %q", d.Name, d.Ctor, want[d.Name])
		}
		if !strings.Contains(d.Render(), "tako."+want[d.Name]) {
			t.Errorf("%s does not print the call it came from:\n%s", d.Name, d.Render())
		}
	}
}

// Alerts sort by name so two runs of the command produce the same
// output. Map iteration order in the middle of a diff is its own bug
// report.
func TestDescribeIsOrdered(t *testing.T) {
	got, err := DescribeAlerts(Config{App: "gallery", Env: "prod"}, describeGraph())
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Name != "Gone" || got[1].Name != "Slowing" {
		t.Errorf("order = %s, %s — want alphabetical", got[0].Name, got[1].Name)
	}
}

// The env is what every physical name in a query is built from, so the
// same alert must render differently under a different env — and the
// command prints which one it used for exactly this reason.
func TestDescribeRendersTheEnvItWasAskedFor(t *testing.T) {
	g := describeGraph()
	prod, err := DescribeAlerts(Config{App: "gallery", Env: "prod"}, g)
	if err != nil {
		t.Fatal(err)
	}
	pr42, err := DescribeAlerts(Config{App: "gallery", Env: "pr-42"}, g)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prod[0].Query, `namespace="gallery-prod"`) {
		t.Errorf("prod query not scoped to prod: %s", prod[0].Query)
	}
	if !strings.Contains(pr42[0].Query, `namespace="gallery-pr-42"`) {
		t.Errorf("preview query not scoped to the preview env: %s", pr42[0].Query)
	}
}

// A long absence cannot carry a retest window — Google refuses one past
// 1d1h, so tako sends duration 0. Printing that is the difference
// between a user understanding why their For did not survive the deploy
// and filing it as a bug.
func TestDescribeSaysWhenARetestWindowIsRefused(t *testing.T) {
	g := describeGraph()
	g.Resources = []analyzer.Resource{{Kind: "alert", Name: "Silent", Package: "gallery",
		Alert: &analyzer.AlertCondition{Op: "absent", Reading: "count", Ctor: "WhenAbsent",
			SubjectKind: "counter", SubjectName: "uploads", ForSeconds: 26 * 3600}}}
	got, err := DescribeAlerts(Config{App: "gallery", Env: "prod"}, g)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Retest {
		t.Fatal("a 26h lookback must forbid a retest window")
	}
	if !strings.Contains(got[0].Render(), "no retest window") {
		t.Errorf("the refusal is not shown:\n%s", got[0].Render())
	}
}

// The query is printed whole. It exists to be pasted into the metrics
// explorer and run, and a wrapped PromQL expression is one the reader
// has to repair first.
func TestRenderedQueryIsOneUnwrappedLine(t *testing.T) {
	got, err := DescribeAlerts(Config{App: "gallery", Env: "prod"}, describeGraph())
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range got {
		for _, line := range strings.Split(d.Render(), "\n") {
			if strings.HasPrefix(line, "  query  ") && !strings.Contains(line, d.Query) {
				t.Errorf("%s: the query line does not carry the whole query", d.Name)
			}
		}
	}
}
