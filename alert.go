package tako

import "time"

// Alerting: a condition on a metric, and somewhere to send it when the
// condition holds.
//
// Every value here is a declaration read at analysis time — nothing in
// this file runs in a request path, and nothing runs locally at all.
// Alerts are evaluated by Cloud Monitoring and delivered by it, which is
// the point: an alert routed through your own service goes quiet exactly
// when the thing it watches has gone wrong.

// ChannelHandle is a declared alert destination. The declaring VARIABLE
// is its identity, like every other service tako deploys — the argument
// is the address, not the name.
type ChannelHandle struct {
	kind   string // email, google_chat, webhook_tokenauth, pubsub
	target string
}

// Email sends alerts to one address.
//
// Some destinations need a human step before they deliver — an address
// verified, an app installed in a space. tako never performs that step
// for you; deploy prints what to do and links to the exact console page.
func Email(address string) *ChannelHandle {
	return &ChannelHandle{kind: "email", target: address}
}

// ChatSpace sends alerts to a Google Chat space, named by its space id
// ("spaces/AAAA1234"). The Cloud Monitoring app must be installed in the
// space before it can deliver; deploy says so and links to it.
func ChatSpace(space string) *ChannelHandle {
	return &ChannelHandle{kind: "google_chat", target: space}
}

// Webhook POSTs the incident to a URL.
func Webhook(url string) *ChannelHandle {
	return &ChannelHandle{kind: "webhook_tokenauth", target: url}
}

// PubSub publishes the incident to a declared topic, so a subscriber can
// route or escalate it in your own code.
//
// The message body is Google's incident JSON, not a tako type: define a
// struct with the fields you need and declare the topic over it
// (reference/alerts.md carries one to copy). tako deliberately does not
// own that shape — a payload it froze into the SDK would make a Google
// change an API break.
//
// Remember what a Pub/Sub destination cannot do: your subscriber runs in
// your app, so it is unreachable in exactly the outage a page is for.
// Use it for routing and enrichment, and keep a channel that does not
// depend on you.
func PubSub[T any](topic *TopicHandle[T]) *ChannelHandle {
	return &ChannelHandle{kind: "pubsub", target: topic.name}
}

// Metric is something a condition can be written about. It is NOT a
// declared Counter or Histogram directly: those are raw recordings, and
// a raw recording does not say which question you are asking of it.
//
//	tako.Rate(uploads)   // uploads per second
//	tako.Count(uploads)  // uploads in the window
//	tako.P99(thumbMs)    // 99th percentile latency
//
// The interface is sealed — only SDK types implement it — so the
// analyzer can enumerate every subject an alert could name, and so the
// AGGREGATION is chosen where it is visible. Metrics are written one
// cumulative series per instance, and an alert that aggregates them the
// wrong way looks configured while being noise: Below(Rate(uploads), 1)
// asks for a rate under one PER SECOND, which is almost always true.
// Making the reading explicit is what stops that from being the default.
type Metric interface {
	metricSubject() metricRef
}

// metricRef is what the analyzer reads: which kind of thing is being
// watched, which declaration, and how to aggregate it.
type metricRef struct {
	kind string // counter, histogram, error_rate, dead_letters, runs
	name string // the declared name, or "" when the subject is a handler
	id   int    // handler id, for subjects that are handler vars
	// on distinguishes what a derived metric is measured against when
	// more than one kind of subject is legal: "service" or "handler".
	on string
	// agg is the reading: rate, count, percentile, ratio.
	agg string
	// q is the quantile when agg is "percentile".
	q float64
	// filters are extra PromQL label matchers for a Platform metric.
	filters []string
}

// Recording is a declared Counter or Histogram — something values were
// recorded into. Sealed, like Metric.
//
// Rate and Count take either, because "how fast" and "how many" are the
// same question whichever recording you ask: a histogram counts its
// observations. Without this a histogram could only be asked about its
// SHAPE and never its volume — you could alert that thumbnails got slow
// but not that none were made, which is the louder failure.
type Recording interface {
	recordingRef() metricRef
}

func (c *CounterHandle) recordingRef() metricRef {
	return metricRef{kind: "counter", name: c.name}
}

func (h *HistogramHandle) recordingRef() metricRef {
	return metricRef{kind: "histogram", name: h.name}
}

