package provision

// FL24: CronMissed derived a lookback Cloud Monitoring refuses.
//
// The deploy died at the last resource with "custom.googleapis.com
// metrics are not supported in PromQL alerts with lookback windows
// longer than 1d1h", and the number that drew it was tako's own: twice
// the schedule, uncapped.
//
// Two bugs, and the second is wider than the report. cronPeriod read the
// schedule by trimming "every " and calling ParseDuration, so it
// understood `every 6h` and NOTHING ELSE — every cron declared as a cron
// expression fell through to a 24h fallback and got a 48h lookback,
// including a five-minutely one, where the alert would also have taken
// two days to notice a stopped job.

import (
	"strings"
	"testing"
	"time"

	"github.com/gsandoval/tako/pkg/analyzer"
)

// The periods are what the schedule ACTUALLY means, walked from the
// parsed form rather than pattern-matched off its prefix.
func TestCronPeriodReadsEveryScheduleForm(t *testing.T) {
	g := &analyzer.AppGraph{Handlers: []analyzer.Handler{
		{Kind: "cron", Var: "app.Every6h", Schedule: "every 6h"},
		{Kind: "cron", Var: "app.FiveMinutely", Schedule: "*/5 * * * *"},
		{Kind: "cron", Var: "app.DailyAt3", Schedule: "0 3 * * *"},
		{Kind: "cron", Var: "app.Weekly", Schedule: "0 0 * * 0"},
		{Kind: "cron", Var: "app.Nonsense", Schedule: "not a schedule"},
	}}
	for _, tc := range []struct {
		v    string
		want time.Duration
	}{
		{"app.Every6h", 6 * time.Hour},
		{"app.FiveMinutely", 5 * time.Minute},
		{"app.DailyAt3", 24 * time.Hour},
		{"app.Weekly", 7 * 24 * time.Hour},
		// Unparseable keeps the old fallback. The analyzer refuses this
		// schedule long before a deploy, so it is a floor, not a guess
		// anyone meets.
		{"app.Nonsense", 24 * time.Hour},
	} {
		if got := cronPeriod(g, tc.v); got != tc.want {
			t.Errorf("cronPeriod(%s) = %v, want %v", tc.v, got, tc.want)
		}
	}
}

// The lookback that reaches Cloud Monitoring is capped at what Cloud
// Monitoring accepts. 172800s is the number in flauta's HTTP 400.
func TestADailyCronsLookbackIsWithinTheCeiling(t *testing.T) {
	daily, retest := query(t, &analyzer.AlertCondition{
		Op: "absent", Reading: "cron_missed", SubjectKind: "cron", SubjectVar: "gallery.Cleanup"})
	if strings.Contains(daily, "[172800s]") {
		t.Fatalf("a daily cron still derives the 48h lookback the API refuses: %q", daily)
	}
	if !strings.Contains(daily, "[90000s]") {
		t.Errorf("daily cron lookback = %q, want 25h — one missed run plus an hour, and the "+
			"longest window Cloud Monitoring takes on a custom metric", daily)
	}
	// At exactly the ceiling a retest window is still allowed.
	if !retest {
		t.Error("a 25h lookback forbids a retest window; the API allows one at 1d1h")
	}
}

// A cron whose period the old code could not read at all. Twice five
// minutes is the answer, not twice a fallback.
func TestACronExpressionsLookbackFollowsItsRealPeriod(t *testing.T) {
	g := alertGraph()
	g.Handlers = append(g.Handlers, analyzer.Handler{
		Kind: "cron", Var: "gallery.Frequent", Service: "gallery", Schedule: "*/5 * * * *"})
	cfg := Config{Project: "p", Region: "us-central1", App: "gallery", Env: "x1"}
	q, retest, err := alertQuery(cfg, g, &analyzer.AlertCondition{
		Op: "absent", Reading: "cron_missed", SubjectKind: "cron", SubjectVar: "gallery.Frequent"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(q, "[600s]") {
		t.Errorf("five-minutely cron lookback = %q, want 10m", q)
	}
	if !retest {
		t.Error("a 10m lookback allows a retest window")
	}
}
