package provision

// What the notification SAYS, as opposed to when it is sent.
//
// Cloud Monitoring generates a body on its own, and for a PromQL
// condition that body is "A PromQL query was observed at 1" — exact,
// and useless to whoever is holding the pager. It cannot do better: it
// knows a query returned a number and nothing about what the number
// meant.
//
// tako knows all of it at deploy time — which cron, its schedule, the
// lookback that expired, the physical names, the console pages — so it
// writes the body itself rather than leaving the responder to reverse
// a PromQL expression at 3am.
//
// Everything here is STATIC text. Cloud Monitoring supports ${…}
// variables in documentation, but they resolve from the labels of the
// series that fired, and an absence has no series — the variable comes
// out `null` in exactly the alert that needs it most. Text baked at
// deploy time has no such hole.

import (
	"fmt"
	"strings"
	"time"

	"github.com/gsandoval/tako/pkg/analyzer"
)

// alertDoc is the documentation attached to one policy.
type alertDoc struct {
	Subject string
	Content string
	Links   []docLink
}

type docLink struct {
	DisplayName string
	URL         string
}

// documentationFor renders the body for one alert.
func documentationFor(cfg Config, g *analyzer.AppGraph, r analyzer.Resource) alertDoc {
	a := r.Alert
	if a == nil {
		return alertDoc{}
	}
	d := alertDoc{Subject: conditionSentence(cfg, g, r)}
	var b strings.Builder
	subject := a.SubjectName
	if subject == "" && a.SubjectVar != "" {
		subject = shortNames([]string{a.SubjectVar})[0]
	}
	service := serviceOf(g, a.SubjectVar)
	if a.SubjectKind == "queue" || a.SubjectKind == "subscriber" {
		// The consumer, not the declaring package's app service.
		if c := consumerServiceOf(g, a.SubjectName); c != "" {
			service = c
		}
	}
	window := time.Duration(a.ForSeconds) * time.Second
	every := time.Duration(a.EverySeconds) * time.Second
	if every == 0 {
		every = presetEvery(a.Reading)
	}

	switch a.Reading {
	case "cron_missed":
		lookback := window
		if lookback == 0 {
			lookback = 2 * cronPeriod(g, a.SubjectVar)
		}
		fmt.Fprintf(&b, "**No run of cron `%s` has reached `%s` in the last %s.**\n\n",
			subject, cfg.Name(service), humanDuration(lookback))
		b.WriteString("Either the schedule never fired, or it fired and the service never answered.\n")
		b.WriteString("A run that *did* arrive and then failed is a different alert (`CronFailed`) — this one means nothing arrived at all.\n\n")
		fmt.Fprintf(&b, "Schedule: every %s. Run it by hand:\n\n", humanDuration(cronPeriod(g, a.SubjectVar)))
		fmt.Fprintf(&b, "    gcloud scheduler jobs run %s --project %s --location %s\n",
			schedulerJobName(cfg, service, subject), cfg.Project, cfg.Region)
		d.Links = []docLink{schedulerLink(cfg), serviceLogsLink(cfg, service)}

	case "cron_failed":
		fmt.Fprintf(&b, "**Cron `%s` ran and failed at least %s in the last %s.**\n\n",
			subject, times(a.Threshold), humanDuration(every))
		b.WriteString("The schedule is firing — the handler is answering 5xx. Its own log lines carry `cron=" + subject + "`.\n")
		d.Links = []docLink{serviceLogsLink(cfg, service), schedulerLink(cfg)}

	case "queue_depth":
		if a.On == "dead" {
			fmt.Fprintf(&b, "**At least %s have been waiting in `%s`'s dead-letter queue for %s.**\n\n",
				messages(a.Threshold), subject, humanDuration(window))
			b.WriteString("They will not be delivered again on their own — they are on the dead-letter topic, not lost.\n")
			b.WriteString("Both ways a message dies land here: retries exhausted, and a handler returning `tako.Permanent`.\n\n")
			fmt.Fprintf(&b, "    tako dlq %s --project %s --env %s            # peek\n", service, cfg.Project, cfg.Env)
			fmt.Fprintf(&b, "    tako dlq %s --project %s --env %s --replay   # try again\n", service, cfg.Project, cfg.Env)
			fmt.Fprintf(&b, "    tako dlq %s --project %s --env %s --ack      # give up on them\n", service, cfg.Project, cfg.Env)
			d.Links = []docLink{subscriptionLink(cfg, subscriptionOf(cfg, g, a)), serviceLogsLink(cfg, service)}
			break
		}
		fmt.Fprintf(&b, "**At least %s have been waiting in queue `%s` for %s.**\n\n",
			messages(a.Threshold), subject, humanDuration(window))
		b.WriteString("The consumer is not keeping up: it is down, too slow, or scaled too tight.\n")
		b.WriteString("Nothing has given up yet — that would be the same alert on the dead-letter queue.\n")
		d.Links = []docLink{subscriptionLink(cfg, subscriptionOf(cfg, g, a)), serviceLink(cfg, service)}

	case "queue_age":
		if a.On == "dead" {
			fmt.Fprintf(&b, "**A dead letter in `%s` has gone untriaged for %s.**\n\n",
				subject, humanDuration(time.Duration(a.Threshold)*time.Second))
			b.WriteString("Something gave up that long ago and nobody has looked at it since.\n\n")
			fmt.Fprintf(&b, "    tako dlq %s --project %s --env %s            # peek\n", service, cfg.Project, cfg.Env)
			d.Links = []docLink{subscriptionLink(cfg, subscriptionOf(cfg, g, a)), serviceLogsLink(cfg, service)}
			break
		}
		fmt.Fprintf(&b, "**The oldest unacknowledged message in queue `%s` is older than %s.**\n\n",
			subject, humanDuration(time.Duration(a.Threshold)*time.Second))
		b.WriteString("Work is going stale. This fires *before* anything gives up — dead-lettering is the later, louder signal.\n")
		d.Links = []docLink{subscriptionLink(cfg, subscriptionOf(cfg, g, a)), serviceLink(cfg, service)}

	case "job_failed":
		fmt.Fprintf(&b, "**Job `%s` had at least %s finish unsuccessfully in the last %s.**\n\n",
			subject, executions(a.Threshold), humanDuration(every))
		b.WriteString("A failed execution is a task that returned an error or exceeded its timeout.\n")
		d.Links = []docLink{jobLink(cfg, subject)}

	case "error_rate":
		fmt.Fprintf(&b, "**%s is returning 5xx for more than %s of requests, sustained for %s.**\n\n",
			what(a), pct(a.Threshold), humanDuration(window))
		b.WriteString("Measured from tako's own `requests` metric, so the share is per route rather than per service.\n")
		d.Links = []docLink{serviceLogsLink(cfg, service), errorLogsLink(cfg, service)}

	case "latency":
		fmt.Fprintf(&b, "**The p%s of %s is over %dms, sustained for %s.**\n\n",
			quantileLabel(a.Quantile), what(a), int64(a.Threshold), humanDuration(window))
		b.WriteString("Measured from tako's own `request_ms` histogram, aggregated across every instance.\n")
		d.Links = []docLink{serviceLink(cfg, service), serviceLogsLink(cfg, service)}

	case "at_capacity":
		fmt.Fprintf(&b, "**`%s` is at its instance ceiling (%d), sustained for %s.**\n\n",
			cfg.Name(service), maxInstancesOf(g, a.SubjectVar), humanDuration(window))
		b.WriteString("It is scaled as far as its footprint allows; anything arriving beyond that queues or is shed.\n")
		b.WriteString("Raise `Footprint.MaxInstances`, raise `Concurrency`, or make each request cheaper.\n")
		d.Links = []docLink{serviceLink(cfg, service), serviceLogsLink(cfg, service)}

	case "singleton_down":
		fmt.Fprintf(&b, "**Singleton `%s` has had no running instance for %s.**\n\n", subject, humanDuration(window))
		b.WriteString("A singleton answers nothing but its own health check, so no request metric can see it — this is the only check there is.\n")
		b.WriteString("Look for a crash loop: a singleton that panics on boot is restarted forever and serves nothing in between.\n")
		d.Links = []docLink{serviceLink(cfg, service), serviceLogsLink(cfg, service)}

	case "platform":
		metricType := a.MetricType
		if metricType == "" {
			metricType = a.SubjectName
		}
		if a.ScopedTo == "service_instances" {
			metricType = "instances"
		}
		fmt.Fprintf(&b, "**`%s` went %s %g.**\n\n", metricType, a.Op, a.Threshold)
		switch a.ScopedTo {
		case "service_percentile":
			fmt.Fprintf(&b, "The p%s of one of Google's own distributions, read about service `%s`.\n",
				quantileLabel(a.Quantile), cfg.Name(service))
			b.WriteString("Cloud Monitoring publishes a distribution only in parts, so this is a quantile over its buckets rather than a value.\n")
			d.Links = []docLink{serviceLink(cfg, service), metricsExplorerLink(cfg)}
		case "service_instances":
			fmt.Fprintf(&b, "How many instances of `%s` are running — the cost question, as opposed to the ceiling question `ServiceAtCapacity` asks.\n",
				cfg.Name(service))
			d.Links = []docLink{serviceLink(cfg, service), metricsExplorerLink(cfg)}
		case "service_metric":
			fmt.Fprintf(&b, "One of Google's own metrics, read about service `%s`.\n", cfg.Name(service))
			d.Links = []docLink{serviceLink(cfg, service), metricsExplorerLink(cfg)}
		case "queue_metric":
			fmt.Fprintf(&b, "One of Google's own metrics, read about subscription `%s`.\n", subscriptionOf(cfg, g, a))
			d.Links = []docLink{subscriptionLink(cfg, subscriptionOf(cfg, g, a)), metricsExplorerLink(cfg)}
		default:
			b.WriteString("A Google metric named directly with `tako.PlatformMetric` — tako knows nothing about it beyond the comparison you declared, and owns none of its label matchers.\n")
			d.Links = []docLink{metricsExplorerLink(cfg)}
		}

	default: // rate, count, sum, mean, percentile over a declared recording
		fmt.Fprintf(&b, "**`%s` (%s) went %s %g over %s.**\n\n",
			a.SubjectName, readingWord(a), a.Op, a.Threshold, humanDuration(every))
		fmt.Fprintf(&b, "Your own `tako.%s(\"%s\")`, exported as `custom.googleapis.com/tako/%s`.\n",
			strings.Title(a.SubjectKind), a.SubjectName, a.SubjectName) //nolint:staticcheck // ASCII metric names
		d.Links = []docLink{metricsExplorerLink(cfg)}
	}

	// The footer every alert carries. The second sentence is the one
	// that saves a wasted afternoon: these policies are reconciled, so a
	// console edit survives exactly until the next deploy.
	fmt.Fprintf(&b, "\n---\napp `%s` · env `%s` · declared in tako as `%s`", cfg.App, cfg.Env, r.Name)
	if service != "" {
		fmt.Fprintf(&b, " · service `%s`", cfg.Name(service))
	}
	b.WriteString("\n\nThis policy is generated by `tako deploy` from the declaration above. ")
	b.WriteString("Editing it in the console works until the next deploy overwrites it — change the declaration instead.\n")

	d.Content = b.String()
	return d
}