// Rate is a recording's increase per second, summed across instances.
// For a histogram it is observations per second — throughput.
func Rate(r Recording) Metric {
	ref := r.recordingRef()
	ref.agg = "rate"
	return derived{ref: ref}
}

// Count is how many were recorded within the window, summed across
// instances — the reading for "fewer than one upload in half an hour",
// and for a histogram, "no thumbnails were made at all".
func Count(r Recording) Metric {
	ref := r.recordingRef()
	ref.agg = "count"
	return derived{ref: ref}
}

// Sum is the total of a histogram's observed values over the window —
// milliseconds of work done, bytes transferred — as opposed to how many
// observations there were.
func Sum(h *HistogramHandle) Metric {
	return derived{ref: metricRef{kind: "histogram", name: h.name, agg: "sum"}}
}

// Mean is a histogram's average observed value over the window.
//
// It is Sum over Count, and both halves are counter-correct rather than
// sample averages: averaging the raw cumulative totals would report the
// mean of running sums, which is not a latency. Verified against a real
// distribution — 4216ms over 14 observations is 301.1ms, matching what
// Cloud Monitoring reports as the distribution's own mean.
//
// Prefer a Percentile for latency. A mean hides the tail that wakes you
// up; it is here because "the average got worse" is still a real
// question for throughput-shaped work.
func Mean(h *HistogramHandle) Metric {
	return derived{ref: metricRef{kind: "histogram", name: h.name, agg: "mean"}}
}

// derived is a metric expression tako writes the query for.
type derived struct{ ref metricRef }

func (d derived) metricSubject() metricRef { return d.ref }

// Percentile is a histogram's q-quantile across instances, q in (0,1):
// Percentile(thumbMs, 0.99) is the 99th.
//
// Any quantile works, which was not true of the first design. Cloud
// Monitoring's aligner enum offers exactly four (99, 95, 50, 05), and
// Y1a took that for a fact about metrics; it is a property of one API.
// Compiled as PromQL, `histogram_quantile(0.87, …)` answers 473.17ms on
// the same data where the aligner path answers nothing at all — measured,
// not assumed (task log, 2026-08-02).
//
// A histogram has no default quantile: which one you mean IS the
// question, so it is written at the call site.
func Percentile(h *HistogramHandle, q float64) Metric {
	return derived{ref: metricRef{kind: "histogram", name: h.name, agg: "percentile", q: q}}
}

// Target is something a service-level alert can be measured on: the
// package's app service (tako.Service) or one async handler, which is
// its own deployable. Sealed, like Metric.
type Target interface {
	alertTarget() metricRef
}

func (s *ServiceHandle) alertTarget() metricRef {
	return metricRef{kind: "service"}
}

func (h Handler) alertTarget() metricRef {
	return metricRef{kind: "handler", id: h.id}
}

// One ROUTE is a target too: request_count and request_latencies both
// carry a `route` label, so "this endpoint is erroring" is answerable
// without alerting on the whole service it shares.
func (e Endpoint[Req, Res]) alertTarget() metricRef {
	return metricRef{kind: "route", name: e.route}
}

// A site is its routes — the static mount and anything served under it.
func (t *SiteHandle) alertTarget() metricRef {
	return metricRef{kind: "site", name: t.dir}
}

// Consumer is something that consumes messages and can therefore fall
// behind or give up: a Queue, or a Subscribe handler. Subscribers
// dead-letter independently of one another, so each is its own subject.
type Consumer interface {
	consumerRef() metricRef
}

func (q *QueueHandle[T]) consumerRef() metricRef {
	return metricRef{kind: "queue", name: q.name}
}

func (h Handler) consumerRef() metricRef {
	return metricRef{kind: "subscriber", id: h.id}
}

// Platform names a Google Cloud metric directly, for the cases the
// presets and derived readings do not cover.
//
// The name is Google's, so it is a string — pretending otherwise would
// be a lie — but it must be constant, and tako derives the rest: the
// PromQL mapping (first "/" becomes ":", other specials become "_") and
// the monitored-resource matcher that a bare selector is rejected
// without. Find names with `tako metrics <search>`; there are more than
// eight thousand in a project, which is why there is no list in the docs.
//
//	tako.Above(tako.Platform("run.googleapis.com/container/network/sent_bytes_count"),
//	    1<<30, tako.For(10*time.Minute))
func Platform(metricType string, filters ...string) Metric {
	return derived{ref: metricRef{kind: "platform", name: metricType, filters: filters}}
}

