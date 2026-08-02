package runtime

// P1: a deployed service's logs have to arrive as Cloud Logging
// records, not as text. These pin the three things that were silently
// wrong before: severity was lost, request logs were the dev format,
// and nothing correlated a handler's lines with its request.

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// cloudLogs captures what the JSON handler writes, with the same
// ReplaceAttr the cloud boot installs.
func cloudLogs(t *testing.T, project string) (*bytes.Buffer, func()) {
	t.Helper()
	var buf bytes.Buffer
	oldLogger, oldProject := slog.Default(), traceProject
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{
		Level: slog.LevelDebug, ReplaceAttr: cloudAttrs,
	})))
	traceProject = project
	return &buf, func() { slog.SetDefault(oldLogger); traceProject = oldProject }
}

func lastLine(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	var out map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &out); err != nil {
		t.Fatalf("log line is not JSON: %v\n%s", err, lines[len(lines)-1])
	}
	return out
}

// The regression that IS this slice: slog spells it WARN, Cloud Logging
// wants WARNING, and a JSON handler alone would ship the wrong one. If
// this ever reverts, a warning stops being findable by severity.
func TestSeverityUsesCloudLoggingNames(t *testing.T) {
	for _, c := range []struct {
		level slog.Level
		want  string
	}{
		{slog.LevelDebug, "DEBUG"},
		{slog.LevelInfo, "INFO"},
		{slog.LevelWarn, "WARNING"}, // NOT "WARN"
		{slog.LevelError, "ERROR"},
		{slog.LevelWarn + 1, "WARNING"}, // between levels: round down
	} {
		if got := severityOf(c.level); got != c.want {
			t.Errorf("severityOf(%v) = %q, want %q", c.level, got, c.want)
		}
	}
}

func TestCloudLinesCarryTheRenamedKeys(t *testing.T) {
	buf, restore := cloudLogs(t, "tako-test-01")
	defer restore()

	slog.Error("notify failed", "err", "i/o timeout")
	line := lastLine(t, buf)

	if line[severityKey] != "ERROR" {
		t.Errorf("severity = %v — Cloud Logging reads this key and no other", line[severityKey])
	}
	if line[messageKey] != "notify failed" {
		t.Errorf("message = %v", line[messageKey])
	}
	if _, stillThere := line["level"]; stillThere {
		t.Error(`the line still carries "level" — the rename did not happen`)
	}
	if _, stillThere := line["msg"]; stillThere {
		t.Error(`the line still carries "msg"`)
	}
	if line["err"] != "i/o timeout" {
		t.Errorf("the handler's own attributes must survive; err = %v", line["err"])
	}
	if _, err := time.Parse(time.RFC3339Nano, line[slog.TimeKey].(string)); err != nil {
		t.Errorf("time %v is not ISO 8601: %v", line[slog.TimeKey], err)
	}
}

func TestRequestLoggerCorrelatesWithTheTrace(t *testing.T) {
	buf, restore := cloudLogs(t, "tako-test-01")
	defer restore()

	r := httptest.NewRequest("GET", "/api/photos", nil)
	r.Header.Set("X-Cloud-Trace-Context", "105445aa7843bc8bf206b12000100000/1234567890;o=1")
	requestLogger(r, "r-8f2c1d").Info("thumbnail written")

	line := lastLine(t, buf)
	want := "projects/tako-test-01/traces/105445aa7843bc8bf206b12000100000"
	if line[traceKey] != want {
		t.Errorf("trace = %v, want %q — without this the line does not nest under its request", line[traceKey], want)
	}
	if line[spanKey] != "1234567890" {
		t.Errorf("spanId = %v", line[spanKey])
	}
	if line["req"] != "r-8f2c1d" {
		t.Errorf("req = %v — the tako request id rides along regardless", line["req"])
	}
}

