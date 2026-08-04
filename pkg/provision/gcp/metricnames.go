package gcp

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// Which metric names PromQL can actually resolve, and why that is not
// the same question as which metrics exist.
//
// Cloud Monitoring validates an alert policy's metric name when the
// policy is CREATED, and it resolves names from DATA rather than from
// descriptors. So a metric that has a descriptor, and is documented, and
// is one Google itself publishes, is still "invalid" until something has
// written a point:
//
//	The following PromQL metric(s) are invalid:
//	run_googleapis_com:container_memory_utilizations (HTTP 400)
//
// tako seeds its OWN metrics for exactly this reason. It cannot seed
// Google's. So the honest thing is to ask first and say plainly which
// alerts cannot be created yet, instead of failing partway through the
// apply with Google's sentence.
//
// The other half of that message is subtler: a DISTRIBUTION is never
// exposed under its bare name at all — only as `_bucket`, `_count` and
// `_sum`. Naming the metric as documented can therefore never work, and
// no amount of waiting for data fixes it.

// KnownMetricNames returns every PromQL metric name the project can
// currently resolve.
func (c *Client) KnownMetricNames(ctx context.Context) (map[string]bool, error) {
	url := c.url(monitoringHost, "/v1/projects/%s/location/global/prometheus/api/v1/label/__name__/values", c.Project)
	var out struct {
		Data []string `json:"data"`
	}
	if err := c.do(ctx, http.MethodGet, url, nil, &out); err != nil {
		return nil, Classify(err, c.Project, "list metric names", "roles/monitoring.viewer")
	}
	known := make(map[string]bool, len(out.Data))
	for _, n := range out.Data {
		known[n] = true
	}
	return known, nil
}

// UnresolvableMetric explains why one alert cannot be created yet.
type UnresolvableMetric struct {
	Alert  string
	Metric string
	// Suffixed lists the names that DO resolve, when the metric is a
	// distribution exposed only in parts.
	Suffixed []string
}

// Error renders the whole set as one message, because a deploy should
// say everything that is wrong at once rather than one item per run.
func UnresolvableError(bad []UnresolvableMetric) error {
	var b strings.Builder
	b.WriteString("these alerts name metrics Cloud Monitoring cannot resolve yet:\n")
	for _, u := range bad {
		fmt.Fprintf(&b, "  %s — %s\n", u.Alert, u.Metric)
		if len(u.Suffixed) > 0 {
			fmt.Fprintf(&b, "      it is a DISTRIBUTION, published only in parts: %s\n",
				strings.Join(u.Suffixed, ", "))
			b.WriteString("      alert on one of those instead of the bare name\n")
			continue
		}
		b.WriteString("      nothing has written this metric in this project yet — PromQL resolves\n")
		b.WriteString("      names from data, so the policy cannot be created until it has some\n")
	}
	b.WriteString("\ntako seeds its own metrics for this reason and cannot seed Google's.\n")
	b.WriteString("Deploy without the alert, let the resource run once, then deploy again.")
	return fmt.Errorf("%s", b.String())
}

// CheckMetricNames reports which of the given (alert, promQL name) pairs
// cannot be resolved. suffixesOf is consulted for distributions.
func CheckMetricNames(known map[string]bool, pairs map[string]string) []UnresolvableMetric {
	var bad []UnresolvableMetric
	for alert, name := range pairs {
		if name == "" || known[name] {
			continue
		}
		u := UnresolvableMetric{Alert: alert, Metric: name}
		for _, suffix := range []string{"_bucket", "_count", "_sum"} {
			if known[name+suffix] {
				u.Suffixed = append(u.Suffixed, name+suffix)
			}
		}
		bad = append(bad, u)
	}
	sort.Slice(bad, func(i, j int) bool { return bad[i].Alert < bad[j].Alert })
	return bad
}
