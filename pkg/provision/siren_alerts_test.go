package provision

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/gsandoval/tako/pkg/analyzer"
)

// The generated PromQL for every condition siren declares, goldened.
//
// The AppGraph golden proves each alert RESOLVED. This proves what it
// resolved to actually becomes — the layer where this feature has been
// wrong the most times: an empty route label, a job label spelled three
// ways, a subscription that named a topic, a threshold off by one. Every
// one of those was a correct-looking graph and a query that matched
// nothing.
//
// Read the golden as prose. A matcher naming something no deploy creates
// is visible here and nowhere else short of a live gate.
func TestSirenQueries(t *testing.T) {
	_, thisFile, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(thisFile), "..", "..")
	g, err := analyzer.Analyze(analyzer.Config{Dir: filepath.Join(root, "examples", "siren")})
	if err != nil {
		t.Fatalf("analyze siren: %v", err)
	}
	cfg := Config{Project: "p", ProjectNumber: "1", Region: "us-central1", App: "siren", Env: "x1"}

	// Through DescribeAlerts, which is what `tako alerts` prints. One
	// renderer, so the golden this repo reads and the command a user
	// reads can never disagree about what a condition compiles to.
	rows, err := DescribeAlerts(cfg, g)
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	if len(rows) < 20 {
		t.Fatalf("only %d alert queries — siren declares the whole catalogue", len(rows))
	}

	var b strings.Builder
	for _, r := range rows {
		fmt.Fprintf(&b, "%s\n  %s\n  %s\n\n", r.Name, r.Sentence, r.Query)
	}
	compareGoldenFile(t, filepath.Join("testdata", "siren_queries.golden.txt"), b.String())
}

// Every matcher that names a deployed object must name one the deploy
// creates. This is the job-label check and the subscription check
// generalised: extract every `<label>="<value>"` from every query and
// hold it against the plan.
func TestSirenMatchersNameDeployedObjects(t *testing.T) {
	_, thisFile, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(thisFile), "..", "..")
	g, err := analyzer.Analyze(analyzer.Config{Dir: filepath.Join(root, "examples", "siren")})
	if err != nil {
		t.Fatalf("analyze siren: %v", err)
	}
	cfg := Config{Project: "p", ProjectNumber: "1", Region: "us-central1", App: "siren", Env: "x1"}
	images := map[string]string{}
	for _, s := range g.Services {
		images[s.Name] = "img"
	}
	desired, err := Desired(g, cfg, images)
	if err != nil {
		t.Fatal(err)
	}
	subscriptions, services, jobs := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, o := range desired {
		switch o.Kind {
		case KindPushSubscription, KindPullSubscription:
			subscriptions[o.Name] = true
		case KindRunService:
			services[o.Name] = true
		case KindRunJob:
			jobs[o.Name] = true
		}
	}
	// The job label is the app's own, written by the runtime rather than
	// created by the deploy, so it is checked against TAKO_SERVICE.
	written := map[string]bool{}
	for _, o := range desired {
		if o.Kind != KindRunService {
			continue
		}
		if env, ok := o.Spec["env"].(map[string]any); ok {
			written[fmt.Sprint(env["TAKO_SERVICE"])] = true
		}
	}

	checks := map[string]map[string]bool{
		"subscription_id": subscriptions,
		"service_name":    services,
		"job_name":        jobs,
		"job":             written,
	}
	for _, r := range g.Resources {
		if r.Kind != "alert" {
			continue
		}
		q, _, err := alertQuery(cfg, g, r.Alert)
		if err != nil {
			continue
		}
		for label, known := range checks {
			for _, m := range matcherValues(q, label) {
				if !known[m] {
					t.Errorf("%s matches %s=%q, which no deploy creates — the query matches nothing, "+
						"and a condition that cannot fire looks exactly like a healthy one", r.Name, label, m)
				}
			}
		}
	}
}

func matcherValues(query, label string) []string {
	var out []string
	for _, part := range strings.Split(query, label+`="`)[1:] {
		if i := strings.Index(part, `"`); i >= 0 {
			out = append(out, part[:i])
		}
	}
	return out
}

// compareGoldenFile writes the golden under -update and diffs otherwise.
func compareGoldenFile(t *testing.T, path, got string) {
	t.Helper()
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if string(want) != got {
		t.Errorf("%s differs.\n--- want\n%s\n--- got\n%s", path, want, got)
	}
}

var updateGolden = flag.Bool("update", false, "rewrite golden files")

// Every generated query must be syntactically plausible PromQL.
//
// The latency query appended its range selector to the LABEL LIST, so it
// landed inside the braces — {…,route="GET /slow"[60s]} — which Cloud
// Monitoring rejects with a 400. Every latency alert was malformed, and
// nothing looked at a rendered query until siren's golden did.
//
// This is not a parser. It checks the two structural mistakes that are
// possible here and would otherwise only surface as a failed deploy.
func TestGeneratedQueriesAreWellFormed(t *testing.T) {
	_, thisFile, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(thisFile), "..", "..")
	g, err := analyzer.Analyze(analyzer.Config{Dir: filepath.Join(root, "examples", "siren")})
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{Project: "p", ProjectNumber: "1", Region: "us-central1", App: "siren", Env: "x1"}
	rangeInBraces := regexp.MustCompile(`\[[0-9]+[smhd]\][^)]*\}`)
	for _, r := range g.Resources {
		if r.Kind != "alert" {
			continue
		}
		q, _, err := alertQuery(cfg, g, r.Alert)
		if err != nil {
			t.Errorf("%s: %v", r.Name, err)
			continue
		}
		if rangeInBraces.MatchString(q) {
			t.Errorf("%s: range selector inside the label braces — PromQL puts it after the closing brace:\n  %s", r.Name, q)
		}
		if strings.Count(q, "(") != strings.Count(q, ")") {
			t.Errorf("%s: unbalanced parentheses:\n  %s", r.Name, q)
		}
		if strings.Count(q, "{") != strings.Count(q, "}") {
			t.Errorf("%s: unbalanced braces:\n  %s", r.Name, q)
		}
	}
}

// A comparison must actually be reachable. `< 0` on an instance count is
// the shape SingletonDown had: a threshold the analyzer never read,
// defaulting to zero, on a metric that is never negative.
func TestComparisonsAreReachable(t *testing.T) {
	_, thisFile, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(thisFile), "..", "..")
	g, err := analyzer.Analyze(analyzer.Config{Dir: filepath.Join(root, "examples", "siren")})
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{Project: "p", ProjectNumber: "1", Region: "us-central1", App: "siren", Env: "x1"}
	below := regexp.MustCompile(`< (-?[0-9.e+]+)$`)
	for _, r := range g.Resources {
		if r.Kind != "alert" {
			continue
		}
		q, _, err := alertQuery(cfg, g, r.Alert)
		if err != nil {
			continue
		}
		if m := below.FindStringSubmatch(q); m != nil {
			if v, err := strconv.ParseFloat(m[1], 64); err == nil && v <= 0 {
				t.Errorf("%s compares below %v — nothing tako measures goes negative, so this can never fire:\n  %s",
					r.Name, v, q)
			}
		}
	}
}
