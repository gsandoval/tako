package runtime

// P1: a deployed service's logs have to arrive as Cloud Logging
// records, not as text. These pin the three things that were silently
// wrong before: severity was lost, request logs were the dev format,
// and nothing correlated a handler's lines with its request.

import (
	"bytes"
	"encoding/json"
	"log/slog"
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

// Local output is a product: the dev console's line, on stdout, not
// JSON.
func TestLocalRequestLogStaysTheDevLine(t *testing.T) {
	buf, restore := cloudLogs(t, "") // not deployed
	defer restore()

	logRequest(httptest.NewRequest("GET", "/greet/ada", nil), 200, time.Millisecond)
	if buf.Len() != 0 {
		t.Errorf("a local request went through the structured logger:\n%s", buf.String())
	}
}

// Deployed, tako writes NO request log: Cloud Run's frontend already
// writes one per request, with the full URL, requestSize, serverIp,
// nanosecond latency and a severity derived from the status. The gate
// walk found tako's copy sitting next to it in the console, carrying
// strictly less — every request logged twice, and request-log volume
// doubled for nothing.
func TestNoRequestLogInTheCloud(t *testing.T) {
	buf, restore := cloudLogs(t, "tako-test-01") // deployed
	defer restore()

	logRequest(httptest.NewRequest("GET", "/api/photos", nil), 200, 14*time.Millisecond)
	if buf.Len() != 0 {
		t.Errorf("tako wrote a request log the platform already writes:\n%s", buf.String())
	}
}