// schedulerJobName is the deployed Cloud Scheduler job for one cron.
//
// It lives here rather than inline at its one call site because the
// documentation prints it as a command a woken human will paste, and a
// name derived twice is a name that can be derived differently — the
// failure mode that made the `job` label disagree three ways.
func schedulerJobName(cfg Config, service, cron string) string {
	return cfg.Name(service) + "-cron-" + strings.ToLower(cron)
}

// consumerServiceOf resolves the service that CONSUMES a queue or topic.
//
// Not the same as serviceOf on the queue's variable: a queue is declared
// in the app package, so a name-prefix match resolves it to the app
// service, while the thing that dead-letters is the worker — its own
// deployable under the partition rule. A dead-letter alert linking to
// the app service's logs would send the responder to a service that
// never touched the message.
func consumerServiceOf(g *analyzer.AppGraph, queue string) string {
	for _, h := range g.Handlers {
		if h.Binding == queue && (h.Kind == "worker" || h.Kind == "subscribe") {
			return h.Service
		}
	}
	return ""
}

// humanDuration writes a duration the way the declaration did: 24h, not
// 24h0m0s. Go's default renders every unit down to seconds, which turns
// "every 24h" into noise in a sentence a human reads once, badly, at
// 3am.
// Built from components rather than by trimming Go's rendering, because
// trimming a "0s" suffix off "10m0s" leaves "10m", and trimming a "0m"
// suffix off THAT leaves "1". The tests caught it; the string form has
// no seam that is safe to cut.
func humanDuration(d time.Duration) string {
	if d < time.Second {
		return d.String()
	}
	h, m, s := int64(d/time.Hour), int64(d/time.Minute)%60, int64(d/time.Second)%60
	var b strings.Builder
	if h > 0 {
		fmt.Fprintf(&b, "%dh", h)
	}
	if m > 0 {
		fmt.Fprintf(&b, "%dm", m)
	}
	if s > 0 {
		fmt.Fprintf(&b, "%ds", s)
	}
	return b.String()
}

