package provision

import (
	"strings"
	"testing"
	"time"
)

// The name mapping is Google's documented rule: first "/" becomes ":",
// every other special character becomes "_". Getting it wrong produces a
// selector that resolves to nothing, which looks exactly like an alert
// that is simply not firing yet.
func TestPromNameMapping(t *testing.T) {
	cases := map[string]string{
		"custom.googleapis.com/tako/uploads":                            "custom_googleapis_com:tako_uploads",
		"custom.googleapis.com/tako/thumb_ms":                           "custom_googleapis_com:tako_thumb_ms",
		"run.googleapis.com/request_count":                              "run_googleapis_com:request_count",
		"run.googleapis.com/container/instance_count":                   "run_googleapis_com:container_instance_count",
		"pubsub.googleapis.com/subscription/oldest_unacked_message_age": "pubsub_googleapis_com:subscription_oldest_unacked_message_age",
	}
	for in, want := range cases {
		if got := promName(in); got != want {
			t.Errorf("promName(%q) = %q, want %q", in, got, want)
		}
	}
}

// A custom-metric selector without monitored_resource is refused by the
// API outright — "must specify a label matcher on the
// 'monitored_resource' label". Every selector carries it.
func TestEverySelectorCarriesMonitoredResource(t *testing.T) {
	s := selector("custom.googleapis.com/tako/uploads", "generic_task")
	if !strings.Contains(s, `monitored_resource="generic_task"`) {
		t.Fatalf("selector = %q, missing the matcher the API demands", s)
	}
	withLabels := selector("run.googleapis.com/request_count", "cloud_run_revision", `route="/_tako/api/cron/Cleanup"`)
	if !strings.Contains(withLabels, `monitored_resource="cloud_run_revision"`) ||
		!strings.Contains(withLabels, `route="/_tako/api/cron/Cleanup"`) {
		t.Fatalf("selector = %q", withLabels)
	}
}

// A DISTRIBUTION resolves to nothing under its bare name — verified with
// data present, which is what makes it a trap rather than an empty
// window. Every histogram reading must go through a suffix.
func TestHistogramReadingsUseTheBucketDecomposition(t *testing.T) {
	m := "custom.googleapis.com/tako/thumb_ms"
	cases := map[string]string{
		"percentile": "_bucket",
		"sum":        "_sum",
		"mean":       "_sum",
		"count":      "_count",
		"rate":       "_count",
	}
	for agg, suffix := range cases {
		q := histogramQuery(m, "generic_task", agg, 0.99, 5*time.Minute)
		if !strings.Contains(q, "custom_googleapis_com:tako_thumb_ms"+suffix) {
			t.Errorf("%s reading = %q, want the %s decomposition", agg, q, suffix)
		}
		if strings.Contains(q, "tako_thumb_ms{") {
			t.Errorf("%s reading uses the BARE name, which resolves to nothing: %q", agg, q)
		}
	}
}

// A quantile needs `by (le)`, or the buckets are summed away and the
// answer is not a percentile of anything.
func TestPercentileKeepsTheBucketLabel(t *testing.T) {
	q := histogramQuery("custom.googleapis.com/tako/thumb_ms", "generic_task", "percentile", 0.99, 5*time.Minute)
	if !strings.Contains(q, "by (le)") {
		t.Fatalf("percentile query drops the le label: %q", q)
	}
	if !strings.Contains(q, "histogram_quantile(0.99,") {
		t.Fatalf("quantile not rendered as a fraction: %q", q)
	}
}