// Locally there is no project and no trace header, and a trace key
// pointing at nothing is worse than no key at all.
func TestNoTraceKeyWithoutCloudRun(t *testing.T) {
	buf, restore := cloudLogs(t, "") // no project: not deployed
	defer restore()

	r := httptest.NewRequest("GET", "/api/photos", nil)
	r.Header.Set("X-Cloud-Trace-Context", "abc/1;o=1") // present but unusable
	requestLogger(r, "r-1").Info("hello")

	if _, there := lastLine(t, buf)[traceKey]; there {
		t.Error("a trace field was written with no project to name in it")
	}
}

func TestParseTraceContext(t *testing.T) {
	for _, c := range []struct{ in, trace, span string }{
		{"105445aa/1234;o=1", "105445aa", "1234"},
		{"105445aa/1234", "105445aa", "1234"},
		{"105445aa", "105445aa", ""},
		{"", "", ""},
	} {
		trace, span := parseTraceContext(c.in)
		if trace != c.trace || span != c.span {
			t.Errorf("parseTraceContext(%q) = %q, %q; want %q, %q", c.in, trace, span, c.trace, c.span)
		}
	}
}

func TestCloudRequestLogIsAnHttpRequestRecord(t *testing.T) {
	buf, restore := cloudLogs(t, "tako-test-01")
	defer restore()

	r := httptest.NewRequest("GET", "/api/photos?page=2", nil)
	r.Header.Set("User-Agent", "curl/8.7.1")
	r.Header.Set("X-Forwarded-For", "203.0.113.7, 130.211.0.1")
	logRequest(r, 200, 812, 14*time.Millisecond)

	line := lastLine(t, buf)
	req, ok := line[httpKey].(map[string]any)
	if !ok {
		t.Fatalf("httpRequest = %v — Cloud Logging renders this key as a request, and text as text", line[httpKey])
	}
	if req["requestMethod"] != "GET" || req["requestUrl"] != "/api/photos?page=2" {
		t.Errorf("method/url = %v %v", req["requestMethod"], req["requestUrl"])
	}
	if req["status"].(float64) != 200 || req["responseSize"].(float64) != 812 {
		t.Errorf("status/size = %v %v", req["status"], req["responseSize"])
	}
	if req["latency"] != "0.014000s" {
		t.Errorf("latency = %v — Cloud Logging wants a seconds-with-suffix duration", req["latency"])
	}
	if req["userAgent"] != "curl/8.7.1" {
		t.Errorf("userAgent = %v", req["userAgent"])
	}
	// RemoteAddr is Cloud Run's frontend on every request; the client is
	// the first hop of X-Forwarded-For or nothing.
	if req["remoteIp"] != "203.0.113.7" {
		t.Errorf("remoteIp = %v — the frontend's own address is not the client", req["remoteIp"])
	}
}

// A page of 500s must be findable with the same severity filter as a
// handler's own error lines.
func TestRequestSeverityFollowsStatus(t *testing.T) {
	for _, c := range []struct {
		status int
		want   slog.Level
	}{{200, slog.LevelInfo}, {404, slog.LevelWarn}, {500, slog.LevelError}} {
		if got := levelFor(c.status); got != c.want {
			t.Errorf("levelFor(%d) = %v, want %v", c.status, got, c.want)
		}
	}
}

// Local output is a product: the dev console's line, on stdout, not
// JSON. This is the half of the slice that must NOT change.
func TestLocalRequestLogStaysTheDevLine(t *testing.T) {
	buf, restore := cloudLogs(t, "") // not deployed
	defer restore()

	logRequest(httptest.NewRequest("GET", "/greet/ada", nil), 200, 20, time.Millisecond)
	if buf.Len() != 0 {
		t.Errorf("a local request went through the structured logger:\n%s", buf.String())
	}
}

// End to end through the middleware: the response size the record
// reports is the bytes actually written.
func TestLogRequestsCountsTheBody(t *testing.T) {
	buf, restore := cloudLogs(t, "tako-test-01")
	defer restore()

	h := logRequests(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"id":"x"}`))
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/photos", nil))

	req := lastLine(t, buf)[httpKey].(map[string]any)
	if req["status"].(float64) != 201 || req["responseSize"].(float64) != 10 {
		t.Errorf("status/size = %v %v, want 201 10", req["status"], req["responseSize"])
	}
}
