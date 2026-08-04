package provision

// Generating the PromQL an alert condition compiles to.
//
// Everything here is shaped by what was MEASURED against the live API on
// 2026-08-02 (task log, Y1). Four facts drive the whole file, and each
// one was a 400 or a silently-empty result before it was a rule:
//
//  1. A custom-metric selector needs a `monitored_resource` matcher.
//     Without it: "must specify a label matcher on the
//     'monitored_resource' label because multiple monitored resource
//     types...".
//  2. A Cloud Monitoring DISTRIBUTION reaches PromQL as _bucket/_count/
//     _sum, never under its bare name. The bare name returns zero series
//     WITH data present, which reads exactly like a broken filter.
//  3. Metric names map as: first "/" becomes ":", every other special
//     character becomes "_".
//  4. A lookback over 1d1h forbids a retest window — "Retest windows are
//     not supported in PromQL queries with lookbacks longer than 1d1h" —
//     so long-window conditions must send duration 0.
//
// The counter functions are equally deliberate: `increase` and `rate`
// are counter-correct and reset-aware, where `delta` is defined for
// gauges and would read an instance restart as a negative, and
// `avg_over_time` would average running totals. All of them PARSE, so
// only the semantics distinguish them.

import (
	"fmt"
	"strings"
	"time"
)

// promName maps a Cloud Monitoring metric type to its PromQL name.
// PromName is the exported spelling, for callers outside this package
// that need the same mapping — the CLI's metric search, so a name it
// prints and a name a query builds can never be derived differently.
func PromName(metricType string) string { return promName(metricType) }

func promName(metricType string) string {
	i := strings.Index(metricType, "/")
	if i < 0 {
		return sanitizeProm(metricType)
	}
	return sanitizeProm(metricType[:i]) + ":" + sanitizeProm(metricType[i+1:])
}

func sanitizeProm(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' {
			return r
		}
		return '_'
	}, s)
}

// selector builds a PromQL vector selector: the mapped name plus label
// matchers, with monitored_resource always present.
func selector(metricType, resource string, matchers ...string) string {
	all := append([]string{fmt.Sprintf("monitored_resource=%q", resource)}, matchers...)
	return promName(metricType) + "{" + strings.Join(all, ",") + "}"
}

// promDuration renders a Go duration the way PromQL range selectors want
// it. Seconds are used throughout rather than mixed units, because
// "1h30m0s" is not a legal range selector and a silently-wrong range is
// worse than a parse error.
func promDuration(d time.Duration) string {
	secs := int64(d / time.Second)
	if secs < 1 {
		secs = 1
	}
	return fmt.Sprintf("%ds", secs)
}

// retestAllowed reports whether a condition with this lookback may carry
// a duration. Google refuses a retest window past 1d1h (measured).
const maxRetestLookback = 25 * time.Hour

func retestAllowed(lookback time.Duration) bool { return lookback <= maxRetestLookback }

// counterQuery is the reading of a cumulative counter over a window.
// `increase` and `rate` handle resets; `delta` does not, and tako's
// metrics reset whenever an instance is replaced.
func counterQuery(sel, agg string, window time.Duration) string {
	w := promDuration(window)
	switch agg {
	case "rate":
		return fmt.Sprintf("sum(rate(%s[%s]))", sel, w)
	default: // count
		return fmt.Sprintf("sum(increase(%s[%s]))", sel, w)
	}
}

// histogramQuery is a reading of a distribution. Every form goes through
// the _bucket/_count/_sum decomposition, because the bare name resolves
// to nothing.
func histogramQuery(metricType, resource, agg string, q float64, window time.Duration, matchers ...string) string {
	w := promDuration(window)
	base := promName(metricType)
	// The matchers belong INSIDE the selector, and namespace is always
	// one of them: without it a histogram alert reads every environment's
	// series at once, so a preview deploy moves prod's p99.
	res := fmt.Sprintf("{%s}", strings.Join(append([]string{fmt.Sprintf("monitored_resource=%q", resource)}, matchers...), ","))
	switch agg {
	case "percentile":
		// by (le) is what makes this a quantile over the histogram
		// rather than over the series: without it the buckets are
		// summed away and the result is meaningless.
		return fmt.Sprintf("histogram_quantile(%g, sum(rate(%s_bucket%s[%s])) by (le))", q, base, res, w)
	case "sum":
		return fmt.Sprintf("sum(increase(%s_sum%s[%s]))", base, res, w)
	case "mean":
		// Sum over count, both counter-correct. avg_over_time here would
		// average the running totals, which is not a mean of anything.
		return fmt.Sprintf("sum(increase(%s_sum%s[%s])) / sum(increase(%s_count%s[%s]))", base, res, w, base, res, w)
	case "rate":
		return fmt.Sprintf("sum(rate(%s_count%s[%s]))", base, res, w)
	default: // count — observations, not values
		return fmt.Sprintf("sum(increase(%s_count%s[%s]))", base, res, w)
	}
}

// absentQuery is "no data at all in the lookback".
//
// It is absent_over_time and NOT `increase(...) == 0`: over a range with
// no samples, increase() returns nothing and `== 0` filters nothing, so
// the alert never fires — the exact silence it exists to break. Proven
// on live data: increase form 0 series, absent_over_time 1 series.
func absentQuery(sel string, lookback time.Duration) string {
	return fmt.Sprintf("absent_over_time(%s[%s])", sel, promDuration(lookback))
}

// comparison closes a threshold query. PromQL conditions fire on the
// series a query RETURNS, so the comparison belongs in the query.
func comparison(query, op string, threshold float64) string {
	switch op {
	case "below":
		// `or vector(0)` is PromQL's idiom for "treat missing data as
		// zero", and it is what makes a below-comparison mean what it
		// says. Without it an absent series yields an empty result, and
		// `empty < threshold` is empty — so the alert goes quiet exactly
		// when the metric has stopped, which is the worst case rather
		// than the healthy one.
		//
		// Verified against Cloud Monitoring on all four cases: a healthy
		// value stays quiet, a genuinely low value fires, a vanished
		// series fires, and a metric that was never written fires.
		//
		// Only "below" gets this. On an above-comparison, zero is not
		// above anything, so the fallback would change nothing.
		return fmt.Sprintf("(%s or vector(0)) < %g", query, threshold)
	case "atleast":
		// The counting presets say "at least n" — DeadLettered(thumbs, 1)
		// is meant to fire on the FIRST dead letter. Compiled as "> 1" it
		// needed two, so the alert a user asked for by its documented
		// meaning was off by one in the quiet direction: it under-reports,
		// which is the direction nobody notices.
		return fmt.Sprintf("%s >= %g", query, threshold)
	default:
		return fmt.Sprintf("%s > %g", query, threshold)
	}
}
