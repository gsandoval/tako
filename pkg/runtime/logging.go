package runtime

// Cloud-shaped logging. Locally a tako service writes Go's default
// human-readable lines and the dev console's terse request lines;
// deployed, it writes the JSON shape Cloud Logging parses, because
// Cloud Logging files everything a container prints as severity DEFAULT
// unless the line is JSON carrying a `severity` field. Without this an
// ERROR log and an INFO log are indistinguishable once they land, and
// `tako logs` has nothing to filter on.
//
// The special field names are Google's, verified against
// docs.cloud.google.com/logging/docs/structured-logging:
//
//	severity                        a LogSeverity string
//	message                         the text
//	time                            ISO 8601
//	logging.googleapis.com/trace    projects/<id>/traces/<trace-id>
//	logging.googleapis.com/spanId   the span
//	httpRequest                     a LogEntry HttpRequest record
//
// The load-bearing detail is the severity RENAME, not the JSON: slog
// spells its levels DEBUG/INFO/WARN/ERROR, and Cloud Logging wants
// WARNING, not WARN. A JSON handler alone would still lose severity.

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
)

// Cloud Logging's special JSON keys.
const (
	severityKey = "severity"
	messageKey  = "message"
	traceKey    = "logging.googleapis.com/trace"
	spanKey     = "logging.googleapis.com/spanId"
	httpKey     = "httpRequest"
)

// traceProject is the project log lines name in their trace field. It is
// set at boot from TAKO_PROJECT (which the deploy injects) and stays
// empty everywhere else, which is what keeps local lines free of a
// trace key that would point at nothing.
var traceProject string

// useCloudLogging installs the JSON handler as the process default and
// records the project for trace correlation. BootGCP calls it; BootLocal
// deliberately does not — the dev console is a product, and JSON in it
// would be a regression.
//
// tako owns the default logger in the cloud. A generated main calls
// Boot() first and gives user code no seam before it, so a user's own
// slog.SetDefault in an init() would be overwritten here. That is the
// deliberate trade: owning the log format is the same kind of derivation
// as owning the service's shape, and it is the only way severity
// filtering and `tako logs` mean anything by default.
func useCloudLogging(project string) {
	traceProject = project
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		ReplaceAttr: cloudAttrs,
	})))
}

// cloudAttrs renames slog's three built-in keys to Cloud Logging's.
func cloudAttrs(groups []string, a slog.Attr) slog.Attr {
	if len(groups) > 0 {
		return a // only the top-level built-ins are special
	}
	switch a.Key {
	case slog.LevelKey:
		lvl, _ := a.Value.Any().(slog.Level)
		return slog.String(severityKey, severityOf(lvl))
	case slog.MessageKey:
		return slog.String(messageKey, a.Value.String())
	case slog.TimeKey:
		return slog.String(slog.TimeKey, a.Value.Time().UTC().Format(time.RFC3339Nano))
	}
	return a
}

// severityOf maps a slog level onto a Cloud Logging LogSeverity. The
// WARN → WARNING rename is the whole reason this function exists;
// levels between the named ones round DOWN, the way slog's own
// Level.String reports them (5 is "WARN+1", not "ERROR").
func severityOf(l slog.Level) string {
	switch {
	case l < slog.LevelInfo:
		return "DEBUG"
	case l < slog.LevelWarn:
		return "INFO"
	case l < slog.LevelError:
		return "WARNING"
	default:
		return "ERROR"
	}
}

// traceAttrs returns the correlation fields for a request, or nothing.
// Cloud Run sends X-Cloud-Trace-Context: TRACE_ID/SPAN_ID;o=1. With
// these attached, every line a handler writes nests under its request
// in the Logs Explorer; without them (local, tests, push deliveries)
// the `req` attribute keeps doing the correlating on its own.
func traceAttrs(r *http.Request) []any {
	if traceProject == "" {
		return nil
	}
	trace, span := parseTraceContext(r.Header.Get("X-Cloud-Trace-Context"))
	if trace == "" {
		return nil
	}
	out := []any{traceKey, "projects/" + traceProject + "/traces/" + trace}
	if span != "" {
		out = append(out, spanKey, span)
	}
	return out
}

// parseTraceContext splits TRACE_ID/SPAN_ID;o=1 into its two ids.
func parseTraceContext(h string) (trace, span string) {
	if h == "" {
		return "", ""
	}
	trace, rest, found := strings.Cut(h, "/")
	if !found {
		return trace, ""
	}
	span, _, _ = strings.Cut(rest, ";")
	return trace, span
}

// requestLogger is the logger a request's Ctx carries: the tako request
// id always, the trace correlation when Cloud Run supplied one. Every
// handler kind builds its Ctx through this, so one request cannot log
// differently from another depending on which handler served it.
func requestLogger(r *http.Request, reqID string) *slog.Logger {
	return slog.Default().With(append([]any{"req", reqID}, traceAttrs(r)...)...)
}

// logRequest prints the dev console's one-line-per-request summary.
//
// It is LOCAL ONLY, deliberately. Cloud Run's frontend already writes a
// request log for every request — to run.googleapis.com/requests, with
// the full URL, requestSize, serverIp, nanosecond latency, the trace,
// and a severity already derived from the status. Tako used to write a
// second one alongside it, carrying strictly less, which doubled
// request-log volume and made every request appear twice in the
// console. Deployed, the platform's record is the request log; tako
// adds nothing to it.
//
// What tako still owns in the cloud is the HANDLER's lines, which carry
// the same trace so they nest under the platform's request entry.
func logRequest(r *http.Request, status int, took time.Duration) {
	if traceProject != "" {
		return // Cloud Run already logged this request, and logged it better
	}
	fmt.Printf("%s %s → %d (%.1fms)\n",
		r.Method, r.URL.Path, status, float64(took.Microseconds())/1000)
}
