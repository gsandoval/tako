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

// AlertEmail sends alerts to one address.
//
// The Alert prefix is not decoration: "email" is a general concept and
// spec rule 6 forbids a primitive from owning one, so a future
// tako.Email that SENDS mail stays possible.
func AlertEmail(address string) *ChannelHandle {
	return &ChannelHandle{kind: "email", target: address}
}

// AlertWebhook POSTs the incident to a URL.
func AlertWebhook(url string) *ChannelHandle {
	return &ChannelHandle{kind: "webhook_tokenauth", target: url}
}

// AlertTopic publishes the incident to a declared tako.Topic, so a
// subscriber can route or escalate it in your own code.
//
// Named for what it takes. It was tako.PubSub, which pointed at the
// wrong concept in tako's own API: tako's pub/sub primitive is Topic.
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
func AlertTopic[T any](topic *TopicHandle[T]) *ChannelHandle {
	return &ChannelHandle{kind: "pubsub", target: topic.name}
}

// Metric is something a condition can be written about. It is NOT a
// declared Counter or Histogram directly: those are raw recordings, and
// a raw recording does not say which question you are asking of it.
//
//	uploads.Rate()             // uploads per second
//	uploads.Count()            // uploads in the window
//	thumbMs.Percentile(0.99)   // 99th percentile latency
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

// Readings. Spec rule 6 says no primitive owns a general concept, and
// its own remedy is methods scoped to an instance — so these are methods
// on the handle they read rather than tako.Rate / tako.Count / tako.Sum,
// which claimed five general words at the top of the namespace.
//
// The reading is mandatory, and that is the point. Metrics are written
// one cumulative series per instance, and an alert that aggregates them
// the wrong way looks configured while being noise: a rate under one PER
// SECOND is almost always true. Making the aggregation explicit at the
// call site is what stops that from being the default.

// Rate is the counter's increase per second, summed across instances.
func (c *CounterHandle) Rate() Metric {
	return derived{ref: metricRef{kind: "counter", name: c.name, agg: "rate"}}
}

// Count is how many were recorded within the window, summed across
// instances — the reading for "fewer than one upload in half an hour".
func (c *CounterHandle) Count() Metric {
	return derived{ref: metricRef{kind: "counter", name: c.name, agg: "count"}}
}

// Rate is observations per second — throughput.
//
// A histogram answers "how fast" and "how many" as well as "how big":
// without these you could alert that thumbnails got slow but not that
// none were made, which is the louder failure.
func (h *HistogramHandle) Rate() Metric {
	return derived{ref: metricRef{kind: "histogram", name: h.name, agg: "rate"}}
}

// Count is how many observations landed in the window.
func (h *HistogramHandle) Count() Metric {
	return derived{ref: metricRef{kind: "histogram", name: h.name, agg: "count"}}
}

// Sum is the total of the observed values over the window —
// milliseconds of work done, bytes transferred.
func (h *HistogramHandle) Sum() Metric {
	return derived{ref: metricRef{kind: "histogram", name: h.name, agg: "sum"}}
}

// Mean is the average observed value over the window.
//
// It is Sum over Count, and both halves are counter-correct rather than
// sample averages: averaging the raw cumulative totals would report the
// mean of running sums, which is not a latency.
//
// Prefer Percentile for latency. A mean hides the tail that wakes you
// up; this is here because "the average got worse" is still a real
// question for throughput-shaped work.
func (h *HistogramHandle) Mean() Metric {
	return derived{ref: metricRef{kind: "histogram", name: h.name, agg: "mean"}}
}

// Percentile is the q-quantile across instances, q in (0,1):
// thumbMs.Percentile(0.99) is the 99th.
//
// Any quantile works, which was not true of the first design. Cloud
// Monitoring's aligner enum offers exactly four (99, 95, 50, 05), and
// that was taken for a fact about metrics; it is a property of one API.
// Compiled as PromQL, histogram_quantile(0.87, …) answers 473.17ms on
// the same data where the aligner path answers nothing at all.
//
// A histogram has no default quantile: which one you mean IS the
// question, so it is written at the call site.
func (h *HistogramHandle) Percentile(q float64) Metric {
	return derived{ref: metricRef{kind: "histogram", name: h.name, agg: "percentile", q: q}}
}

