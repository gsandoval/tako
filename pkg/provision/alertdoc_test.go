package provision

import (
	"strings"
	"testing"
	"time"

	"github.com/gsandoval/tako/pkg/analyzer"
)

func doc(t *testing.T, a *analyzer.AlertCondition, name string) alertDoc {
	t.Helper()
	cfg := Config{Project: "tako-test-01", Region: "us-central1", App: "gallery", Env: "x1"}
	return documentationFor(cfg, alertGraph(), analyzer.Resource{Kind: "alert", Name: name, Alert: a})
}

// The whole point of the feature. Google's generated body for a PromQL
// condition is "A PromQL query was observed at 1" — true, and no help to
// anyone woken by it. Every alert tako generates must say what broke,
// where, and what to do.
func TestEveryConditionKindSaysSomethingActionable(t *testing.T) {
	cases := []struct {
		name string
		cond *analyzer.AlertCondition
		want []string // substrings that make the body worth reading
	}{
		{"cron_missed", &analyzer.AlertCondition{
			Op: "absent", Reading: "cron_missed", SubjectKind: "cron", SubjectVar: "gallery.Cleanup", ForSeconds: 300},
			[]string{"cron `Cleanup`", "gcloud scheduler jobs run x1-gallery-cron-cleanup", "CronFailed"}},
		{"cron_failed", &analyzer.AlertCondition{
			Op: "above", Reading: "cron_failed", SubjectKind: "cron", SubjectVar: "gallery.Cleanup", Threshold: 3},
			[]string{"Cron `Cleanup` ran and failed at least", "3 failures", "cron=Cleanup"}},
		{"queue_depth_dead", &analyzer.AlertCondition{
			Op: "atleast", Reading: "queue_depth", SubjectKind: "queue", SubjectName: "thumbnails",
			SubjectVar: "gallery.thumbs", Threshold: 1, On: "dead", ForSeconds: 600},
			[]string{"dead-letter queue", "1 message", "tako dlq gallery-makethumb", "--ack", "tako.Permanent"}},
		{"queue_age_live", &analyzer.AlertCondition{
			Op: "above", Reading: "queue_age", SubjectKind: "queue", SubjectName: "thumbnails",
			SubjectVar: "gallery.thumbs", Threshold: 600, ForSeconds: 300},
			[]string{"older than 10m", "before* anything gives up"}},
		{"job_failed", &analyzer.AlertCondition{
			Op: "above", Reading: "job_failed", SubjectKind: "job", SubjectName: "Import", Threshold: 2},
			[]string{"Job `Import`", "2 executions"}},
		{"error_rate", &analyzer.AlertCondition{
			Op: "above", Reading: "error_rate", SubjectKind: "route", SubjectName: "GET /api/photos",
			SubjectVar: "gallery.List", Threshold: 0.05, ForSeconds: 300},
			[]string{"`GET /api/photos` is returning 5xx", "5%", "sustained for 5m"}},
		{"latency", &analyzer.AlertCondition{
			Op: "above", Reading: "latency", SubjectKind: "route", SubjectName: "GET /api/photos",
			SubjectVar: "gallery.List", Threshold: 2000, Quantile: 0.99, ForSeconds: 300},
			[]string{"p99", "over 2000ms"}},
		{"at_capacity", &analyzer.AlertCondition{
			Op: "above", Reading: "at_capacity", SubjectKind: "handler",
			SubjectVar: "gallery.MakeThumb", ForSeconds: 600},
			[]string{"instance ceiling (5)", "Footprint.MaxInstances"}},
		{"singleton_down", &analyzer.AlertCondition{
			Op: "below", Reading: "singleton_down", SubjectKind: "singleton", SubjectVar: "gallery.Loop",
			Threshold: 1, ForSeconds: 300},
			[]string{"Singleton `Loop`", "crash loop"}},
		{"platform", &analyzer.AlertCondition{
			Op: "above", Reading: "platform", SubjectKind: "platform",
			SubjectName: "run.googleapis.com/container/cpu/utilizations", Threshold: 0.9},
			[]string{"run.googleapis.com/container/cpu/utilizations", "tako.PlatformMetric"}},
		{"count", &analyzer.AlertCondition{
			Op: "below", Reading: "count", SubjectKind: "counter", SubjectName: "uploads",
			Threshold: 1, EverySeconds: 1800},
			[]string{"`uploads`", "custom.googleapis.com/tako/uploads", "30m"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := doc(t, tc.cond, "Watcher")
			if d.Content == "" {
				t.Fatal("no documentation — the mail falls back to \"A PromQL query was observed at 1\"")
			}
			for _, want := range tc.want {
				if !strings.Contains(d.Content, want) {
					t.Errorf("body does not mention %q:\n%s", want, d.Content)
				}
			}
			// The footer is the part that saves a wasted afternoon.
			if !strings.Contains(d.Content, "declared in tako as `Watcher`") {
				t.Error("body does not name the declaration it came from")
			}
			if !strings.Contains(d.Content, "until the next deploy overwrites it") {
				t.Error("body does not warn that console edits are reconciled away")
			}
			if d.Subject == "" {
				t.Error("no subject override — the mail keeps Google's \" on __missing__\" tail")
			}
			if len(d.Links) == 0 || len(d.Links) > 3 {
				t.Errorf("%d links — the API caps them at 3, and zero wastes the Quick Links section", len(d.Links))
			}
		})
	}
}