// Window is when a condition is measured. The knobs are deliberately
// not hidden: which period a metric is reduced over changes what the
// alert MEANS, so it is stated rather than inferred.
type Window struct {
	// For is how long the breach must persist before the alert fires.
	//
	// On an Absent condition it is the LOOKBACK instead — "no data for
	// 26 hours" — because absence has nothing to sustain.
	For time.Duration
	// Every is the period each series is reduced into: the rate window
	// for Rate, the counting window for Count, the bucket a Percentile
	// is computed over.
	//
	// Zero derives it — 60s for rate, ratio and percentile readings,
	// where the period only smooths. Count has no defensible default,
	// because the period IS the count, so leaving Every zero there is an
	// analysis error rather than a silent guess.
	Every time.Duration
}

// For is the short spelling of Window{For: d}, for the common case where
// the reduction period does not matter.
func For(d time.Duration) Window { return Window{For: d} }

// Condition is a metric compared against a threshold over a window.
// Threshold and window are both required: an alert with no threshold has
// nothing to compare, and one with no window fires on a single sample.
type Condition struct {
	op        string // above, below, absent
	subject   metricRef
	threshold float64
	window    Window
}

// Above holds while the metric stays over threshold for the window.
func Above(m Metric, threshold float64, w Window) Condition {
	return Condition{op: "above", subject: m.metricSubject(), threshold: threshold, window: w}
}

// Below holds while the metric stays under threshold for the window —
// the shape for "this stopped happening as often as it should".
func Below(m Metric, threshold float64, w Window) Condition {
	return Condition{op: "below", subject: m.metricSubject(), threshold: threshold, window: w}
}

// Absent holds when the metric receives no data at all across the
// window's lookback. It takes no threshold: absence is the condition.
//
// This compiles to a PromQL absent_over_time, not to Cloud Monitoring's
// conditionAbsent, which caps at 23h30m — shorter than a daily cron's
// own period, so it could never watch one. A PromQL lookback has no such
// ceiling: [26h] and [7d] are both accepted.
func Absent(m Metric, w Window) Condition {
	return Condition{op: "absent", subject: m.metricSubject(), window: w}
}

// AlertHandle is a declared alert. Like every declaration the variable
// is the identity: var Stuck deploys as <app>-alert-stuck.
type AlertHandle struct {
	when   Condition
	notify []*ChannelHandle
}

// Alert fires when the condition holds, notifying every channel given.
//
// At least one channel is a PARAMETER rather than a slice or a config
// field, so an alert nobody receives does not compile:
//
//	var Stuck = tako.Alert(tako.Above(tako.DeadLetters(thumbs), 0, 5*time.Minute), Oncall)
//	var Broken = tako.Alert(tako.Above(tako.ErrorRate(api), 0.05, 5*time.Minute), Oncall, Room)
//
// Alerts are evaluated in the cloud only. Locally `tako dev` lists them
// and runs none — an evaluator with different windowing than Cloud
// Monitoring's would be worse than none, because it would disagree with
// production about when you get woken up.
func Alert(when Condition, notify *ChannelHandle, also ...*ChannelHandle) AlertHandle {
	return AlertHandle{when: when, notify: append([]*ChannelHandle{notify}, also...)}
}

// Presets: named situations.
//
// Each is an ordinary Condition, so it composes with tako.Alert like any
// other — but it names a failure instead of a metric, and derives the
// query, the reading and the window from what tako already knows about
// the thing being watched. The raw vocabulary above stays for YOUR
// counters and histograms, which tako cannot name situations for.
//
// Every preset takes an optional trailing Window. The defaults are
// documented per preset and stated in reference/alerts.md; they are
// defaults, not a ceiling.

// windowOr picks the caller's window, or the preset's default.
func windowOr(w []Window, def Window) Window {
	if len(w) > 0 {
		return w[0]
	}
	return def
}

