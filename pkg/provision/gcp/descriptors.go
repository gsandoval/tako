package gcp

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gsandoval/tako/pkg/analyzer"
)

// MetricDescriptors declares tako's custom metrics before anything reads
// them.
//
// Cloud Monitoring validates that a PromQL alert's metric EXISTS when
// the policy is created — a fresh deploy is refused with "The following
// PromQL metric(s) are invalid: custom_googleapis_com:tako_requests".
// A custom metric otherwise springs into existence on its first write,
// so an alert on one could never be created until the app had already
// served traffic: alerting would work on the second deploy and not the
// first, which is the kind of "works on my project" behaviour that
// wastes an afternoon.
//
// Declaring them also pins the schema — value type, kind, and which
// labels exist — instead of letting the first write infer it.
//
// This is NOT a reconciled kind and destroy never touches it. A metric
// descriptor owns its data, and data destruction is never automatic
// (spec §4.6a); a stale descriptor costs nothing and keeps history
// readable after the app is gone.
type MetricDescriptors struct {
	C *Client
}

// seedJob is the generic_task `job` value the deploy's seed points carry.
// Services write their own logical name there (pkg/provision.metricJob);
// this one deliberately names no service, so no query scoped to a
// service can ever match a seed point.
const seedJob = "tako-deploy"

type metricDescriptor struct {
	Type        string            `json:"type"`
	MetricKind  string            `json:"metricKind"`
	ValueType   string            `json:"valueType"`
	Description string            `json:"description,omitempty"`
	Labels      []descriptorLabel `json:"labels,omitempty"`
}

type descriptorLabel struct {
	Key         string `json:"key"`
	Description string `json:"description,omitempty"`
}

// Declared lists every descriptor an app needs: the two tako records
// itself, plus one per declared Counter and Histogram.
func Declared(g *analyzer.AppGraph) []metricDescriptor {
	out := []metricDescriptor{
		{
			Type: "custom.googleapis.com/tako/requests", MetricKind: "CUMULATIVE", ValueType: "INT64",
			Description: "Requests served, by route and status class (tako).",
			Labels: []descriptorLabel{
				{Key: "route", Description: "The declared route pattern."},
				{Key: "status", Description: "Status class: 2xx, 3xx, 4xx, 5xx."},
				{Key: "revision", Description: "The Cloud Run revision."},
			},
		},
		{
			Type: "custom.googleapis.com/tako/request_ms", MetricKind: "CUMULATIVE", ValueType: "DISTRIBUTION",
			Description: "Request latency in milliseconds, by route (tako).",
			Labels: []descriptorLabel{
				{Key: "route", Description: "The declared route pattern."},
				{Key: "revision", Description: "The Cloud Run revision."},
			},
		},
	}
	for _, r := range g.Resources {
		switch r.Kind {
		case "counter":
			out = append(out, metricDescriptor{
				Type: "custom.googleapis.com/tako/" + r.Name, MetricKind: "CUMULATIVE", ValueType: "INT64",
				Description: "tako.Counter " + r.Name,
				Labels:      []descriptorLabel{{Key: "revision"}},
			})
		case "histogram":
			out = append(out, metricDescriptor{
				Type: "custom.googleapis.com/tako/" + r.Name, MetricKind: "CUMULATIVE", ValueType: "DISTRIBUTION",
				Description: "tako.Histogram " + r.Name,
				Labels:      []descriptorLabel{{Key: "revision"}},
			})
		}
	}
	return out
}

// Seed writes one zero point per declared metric so the SERIES exists.
//
// Declaring the descriptor is not enough: PromQL's view of a project is
// built from data, not descriptors — with the descriptor present and no
// points written, `api/v1/metadata` still answers empty and a policy
// naming the metric is refused as invalid. So a first deploy has to make
// the series real, or alerting on tako's own metrics would only work
// after the app had already served traffic and flushed.
//
// A zero point is the honest seed: a CUMULATIVE counter starts at zero
// by definition, so this states the starting value rather than inventing
// a reading. It is written under task_id "deploy", which keeps it out of
// any instance's series — instances write their own, and aggregation
// across task_id is what every query already does.
//
// It is also written under job "tako-deploy" and NOT under the app or
// any service name (seedJob). The seed is not a service, and a job label
// that collides with a real one makes the deploy look like a writer:
// an Absent condition scoped to that service would see the seed and stay
// quiet for one window after every deploy — an alert suppressed at
// exactly the moment a bad deploy would need it.
func (m *MetricDescriptors) Seed(ctx context.Context, g *analyzer.AppGraph, app, env, region string) error {
	// A CUMULATIVE point spans an interval, and the API requires
	// start < end strictly — sending one instant for both is refused
	// with "The start time must be before the end time".
	end := time.Now().UTC()
	start := end.Add(-time.Minute)
	startStr := start.Format(time.RFC3339Nano)
	endStr := end.Format(time.RFC3339Nano)
	var series []map[string]any
	for _, d := range Declared(g) {
		value := map[string]any{"int64Value": "0"}
		if d.ValueType == "DISTRIBUTION" {
			value = map[string]any{"distributionValue": map[string]any{
				"count": "0", "mean": 0.0,
				"bucketOptions": map[string]any{"exponentialBuckets": map[string]any{
					"numFiniteBuckets": 1, "growthFactor": 2, "scale": 1}},
				"bucketCounts": []string{"0", "0", "0"},
			}}
		}
		labels := map[string]string{}
		for _, l := range d.Labels {
			labels[l.Key] = ""
		}
		series = append(series, map[string]any{
			"metric": map[string]any{"type": d.Type, "labels": labels},
			"resource": map[string]any{"type": "generic_task", "labels": map[string]string{
				"project_id": m.C.Project, "location": region,
				"namespace": app + "-" + env, "job": seedJob, "task_id": "deploy",
			}},
			"metricKind": "CUMULATIVE", "valueType": d.ValueType,
			"points": []map[string]any{{
				"interval": map[string]any{"startTime": startStr, "endTime": endStr},
				"value":    value,
			}},
		})
	}
	if len(series) == 0 {
		return nil
	}
	url := m.C.url(monitoringHost, "/v3/projects/%s/timeSeries", m.C.Project)
	if err := m.C.do(ctx, http.MethodPost, url, map[string]any{"timeSeries": series}, nil); err != nil {
		return fmt.Errorf("seed metric series: %w", err)
	}
	return nil
}

// Ensure creates any descriptor that does not exist. It is idempotent:
// a descriptor that is already there answers 409 (or 200 with the same
// shape), and neither is a failure.
func (m *MetricDescriptors) Ensure(ctx context.Context, g *analyzer.AppGraph) error {
	url := m.C.url(monitoringHost, "/v3/projects/%s/metricDescriptors", m.C.Project)
	for _, d := range Declared(g) {
		err := m.C.do(ctx, http.MethodPost, url, d, nil)
		if err == nil {
			continue
		}
		if ae, ok := asAPIError(err); ok && (ae.Status == http.StatusConflict || ae.Status == http.StatusOK) {
			continue
		}
		// An existing descriptor with a DIFFERENT shape is refused, and
		// that is worth surfacing rather than swallowing: it means the
		// metric's schema changed under an app that is still writing the
		// old one.
		return fmt.Errorf("declare metric %s: %w", d.Type, err)
	}
	return nil
}
