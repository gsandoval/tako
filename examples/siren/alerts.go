package siren

import "time"

import "github.com/gsandoval/tako"

// Every condition tako can express, declared once, against the switches
// in app.go. This file IS the acceptance criterion for alerting: if a
// constructor is not used here, nothing demands it and it does not ship.
//
// THE WINDOWS AND THRESHOLDS HERE ARE TUNED FOR THE WALK, NOT FOR
// PRODUCTION. Nobody wants a two-minute dead-letter-triage alert; the
// realistic value is a day. They are short so that a gate can watch
// every one of them fire in an afternoon rather than over a week —
// gallery's `Silent` carries the same note for the same reason.
//
// The first walk left fourteen of twenty-two unfired and the write-up
// called it "needing time". It was not time, it was these numbers. Only
// two things are actually fixed: Window.Every must be 1m–25h (Cloud
// Monitoring's limit), and the 60s metric flush plus ingestion lag puts
// a couple of minutes under everything.

// ── where alerts go ─────────────────────────────────────────────────
//
// Three destinations, because they fail differently. Email is the one
// that keeps working when siren does not. The webhook and the topic
// both land back inside this app, which is the caveat worth
// demonstrating rather than only documenting: a route through your own
// service is unreachable in exactly the outage a page is for.

var (
	oncall = tako.AlertEmail("darchitect@gmail.com")
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
		tako.RequestsErroring(Boom, 0.05, tako.Window{For: 2 * time.Minute}), oncall)

	Slowing = tako.Alert(
		tako.RequestsSlow(Slow, 2*time.Second, 0.99, tako.Window{For: 2 * time.Minute}), oncall, hook)
)

// ── queues ──────────────────────────────────────────────────────────
//
// Four situations, two constructors, one boolean. The live queue and
// the dead-letter queue are the same two gauges on two subscriptions;
// DeadLetters is the only thing that differs.

var (
	// The consumer is not keeping up.
	Piling = tako.Alert(
		tako.QueueBacklogged(work, 50, tako.QueueWatch{For: 2 * time.Minute}), oncall)

	// Work is going stale — nothing has failed, it is just not moving.
	Stalling = tako.Alert(
		tako.QueueStalled(work, time.Minute, tako.QueueWatch{For: 2 * time.Minute}), oncall)

	// Anything at all in the dead-letter queue. One is the honest
	// threshold: there is no good number of dead letters.
	Poisoned = tako.Alert(
		tako.QueueBacklogged(work, 1, tako.QueueWatch{DeadLetters: true, For: 2 * time.Minute}),
		oncall, feed)

	// Nobody is triaging: poison that has sat unread since yesterday.
	Untriaged = tako.Alert(
		tako.QueueStalled(work, 2*time.Minute, tako.QueueWatch{DeadLetters: true, For: 2 * time.Minute}), oncall)

	// A subscriber has its own fate, and its own dead-letter queue.
	SubPoisoned = tako.Alert(
		tako.QueueBacklogged(OnAlert, 1, tako.QueueWatch{DeadLetters: true, For: 2 * time.Minute}), oncall)
)

// ── scheduled and background work ───────────────────────────────────