// A dead-letter alert must link to the service that CONSUMES the queue.
// The queue is declared in the app package, so the ordinary name-prefix
// resolution lands on the app service — which never touched the message.
// Sending a responder to the wrong service's logs is worse than sending
// them nowhere, because they will believe what they find there.
func TestDeadLetterDocsPointAtTheConsumer(t *testing.T) {
	d := doc(t, &analyzer.AlertCondition{
		Op: "atleast", Reading: "queue_depth", SubjectKind: "queue", SubjectName: "thumbnails",
		SubjectVar: "gallery.thumbs", Threshold: 1, On: "dead", ForSeconds: 600}, "Stuck")
	var logs string
	for _, l := range d.Links {
		if l.DisplayName == "Service logs" {
			logs = l.URL
		}
	}
	if !strings.Contains(logs, "/x1-gallery-makethumb/") {
		t.Errorf("logs link = %q, want the worker that consumes thumbnails, not the app service", logs)
	}
	if !strings.Contains(d.Content, "service `x1-gallery-makethumb`") {
		t.Errorf("body names the wrong service:\n%s", d.Content)
	}
}

// Durations are read once, badly, by someone half awake. Go's default
// renders every unit down to seconds, so a daily cron's schedule reads
// "every 24h0m0s".
func TestDurationsReadTheWayTheyWereDeclared(t *testing.T) {
	d := doc(t, &analyzer.AlertCondition{
		Op: "absent", Reading: "cron_missed", SubjectKind: "cron", SubjectVar: "gallery.Cleanup"}, "Silent")
	if strings.Contains(d.Content, "0m0s") {
		t.Errorf("raw Go duration in the body:\n%s", d.Content)
	}
	if !strings.Contains(d.Content, "every 24h") {
		t.Errorf("schedule not rendered from the declaration:\n%s", d.Content)
	}
	// 48h is twice the schedule — the derived default lookback.
	if !strings.Contains(d.Content, "in the last 48h") {
		t.Errorf("lookback not stated:\n%s", d.Content)
	}
}

// The documentation must not depend on ${…} variables. They resolve from
// the labels of the series that fired, and an absence has no series — so
// the variable renders as "null" in precisely the alert that needs it
// most. This is the same trap as the "__missing__" in the subject line.
func TestDocumentationUsesNoRuntimeVariables(t *testing.T) {
	for _, reading := range []string{"cron_missed", "queue_depth", "error_rate", "latency", "at_capacity"} {
		d := doc(t, &analyzer.AlertCondition{
			Op: "above", Reading: reading, SubjectKind: "queue", SubjectName: "thumbnails",
			SubjectVar: "gallery.thumbs", Threshold: 1}, "W")
		if strings.Contains(d.Content, "${") {
			t.Errorf("%s: body interpolates a runtime variable, which is null on an absence:\n%s", reading, d.Content)
		}
	}
}

