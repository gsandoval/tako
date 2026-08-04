package siren

import "time"

import "github.com/gsandoval/tako"

// Every condition tako can express, declared once, against the switches
// in app.go. This file IS the acceptance criterion for alerting: if a
// constructor is not used here, nothing demands it and it does not ship.

// ── where alerts go ─────────────────────────────────────────────────
//
// Three destinations, because they fail differently. Email is the one
// that keeps working when siren does not. The webhook and the topic
// both land back inside this app, which is the caveat worth
// demonstrating rather than only documenting: a route through your own
// service is unreachable in exactly the outage a page is for.

var (
	oncall = tako.AlertEmail("ops@example.com")
	hook   = tako.AlertWebhook("https://siren.example.com/incidents")
	feed   = tako.AlertTopic(alerts)
)

// ── requests ────────────────────────────────────────────────────────
//
// The subject is the endpoint var itself. Both readings come from
// tako's own per-endpoint metrics, because Cloud Run publishes a route
// label and leaves it empty on every series — so its request_count
// cannot answer a question about one endpoint at all.

var (
	Erroring = tako.Alert(
		tako.RequestsErroring(Boom, 0.05, tako.Window{For: 5 * time.Minute}), oncall)

	Slowing = tako.Alert(
		tako.RequestsSlow(Slow, 2*time.Second, 0.99, tako.Window{For: 5 * time.Minute}), oncall, hook)
)

// ── queues ──────────────────────────────────────────────────────────
//
// Four situations, two constructors, one boolean. The live queue and
// the dead-letter queue are the same two gauges on two subscriptions;
// DeadLetters is the only thing that differs.

var (
	// The consumer is not keeping up.
	Piling = tako.Alert(
		tako.QueueBacklogged(work, 50, tako.QueueWatch{For: 10 * time.Minute}), oncall)

	// Work is going stale — nothing has failed, it is just not moving.
	Stalling = tako.Alert(
		tako.QueueStalled(work, 15*time.Minute, tako.QueueWatch{For: 5 * time.Minute}), oncall)

	// Anything at all in the dead-letter queue. One is the honest
	// threshold: there is no good number of dead letters.
	Poisoned = tako.Alert(
		tako.QueueBacklogged(work, 1, tako.QueueWatch{DeadLetters: true, For: 10 * time.Minute}),
		oncall, feed)

	// Nobody is triaging: poison that has sat unread since yesterday.
	Untriaged = tako.Alert(
		tako.QueueStalled(work, 24*time.Hour, tako.QueueWatch{DeadLetters: true, For: time.Hour}), oncall)

	// A subscriber has its own fate, and its own dead-letter queue.
	SubPoisoned = tako.Alert(
		tako.QueueBacklogged(OnAlert, 1, tako.QueueWatch{DeadLetters: true, For: 10 * time.Minute}), oncall)
)

// ── scheduled and background work ───────────────────────────────────

var (
	// The lookback is twice the schedule when left unset; 10 minutes
	// here so a walk does not take an hour.
	Missed = tako.Alert(
		tako.CronMissed(Sweep, tako.Window{For: 10 * time.Minute}), oncall)

	// A cron that RAN and failed is a different alert from one that
	// never arrived.
	Failing = tako.Alert(
		tako.CronFailed(Flaky, 1, tako.Window{Every: time.Hour}), oncall)

	JobBad = tako.Alert(
		tako.JobFailed(Rebuild, 1, tako.Window{Every: time.Hour}), oncall)

	// The only check there is for a singleton.
	LoopDown = tako.Alert(
		tako.SingletonDown(Ticker, tako.Window{For: 5 * time.Minute}), oncall)

	// No threshold: the ceiling is Grind's own MaxInstances, so the
	// number lives in one place.
	Maxed = tako.Alert(
		tako.ServiceAtCapacity(Grind, tako.Window{For: 10 * time.Minute}), oncall)
)

// ── your own recordings ─────────────────────────────────────────────
//
// The reading is a method on the handle, and it is mandatory: metrics
// are written one cumulative series per instance, and an alert that
// aggregates them the wrong way looks configured while being noise.

var (
	// Traffic stopped. A rate is per SECOND — 0.01/s is one hit every
	// hundred seconds, which is the kind of number that has to be
	// thought about rather than defaulted.
	Quiet = tako.Alert(
		tako.WhenBelow(hits.Rate(), 0.01, tako.Window{For: 30 * time.Minute}), oncall)

	// Any quantile works, not only the four Cloud Monitoring's aligner
	// enum offers.
	Grinding = tako.Alert(
		tako.WhenAbove(workMs.Percentile(0.99), 5000, tako.Window{For: 10 * time.Minute}), oncall)

	// Counting needs a period, because the period IS the count.
	Idle = tako.Alert(
		tako.WhenBelow(hits.Count(), 1, tako.Window{For: 2 * time.Hour, Every: time.Hour}), oncall)

	// Total work done, as opposed to how many pieces.
	Overworked = tako.Alert(
		tako.WhenAbove(workMs.Sum(), 600000, tako.Window{Every: 10 * time.Minute}), oncall)

	// A mean hides the tail; it is here because "the average got worse"
	// is still a real question for throughput-shaped work.
	Sluggish = tako.Alert(
		tako.WhenAbove(workMs.Mean(), 800, tako.Window{For: 10 * time.Minute}), oncall)

	// Throughput, from a histogram: it counts its observations.
	NoThroughput = tako.Alert(
		tako.WhenBelow(workMs.Rate(), 0.001, tako.Window{For: 30 * time.Minute}), oncall)

	// Nothing is writing this metric AT ALL, which is different from it
	// reading low.
	Dark = tako.Alert(
		tako.WhenAbsent(hits.Count(), tako.Window{For: 6 * time.Hour, Every: time.Hour}), oncall)
)

// ── Google's own metrics ────────────────────────────────────────────
//
// The metric type is Google's, so it is a string. WHICH RESOURCE it is
// about is not: handing tako the object lets it write the matcher from
// the same names the presets use. Spelling it by hand would mean
// writing an env-prefixed physical name that does not exist until after
// a deploy.

var (
	// Memory allocation rather than utilization, and the reason is worth
	// knowing before you pick a metric: utilizations is a DISTRIBUTION,
	// and Cloud Monitoring publishes a distribution only as _bucket,
	// _count and _sum — never under its bare name. tako's preflight says
	// so by name rather than letting the deploy fail on Google's 400.
	Fat = tako.Alert(
		tako.WhenAbove(tako.ServiceMetric(Grind,
			"run.googleapis.com/container/memory/allocation_time"), 1e9,
			tako.Window{For: 10 * time.Minute}), oncall)

	Pushback = tako.Alert(
		tako.WhenAbove(tako.QueueMetric(work,
			"pubsub.googleapis.com/subscription/push_request_count",
			`response_class="4xx"`), 10,
			tako.Window{Every: 5 * time.Minute}), oncall)

	// The escape hatch: no tako object, so every matcher is yours —
	// including the one that says WHICH topic. Omit it and this reads
	// every topic in the project, which is the trap ServiceMetric and
	// QueueMetric exist to close.
	Billing = tako.Alert(
		tako.WhenAbove(tako.PlatformMetric(
			"pubsub.googleapis.com/topic/byte_cost",
			`topic_id="x1-incidents"`), 1<<30,
			tako.Window{Every: time.Hour}), oncall)
)