// ── tako's own request metrics, as readings ─────────────────────────
//
// Every service records requests and request_ms without being asked, and
// until now only the presets could reach them: RequestsErroring and
// RequestsSlow read them, and nothing in the raw vocabulary could. That
// made the presets a ceiling rather than a starting point — the moment a
// question was one step off a preset, there was nowhere to go.
//
// The gap was never NAMING. PlatformMetric could already spell
// custom.googleapis.com/tako/requests. It was reduction and scoping:
// the raw Google-metric path offers exactly one reduction, max(), where
// these need a rate, an increase or a quantile — and the `namespace`
// label is <app>-<env>, a value that does not exist until a deploy picks
// an environment, so no hand-written matcher could be right.
//
// So these take a tako object and carry the SAME reading methods a
// declared Counter or Histogram carries. No second vocabulary: what you
// know about uploads.Rate() is what you know about Requests(Boom).Rate().

// RequestsHandle reads the request COUNTER for one target.
type RequestsHandle struct {
	subject metricRef
	status  string
}

// RequestMsHandle reads the request-latency HISTOGRAM for one target.
type RequestMsHandle struct {
	subject metricRef
}

// Requests counts what a service, handler, endpoint or site served.
//
//	tako.WhenAbove(tako.Requests(Boom).Rate(), 100, w)
//	tako.WhenAbsent(tako.Requests(Sweep).Count(), w)  // it has not run
//
// The reading is named even for an absence, exactly as it is for a
// declared Counter: WhenAbsent(uploads.Count(), w) reads the same way.
// Absence ignores the reduction — it asks whether the series exists at
// all — but one grammar for every condition beats an exception.
//
// The target supplies the env-prefixed scoping — namespace and job, and
// the route when the target is one endpoint — which is the half that
// cannot be written by hand at declaration time.
func Requests(t Target) *RequestsHandle {
	return &RequestsHandle{subject: t.alertTarget()}
}

// Status narrows to one response class: "2xx", "3xx", "4xx", "5xx".
//
// The class, not the code. tako records a class because three digits
// would be three times the series for a distinction almost no alert
// draws, so `Status("503")` matches nothing — the analyzer refuses it
// rather than letting it deploy quiet.
func (r *RequestsHandle) Status(class string) *RequestsHandle {
	out := *r
	out.status = class
	return &out
}

// Rate is requests per second, summed across instances.
func (r *RequestsHandle) Rate() Metric { return r.reading("rate") }

// Count is how many requests landed in the window.
func (r *RequestsHandle) Count() Metric { return r.reading("count") }

// Fraction is this slice of requests as a share of ALL requests to the
// same target — what RequestsErroring computes, with the class as yours
// to choose.
//
//	tako.WhenAbove(tako.Requests(Boom).Status("5xx").Fraction(), 0.05, w)
//
// The denominator is the same selector without the Status narrowing,
// which is what makes this a share rather than two unrelated numbers
// divided. It is meaningless without a Status, and the analyzer says so.
func (r *RequestsHandle) Fraction() Metric { return r.reading("fraction") }

func (r *RequestsHandle) reading(agg string) Metric {
	ref := r.subject
	ref.kind, ref.agg, ref.name = "requests", agg, r.status
	return derived{ref: ref}
}

// RequestMs times the same requests, in milliseconds.
//
//	tako.WhenAbove(tako.RequestMs(Slow).Percentile(0.99), 2000, w)
//
// No Status: tako does not split the latency distribution by response
// class, because a p99 per class answers a question nobody asks and
// multiplies the series by five.
func RequestMs(t Target) *RequestMsHandle {
	return &RequestMsHandle{subject: t.alertTarget()}
}

// Rate is requests per second, read off the distribution's count.
func (r *RequestMsHandle) Rate() Metric { return r.reading("rate", 0) }

// Count is how many requests were timed in the window.
func (r *RequestMsHandle) Count() Metric { return r.reading("count", 0) }

// Sum is total milliseconds spent serving, over the window.
func (r *RequestMsHandle) Sum() Metric { return r.reading("sum", 0) }