// what names the thing an Erroring/SlowerThan alert is measured on: one
// route reads as the route, anything else as its service.
func what(a *analyzer.AlertCondition) string {
	if (a.SubjectKind == "route" || a.SubjectKind == "site") && a.SubjectName != "" {
		return "`" + a.SubjectName + "`"
	}
	return "the service"
}

// quantileLabel renders 0.99 as "99" — the way a human says it.
func quantileLabel(q float64) string {
	return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%g", q*100), "0"), ".")
}

func pct(f float64) string { return fmt.Sprintf("%g%%", f*100) }

func plural(n float64, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%g %s", n, one)
	}
	return fmt.Sprintf("%g %s", n, many)
}

func times(n float64) string      { return plural(n, "failure", "failures") }
func messages(n float64) string   { return plural(n, "message", "messages") }
func executions(n float64) string { return plural(n, "execution", "executions") }

// readingWord says the aggregation in words, since the reading is the
// difference between "uploads stopped" and "uploads got slow".
func readingWord(a *analyzer.AlertCondition) string {
	switch a.Reading {
	case "rate":
		return "per second"
	case "count":
		return "count in the window"
	case "sum":
		return "sum of observations"
	case "mean":
		return "mean observation"
	case "percentile":
		return "p" + quantileLabel(a.Quantile)
	}
	return a.Reading
}

