package provision

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gsandoval/tako/internal/metrics"
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
			{Kind: "worker", Var: "gallery.MakeThumb", Service: "gallery-makethumb", Binding: "thumbnails"},
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
	if !strings.Contains(daily, `route="/_tako/api/cron/Cleanup"`) {
		t.Errorf("cron route not derived from the variable: %q", daily)
	}
	// The cron's dispatcher refines this label per cron; the mux pattern
	// alone is POST /_tako/cron/{cron} and would merge every cron in the
	// service into one series.
	if strings.Contains(daily, "{cron}") {
		t.Errorf("cron alert watches the wildcard pattern, not one cron: %q", daily)
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
	// A real queue with a real consumer: the subscription is the
	// consumer's, env-prefixed. Written with a queue nothing consumes,
	// this passed through a fallback path and proved nothing — see
	// TestSubscriptionMatchersNameSubscriptionsTheDeployCreates for the
	// defect that hid there.
	q, _ := query(t, &analyzer.AlertCondition{
		Op: "above", Reading: "queue_depth", SubjectKind: "queue", SubjectName: "thumbnails",
		SubjectVar: "gallery.thumbs", Threshold: 5, On: "dead"})
	if !strings.Contains(q, `subscription_id="x1-gallery-makethumb-dead"`) {
		t.Errorf("dead-letter subscription is not the consumer's, env-prefixed: %q", q)
	}
	// The service is the generic_task `job` label, because these are
	// tako's own metrics now — Cloud Run's route label is never
	// populated, so its request_count cannot answer per-endpoint
	// questions at all. That label is NOT env-prefixed; see
	// TestJobLabelMatchesWhatTheServiceWrites for why, and for the check
	// that keeps the two sides together.
	svc, _ := query(t, &analyzer.AlertCondition{
		Op: "above", Reading: "error_rate", SubjectKind: "handler", SubjectVar: "gallery.MakeThumb", Threshold: 0.05})
	if strings.Contains(svc, "run_googleapis_com:request_count") {
		t.Errorf("still reading the platform metric, whose route label is always empty: %q", svc)
	}
}