// Mean is the average request in milliseconds. Prefer Percentile: a mean
// hides the tail that wakes you up.
func (r *RequestMsHandle) Mean() Metric { return r.reading("mean", 0) }

// Percentile is the q-quantile in milliseconds, q in (0,1) — what
// RequestsSlow reads, with the quantile and threshold yours.
func (r *RequestMsHandle) Percentile(q float64) Metric { return r.reading("percentile", q) }

func (r *RequestMsHandle) reading(agg string, q float64) Metric {
	ref := r.subject
	ref.kind, ref.agg, ref.q = "request_ms", agg, q
	return derived{ref: ref}
}

// derived is a metric expression tako writes the query for.
type derived struct{ ref metricRef }

func (d derived) metricSubject() metricRef { return d.ref }

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

// Google's own metrics, scoped to a tako object.
//
// The metric type is Google's, so it is a string — pretending otherwise
// would be a lie — but WHICH RESOURCE it is about is not something you
// should have to spell. The first design made you write the label
// matcher yourself:
//
//	tako.Platform("run.googleapis.com/container/memory/utilizations",
//	    `service_name="x1-siren"`)
//
// That name is env-prefixed and only exists after a deploy, so it could
// not be written correctly at declaration time at all: the bare name
// matches nothing, and omitting the filter matches every Cloud Run
// service in the project. Handing tako the object instead lets it write
// the matcher from the same names the presets already use.

// ServiceMetric reads a Cloud Run metric about one tako service — the
// app service, one async handler, or anything else a Target names.
//
//	tako.WhenAbove(tako.ServiceMetric(Grind,
//	    "run.googleapis.com/container/memory/utilizations"), 0.9,
//	    tako.Window{For: 10 * time.Minute})
func ServiceMetric(t Target, metricType string, filters ...string) Metric {
	ref := t.alertTarget()
	ref.kind, ref.agg = "service_metric", "platform"
	ref.name = metricType
	ref.filters = filters
	return derived{ref: ref}
}

// QueueMetric reads a Pub/Sub subscription metric about one queue or
// subscriber — the subscription tako created for it.
func QueueMetric(c Consumer, metricType string, filters ...string) Metric {
	ref := c.consumerRef()
	ref.kind, ref.agg = "queue_metric", "platform"
	ref.name = metricType
	ref.filters = filters
	return derived{ref: ref}
}

// ServicePercentile reads a DISTRIBUTION metric about one tako service.
//
// Google publishes a distribution only as `_bucket`, `_count` and
// `_sum` — never under its bare name — so naming one the way its
// documentation writes it produces a policy Cloud Monitoring refuses.
// Until this existed there was no way to alert on CPU or memory
// utilization at all:
//
//	tako.WhenAbove(tako.ServicePercentile(Grind,
//	    "run.googleapis.com/container/cpu/utilizations", 0.99), 0.9,
//	    tako.Window{For: 10 * time.Minute})
//
// The quantile is a parameter rather than a default for the same reason
// a histogram's Percentile takes one: which quantile you mean IS the
// question.
func ServicePercentile(t Target, metricType string, q float64, filters ...string) Metric {
	ref := t.alertTarget()
	ref.kind, ref.agg = "service_percentile", "platform"
	ref.name = metricType
	ref.q = q
	ref.filters = filters
	return derived{ref: ref}
}

// ServiceInstances is how many instances of a service are running.
//
// ServiceAtCapacity answers "am I at the ceiling"; this answers "how
// many am I paying for", which is a different question and the one that
// turns into a bill. tako supplies the metric type and the state label,
// because there is exactly one right answer for both.
func ServiceInstances(t Target) Metric {
	ref := t.alertTarget()
	ref.kind, ref.agg = "service_instances", "platform"
	return derived{ref: ref}
}

