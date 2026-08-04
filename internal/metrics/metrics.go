// Package metrics is the ONE definition of every metric tako writes on
// the user's behalf, and of the labels those metrics carry.
//
// It exists because the two halves used to agree by hand. The runtime
// writes a series; the provisioner writes a PromQL query that reads it;
// the deploy declares a descriptor that describes it. Nothing checked
// that the three named the same thing, and every label defect in Stage
// 10 came out of that gap:
//
//   - a `route` label declared in a descriptor and empty on all 88 series
//   - a `job` label spelled three ways
//   - a `subscription_id` naming a topic
//   - `response_class="2xx"` where Pub/Sub writes `ack`
//
// In each case the code was well-formed and the query matched nothing,
// which reads exactly like a healthy system. A misspelling here is now a
// failing test rather than an alert that is quiet for the wrong reason.
//
// These names are a CONTRACT. They appear in the queries of every policy
// tako has ever deployed, in dashboards users build by hand, and in the
// SDK's request readers. Changing one is a breaking change, and
// testdata/lock.golden.txt exists to make that fact arrive at the moment
// of the edit rather than at the moment an alert stops firing.
package metrics

import (
	"fmt"
	"sort"
	"strings"
)

// Prefix is the Cloud Monitoring namespace for everything tako writes,
// its own metrics and the user's Counters and Histograms alike.
const Prefix = "custom.googleapis.com/tako/"

// Resource is the monitored resource every tako metric is written
// against.
//
// generic_task, NOT cloud_run_revision: user-defined metrics may only be
// written against a specific set of resource types and Cloud Run's own
// is not among them — the write is refused outright. generic_task is
// where Google points serverless writers, and its schema fits: namespace
// scopes the deployment, job names the service, task_id identifies the
// instance.
const Resource = "generic_task"

// The resource labels. These are generic_task's own, not tako's
// invention, but which VALUE goes in each one is tako's decision and is
// what every query scopes on.
const (
	// ResourceNamespace is "<app>-<env>". It is what keeps prod and a
	// preview environment from sharing a series, and therefore from
	// sharing an alert. It is also why a hand-written matcher over
	// tako's metrics cannot be correct at declaration time: the value
	// does not exist until a deploy picks an env.
	ResourceNamespace = "namespace"
	// ResourceJob is the tako service name — the name the user wrote,
	// not the env-prefixed deployed one.
	ResourceJob = "job"
	// ResourceTaskID is the instance. Every instance of a revision would
	// otherwise share one cumulative series, and concurrent writers to a
	// single series is either a rejection or nonsense.
	ResourceTaskID = "task_id"
	// ResourceLocation is the region.
	ResourceLocation = "location"
)

// ResourceLabels is the closed set, in the order a lock file lists them.
var ResourceLabels = []string{ResourceNamespace, ResourceJob, ResourceTaskID, ResourceLocation}

// The metric labels tako writes.
const (
	// LabelRoute is the matched route PATTERN, never the path:
	// /api/photos/{id} is one series no matter how many photos exist,
	// where the raw path would mint one per photo.
	LabelRoute = "route"
	// LabelStatus is the response class, not the code — 5xx rather than
	// 503. Three digits would be three times the series for a
	// distinction almost no alert draws.
	LabelStatus = "status"
	// LabelRevision rides on every metric so a shift in a number can be
	// traced to the deploy that caused it. It costs no extra series:
	// task_id is already finer-grained.
	LabelRevision = "revision"
)

// UnmatchedRoute is the route label for a request no pattern matched —
// a 404. Bucketing them under one value keeps a flood of unknown paths
// from minting a series each.
const UnmatchedRoute = "<unmatched>"

// StatusClasses is every value LabelStatus takes, and the list is
// CLOSED: a query matching anything else matches nothing, which is the
// failure this package exists to make loud.
//
// 1xx is here because the runtime can emit it. It was missing from the
// descriptor's documented set for as long as both were maintained by
// hand — harmless, and exactly the kind of drift that is invisible
// until something depends on it.
var StatusClasses = []string{"1xx", "2xx", "3xx", "4xx", "5xx"}

// Label is one metric label and, where the set is closed, every value it
// takes.
type Label struct {
	Key         string
	Description string
	// Values is the closed vocabulary, or nil when the label is
	// open-ended (a route is whatever the user declared).
	Values []string
}