// DeadLettered fires when at least n messages have given up — exhausted
// their retries and landed on the dead-letter topic — within the window
// (default: 5 minutes).
//
// It takes a queue or a SUBSCRIBER, because subscribers fan out with
// independent fates: one giving up says nothing about the others.
//
// Pair it with Backlogged. This one only fires AFTER retries are spent,
// so a consumer that is merely down shows nothing here for a while.
func DeadLettered(c Consumer, n int, w ...Window) Condition {
	ref := c.consumerRef()
	ref.agg = "dead_letters"
	return Condition{op: "above", subject: ref, threshold: float64(n),
		window: windowOr(w, Window{Every: 5 * time.Minute})}
}

// Backlogged fires when the oldest unacknowledged message has been
// waiting longer than age (default window: evaluated every minute).
//
// This is the "work is piling up" alert, and it is the one that catches
// a consumer that is down or too slow — before anything has given up.
func Backlogged(c Consumer, age time.Duration, w ...Window) Condition {
	ref := c.consumerRef()
	ref.agg = "backlog_age"
	return Condition{op: "above", subject: ref, threshold: age.Seconds(),
		window: windowOr(w, Window{For: 5 * time.Minute})}
}

// CronFailed fires when a cron ran and failed at least times within the
// window (default: 1 hour) — its route answering 5xx.
func CronFailed(cron Handler, times int, w ...Window) Condition {
	return Condition{op: "above", subject: metricRef{kind: "cron_failed", id: cron.id},
		threshold: float64(times), window: windowOr(w, Window{Every: time.Hour})}
}

// CronMissed fires when a cron has not run at all for twice its own
// schedule — a 6-hourly job gets a 12-hour lookback, a daily one 26
// hours. tako reads the schedule from the declaration, so there is no
// interval to repeat and no chance of the two disagreeing.
//
// It catches a schedule that never fired AND a run that never arrived.
// It does NOT catch a run that arrived and failed: that is CronFailed.
func CronMissed(cron Handler, w ...Window) Condition {
	return Condition{op: "absent", subject: metricRef{kind: "cron_missed", id: cron.id},
		window: windowOr(w, Window{})}
}

// JobFailed fires when at least times executions of the job finished
// unsuccessfully within the window (default: 1 hour).
func JobFailed[T any](job *JobHandle[T], times int, w ...Window) Condition {
	return Condition{op: "above", subject: metricRef{kind: "job_failed", name: job.name},
		threshold: float64(times), window: windowOr(w, Window{Every: time.Hour})}
}

// Erroring fires when the 5xx share of requests exceeds fraction (0–1)
// for the window (default: 5 minutes).
//
// The target may be a whole service, one async handler, a single API or
// Page route, or a Site — request metrics carry a route label, so an
// endpoint can be watched without alerting on everything beside it.
func Erroring(t Target, fraction float64, w ...Window) Condition {
	ref := t.alertTarget()
	ref.agg = "error_rate"
	return Condition{op: "above", subject: ref, threshold: fraction,
		window: windowOr(w, Window{For: 5 * time.Minute})}
}

// SlowerThan fires when the given quantile of request latency exceeds d
// for the window (default: 5 minutes). Same targets as Erroring.
//
//	tako.SlowerThan(Photos, 2*time.Second, 0.99)
func SlowerThan(t Target, d time.Duration, quantile float64, w ...Window) Condition {
	ref := t.alertTarget()
	ref.agg = "latency"
	ref.q = quantile
	return Condition{op: "above", subject: ref, threshold: float64(d.Milliseconds()),
		window: windowOr(w, Window{For: 5 * time.Minute})}
}

// AtCapacity fires when a service sits at its maximum instance count —
// scaled out as far as it is allowed and shedding whatever arrives next.
//
// It takes no threshold: tako reads MaxInstances from the service's
// footprint, so the number lives in one place.
func AtCapacity(t Target, w ...Window) Condition {
	ref := t.alertTarget()
	ref.agg = "at_capacity"
	return Condition{op: "above", subject: ref,
		window: windowOr(w, Window{For: 10 * time.Minute})}
}

// SingletonDown fires when a singleton's exactly-one instance is not
// running.
//
// A singleton answers nothing but its own health check, so no request
// metric can see it — this is the only check for a primitive whose
// entire promise is that one of it exists.
func SingletonDown(loop Handler, w ...Window) Condition {
	return Condition{op: "below", subject: metricRef{kind: "singleton_down", id: loop.id},
		threshold: 1, window: windowOr(w, Window{For: 5 * time.Minute})}
}
