package gcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gsandoval/tako/pkg/analyzer"
)

// Cloud Monitoring refuses a PromQL policy naming a metric that does not
// exist, and a custom metric does not exist until something writes it.
// Without declared descriptors a first deploy cannot create its own
// alerts — the live gate hit exactly this.
func TestDeclaredCoversTakosOwnMetricsAndEveryUserMetric(t *testing.T) {
	g := &analyzer.AppGraph{Resources: []analyzer.Resource{
		{Kind: "counter", Name: "uploads"},
		{Kind: "histogram", Name: "thumb_ms"},
		{Kind: "queue", Name: "thumbs"}, // not a metric: must not appear
	}}
	byType := map[string]metricDescriptor{}
	for _, d := range Declared(g) {
		byType[d.Type] = d
	}
	want := map[string]string{
		"custom.googleapis.com/tako/requests":   "INT64",
		"custom.googleapis.com/tako/request_ms": "DISTRIBUTION",
		"custom.googleapis.com/tako/uploads":    "INT64",
		"custom.googleapis.com/tako/thumb_ms":   "DISTRIBUTION",
	}
	for typ, valueType := range want {
		d, ok := byType[typ]
		if !ok {
			t.Errorf("%s is never declared — an alert on it cannot be created on a first deploy", typ)
			continue
		}
		if d.ValueType != valueType {
			t.Errorf("%s valueType = %s, want %s", typ, d.ValueType, valueType)
		}
		if d.MetricKind != "CUMULATIVE" {
			t.Errorf("%s metricKind = %s — tako writes cumulatively", typ, d.MetricKind)
		}
	}
	if _, leaked := byType["custom.googleapis.com/tako/thumbs"]; leaked {
		t.Error("a queue was declared as a metric")
	}
	// The route label has to be in the SCHEMA, not just written at
	// runtime: it is what every per-endpoint alert filters on.
	var hasRoute bool
	for _, l := range byType["custom.googleapis.com/tako/requests"].Labels {
		if l.Key == "route" {
			hasRoute = true
		}
	}
	if !hasRoute {
		t.Error("requests declares no route label — per-endpoint alerting filters on it")
	}
}

// Re-declaring is normal on every deploy after the first, and must not
// be an error.
func TestEnsureToleratesExistingDescriptors(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"message": "A metric named custom.googleapis.com/tako/requests already exists."},
		})
	}))
	t.Cleanup(srv.Close)
	c := &Client{HC: srv.Client(), Project: "p", Region: "us-central1", Base: srv.URL}
	if err := (&MetricDescriptors{C: c}).Ensure(context.Background(), &analyzer.AppGraph{}); err != nil {
		t.Fatalf("an existing descriptor must not fail the deploy: %v", err)
	}
	if calls == 0 {
		t.Fatal("nothing was declared")
	}
}