// Console links. Deep links are built from names the deploy already
// owns, so a link that 404s means a name is wrong — which is worth
// finding.
const consoleHost = "https://console.cloud.google.com"

// The Cloud Run detail pages sit under /observability/, which is not
// where they used to be: /run/detail/<region>/<svc>/logs answers
// "URL not found". Verified by loading both in the console — the shapes
// here are the ones it redirects a bare service path to.
func serviceLink(cfg Config, service string) docLink {
	return docLink{"Cloud Run service", fmt.Sprintf("%s/run/detail/%s/%s/observability/metrics?project=%s",
		consoleHost, cfg.Region, cfg.Name(service), cfg.Project)}
}

func serviceLogsLink(cfg Config, service string) docLink {
	return docLink{"Service logs", fmt.Sprintf("%s/run/detail/%s/%s/observability/logs?project=%s",
		consoleHost, cfg.Region, cfg.Name(service), cfg.Project)}
}

// errorLogsLink pre-filters the Logs Explorer to this service's errors,
// which is the query a responder types first.
func errorLogsLink(cfg Config, service string) docLink {
	q := fmt.Sprintf(`resource.labels.service_name="%s" severity>=ERROR`, cfg.Name(service))
	return docLink{"Errors in the logs", fmt.Sprintf("%s/logs/query;query=%s?project=%s",
		consoleHost, URLEscape(q), cfg.Project)}
}

func schedulerLink(cfg Config) docLink {
	return docLink{"Cloud Scheduler", fmt.Sprintf("%s/cloudscheduler?project=%s", consoleHost, cfg.Project)}
}

// subscriptionLink takes the DEPLOYED subscription name, already
// resolved by subscriptionOf — not the queue's logical name. The first
// version prefixed the queue name here and produced a console page that
// answers "Unable to find the resource you requested", which is how the
// query's identical mistake was found.
func subscriptionLink(cfg Config, subscription string) docLink {
	return docLink{"Pub/Sub subscription", fmt.Sprintf("%s/cloudpubsub/subscription/detail/%s?project=%s",
		consoleHost, subscription, cfg.Project)}
}

func jobLink(cfg Config, job string) docLink {
	return docLink{"Cloud Run job", fmt.Sprintf("%s/run/jobs/details/%s/%s/executions?project=%s",
		consoleHost, cfg.Region, cfg.Name(job), cfg.Project)}
}

func metricsExplorerLink(cfg Config) docLink {
	return docLink{"Metrics Explorer", fmt.Sprintf("%s/monitoring/metrics-explorer?project=%s",
		consoleHost, cfg.Project)}
}

// URLEscape percent-encodes a string for a Google console URL: the
// RFC 3986 unreserved set, everything else in %XX.
//
// Hand-rolled rather than url.QueryEscape because the Logs Explorer
// reads its query from a PATH segment (;query=…), not a query
// parameter, so a space must not become "+". The Metrics Explorer's
// filter has the same requirement, which is why this is exported and
// pkg/provision/gcp calls it rather than keeping a second copy.
func URLEscape(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&0xf])
	}
	return b.String()
}
