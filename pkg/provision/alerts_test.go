package provision

import (
	"strings"
	"testing"

	"github.com/gsandoval/tako/pkg/analyzer"
)

func alertGraph() *analyzer.AppGraph {
	return &analyzer.AppGraph{
		Module: "gallery",
		Services: []analyzer.Service{
			{Name: "gallery", Package: "gallery"},
			{Name: "gallery-makethumb", Package: "gallery",
				Footprint: &analyzer.Footprint{MaxInstances: 5}},
		},
		Handlers: []analyzer.Handler{
			{Kind: "cron", Var: "gallery.Cleanup", Service: "gallery", Schedule: "every 24h"},
			{Kind: "cron", Var: "gallery.Sweep", Service: "gallery", Schedule: "every 6h"},
			{Kind: "worker", Var: "gallery.MakeThumb", Service: "gallery-makethumb"},
			{Kind: "api", Var: "gallery.List", Service: "gallery", Route: "GET /api/photos"},
		},
	}
}

func query(t *testing.T, a *analyzer.AlertCondition) (string, bool) {
	t.Helper()
	cfg := Config{Project: "p", Region: "us-central1", App: "gallery", Env: "x1"}
	q, retest, err := alertQuery(cfg, alertGraph(), a)
	if err != nil {
		t.Fatalf("alertQuery: %v", err)
	}
	return q, retest
}

// A daily cron gets a 48h lookback with no window given — twice its own
// schedule, read from the graph. This is the case that could not exist
// under conditionAbsent's 23h30m ceiling, and the reason Absent compiles
// to PromQL.
func TestCronMissedDoublesTheScheduleItReads(t *testing.T) {
	daily, retest := query(t, &analyzer.AlertCondition{
		Op: "absent", Reading: "cron_missed", SubjectKind: "cron", SubjectVar: "gallery.Cleanup"})
	if !strings.Contains(daily, "[172800s]") {
		t.Errorf("daily cron lookback = %q, want 48h", daily)
	}
	if retest {
		t.Error("a 48h lookback must forbid a retest window — the API refuses it")
	}
	if !strings.Contains(daily, `route="/_tako/cron/Cleanup"`) {
		t.Errorf("cron route not derived from the variable: %q", daily)
	}

	sixHourly, retest6 := query(t, &analyzer.AlertCondition{
		Op: "absent", Reading: "cron_missed", SubjectKind: "cron", SubjectVar: "gallery.Sweep"})
	if !strings.Contains(sixHourly, "[43200s]") {
		t.Errorf("6h cron lookback = %q, want 12h", sixHourly)
	}
	if !retest6 {
		t.Error("a 12h lookback still allows a retest window")
	}
}

// Names reaching Google are env-prefixed, or the policy watches the
// wrong environment's resources — the failure that would look like an
// alert simply never firing.
func TestSubjectNamesCarryTheEnvPrefix(t *testing.T) {
	q, _ := query(t, &analyzer.AlertCondition{
		Op: "above", Reading: "dead_letters", SubjectKind: "queue", SubjectName: "thumbs", Threshold: 5})
	if !strings.Contains(q, `subscription_id="x1-thumbs"`) {
		t.Errorf("dead-letter subscription not env-prefixed: %q", q)
	}
	svc, _ := query(t, &analyzer.AlertCondition{
		Op: "above", Reading: "error_rate", SubjectKind: "handler", SubjectVar: "gallery.MakeThumb", Threshold: 0.05})
	if !strings.Contains(svc, `service_name="x1-gallery-makethumb"`) {
		t.Errorf("service not env-prefixed: %q", svc)
	}
}

// Per-route alerting is the point of the route label: an endpoint
// condition must NOT silently widen to its whole service.
func TestRouteSubjectsFilterToTheirRoute(t *testing.T) {
	q, _ := query(t, &analyzer.AlertCondition{
		Op: "above", Reading: "error_rate", SubjectKind: "route",
		SubjectName: "GET /api/photos", SubjectVar: "gallery.List", Threshold: 0.02})
	if !strings.Contains(q, `route="/api/photos"`) {
		t.Fatalf("route matcher missing — the alert would watch the whole service: %q", q)
	}
	if strings.Contains(q, `route="GET /api/photos"`) {
		t.Fatalf("method leaked into the route label: %q", q)
	}
	// The ratio needs BOTH halves filtered, or it divides one route's
	// errors by the whole service's traffic and reads far too low.
	if strings.Count(q, `route="/api/photos"`) != 2 {
		t.Errorf("both numerator and denominator must carry the route: %q", q)
	}
}

// AtCapacity asks for no threshold: it reads MaxInstances from the
// footprint, so the number lives in one place.
func TestAtCapacityReadsTheDeclaredMaximum(t *testing.T) {
	q, _ := query(t, &analyzer.AlertCondition{
		Op: "above", Reading: "at_capacity", SubjectKind: "handler", SubjectVar: "gallery.MakeThumb"})
	if !strings.Contains(q, "> 4.5") {
		t.Errorf("at-capacity threshold = %q, want just under the declared max of 5", q)
	}
}

// A gauge has no rate: comparing increase() of an age would be
// meaningless, and the API would not complain.
func TestGaugeReadingsDoNotUseRateOrIncrease(t *testing.T) {
	for _, reading := range []string{"backlog_age", "at_capacity"} {
		a := &analyzer.AlertCondition{Op: "above", Reading: reading, SubjectKind: "queue",
			SubjectName: "thumbs", SubjectVar: "gallery.MakeThumb", Threshold: 600}
		q, _ := query(t, a)
		for _, banned := range []string{"rate(", "increase("} {
			if strings.Contains(q, banned) {
				t.Errorf("%s uses %s on a GAUGE: %q", reading, banned, q)
			}
		}
	}
}

// Custom metrics must be namespaced per env, the same way their series
// are — otherwise prod and a preview env share an alert.
func TestCustomMetricsAreScopedToTheEnv(t *testing.T) {
	q, _ := query(t, &analyzer.AlertCondition{
		Op: "below", Reading: "count", SubjectKind: "counter", SubjectName: "uploads",
		Threshold: 1, EverySeconds: 1800})
	if !strings.Contains(q, `namespace="gallery-x1"`) {
		t.Errorf("counter not scoped to the env: %q", q)
	}
	if !strings.Contains(q, "[1800s]") {
		t.Errorf("declared period not used as the counting window: %q", q)
	}
}