// Metric is one metric tako writes without being asked.
type Metric struct {
	// Name is the short name. The full metric type is Prefix + Name.
	Name        string
	Kind        string // CUMULATIVE
	ValueType   string // INT64 | DISTRIBUTION
	Description string
	Labels      []Label
}

// Type is the Cloud Monitoring metric type.
func (m Metric) Type() string { return Prefix + m.Name }

// The short names, as constants: pkg/runtime re-exports them and its
// exported surface is locked, so they must be usable in a const
// declaration. Generated code in users' repositories imports those
// names, which is why a var here would be a breaking change for a
// refactor that changes nothing.
const (
	RequestsName  = "requests"
	RequestMsName = "request_ms"
)

// Requests counts what every tako service serves, per endpoint.
//
// It is not duplication of Cloud Run's request_count: Cloud Run's own
// `route` label is NEVER POPULATED for an ordinary service — measured,
// 88 series, empty on every one — so alerting on one endpoint is
// impossible from platform metrics alone. tako writes the route because
// tako is the only one that knows it.
var Requests = Metric{
	Name: RequestsName, Kind: "CUMULATIVE", ValueType: "INT64",
	Description: "Requests served, by route and status class (tako).",
	Labels: []Label{
		{Key: LabelRoute, Description: "The declared route pattern."},
		{Key: LabelStatus, Description: "Status class.", Values: StatusClasses},
		{Key: LabelRevision, Description: "The Cloud Run revision."},
	},
}

// RequestMs times the same requests. No status label: a latency
// distribution split by status class answers a question nobody asks and
// multiplies the series by five.
var RequestMs = Metric{
	Name: RequestMsName, Kind: "CUMULATIVE", ValueType: "DISTRIBUTION",
	Description: "Request latency in milliseconds, by route (tako).",
	Labels: []Label{
		{Key: LabelRoute, Description: "The declared route pattern."},
		{Key: LabelRevision, Description: "The Cloud Run revision."},
	},
}

// Framework is every metric tako writes on its own initiative, as
// opposed to the Counters and Histograms a user declares.
var Framework = []Metric{Requests, RequestMs}

// LabelSet is every label a query may legally match on for this metric:
// its own, plus the resource labels every selector scopes with.
func (m Metric) LabelSet() map[string]bool {
	out := map[string]bool{}
	for _, l := range ResourceLabels {
		out[l] = true
	}
	// Present on every selector tako builds, and derived rather than
	// written — see the alert reference's scoped-label rule.
	out["monitored_resource"] = true
	for _, l := range m.Labels {
		out[l.Key] = true
	}
	return out
}

// ByType finds a framework metric by its full Cloud Monitoring type.
func ByType(metricType string) (Metric, bool) {
	for _, m := range Framework {
		if m.Type() == metricType {
			return m, true
		}
	}
	return Metric{}, false
}

// StatusClass maps an HTTP status to the label value. It is here rather
// than in the runtime so the values a writer produces and the values a
// lock file promises cannot be maintained separately.
func StatusClass(status int) string {
	switch {
	case status >= 500:
		return "5xx"
	case status >= 400:
		return "4xx"
	case status >= 300:
		return "3xx"
	case status >= 200:
		return "2xx"
	}
	return "1xx"
}

// Lock renders the contract as text, for the golden that pins it.
// Sorted so the file is a stable artifact and a diff names what changed.
func Lock() string {
	var b strings.Builder
	fmt.Fprintf(&b, "prefix   %s\n", Prefix)
	fmt.Fprintf(&b, "resource %s\n", Resource)
	fmt.Fprintf(&b, "resource labels %s\n\n", strings.Join(ResourceLabels, ", "))
	ms := append([]Metric(nil), Framework...)
	sort.Slice(ms, func(i, j int) bool { return ms[i].Name < ms[j].Name })
	for _, m := range ms {
		fmt.Fprintf(&b, "%s\n  %s %s\n", m.Type(), m.Kind, m.ValueType)
		labels := append([]Label(nil), m.Labels...)
		sort.Slice(labels, func(i, j int) bool { return labels[i].Key < labels[j].Key })
		for _, l := range labels {
			line := "  label " + l.Key
			if len(l.Values) > 0 {
				line += " = " + strings.Join(l.Values, " | ")
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}
