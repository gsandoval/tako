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

// Metric is something a condition can be written about: a declared
// Counter or Histogram, or one of the derived expressions below.
//
// It is a sealed interface — only SDK types implement it — so the
// analyzer can enumerate every subject an alert could name, and so the
// AGGREGATION can be derived from the subject's type rather than left to
// the user. That is the reason conditions take a handle and not a metric
// name: your metrics are written one cumulative series per instance, and
// a condition that forgets to aggregate across instances is per-instance
// noise or silence. A counter alerts on its summed rate, a histogram on
// its 95th percentile across instances; a string could not carry that.
type Metric interface {
	metricSubject() metricRef
}

// metricRef is what the analyzer reads: which kind of thing is being
// watched, and which declaration.
type metricRef struct {
	kind string // counter, histogram, error_rate, dead_letters, runs
	name string // the declared name, or "" when the subject is a handler
	id   int    // handler id, for subjects that are handler vars
	// on distinguishes what a derived metric is measured against when
	// more than one kind of subject is legal: "service" or "handler".
	on string
}

func (c *CounterHandle) metricSubject() metricRef {
	return metricRef{kind: "counter", name: c.name}
}

func (h *HistogramHandle) metricSubject() metricRef {
	return metricRef{kind: "histogram", name: h.name}
}

// derived is a platform metric tako writes the filter for.
type derived struct{ ref metricRef }

func (d derived) metricSubject() metricRef { return d.ref }

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
	return derived{ref: metricRef{kind: "error_rate", on: t.kind, id: t.id}}
}

// DeadLetters counts messages that exhausted their retries and landed on
// a queue's dead-letter topic. Above(DeadLetters(q), 0, …) is "anything
// at all gave up".
func DeadLetters[T any](queue *QueueHandle[T]) Metric {
	return derived{ref: metricRef{kind: "dead_letters", name: queue.name}}
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
	return derived{ref: metricRef{kind: "runs", id: cron.id}}
}

// Condition is a metric compared against a threshold for a sustained
// period. Both are required: an alert with no threshold has nothing to
// compare, and one with no period fires on a single noisy sample.
type Condition struct {
	op        string // above, below, absent
	subject   metricRef
	threshold float64
	window    time.Duration
}

// Above holds while the metric stays over threshold for the given period.
func Above(m Metric, threshold float64, sustained time.Duration) Condition {
	return Condition{op: "above", subject: m.metricSubject(), threshold: threshold, window: sustained}
}

// Below holds while the metric stays under threshold for the given
// period — the shape for "this stopped happening as often as it should".
func Below(m Metric, threshold float64, sustained time.Duration) Condition {
	return Condition{op: "below", subject: m.metricSubject(), threshold: threshold, window: sustained}
}

// Absent holds when the metric receives no data at all for the given
// window. It takes no threshold: absence is the condition.
func Absent(m Metric, window time.Duration) Condition {
	return Condition{op: "absent", subject: m.metricSubject(), window: window}
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
