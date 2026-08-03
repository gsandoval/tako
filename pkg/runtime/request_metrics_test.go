package runtime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

type recordedMetrics struct {
	counters []record
	dists    []record
}

type record struct {
	name   string
	labels map[string]string
	value  float64
}

func (m *recordedMetrics) Add(_ context.Context, name string, labels map[string]string, delta int64) {
	m.counters = append(m.counters, record{name, labels, float64(delta)})
}

func (m *recordedMetrics) Observe(_ context.Context, name string, labels map[string]string, v float64) {
	m.dists = append(m.dists, record{name, labels, v})
}

func serveThrough(t *testing.T, mux *http.ServeMux, method, path string) *recordedMetrics {
	t.Helper()
	m := &recordedMetrics{}
	SetRequestMetrics(m)
	t.Cleanup(func() { SetRequestMetrics(nil) })
	rec := httptest.NewRecorder()
	logRequests(mux).ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return m
}

// THE defect this whole slice exists for: Cloud Run's own `route` label
// is empty on every series it publishes, so per-endpoint alerting is
// impossible unless tako writes the route itself.
func TestRequestsAreLabelledWithTheRoute(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/photos", func(w http.ResponseWriter, r *http.Request) {})
	m := serveThrough(t, mux, http.MethodGet, "/api/photos")

	if len(m.counters) != 1 || m.counters[0].name != RequestsMetric {
		t.Fatalf("counters = %+v", m.counters)
	}
	if got := m.counters[0].labels["route"]; got != "GET /api/photos" {
		t.Errorf("route label = %q — an empty or wrong route makes every per-endpoint alert dead", got)
	}
	if got := m.counters[0].labels["status"]; got != "2xx" {
		t.Errorf("status label = %q", got)
	}
	if len(m.dists) != 1 || m.dists[0].name != LatencyMetric {
		t.Fatalf("latency not recorded: %+v", m.dists)
	}
	if _, ok := m.dists[0].labels["route"]; !ok {
		t.Error("latency carries no route, so SlowerThan on one endpoint cannot work")
	}
}

// Cardinality is the reason this is a PATTERN and not a path. A path
// label would mint one series per photo id and bankrupt the metric.
func TestRouteLabelIsThePatternNotThePath(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/photos/{id}/image", func(w http.ResponseWriter, r *http.Request) {})
	for _, id := range []string{"abc", "def", "ghi"} {
		m := serveThrough(t, mux, http.MethodGet, "/api/photos/"+id+"/image")
		got := m.counters[0].labels["route"]
		if got != "GET /api/photos/{id}/image" {
			t.Fatalf("route label = %q, want the pattern — a path label is one series PER ID", got)
		}
	}
}

// An unmatched path must not mint a series each, or a scanner probing
// random URLs becomes a metrics bill.
func TestUnmatchedPathsShareOneSeries(t *testing.T) {
	mux := http.NewServeMux()
	for _, p := range []string{"/nope", "/also-nope", "/.env"} {
		m := serveThrough(t, mux, http.MethodGet, p)
		if got := m.counters[0].labels["route"]; got != "<unmatched>" {
			t.Fatalf("unmatched %s labelled %q — one series per probed URL", p, got)
		}
	}
}

// Status classes are bucketed: five values at most, never the raw code.
func TestStatusIsBucketedIntoClasses(t *testing.T) {
	cases := map[int]string{200: "2xx", 204: "2xx", 301: "3xx", 404: "4xx", 500: "5xx", 503: "5xx"}
	for code, want := range cases {
		if got := statusClass(code); got != want {
			t.Errorf("statusClass(%d) = %q, want %q", code, got, want)
		}
	}
}

// Recording must never be why a handler fails: an unwired sink is the
// normal state in tests and in any embedding that skips boot.
func TestRecordingWithNoSinkIsSafe(t *testing.T) {
	SetRequestMetrics(nil)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /x", func(w http.ResponseWriter, r *http.Request) {})
	rec := httptest.NewRecorder()
	logRequests(mux).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
}

// Crons share ONE mux pattern — POST /_tako/cron/{cron} — so without
// refinement every cron in a service records into one series, and
// CronMissed on a healthy cron would fire whenever any other cron in
// the same service stopped. This is the wildcard case the route label
// cannot see by itself.
func TestWildcardDispatchersNameWhatTheyRan(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /_tako/cron/{cron}", func(w http.ResponseWriter, r *http.Request) {
		RefineRoute(r, "/_tako/cron/"+r.PathValue("cron"))
	})
	for _, name := range []string{"Cleanup", "Sweep"} {
		m := serveThrough(t, mux, http.MethodPost, "/_tako/cron/"+name)
		if got := m.counters[0].labels["route"]; got != "/_tako/cron/"+name {
			t.Fatalf("cron %s recorded as %q — every cron would share one series", name, got)
		}
	}
}

// Refinement must not be required: an ordinary route keeps its pattern.
func TestUnrefinedRoutesKeepTheirPattern(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/photos", func(w http.ResponseWriter, r *http.Request) {})
	m := serveThrough(t, mux, http.MethodGet, "/api/photos")
	if got := m.counters[0].labels["route"]; got != "GET /api/photos" {
		t.Fatalf("route = %q", got)
	}
}