// The API refuses content past 8192 characters. Nothing here is close,
// but the failure mode is a deploy that cannot create the policy at all,
// so it is worth a cheap guard as the catalogue grows.
func TestDocumentationFitsTheAPILimit(t *testing.T) {
	d := doc(t, &analyzer.AlertCondition{
		Op: "absent", Reading: "cron_missed", SubjectKind: "cron", SubjectVar: "gallery.Cleanup"}, "Silent")
	if len(d.Content) > 8192 {
		t.Errorf("documentation is %d characters; the API caps it at 8192", len(d.Content))
	}
	for _, l := range d.Links {
		if len(l.DisplayName) > 63 {
			t.Errorf("link display name %q exceeds the API's 63 characters", l.DisplayName)
		}
	}
}

// The scheduler job name printed as a paste-able command has to be the
// name the scheduler reconciler actually deployed. Two derivations of one
// name is how the `job` label came to disagree three ways.
func TestSchedulerCommandNamesTheDeployedJob(t *testing.T) {
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
	deployed := map[string]bool{}
	for _, o := range desired {
		if o.Kind == KindSchedulerJob {
			deployed[o.Name] = true
		}
	}
	if len(deployed) == 0 {
		t.Fatal("no scheduler jobs in the plan — this test is checking nothing")
	}
	d := documentationFor(cfg, g, analyzer.Resource{Kind: "alert", Name: "Silent",
		Alert: &analyzer.AlertCondition{Op: "absent", Reading: "cron_missed",
			SubjectKind: "cron", SubjectVar: "gallery.Cleanup"}})
	var named string
	for _, line := range strings.Split(d.Content, "\n") {
		if strings.Contains(line, "gcloud scheduler jobs run") {
			named = strings.Fields(line)[4]
		}
	}
	if named == "" {
		t.Fatal("no scheduler command in the body")
	}
	if !deployed[named] {
		t.Errorf("body says to run %q, but the deploy creates %v — the command would 404", named, keysOfBool(deployed))
	}
}

func keysOfBool(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// humanDuration's first version trimmed Go's string form by suffix, so
// "10m0s" became "10m" and then "1". Every value a window can take,
// checked against the way a person writes it.
func TestHumanDurationHasNoTrimmingSeam(t *testing.T) {
	cases := map[time.Duration]string{
		30 * time.Second: "30s",
		time.Minute:      "1m",
		5 * time.Minute:  "5m",
		10 * time.Minute: "10m",
		30 * time.Minute: "30m",
		time.Hour:        "1h",
		90 * time.Minute: "1h30m",
		6 * time.Hour:    "6h",
		24 * time.Hour:   "24h",
		48 * time.Hour:   "48h",
	}
	for d, want := range cases {
		if got := humanDuration(d); got != want {
			t.Errorf("humanDuration(%s) = %q, want %q", d, got, want)
		}
	}
}

// The dlq command in a dead-letter body has to name something
// `tako dlq` accepts. Dead letters are held PER CONSUMER — one dead
// topic per async service, none per queue — so the queue name, which is
// what a reader of the alert would reach for, is exactly the wrong
// argument. Pasted, it answered:
//
//	tako: no async service named "thumbnails" — async services: …
//
// The cron body's command was cross-checked against the deploy plan and
// was right; this one was not checked and was wrong. Same gap, one
// command over.
func TestDlqCommandNamesAnAsyncService(t *testing.T) {
	g := alertGraph()
	async := map[string]bool{}
	for _, h := range g.Handlers {
		if h.Kind == "worker" || h.Kind == "subscribe" {
			async[h.Service] = true
		}
	}
	if len(async) == 0 {
		t.Fatal("no async services in the fixture — this test is checking nothing")
	}
	d := doc(t, &analyzer.AlertCondition{
		Op: "atleast", Reading: "queue_depth", SubjectKind: "queue", SubjectName: "thumbnails",
		SubjectVar: "gallery.thumbs", Threshold: 1, On: "dead", ForSeconds: 600}, "Stuck")
	var named string
	for _, line := range strings.Split(d.Content, "\n") {
		if strings.Contains(line, "tako dlq") {
			named = strings.Fields(line)[2]
			break
		}
	}
	if named == "" {
		t.Fatal("no dlq command in the body")
	}
	if !async[named] {
		t.Errorf("body says `tako dlq %s`, but the async services are %v — the command errors when pasted",
			named, keysOfBool(async))
	}
}