// PlatformMetric names a Google metric with no tako object to scope it —
// a project-wide reading, or a resource tako does not model.
//
// The filters are raw PromQL label matchers, and everything they say is
// yours to get right. Prefer ServiceMetric or QueueMetric whenever the
// thing you are watching is something tako deployed.
func PlatformMetric(metricType string, filters ...string) Metric {
	return derived{ref: metricRef{kind: "platform", name: metricType, agg: "platform", filters: filters}}
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

// Condition is a metric compared against a threshold over a window.
// Threshold and window are both required: an alert with no threshold has
// nothing to compare, and one with no window fires on a single sample.
type Condition struct {
	op        string // above, below, absent
	subject   metricRef
	threshold float64
	window    Window
}

// WhenAbove holds while the metric stays over threshold for the window.
func WhenAbove(m Metric, threshold float64, w Window) Condition {
	return Condition{op: "above", subject: m.metricSubject(), threshold: threshold, window: w}
}

// WhenBelow holds while the metric stays under threshold for the window
// — including when it stops entirely.
//
// That last part is not free, and getting it wrong is silent. A
// comparison needs something to compare: when a metric stops being
// written its series disappears, and `empty < threshold` is empty, so
// the naive query goes quiet exactly when the thing has failed hardest.
// tako compiles the PromQL idiom for it — `(… or vector(0)) < t` —
// which reads a missing series as zero.
//
// So this covers both "slowed down" and "stopped". WhenAbsent is still
// the sharper tool when zero is a LEGITIMATE value and you only want to
// hear about the series vanishing.
//
// One consequence worth knowing before you declare it: a metric nothing
// has ever written reads as zero, so a fresh deploy with no traffic will
// fire once Window.For elapses. That is usually correct — there really
// is no traffic — but it is why For is the knob that matters here.
func WhenBelow(m Metric, threshold float64, w Window) Condition {
	return Condition{op: "below", subject: m.metricSubject(), threshold: threshold, window: w}
}

// WhenAtLeast holds while the metric is at or above n over the window.
//
// It exists because the counting presets compile ">= n" and WhenAbove
// compiles "> n". Rewriting QueueBacklogged(work, 50, w) by hand with
// WhenAbove produces a condition that needs 51 — under-reporting, which
// is the direction nobody notices, and the exact off-by-one the atleast
// operator was introduced to fix. A vocabulary you are invited to fork
// into has to be able to say what the thing you forked said.
//
// Use it for counts of discrete events ("at least one dead letter", "at
// least 3 failures"); WhenAbove is right for rates and ratios, where
// "exceeds" is what you mean.
func WhenAtLeast(m Metric, n float64, w Window) Condition {
	return Condition{op: "atleast", subject: m.metricSubject(), threshold: n, window: w}
}

// WhenAbsent holds when the metric receives no data at all across the
// window's lookback. It takes no threshold: absence is the condition.
//
// This compiles to a PromQL absent_over_time, not to Cloud Monitoring's
// conditionAbsent, which caps at 23h30m — shorter than a daily cron's
// own period, so it could never watch one. A PromQL lookback has no such
// ceiling: [26h] and [7d] are both accepted.
func WhenAbsent(m Metric, w Window) Condition {
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
//	var Stuck = tako.Alert(tako.QueueBacklogged(work, 1,
//	    tako.QueueWatch{DeadLetters: true, For: 10 * time.Minute}), oncall)
//	var Broken = tako.Alert(tako.RequestsErroring(Boom, 0.05,
//	    tako.Window{For: 5 * time.Minute}), oncall, room)
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

// QueueWatch configures a queue condition: which of the two queues to
// watch, and how long the level must hold.
//
// The dead-letter queue is a FIELD rather than a symbol because it is
// not a general concept anyone should have to name — and because both
// queues are read by the same two gauges on the same kind of
// subscription. There is nothing different about them but which one.
type QueueWatch struct {
	// DeadLetters watches the dead-letter queue rather than the live one
	// — the same queue `tako dlq` reads, so the alert and the CLI agree
	// about what is in it.
	//
	// It catches BOTH ways a message dies: retries exhausted, and a
	// handler returning tako.Permanent. The count of Pub/Sub's own
	// dead-letter forwards sees only the first, and the second is the
	// commoner one.
	DeadLetters bool
	// For is how long the level must hold, and it is REQUIRED.
	//
	// A queue crosses any depth for an instant every time a burst
	// arrives, so how long it stays there IS the alert. There is no
	// default worth guessing, exactly as with a Count's period.
	For time.Duration
}

// QueueBacklogged fires when at least n messages are waiting.
//
// On the live queue that means the consumer is not keeping up. On the
// dead-letter queue it means poison is piling up, and n=1 is the honest
// threshold — there is no good number of dead letters.
//
//	tako.QueueBacklogged(work, 500, tako.QueueWatch{For: 10 * time.Minute})
//	tako.QueueBacklogged(work, 1, tako.QueueWatch{DeadLetters: true, For: 10 * time.Minute})
//
// It takes a queue or a SUBSCRIBER, because subscribers fan out with
// independent fates: one falling behind says nothing about the others.
func QueueBacklogged(c Consumer, n int, w QueueWatch) Condition {
	ref := c.consumerRef()
	ref.agg, ref.on = "queue_depth", queueSource(w)
	return Condition{op: "atleast", subject: ref, threshold: float64(n),
		window: Window{For: w.For}}
}

// QueueStalled fires when the oldest waiting message is older than age.
//
// On the live queue that is work going stale — a consumer that is down,
// too slow, or scaled too tight. On the dead-letter queue it is nobody
// triaging: poison that has sat unread since yesterday.
//
//	tako.QueueStalled(work, 15*time.Minute, tako.QueueWatch{For: 5 * time.Minute})
//	tako.QueueStalled(work, 24*time.Hour, tako.QueueWatch{DeadLetters: true, For: time.Hour})
func QueueStalled(c Consumer, age time.Duration, w QueueWatch) Condition {
	ref := c.consumerRef()
	ref.agg, ref.on = "queue_age", queueSource(w)
	return Condition{op: "above", subject: ref, threshold: age.Seconds(),
		window: Window{For: w.For}}
}

// queueSource names which of a consumer's two queues is meant.
func queueSource(w QueueWatch) string {
	if w.DeadLetters {
		return "dead"
	}
	return "live"
}

// CronFailed fires when a cron ran and failed at least times within the
// window (default: 1 hour) — its route answering 5xx.
func CronFailed(cron Handler, times int, w ...Window) Condition {
	return Condition{op: "above", subject: metricRef{kind: "cron_failed", id: cron.id},
		threshold: float64(times), window: windowOr(w, Window{Every: time.Hour})}
}

// CronMissed fires when a cron has not run at all for twice its own
// schedule, capped at 25 hours — a 6-hourly job gets a 12-hour lookback,
// a daily one 25. tako reads the schedule from the declaration, so there
// is no interval to repeat and no chance of the two disagreeing.
//
// The cap is Cloud Monitoring's: it refuses a lookback past 1d1h on a
// custom metric, and tako's request metric is one. A cron slower than
// daily therefore has no window that works — anything the API accepts is
// shorter than the cron's own interval — and the analyzer refuses that
// alert rather than deploying a policy Google will reject.
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

// RequestsErroring fires when the 5xx share of requests exceeds fraction (0–1)
// for the window (default: 5 minutes).
//
// The target may be a whole service, one async handler, a single API or
// Page route, or a Site — request metrics carry a route label, so an
// endpoint can be watched without alerting on everything beside it.
func RequestsErroring(t Target, fraction float64, w ...Window) Condition {
	ref := t.alertTarget()
	ref.agg = "error_rate"
	return Condition{op: "above", subject: ref, threshold: fraction,
		window: windowOr(w, Window{For: 5 * time.Minute})}
}

// RequestsSlow fires when the given quantile of request latency exceeds
// d for the window (default: 5 minutes). Same targets as
// RequestsErroring.
//
// Named for what is MEASURED, not for the target: the target may be a
// service, a handler, one route or a site, so any primitive-shaped
// prefix would be a lie for three of the four.
//
//	tako.RequestsSlow(Photos, 2*time.Second, 0.99)
func RequestsSlow(t Target, d time.Duration, quantile float64, w ...Window) Condition {
	ref := t.alertTarget()
	ref.agg = "latency"
	ref.q = quantile
	return Condition{op: "above", subject: ref, threshold: float64(d.Milliseconds()),
		window: windowOr(w, Window{For: 5 * time.Minute})}
}

// ServiceAtCapacity fires when a service sits at its maximum instance count —
// scaled out as far as it is allowed and shedding whatever arrives next.
//
// It takes no threshold: tako reads MaxInstances from the service's
// footprint, so the number lives in one place.
func ServiceAtCapacity(t Target, w ...Window) Condition {
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