// Counters read through increase/rate, never delta or avg_over_time:
// tako writes cumulative per-instance metrics, and delta is defined for
// gauges — an instance restart would read as a negative.
func TestCounterReadingsAreResetSafe(t *testing.T) {
	sel := selector("custom.googleapis.com/tako/uploads", "generic_task")
	count := counterQuery(sel, "count", 30*time.Minute)
	rate := counterQuery(sel, "rate", 5*time.Minute)
	if !strings.HasPrefix(count, "sum(increase(") {
		t.Errorf("count = %q, want increase()", count)
	}
	if !strings.HasPrefix(rate, "sum(rate(") {
		t.Errorf("rate = %q, want rate()", rate)
	}
	for _, q := range []string{count, rate} {
		for _, banned := range []string{"delta(", "avg_over_time(", "sum_over_time("} {
			if strings.Contains(q, banned) {
				t.Errorf("%q uses %s, which misreads a cumulative counter", q, banned)
			}
		}
	}
}

// The absence query must be absent_over_time. increase(...) == 0 parses,
// is accepted by the API, and never fires — measured: 0 series against a
// route with no data, where absent_over_time returned 1.
func TestAbsenceUsesAbsentOverTimeNotIncreaseEqualsZero(t *testing.T) {
	sel := selector("run.googleapis.com/request_count", "cloud_run_revision", `route="/_tako/api/cron/Cleanup"`)
	q := absentQuery(sel, 26*time.Hour)
	if !strings.HasPrefix(q, "absent_over_time(") {
		t.Fatalf("absence query = %q", q)
	}
	if strings.Contains(q, "== 0") {
		t.Fatal("absence built as increase(...) == 0, which never fires on missing data")
	}
	if !strings.Contains(q, "[93600s]") {
		t.Fatalf("26h lookback not rendered: %q", q)
	}
}

// Google refuses a retest window past 1d1h. A 26h absence must send
// duration 0 or the create 400s.
func TestLongLookbacksForbidARetestWindow(t *testing.T) {
	if !retestAllowed(24 * time.Hour) {
		t.Error("24h should still allow a retest window")
	}
	if retestAllowed(26 * time.Hour) {
		t.Error("26h must forbid a retest window — the API refuses it")
	}
	if retestAllowed(7 * 24 * time.Hour) {
		t.Error("7d must forbid a retest window")
	}
}

// Ranges render in seconds: "1h30m0s" is not a legal range selector, and
// a range PromQL misparses is worse than one it rejects.
func TestRangesRenderInSeconds(t *testing.T) {
	cases := map[time.Duration]string{
		time.Minute:      "60s",
		90 * time.Minute: "5400s",
		26 * time.Hour:   "93600s",
		time.Millisecond: "1s", // never zero: [0s] is not a range
	}
	for d, want := range cases {
		if got := promDuration(d); got != want {
			t.Errorf("promDuration(%s) = %q, want %q", d, got, want)
		}
	}
}

// A below-comparison must fire when the data is gone.
//
// The whole trap in one test: an empty result set satisfies no
// comparison, so `empty < 0.01` is empty and the alert stays quiet
// exactly when the metric has stopped. Measured live before this was
// fixed — thirteen minutes with nothing writing and three alerts silent.
//
// `or vector(0)` is PromQL's idiom for treating missing data as zero,
// and it is verified against Cloud Monitoring on all four cases: a
// healthy value stays quiet, a low value fires, a vanished series fires,
// a never-written metric fires.
func TestBelowTreatsMissingDataAsZero(t *testing.T) {
	got := comparison("sum(rate(x[60s]))", "below", 0.01)
	if !strings.Contains(got, "or vector(0)") {
		t.Errorf("below compiles to %q — without the missing-data fallback it cannot fire on a stopped metric", got)
	}
	if !strings.HasPrefix(got, "(") {
		t.Errorf("the fallback must be inside the comparison, got %q", got)
	}
}

// The fallback belongs ONLY on below. Zero is not above anything, so on
// an above-comparison it would add noise to every query and change no
// outcome.
func TestAboveDoesNotCarryTheFallback(t *testing.T) {
	for _, op := range []string{"above", "atleast"} {
		if got := comparison("sum(rate(x[60s]))", op, 5); strings.Contains(got, "vector(0)") {
			t.Errorf("%s carries the missing-data fallback for no reason: %q", op, got)
		}
	}
}
