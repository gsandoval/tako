package gcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// fakeMonitoring stands in for projects.timeSeries.create. The store
// takes a base URL precisely so this is possible without an HTTP seam on
// the shared client.
type fakeMonitoring struct {
	*httptest.Server
	bodies []map[string]any
	status int
}

func newFakeMonitoring(t *testing.T) *fakeMonitoring {
	t.Helper()
	f := &fakeMonitoring{status: http.StatusOK}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		f.bodies = append(f.bodies, body)
		w.WriteHeader(f.status)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(f.Close)
	return f
}

func testStore(t *testing.T, f *fakeMonitoring) *MetricsStore {
	t.Helper()
	m := NewMetricsStore(&Client{HC: f.Client(), Opts: Options{Project: "p"}},
		Identity{Service: "gallery", Revision: "gallery-00007-abc", Location: "us-central1", Instance: "42"})
	m.base = f.URL
	return m
}

// series pulls the one time series out of the last request.
func (f *fakeMonitoring) series(t *testing.T) []any {
	t.Helper()
	if len(f.bodies) == 0 {
		t.Fatal("no write reached the API")
	}
	ts, ok := f.bodies[len(f.bodies)-1]["timeSeries"].([]any)
	if !ok {
		t.Fatalf("body carries no timeSeries: %v", f.bodies[len(f.bodies)-1])
	}
	return ts
}

func TestCounterWritesCumulativeTotal(t *testing.T) {
	f := newFakeMonitoring(t)
	m := testStore(t, f)
	ctx := context.Background()

	m.Add(ctx, "uploads", 2)
	m.Add(ctx, "uploads", 3)
	if err := m.flush(ctx, false); err != nil {
		t.Fatal(err)
	}
	one := f.series(t)[0].(map[string]any)

	if got := one["metricKind"]; got != "CUMULATIVE" {
		t.Errorf("metricKind = %v, want CUMULATIVE — a dropped flush must not become a gap", got)
	}
	metric := one["metric"].(map[string]any)
	if got := metric["type"]; got != "custom.googleapis.com/tako/uploads" {
		t.Errorf("metric type = %v", got)
	}
	// The label that keeps two instances of one revision from writing the
	// same series: cloud_run_revision has no instance among its labels.
	if got := metric["labels"].(map[string]any)["instance"]; got != "42" {
		t.Errorf("instance label = %v, want the writer's id", got)
	}
	res := one["resource"].(map[string]any)
	if got := res["type"]; got != "cloud_run_revision" {
		t.Errorf("resource type = %v", got)
	}
	labels := res["labels"].(map[string]any)
	for k, want := range map[string]string{
		"project_id": "p", "service_name": "gallery",
		"revision_name": "gallery-00007-abc", "location": "us-central1",
	} {
		if labels[k] != want {
			t.Errorf("resource label %s = %v, want %s", k, labels[k], want)
		}
	}
	point := one["points"].([]any)[0].(map[string]any)
	if got := point["value"].(map[string]any)["int64Value"]; got != "5" {
		t.Errorf("value = %v, want the running total 5, not the last delta", got)
	}

	// The second flush carries the TOTAL, not what accrued since. That is
	// what makes a dropped flush recoverable.
	m.Add(ctx, "uploads", 1)
	m.lastFlush = time.Time{}
	if err := m.flush(ctx, false); err != nil {
		t.Fatal(err)
	}
	p2 := f.series(t)[0].(map[string]any)["points"].([]any)[0].(map[string]any)
	if got := p2["value"].(map[string]any)["int64Value"]; got != "6" {
		t.Errorf("second flush value = %v, want the total 6", got)
	}
}

