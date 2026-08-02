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
}

// Rate is a counter's increase per second, summed across instances.
func Rate(c *CounterHandle) Metric {
	return derived{ref: metricRef{kind: "counter", name: c.name, agg: "rate"}}
}

// Count is how many a counter recorded within the condition's window,
// summed across instances — the reading for "fewer than one upload in
// half an hour".
func Count(c *CounterHandle) Metric {
	return derived{ref: metricRef{kind: "counter", name: c.name, agg: "count"}}
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

// Target is something ErrorRate can be measured on: the package's app
// service (tako.Service) or one async handler, which is its own
// deployable. Sealed like Metric.
type Target interface {
	alertTarget() metricRef
}

func (s *ServiceHandle) alertTarget() metricRef {
	return metricRef{kind: "service"}
}

func (h Handler) alertTarget() metricRef {
	return metricRef{kind: "handler", id: h.id}
}

// ErrorRate is the fraction of requests a service answers with 5xx, over
// the condition's window. Pass tako.Service's handle for the package's
// app service, or an async handler var (a worker, subscriber, runner or
// singleton) for its own deployable.
func ErrorRate(of Target) Metric {
	t := of.alertTarget()
	return derived{ref: metricRef{kind: "error_rate", on: t.kind, id: t.id, agg: "ratio"}}
}

// DeadLetters counts messages that exhausted their retries and landed on
// a queue's dead-letter topic, within the condition's window.
// Above(DeadLetters(q), 0, …) is "anything at all gave up".
//
// Unlike a counter this takes no reading: a per-second dead-letter rate
// is not a question anyone asks, so the count is the only sensible
// aggregation and tako picks it.
func DeadLetters[T any](queue *QueueHandle[T]) Metric {
	return derived{ref: metricRef{kind: "dead_letters", name: queue.name, agg: "count"}}
}

// Runs counts a cron's executions — measured where the cron ARRIVES, on
// the request its schedule makes to its own service, because Cloud
// Scheduler publishes no metrics of its own.
//
// Use it with Absent: "no run in 26 hours" is the alert a daily job
// wants. It fires whether the schedule never triggered or the request
// never arrived, which is more than a scheduler-side metric would catch;
// it does NOT fire when a run arrives and the handler fails — that is
// ErrorRate, a separate alert.
func Runs(cron Handler) Metric {
	return derived{ref: metricRef{kind: "runs", id: cron.id, agg: "count"}}
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
