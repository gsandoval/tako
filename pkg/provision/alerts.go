package provision

// Alert channels and policies: AppGraph → Objects.
//
// The analyzer hands over LOGICAL conditions — "the dead letters of
// queue thumbs", "the p99 of histogram thumb_ms" — and this file maps
// them onto Google's metric types, monitored resources and label
// matchers, because only here are the env prefix and the deployed
// service names known.
//
// Every metric type below was read off the live descriptors rather than
// recalled (task log, Y1): the labels are the ones the API actually
// publishes, and a wrong label name produces a policy that watches
// nothing rather than an error.

import (
	"fmt"
	"strings"
	"time"

	"github.com/gsandoval/tako/pkg/analyzer"
)

// Metric types, verified against projects.metricDescriptors.
const (
	metricRequestCount   = "run.googleapis.com/request_count"                 // labels: response_code, response_code_class, route
	metricRequestLatency = "run.googleapis.com/request_latencies"             // DISTRIBUTION, same labels
	metricInstanceCount  = "run.googleapis.com/container/instance_count"      // GAUGE, label: state
	metricJobExecutions  = "run.googleapis.com/job/completed_execution_count" // label: result
	metricDeadLetters    = "pubsub.googleapis.com/subscription/dead_letter_message_count"
	metricOldestUnacked  = "pubsub.googleapis.com/subscription/oldest_unacked_message_age" // GAUGE
	resourceRun          = "cloud_run_revision"
	resourceRunJob       = "cloud_run_job"
	resourceSubscription = "pubsub_subscription"
	resourceGenericTask  = "generic_task"
	customMetricPrefix   = "custom.googleapis.com/tako/"
)

// defaultEvery is the reduction period for readings whose units do not
// depend on it (rate, ratio, percentile): the period only smooths.
const defaultEvery = time.Minute

// alertQuery renders one condition as the PromQL Cloud Monitoring will
// evaluate, plus whether a retest window is still allowed.
func alertQuery(cfg Config, g *analyzer.AppGraph, a *analyzer.AlertCondition) (string, bool, error) {
	if a == nil {
		return "", false, fmt.Errorf("alert has no condition")
	}
	every := time.Duration(a.EverySeconds) * time.Second
	if every == 0 {
		every = defaultEvery
	}
	lookback := every
	if a.Op == "absent" {
		lookback = time.Duration(a.ForSeconds) * time.Second
		if lookback == 0 {
			// CronMissed with no explicit window: twice the schedule, so
			// a job that merely ran late is not a page. The schedule is
			// in the graph, which is the whole reason this can be
			// derived rather than asked for.
			lookback = 2 * cronPeriod(g, a.SubjectVar)
		}
	}

	switch a.Reading {
	case "rate", "count", "sum", "mean", "percentile":
		return customQuery(cfg, a, every)
	case "dead_letters":
		sel := selector(metricDeadLetters, resourceSubscription,
			fmt.Sprintf("subscription_id=%q", cfg.Name(a.SubjectName)))
		return comparison(counterQuery(sel, "count", every), a.Op, a.Threshold), retestAllowed(lookback), nil
	case "backlog_age":
		// A GAUGE: the latest value is the answer, no rate or increase.
		sel := selector(metricOldestUnacked, resourceSubscription,
			fmt.Sprintf("subscription_id=%q", cfg.Name(a.SubjectName)))
		return comparison("max("+sel+")", a.Op, a.Threshold), retestAllowed(lookback), nil
	case "cron_failed":
		sel := takoRequests(cfg, g, a, fmt.Sprintf("route=%q", cronRoute(g, a.SubjectVar)), `status="5xx"`)
		return comparison(counterQuery(sel, "count", every), a.Op, a.Threshold), retestAllowed(lookback), nil
	case "cron_missed":
		sel := takoRequests(cfg, g, a, fmt.Sprintf("route=%q", cronRoute(g, a.SubjectVar)))
		return absentQuery(sel, lookback), retestAllowed(lookback), nil
	case "job_failed":
		sel := selector(metricJobExecutions, resourceRunJob,
			fmt.Sprintf("job_name=%q", cfg.Name(a.SubjectName)), `result="failed"`)
		return comparison(counterQuery(sel, "count", every), a.Op, a.Threshold), retestAllowed(lookback), nil
	case "error_rate":
		all := takoRequests(cfg, g, a, routeMatcher(a)...)
		bad := takoRequests(cfg, g, a, append(routeMatcher(a), `status="5xx"`)...)
		q := fmt.Sprintf("sum(rate(%s[%s])) / sum(rate(%s[%s]))", bad, promDuration(every), all, promDuration(every))
		return comparison(q, a.Op, a.Threshold), retestAllowed(lookback), nil
	case "latency":
		base := promName(customMetricPrefix + "request_ms")
		labels := append([]string{
			fmt.Sprintf("monitored_resource=%q", resourceGenericTask),
			fmt.Sprintf("namespace=%q", cfg.App+"-"+cfg.Env),
			fmt.Sprintf("job=%q", cfg.Name(serviceOf(g, a.SubjectVar))),
		}, routeMatcher(a)...)
		q := fmt.Sprintf("histogram_quantile(%g, sum(rate(%s_bucket{%s})) by (le))",
			a.Quantile, base, strings.Join(labels, ",")+"["+promDuration(every)+"]")
		return comparison(q, a.Op, a.Threshold), retestAllowed(lookback), nil
	case "at_capacity", "singleton_down":
		sel := selector(metricInstanceCount, resourceRun,
			fmt.Sprintf("service_name=%q", cfg.Name(serviceOf(g, a.SubjectVar))), `state="active"`)
		threshold := a.Threshold
		if a.Reading == "at_capacity" {
			// No threshold was asked for: the ceiling is the service's
			// own MaxInstances, which the graph already carries.
			threshold = float64(maxInstancesOf(g, a.SubjectVar)) - 0.5
		}
		return comparison("max("+sel+")", a.Op, threshold), retestAllowed(lookback), nil
	case "platform":
		sel := selector(a.SubjectName, platformResource(a.SubjectName), a.Matchers...)
		return comparison("max("+sel+")", a.Op, a.Threshold), retestAllowed(lookback), nil
	}
	return "", false, fmt.Errorf("unknown alert reading %q", a.Reading)
}