func TestHistogramWritesDistribution(t *testing.T) {
	f := newFakeMonitoring(t)
	m := testStore(t, f)
	ctx := context.Background()

	// gallery's thumb_ms lives here: small images, single-digit to a few
	// hundred milliseconds.
	for _, v := range []float64{5, 9, 120, 300} {
		m.Observe(ctx, "thumb_ms", v)
	}
	if err := m.flush(ctx, false); err != nil {
		t.Fatal(err)
	}
	one := f.series(t)[0].(map[string]any)
	if got := one["valueType"]; got != "DISTRIBUTION" {
		t.Errorf("valueType = %v", got)
	}
	d := one["points"].([]any)[0].(map[string]any)["value"].(map[string]any)["distributionValue"].(map[string]any)
	if got := d["count"]; got != "4" {
		t.Errorf("count = %v, want 4", got)
	}
	counts := d["bucketCounts"].([]any)
	if len(counts) != histBuckets+2 {
		t.Fatalf("bucketCounts has %d entries, want numFiniteBuckets+2 = %d", len(counts), histBuckets+2)
	}
	var total int64
	for _, c := range counts {
		if c.(string) != "0" {
			total++
		}
	}
	if total == 0 {
		t.Error("every sample fell outside the buckets")
	}
	opts := d["bucketOptions"].(map[string]any)["exponentialBuckets"].(map[string]any)
	if opts["growthFactor"].(float64) != histGrowth {
		t.Errorf("growthFactor = %v", opts["growthFactor"])
	}
}

// The bucket layout has to hold whatever a caller means by a float64,
// because tako.Histogram carries no unit.
func TestBucketPlacement(t *testing.T) {
	for _, tc := range []struct {
		v    float64
		want int
	}{
		{0.5, 0},                   // under scale
		{0, 0},                     // zero is not a band
		{1, 1},                     // first finite band
		{1.5, 1},                   // still the first
		{2, 2},                     // the doubling boundary
		{5, 3},                     // gallery territory
		{300, 9},                   // thumb_ms's slow end
		{1 << 20, histBuckets + 1}, // overflow, not lost
	} {
		if got := bucketOf(tc.v); got != tc.want {
			t.Errorf("bucketOf(%v) = %d, want %d", tc.v, got, tc.want)
		}
	}
}

// Nothing recorded must not become an empty write: an API call per
// minute per idle service is real money for no information.
func TestIdleServiceWritesNothing(t *testing.T) {
	f := newFakeMonitoring(t)
	m := testStore(t, f)
	if err := m.flush(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	m.Add(context.Background(), "uploads", 1)
	if err := m.flush(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	// The second flush after the first has nothing NEW, but cumulative
	// series are still worth re-sending only when they moved.
	if err := m.flush(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if len(f.bodies) != 1 {
		t.Errorf("%d writes for one recording — an unchanged metric should not be re-sent", len(f.bodies))
	}
}

// A 4xx is the batch's own fault; retrying spends the shutdown budget on
// the same answer.
func TestClientErrorIsNotRetried(t *testing.T) {
	f := newFakeMonitoring(t)
	f.status = http.StatusBadRequest
	m := testStore(t, f)
	m.Add(context.Background(), "uploads", 1)
	if err := m.flush(context.Background(), false); err == nil {
		t.Fatal("a 400 must surface as an error for the caller to log")
	}
	if len(f.bodies) != 1 {
		t.Errorf("%d attempts, want 1 — a rejected batch is not retried", len(f.bodies))
	}
}

// Recording must never block on I/O, whatever the API is doing. The
// contract has no error return precisely because callers are handlers.
func TestRecordingDoesNotWaitOnTheAPI(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
	}))
	defer slow.Close()
	m := NewMetricsStore(&Client{HC: slow.Client(), Opts: Options{Project: "p"}}, Identity{})
	m.base = slow.URL

	m.Add(context.Background(), "uploads", 1)
	go func() { _ = m.flush(context.Background(), false) }()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			m.Add(context.Background(), "uploads", 1)
			m.Observe(context.Background(), "thumb_ms", 12)
		}
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("recording blocked behind a slow flush — the request path must never wait on Cloud Monitoring")
	}
}
