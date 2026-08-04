package gcp

import (
	"context"
	"net/http"
	"sort"
	"strings"

	"github.com/gsandoval/tako/pkg/provision"
)

// Finding a metric to alert on.
//
// A project publishes upwards of eight thousand metric descriptors,
// which is why the reference documentation carries no list of them and
// why, without this, ServiceMetric and PlatformMetric are only usable by
// someone who already knows the name.
//
// Two things a raw descriptor listing will not tell you, and both of
// them cost a failed deploy to learn:
//
//   - A DISTRIBUTION has no bare name. It is published only as _bucket,
//     _count and _sum, so naming it the way its own documentation writes
//     it produces a policy Cloud Monitoring refuses. The reading has to
//     be a quantile.
//   - A metric can EXIST and still not be nameable here. Cloud
//     Monitoring resolves a PromQL name against what the project can
//     currently see, and a service you have never used contributes
//     nothing to it — Pub/Sub subscription metrics appeared in this
//     project only once it had a subscription. A policy naming an
//     unresolvable metric is refused at creation.
//
// So the search answers "which constructor" and "can I name this yet"
// rather than just "does this exist".

// Descriptor is one metric, as the CLI needs it.
type Descriptor struct {
	Type        string
	Kind        string
	ValueType   string
	Description string
	Resources   []string
	Labels      []string
	// Resolvable is whether Cloud Monitoring can currently resolve this
	// name in PromQL — a different question from whether the metric
	// exists, and the one that decides whether a policy can be created.
	//
	// It is NOT "has data". Google's built-in descriptors resolve
	// whether or not anything has ever written to them:
	// agent.googleapis.com metrics resolve in a project that has never
	// run a GCE instance. Measured, after a first version of this called
	// the field HasData and marked every one of those true.
	Resolvable bool
}

// Distribution reports whether this metric is published only in parts.
func (d Descriptor) Distribution() bool { return d.ValueType == "DISTRIBUTION" }

// Constructor is the tako call that reads this metric, chosen from its
// shape rather than left to the reader to work out.
func (d Descriptor) Constructor() string {
	scope := "svc"
	for _, r := range d.Resources {
		if strings.HasPrefix(r, "pubsub_subscription") {
			scope = "queue"
		}
	}
	if d.Distribution() {
		if scope == "queue" {
			// No QueuePercentile exists yet: nothing has demanded one.
			return "tako.PlatformMetric(\"" + d.Type + "\", …)   // DISTRIBUTION: alert on " + d.Type + "_count or _sum"
		}
		return "tako.ServicePercentile(svc, \"" + d.Type + "\", 0.99)"
	}
	switch scope {
	case "queue":
		return "tako.QueueMetric(q, \"" + d.Type + "\")"
	}
	return "tako.ServiceMetric(svc, \"" + d.Type + "\")"
}

// SearchDescriptors finds metrics whose type contains the term.
func (c *Client) SearchDescriptors(ctx context.Context, term string, limit int) ([]Descriptor, error) {
	url := c.url(monitoringHost, "/v3/projects/%s/metricDescriptors", c.Project)
	var out struct {
		Descriptors []struct {
			Type                   string   `json:"type"`
			MetricKind             string   `json:"metricKind"`
			ValueType              string   `json:"valueType"`
			Description            string   `json:"description"`
			MonitoredResourceTypes []string `json:"monitoredResourceTypes"`
			Labels                 []struct {
				Key string `json:"key"`
			} `json:"labels"`
		} `json:"metricDescriptors"`
	}
	q := "?filter=" + urlQueryEscape(`metric.type=has_substring("`+term+`")`) + "&pageSize=500"
	if err := c.do(ctx, http.MethodGet, url+q, nil, &out); err != nil {
		return nil, Classify(err, c.Project, "list metric descriptors", "roles/monitoring.viewer")
	}
	known, err := c.KnownMetricNames(ctx)
	if err != nil {
		// A name list is a nicety; a descriptor list is the answer. Do
		// not fail the whole search because the second call was refused.
		known = map[string]bool{}
	}
	var found []Descriptor
	for _, d := range out.Descriptors {
		desc := Descriptor{
			Type: d.Type, Kind: d.MetricKind, ValueType: d.ValueType,
			Description: d.Description, Resources: d.MonitoredResourceTypes,
		}
		for _, l := range d.Labels {
			desc.Labels = append(desc.Labels, l.Key)
		}
		name := provision.PromName(d.Type)
		desc.Resolvable = known[name] || known[name+"_bucket"] || known[name+"_count"]
		found = append(found, desc)
	}
	sort.Slice(found, func(i, j int) bool {
		// Names that can be alerted on today come first: one that cannot
		// be is the less useful answer.
		if found[i].Resolvable != found[j].Resolvable {
			return found[i].Resolvable
		}
		return found[i].Type < found[j].Type
	})
	if limit > 0 && len(found) > limit {
		found = found[:limit]
	}
	return found, nil
}

// urlQueryEscape percent-encodes a filter for a query string.
func urlQueryEscape(s string) string {
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

// FirstSentence trims a descriptor's prose to the part that identifies
// it. Google's descriptions run to paragraphs, and every one of them
// ends with the same sampling note.
func FirstSentence(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if i := strings.Index(s, ". "); i > 0 {
		s = s[:i+1]
	}
	if len(s) > 110 {
		s = strings.TrimSpace(s[:107]) + "…"
	}
	return s
}