// The `job` matcher is the one label a query and the app it watches must
// spell identically, and nothing forced them to. They drifted three
// ways: the app wrote the logical name, the deploy's seed wrote the app
// name, the query asked for the env-prefixed name. Nothing matched, so
// absent_over_time was permanently true and CronMissed fired forever on
// a cron that ran on time — a false positive that survived a live gate,
// because a firing alert and a correct alert look identical in an inbox.
//
// The previous test asserted the query was env-prefixed. It passed. It
// was checking the shape of the label instead of asking whether anything
// writes that value.
//
// This one reads BOTH sides from the same graph: the value the deploy
// injects as TAKO_SERVICE (which pkg/backends/gcp.WhoAmI copies into the
// job label) and the value the alert query matches on.
func TestJobLabelMatchesWhatTheServiceWrites(t *testing.T) {
	cfg := Config{Project: "p", Region: "us-central1", App: "gallery", Env: "x1"}
	g := alertGraph()
	images := map[string]string{}
	for i, s := range g.Services {
		images[s.Name] = "img"
		g.Services[i].Profile = "default" // Desired refuses an unset profile
	}
	desired, err := Desired(g, cfg, images)
	if err != nil {
		t.Fatal(err)
	}
	// What the RUNNING service will write, straight off the deploy plan.
	written := map[string]string{}
	for _, o := range desired {
		if o.Kind != KindRunService {
			continue
		}
		env, _ := o.Spec["env"].(map[string]any)
		written[fmt.Sprint(env["TAKO_SERVICE"])] = o.Name
	}
	if len(written) == 0 {
		t.Fatal("no run services in the plan — this test is checking nothing")
	}

	// What the alert QUERY will look for, for a handler in each service.
	for _, h := range g.Handlers {
		q, _ := query(t, &analyzer.AlertCondition{
			Op: "above", Reading: "error_rate", SubjectKind: "handler",
			SubjectVar: h.Var, Threshold: 0.05})
		m := regexp.MustCompile(`job="([^"]*)"`).FindStringSubmatch(q)
		if m == nil {
			t.Fatalf("%s: query carries no job matcher: %q", h.Var, q)
		}
		if _, ok := written[m[1]]; !ok {
			t.Errorf("%s: the alert matches job=%q, but no service writes that — "+
				"services write %v. The query matches nothing and the alert reads as permanently absent.",
				h.Var, m[1], keysOf(written))
		}
	}
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Per-route alerting is the point of the route label: an endpoint
// condition must NOT silently widen to its whole service.
func TestRouteSubjectsFilterToTheirRoute(t *testing.T) {
	q, _ := query(t, &analyzer.AlertCondition{
		Op: "above", Reading: "error_rate", SubjectKind: "route",
		SubjectName: "GET /api/photos", SubjectVar: "gallery.List", Threshold: 0.02})
	// The label value is the ServeMux PATTERN, method included, because
	// that is what tako writes. Stripping the method — which the first
	// version did, for Cloud Run's differently-shaped label — produces a
	// matcher that never matches.
	if !strings.Contains(q, `route="GET /api/photos"`) {
		t.Fatalf("route matcher missing or reshaped — the alert would watch the whole service: %q", q)
	}
	// The ratio needs BOTH halves filtered, or it divides one route's
	// errors by the whole service's traffic and reads far too low.
	if strings.Count(q, `route="GET /api/photos"`) != 2 {
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
	for _, reading := range []string{"queue_age", "at_capacity"} {
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

// A preset's documented default has to be applied where the deploy can
// see it. The SDK's windowOr default is a RUNTIME value and the deploy
// reads source, so a default that lives only in the SDK ships as
// whatever the provisioner happens to fall back to — the live gate found
// DeadLettered documenting five minutes and deploying sixty seconds.
func TestPresetDefaultsAreAppliedWhereTheDeployReadsThem(t *testing.T) {
	q, _ := query(t, &analyzer.AlertCondition{
		Op: "atleast", Reading: "queue_depth", SubjectKind: "queue", SubjectName: "thumbnails", SubjectVar: "gallery.thumbs", Threshold: 1, On: "dead"})
	if !strings.Contains(q, "max(") {
		t.Errorf("queue reading is not a gauge = %q, want the documented 5 minutes", q)
	}
}

// …and it has to hold for EVERY preset, which the check above did not
// say. It asserted one, the comment above it claimed the principle, and
// two more presets were quietly wrong the whole time: CronFailed and
// JobFailed document an hour and were deploying five minutes.
//
// One preset checked is a sample. This reads both sides — the SDK's own
// windowOr default out of alert.go, the reading name out of the
// analyzer's preset table — so a new preset with a default the deploy
// does not honour fails here rather than in someone's on-call rotation.
func TestEveryPresetsDefaultWindowSurvivesToTheDeploy(t *testing.T) {
	sdk := sdkPresetDefaults(t)   // constructor -> its default Every (0 if it sets none)
	readings := presetReadings(t) // constructor -> the reading the provisioner sees
	if len(readings) == 0 || len(sdk) == 0 {
		t.Fatal("parsed nothing — this test is checking nothing")
	}
	for ctor, reading := range readings {
		want, declared := sdk[ctor]
		if !declared {
			// The queue conditions take a QueueWatch whose For is
			// REQUIRED, so they have no default to survive — the
			// analyzer refuses the call instead.
			if strings.HasPrefix(ctor, "Queue") {
				continue
			}
			t.Errorf("%s is in the analyzer's preset table but declares no default window in alert.go", ctor)
			continue
		}
		if want == 0 {
			// The preset sets only For; Every derives, and 60s is the
			// documented derivation.
			want = defaultEvery
		}
		if got := presetEvery(reading); got != want {
			t.Errorf("tako.%s documents a %s window and the deploy applies %s — "+
				"the SDK default is a runtime value the deploy never reads, so the doc is the only thing that was right",
				ctor, want, got)
		}
	}
}

// sdkPresetDefaults reads each preset's windowOr default straight out of
// the SDK source, because that default is a runtime value: nothing the
// provisioner can import, and the exact reason the two can drift.
func sdkPresetDefaults(t *testing.T) map[string]time.Duration {
	t.Helper()
	src, err := os.ReadFile("../../alert.go")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]time.Duration{}
	fn := regexp.MustCompile(`(?m)^func (\w+)[\[(]`)
	def := regexp.MustCompile(`windowOr\(w, Window\{([^}]*)\}\)`)
	every := regexp.MustCompile(`Every:\s*([^,}]+)`)
	locs := fn.FindAllStringSubmatchIndex(string(src), -1)
	for i, loc := range locs {
		end := len(src)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		body := string(src[loc[1]:end])
		m := def.FindStringSubmatch(body)
		if m == nil {
			continue // not a preset
		}
		name := string(src[loc[2]:loc[3]])
		if e := every.FindStringSubmatch(m[1]); e != nil {
			out[name] = parseDurationExpr(t, name, strings.TrimSpace(e[1]))
		} else {
			out[name] = 0
		}
	}
	return out
}

// presetReadings reads the analyzer's constructor→reading table, so a
// preset renamed on one side cannot pass by matching nothing.
func presetReadings(t *testing.T) map[string]string {
	t.Helper()
	src, err := os.ReadFile("../analyzer/alerts.go")
	if err != nil {
		t.Fatal(err)
	}
	// Requiring an `op` field is what keeps this to the PRESET table.
	// The scoped-metric table has the same entry shape, so an unscoped
	// pattern reads both and reports ServiceMetric as a preset missing a
	// default window it was never meant to have.
	entry := regexp.MustCompile(`"(\w+)":\s*\{reading: "(\w+)"[^}]*op: "`)
	out := map[string]string{}
	for _, m := range entry.FindAllStringSubmatch(string(src), -1) {
		out[m[1]] = m[2]
	}
	if len(out) < 5 {
		t.Fatalf("only %d presets parsed — the table shape changed and this test is checking almost nothing", len(out))
	}
	return out
}

// parseDurationExpr evaluates the handful of shapes a Go duration
// constant takes in these defaults: time.Hour, 5 * time.Minute.
func parseDurationExpr(t *testing.T, ctor, expr string) time.Duration {
	t.Helper()
	units := map[string]time.Duration{
		"time.Second": time.Second, "time.Minute": time.Minute, "time.Hour": time.Hour,
	}
	n := 1
	if i := strings.Index(expr, "*"); i >= 0 {
		v, err := strconv.Atoi(strings.TrimSpace(expr[:i]))
		if err != nil {
			t.Fatalf("%s: cannot read duration %q", ctor, expr)
		}
		n, expr = v, strings.TrimSpace(expr[i+1:])
	}
	u, ok := units[expr]
	if !ok {
		t.Fatalf("%s: unknown duration unit %q — teach this test about it", ctor, expr)
	}
	return time.Duration(n) * u
}

// A dead-letter alert filters on `subscription_id`, and that name has to
// be one the deploy creates. It was not. A queue deploys as a TOPIC and
// the subscription draining it is named after its CONSUMER, so
// DeadLettered(thumbs, 1) compiled to subscription_id="x1-thumbnails" —
// which exists nowhere — and the alert could never fire.
//
// It survived the whole Y1 gate. The task log recorded Stuck as "created
// but never fired, nothing dead-lettered"; the truth was that nothing
// COULD fire it. A query matching nothing is indistinguishable from a
// healthy queue, which is the third time that exact sentence has been
// the diagnosis (see the job label, and the empty route).
//
// Found by opening the console link the notification carries. This test
// is that click, made cheap: both sides read off the same plan.
func TestSubscriptionMatchersNameSubscriptionsTheDeployCreates(t *testing.T) {
	cfg := Config{Project: "p", Region: "us-central1", App: "gallery", Env: "x1"}
	g := alertGraph()
	for i := range g.Services {
		g.Services[i].Profile = "default"
	}
	images := map[string]string{}
	for _, s := range g.Services {
		images[s.Name] = "img"
	}
	desired, err := Desired(g, cfg, images)
	if err != nil {
		t.Fatal(err)
	}
	created := map[string]bool{}
	for _, o := range desired {
		if o.Kind == KindPushSubscription || o.Kind == KindPullSubscription {
			created[o.Name] = true
		}
	}
	if len(created) == 0 {
		t.Fatal("no subscriptions in the plan — this test is checking nothing")
	}
	for _, reading := range []string{"queue_depth", "queue_age"} {
		q, _ := query(t, &analyzer.AlertCondition{
			Op: "above", Reading: reading, SubjectKind: "queue", SubjectName: "thumbnails",
			SubjectVar: "gallery.thumbs", Threshold: 1})
		m := regexp.MustCompile(`subscription_id="([^"]*)"`).FindStringSubmatch(q)
		if m == nil {
			t.Fatalf("%s: no subscription matcher in %q", reading, q)
		}
		if !created[m[1]] {
			t.Errorf("%s watches subscription_id=%q, which the deploy never creates — it creates %v. "+
				"The query matches nothing, and a queue that cannot alert looks exactly like a queue that is fine.",
				reading, m[1], keysOfBool(created))
		}
	}
}

// "At least n" has to compile to >= n. Three presets document that
// wording and compiled to "> n", so DeadLettered(thumbs, 1) — the way
// you say "tell me when anything gives up" — needed TWO dead letters.
//
// The error is in the quiet direction, which is the direction nobody
// notices: the alert simply does not arrive, and a queue that is not
// alerting looks like a queue that is fine. Found by reading the preset
// doc against the operator before the Z2 walk, not by the walk.
func TestAtLeastPresetsFireOnTheNthEvent(t *testing.T) {
	cases := []struct {
		reading string
		cond    *analyzer.AlertCondition
	}{
		{"queue_depth", &analyzer.AlertCondition{Op: "atleast", Reading: "queue_depth",
			SubjectKind: "queue", SubjectName: "thumbnails", SubjectVar: "gallery.thumbs", Threshold: 1, On: "dead"}},
		{"cron_failed", &analyzer.AlertCondition{Op: "atleast", Reading: "cron_failed",
			SubjectKind: "cron", SubjectVar: "gallery.Cleanup", Threshold: 3}},
		{"job_failed", &analyzer.AlertCondition{Op: "atleast", Reading: "job_failed",
			SubjectKind: "job", SubjectName: "Import", Threshold: 2}},
	}
	for _, tc := range cases {
		q, _ := query(t, tc.cond)
		want := fmt.Sprintf(">= %g", tc.cond.Threshold)
		if !strings.Contains(q, want) {
			t.Errorf("%s compiles to %q, want %q — \"at least %g\" must include %g itself",
				tc.reading, q, want, tc.cond.Threshold, tc.cond.Threshold)
		}
	}
	// The thresholds that mean "exceeds" keep strict >.
	for _, reading := range []string{"error_rate", "latency"} {
		q, _ := query(t, &analyzer.AlertCondition{Op: "above", Reading: reading,
			SubjectKind: "route", SubjectName: "GET /api/photos", SubjectVar: "gallery.List",
			Threshold: 0.05, Quantile: 0.99, ForSeconds: 300})
		if strings.Contains(q, ">=") {
			t.Errorf("%s uses >=, but it documents \"exceeds\": %q", reading, q)
		}
	}
}

// The analyzer's preset table and the SDK's own wording must agree about
// which presets count "at least". Reading one and writing the other is
// how they came apart.
func TestAtLeastWordingMatchesTheOperator(t *testing.T) {
	src, err := os.ReadFile("../../alert.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	table, err := os.ReadFile("../analyzer/alerts.go")
	if err != nil {
		t.Fatal(err)
	}
	entry := regexp.MustCompile(`"(\w+)":\s*\{reading: "\w+"[^}]*op: "(\w+)"`)
	fn := regexp.MustCompile(`(?m)^func (\w+)[\[(]`)
	docs := map[string]string{}
	// ONLY the doc comment, which is the wording under test. Running the
	// block to the next func's start swallows that func's doc comment and
	// reports its neighbour's wording — the first version did, and blamed
	// Backlogged for CronFailed's "at least".
	for _, loc := range fn.FindAllStringSubmatchIndex(text, -1) {
		head := text[:loc[0]]
		if j := strings.LastIndex(head, "\n\n"); j >= 0 {
			head = head[j:]
		}
		docs[text[loc[2]:loc[3]]] = head
	}
	for _, m := range entry.FindAllStringSubmatch(string(table), -1) {
		ctor, op := m[1], m[2]
		doc, ok := docs[ctor]
		if !ok {
			continue
		}
		saysAtLeast := strings.Contains(doc, "at least")
		if saysAtLeast && op != "atleast" {
			t.Errorf("tako.%s documents \"at least\" but compiles with op %q — it will miss the nth event", ctor, op)
		}
		if !saysAtLeast && op == "atleast" {
			t.Errorf("tako.%s compiles with op \"atleast\" but its doc does not say so", ctor)
		}
	}
}

// WhenAbsent over a Google metric asked one question and compiled to
// another. The platform case handed every op to comparison(), whose
// default arm is `> threshold`, and an absent condition carries
// threshold 0 — so `WhenAbsent(ServiceMetric(svc, …))` became
// `max(…) > 0`: an alert that fires while the metric is being written
// and falls silent the moment it stops. Every raw absence over Google's
// metrics was inverted, and an inverted alert reads as a healthy one.
func TestAbsenceOverGoogleMetricsIsNotAThresholdComparison(t *testing.T) {
	cases := map[string]*analyzer.AlertCondition{
		"service_metric": {Op: "absent", Reading: "platform", ScopedTo: "service_metric",
			SubjectKind: "handler", SubjectVar: "gallery.MakeThumb", ForSeconds: 600,
			MetricType: "run.googleapis.com/container/instance_count"},
		"queue_metric": {Op: "absent", Reading: "platform", ScopedTo: "queue_metric",
			SubjectKind: "queue", SubjectName: "thumbnails", SubjectVar: "gallery.thumbs", ForSeconds: 600,
			MetricType: "pubsub.googleapis.com/subscription/num_undelivered_messages"},
		"service_instances": {Op: "absent", Reading: "platform", ScopedTo: "service_instances",
			SubjectKind: "handler", SubjectVar: "gallery.MakeThumb", ForSeconds: 600},
		"service_percentile": {Op: "absent", Reading: "platform", ScopedTo: "service_percentile",
			SubjectKind: "handler", SubjectVar: "gallery.MakeThumb", ForSeconds: 600, Quantile: 0.99,
			MetricType: "run.googleapis.com/container/cpu/utilizations"},
		"unscoped": {Op: "absent", Reading: "platform", SubjectKind: "platform", ForSeconds: 600,
			SubjectName: "pubsub.googleapis.com/topic/byte_cost"},
	}
	for name, a := range cases {
		q, _ := query(t, a)
		if !strings.HasPrefix(q, "absent_over_time(") {
			t.Errorf("%s absence = %q, want absent_over_time — a comparison cannot detect missing data", name, q)
		}
		for _, banned := range []string{"> 0", ">= 0", "< 0"} {
			if strings.Contains(q, banned) {
				t.Errorf("%s absence compiled to a comparison (%s): %q", name, banned, q)
			}
		}
		// absent_over_time over max() can never fire: max of an empty
		// vector is empty, so the wrapper would hide the absence.
		if strings.Contains(q, "max(") {
			t.Errorf("%s absence reduces before asking: %q", name, q)
		}
		if !strings.Contains(q, "[600s]") {
			t.Errorf("%s absence lost its lookback: %q", name, q)
		}
	}
}

// A distribution has no bare series, so absence must ask about _count —
// the series that exists whenever anything at all was recorded. Asking
// under the bare name would return "absent" forever, on a metric being
// written every second.
func TestAbsenceOnADistributionAsksAboutCount(t *testing.T) {
	q, _ := query(t, &analyzer.AlertCondition{Op: "absent", Reading: "platform", ScopedTo: "service_percentile",
		SubjectKind: "handler", SubjectVar: "gallery.MakeThumb", ForSeconds: 600, Quantile: 0.99,
		MetricType: "run.googleapis.com/container/cpu/utilizations"})
	if !strings.Contains(q, "container_cpu_utilizations_count{") {
		t.Errorf("distribution absence = %q, want the _count decomposition", q)
	}
}

// A Google metric has no schedule to derive a lookback from, and a
// missing one rendered [0s] — which PromQL does not accept as a range.
func TestAbsenceWithNoWindowFallsBackToTheReductionPeriod(t *testing.T) {
	q, _ := query(t, &analyzer.AlertCondition{Op: "absent", Reading: "platform", ScopedTo: "service_instances",
		SubjectKind: "handler", SubjectVar: "gallery.MakeThumb"})
	if strings.Contains(q, "[0s]") {
		t.Errorf("absence rendered a zero range: %q", q)
	}
	if !strings.Contains(q, "[60s]") {
		t.Errorf("absence = %q, want the default reduction period as the lookback", q)
	}
}

// The notification sentence has to say what stopped. Sharing the
// threshold sentence gave every absence "is above 0", which describes
// the query that used to be generated rather than the one asked for.
func TestAbsenceSentencesSayWhatStopped(t *testing.T) {
	cases := map[string]*analyzer.AlertCondition{
		"service x1-gallery-makethumb is reporting no instance count": {
			Op: "absent", Reading: "platform", ScopedTo: "service_instances",
			SubjectKind: "handler", SubjectVar: "gallery.MakeThumb", ForSeconds: 600},
		"container/cpu/utilizations is not being written for x1-gallery-makethumb": {
			Op: "absent", Reading: "platform", ScopedTo: "service_metric",
			SubjectKind: "handler", SubjectVar: "gallery.MakeThumb", ForSeconds: 600,
			MetricType: "run.googleapis.com/container/cpu/utilizations"},
		"nothing is writing topic/byte_cost": {
			Op: "absent", Reading: "platform", SubjectKind: "platform", ForSeconds: 600,
			SubjectName: "pubsub.googleapis.com/topic/byte_cost"},
	}
	cfg := Config{Project: "p", Region: "us-central1", App: "gallery", Env: "x1"}
	for want, a := range cases {
		got := conditionSentence(cfg, alertGraph(), analyzer.Resource{Name: "Stopped", Alert: a})
		if !strings.HasSuffix(got, want) {
			t.Errorf("sentence = %q, want it to end with %q", got, want)
		}
		if strings.Contains(got, "above") || strings.Contains(got, "below") {
			t.Errorf("absence sentence describes a threshold: %q", got)
		}
	}
}

// Every label a generated query matches on must be one the runtime
// actually writes.
//
// This is the check whose absence produced every label defect in Stage
// 10: a `route` declared in a descriptor and empty on all 88 series, a
// `job` spelled three ways, a `subscription_id` naming a topic,
// `response_class="2xx"` where Pub/Sub writes `ack`. In each case the
// code compiled, the deploy succeeded, and the query matched nothing —
// which reads exactly like a healthy system.
//
// It reads the RENDERED golden rather than the code that builds it,
// because the golden is what Cloud Monitoring evaluates. Nine defects
// this stage came out of reading that file; this asserts one property of
// it on every run.
//
// Honest about its reach: it can only check tako's OWN metrics, since
// those are the ones internal/metrics describes. A wrong label on a
// Google metric — which is what response_class="2xx" was — is still
// only catchable live.
func TestGeneratedQueriesOnlyMatchLabelsTheRuntimeWrites(t *testing.T) {
	golden, err := os.ReadFile(filepath.Join("testdata", "siren_queries.golden.txt"))
	if err != nil {
		t.Fatal(err)
	}
	// promName maps "custom.googleapis.com/tako/requests" to
	// "custom_googleapis_com:tako_requests"; find those selectors and
	// read the labels out of the braces.
	sel := regexp.MustCompile(`(custom_googleapis_com:tako_[a-z_]+)\{([^}]*)\}`)
	label := regexp.MustCompile(`([a-zA-Z_]+)\s*=`)

	declared := map[string]map[string]bool{}
	for _, m := range metrics.Framework {
		declared[promName(m.Type())] = m.LabelSet()
		// A DISTRIBUTION is only ever published decomposed.
		for _, suffix := range []string{"_bucket", "_count", "_sum"} {
			declared[promName(m.Type())+suffix] = m.LabelSet()
		}
	}

	checked := 0
	for _, m := range sel.FindAllStringSubmatch(string(golden), -1) {
		name, body := m[1], m[2]
		allowed, isFramework := declared[name]
		if !isFramework {
			// A user's own Counter or Histogram: same prefix, and its
			// labels are the resource ones plus revision.
			continue
		}
		checked++
		for _, l := range label.FindAllStringSubmatch(body, -1) {
			if !allowed[l[1]] {
				t.Errorf("query on %s matches label %q, which the runtime does not write.\n"+
					"  A label a writer does not produce makes the query match nothing, and an\n"+
					"  alert that matches nothing is quiet for the wrong reason.\n  in: %s{%s}",
					name, l[1], name, body)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no query over tako's own metrics found in the golden — the scan broke, " +
			"which would make this test vacuous")
	}
	t.Logf("checked %d selectors over tako's own metrics", checked)
}

// The status values a query asks for must be values the runtime can
// produce. `status="500"` and `status="error"` both look right, parse
// fine, and match nothing.
func TestGeneratedQueriesUseDeclaredStatusClasses(t *testing.T) {
	golden, err := os.ReadFile(filepath.Join("testdata", "siren_queries.golden.txt"))
	if err != nil {
		t.Fatal(err)
	}
	declared := map[string]bool{}
	for _, v := range metrics.StatusClasses {
		declared[v] = true
	}
	found := regexp.MustCompile(`\bstatus="([^"]*)"`).FindAllStringSubmatch(string(golden), -1)
	if len(found) == 0 {
		t.Fatal("no status matcher in the golden — the scan broke")
	}
	for _, m := range found {
		if !declared[m[1]] {
			t.Errorf("query matches status=%q, which is not one of %v", m[1], metrics.StatusClasses)
		}
	}
}