var (
	// The lookback is twice the schedule when left unset; 10 minutes
	// here so a walk does not take an hour.
	Missed = tako.Alert(
		tako.CronMissed(Sweep, tako.Window{For: 8 * time.Minute}), oncall)

	// A cron that RAN and failed is a different alert from one that
	// never arrived.
	Failing = tako.Alert(
		tako.CronFailed(Flaky, 1, tako.Window{Every: 5 * time.Minute}), oncall)

	JobBad = tako.Alert(
		tako.JobFailed(Rebuild, 1, tako.Window{Every: 5 * time.Minute}), oncall)

	// The only check there is for a singleton.
	LoopDown = tako.Alert(
		tako.SingletonDown(Ticker, tako.Window{For: 2 * time.Minute}), oncall)

	// No threshold: the ceiling is Grind's own MaxInstances, so the
	// number lives in one place.
	Maxed = tako.Alert(
		tako.ServiceAtCapacity(Grind, tako.Window{For: 2 * time.Minute}), oncall)
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
		tako.WhenBelow(hits.Rate(), 0.01, tako.Window{For: 5 * time.Minute}), oncall)

	// Any quantile works, not only the four Cloud Monitoring's aligner
	// enum offers.
	Grinding = tako.Alert(
		tako.WhenAbove(workMs.Percentile(0.99), 5000, tako.Window{For: 2 * time.Minute}), oncall)

	// Counting needs a period, because the period IS the count.
	Idle = tako.Alert(
		tako.WhenBelow(hits.Count(), 1, tako.Window{For: 5 * time.Minute, Every: time.Minute}), oncall)

	// Total work done, as opposed to how many pieces.
	Overworked = tako.Alert(
		tako.WhenAbove(workMs.Sum(), 20000, tako.Window{Every: 5 * time.Minute}), oncall)

	// A mean hides the tail; it is here because "the average got worse"
	// is still a real question for throughput-shaped work.
	Sluggish = tako.Alert(
		tako.WhenAbove(workMs.Mean(), 800, tako.Window{For: 2 * time.Minute}), oncall)

	// Throughput, from a histogram: it counts its observations.
	NoThroughput = tako.Alert(
		tako.WhenBelow(workMs.Rate(), 0.001, tako.Window{For: 5 * time.Minute}), oncall)

	// Nothing is writing this metric AT ALL, which is a different
	// question from it reading low — though no longer a trap.
	//
	// The walk found `Quiet` above silent through thirteen minutes with
	// no writer, because a vanished series makes `nothing < 0.01`
	// nothing. WhenBelow now compiles `or vector(0)`, so it catches a
	// stop too. WhenAbsent stays for the case that is genuinely
	// different: when zero is a LEGITIMATE reading and only the series
	// disappearing is news.
	Dark = tako.Alert(
		tako.WhenAbsent(hits.Count(), tako.Window{For: 10 * time.Minute, Every: time.Minute}), oncall)
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
			"run.googleapis.com/container/memory/allocation_time"), 1,
			tako.Window{For: 2 * time.Minute}), oncall)

	// response_class="ack", not "2xx". The walk found this by watching
	// the alert stay quiet while deliveries were obviously happening:
	// Pub/Sub's classes are ack / unreachable / remote_server_5xx, and
	// "2xx" was a value assumed rather than looked up. A label value is
	// exactly as checkable as a label name, and exactly as silent when
	// wrong — nothing errors, the query just matches nothing.
	Pushing = tako.Alert(
		tako.WhenAbove(tako.QueueMetric(work,
			"pubsub.googleapis.com/subscription/push_request_count",
			`response_class="ack"`), 1,
			tako.Window{Every: 5 * time.Minute}), oncall)

	// CPU saturation. This is the alert that could not be written at
	// all before Z3: cpu/utilizations is a DISTRIBUTION, so its bare
	// name never resolves and ServiceMetric produces a policy Cloud
	// Monitoring refuses. A quantile over its buckets is the only
	// reading that means anything.
	// CPU saturation, watched on the WORKER — and which service you
	// watch is the whole substance of this alert.
	//
	// Cloud Run scales out to avoid saturating anything, so per-instance
	// CPU on a service that can scale stays low no matter how hard you
	// push it: ten concurrent CPU-burning requests against the API
	// service produced ten instances and a p99 of 0.169. Grind is capped
	// at one instance, so its work concentrates instead of spreading,
	// which is what makes a saturation threshold mean something.
	//
	// A saturation alert on a freely-scaling service mostly measures
	// how well autoscaling is working.
	Hot = tako.Alert(
		tako.WhenAbove(tako.ServicePercentile(Grind,
			"run.googleapis.com/container/cpu/utilizations", 0.99), 0.2,
			tako.Window{For: time.Minute}), oncall)

	// The cost question, as opposed to the ceiling question Maxed asks.
	// A service pinned at its maximum is a capacity problem; a service
	// running more instances than you budgeted for is a bill.
	//
	// Pointed at the API service rather than the worker, and that is the
	// whole difference between a demonstration and a tautology: Grind is
	// capped at one instance, so "more than zero" is the only threshold
	// it can ever cross and the alert fires the moment anything runs.
	// The API service scales, so a number here means something.
	Crowded = tako.Alert(
		tako.WhenAbove(tako.ServiceInstances(Boom), 2,
			tako.Window{For: 2 * time.Minute}), oncall)

	// The escape hatch, used for what it is actually good at: a reading
	// with no tako object to scope it. This is every topic in the
	// project, which is a real question ("what is Pub/Sub costing me")
	// and the one shape ServiceMetric and QueueMetric cannot express.
	//
	// The first version filtered `topic_id="z2-work"` — an env-prefixed
	// physical name hardcoded into an example, which is precisely the
	// trap the scoped constructors exist to close, committed in the file
	// demonstrating them.
	Billing = tako.Alert(
		tako.WhenAbove(tako.PlatformMetric(
			"pubsub.googleapis.com/topic/byte_cost"), 1,
			tako.Window{Every: 5 * time.Minute}), oncall)
)