// customQuery reads one of the app's own Counters or Histograms.
func customQuery(cfg Config, a *analyzer.AlertCondition, every time.Duration) (string, bool, error) {
	metric := customMetricPrefix + a.SubjectName
	// namespace keeps prod and a preview env from sharing an alert, the
	// same way it keeps them from sharing a series.
	ns := fmt.Sprintf("namespace=%q", cfg.App+"-"+cfg.Env)
	if a.SubjectKind == "histogram" {
		q := histogramQuery(metric, resourceGenericTask, a.Reading, a.Quantile, every)
		return comparison(q, a.Op, a.Threshold), true, nil
	}
	sel := selector(metric, resourceGenericTask, ns)
	return comparison(counterQuery(sel, a.Reading, every), a.Op, a.Threshold), true, nil
}

// takoRequests selects tako's OWN per-endpoint counter, scoped to the
// env and the service.
//
// It is tako's rather than run.googleapis.com/request_count because
// Cloud Run's `route` label is never populated — measured across 88
// series — so the platform metric cannot answer a question about one
// endpoint or one cron, and this one can.
func takoRequests(cfg Config, g *analyzer.AppGraph, a *analyzer.AlertCondition, extra ...string) string {
	matchers := append([]string{
		fmt.Sprintf("namespace=%q", cfg.App+"-"+cfg.Env),
		fmt.Sprintf("job=%q", cfg.Name(serviceOf(g, a.SubjectVar))),
	}, extra...)
	return selector(customMetricPrefix+"requests", resourceGenericTask, matchers...)
}

// routeMatcher narrows to ONE endpoint when the subject is a route.
//
// The value is the declared pattern verbatim — "GET /api/photos" — and
// not a bare path, because the label tako writes IS the ServeMux
// pattern, method included. Stripping the method here would produce a
// matcher that never matches.
func routeMatcher(a *analyzer.AlertCondition) []string {
	if a.SubjectKind == "route" && a.SubjectName != "" {
		return []string{fmt.Sprintf("route=%q", a.SubjectName)}
	}
	return nil
}

// platformResource guesses the monitored resource for a raw platform
// metric from its domain. A selector without one is refused outright, so
// an unknown domain must fail loudly rather than emit a broken query.
func platformResource(metricType string) string {
	switch {
	case strings.HasPrefix(metricType, "run.googleapis.com/job/"):
		return resourceRunJob
	case strings.HasPrefix(metricType, "run.googleapis.com/"):
		return resourceRun
	case strings.HasPrefix(metricType, "pubsub.googleapis.com/subscription/"):
		return resourceSubscription
	case strings.HasPrefix(metricType, "pubsub.googleapis.com/topic/"):
		return "pubsub_topic"
	case strings.HasPrefix(metricType, "storage.googleapis.com/"):
		return "gcs_bucket"
	case strings.HasPrefix(metricType, customMetricPrefix):
		return resourceGenericTask
	}
	return resourceGenericTask
}

// serviceOf resolves the service a handler var belongs to.
func serviceOf(g *analyzer.AppGraph, v string) string {
	for _, h := range g.Handlers {
		if h.Var == v {
			return h.Service
		}
	}
	for _, s := range g.Services {
		if s.Package != "" && strings.HasPrefix(v, s.Package+".") {
			return s.Name
		}
	}
	if len(g.Services) > 0 {
		return g.Services[0].Name
	}
	return ""
}

// cronRoute is the HTTP path a cron's schedule calls — derived from the
// declaring VARIABLE (X1), which is the same derivation the Scheduler
// job's target uses, so the two cannot disagree.
func cronRoute(g *analyzer.AppGraph, v string) string {
	for _, h := range g.Handlers {
		if h.Var == v && h.Kind == "cron" {
			if dot := strings.LastIndexByte(h.Var, '.'); dot >= 0 {
				return "/_tako/cron/" + h.Var[dot+1:]
			}
		}
	}
	return ""
}

// cronPeriod is a cron's schedule as a duration, for deriving a
// CronMissed lookback.
func cronPeriod(g *analyzer.AppGraph, v string) time.Duration {
	for _, h := range g.Handlers {
		if h.Var == v && h.Schedule != "" {
			if d, err := time.ParseDuration(strings.TrimPrefix(h.Schedule, "every ")); err == nil {
				return d
			}
		}
	}
	return 24 * time.Hour
}

func maxInstancesOf(g *analyzer.AppGraph, v string) int {
	svc := serviceOf(g, v)
	for _, s := range g.Services {
		if s.Name == svc && s.Footprint != nil && s.Footprint.MaxInstances > 0 {
			return s.Footprint.MaxInstances
		}
	}
	return 100 // the default profile ceiling
}
