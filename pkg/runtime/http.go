package runtime

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gsandoval/tako/pkg/backends"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	tako "github.com/gsandoval/tako"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

// JSONHandler adapts an API endpoint to net/http: decode the request via
// the generated decode function, invoke the handler, encode the result as
// JSON. A nil decode means the endpoint takes an empty Req. Binding code
// is generated statically — decode is plain function calls, never
// reflection.
func JSONHandler[Req, Res any](e tako.Endpoint[Req, Res], decode func(*http.Request) (Req, error), opts ...HandlerOption) http.Handler {
	var conf handlerConf
	for _, o := range opts {
		o(&conf)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := incomingRequestID(r)
		session := loadSession(r)
		if conf.requireAuth && session == nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": authRequired})
			return
		}
		// Cross-service context: session id + trace id ride as values
		// (outgoing Calls re-propagate them); a propagated deadline
		// bounds this handler too.
		base, cancel := callContext(r, session, reqID)
		defer cancel()
		ctx := &requestCtx{
			Context: base,
			log:     requestLogger(r, reqID),
		}
		if session != nil {
			ctx.user = &tako.User{ID: session.UID, Email: session.Email, Name: session.Name}
		}

		var req Req
		if decode != nil {
			var err error
			if req, err = decode(r); err != nil {
				writeError(w, ctx, reqID, err)
				return
			}
		}
		res, err := e.Call(ctx, req)
		if err != nil {
			writeError(w, ctx, reqID, err)
			return
		}
		writeJSON(w, http.StatusOK, res)
	})
}

// writeError maps a handler or decode error to its response. Errors with
// an explicit status (Errorf, NotFound, decode failures) surface as-is;
// anything else is an opaque 500 — the real error is logged with the
// request id, never sent to the client.
func writeError(w http.ResponseWriter, ctx tako.Ctx, reqID string, err error) {
	if he, ok := err.(interface{ HTTPStatus() int }); ok {
		writeJSON(w, he.HTTPStatus(), map[string]string{"error": err.Error()})
		return
	}
	ctx.Log().Error("handler error", "err", err)
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	_ = enc.Encode(v)
}

func newRequestID() string {
	var b [3]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Serve runs the service on PORT (default 8080) with request logging.
// Generated mains call it last.
//
// It shuts down as a lame duck: on SIGTERM (Cloud Run replacing the
// revision) or SIGINT (Ctrl-C) the listener closes so no new request is
// accepted, the requests already executing run to completion, and only
// then do the shutdown hooks flush. Without this the process died where
// it stood — responses cut mid-write, buffered metrics lost — and Cloud
// Run's routine drain was indistinguishable from a crash.
//
// A second signal exits immediately, for the handler that will not let
// go.
func Serve(h http.Handler) {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	var live inflight
	// h2c.NewHandler serves BOTH: it detects HTTP/2's cleartext preface
	// and upgrades, and passes everything else through as HTTP/1.1. So
	// the server is always HTTP/2-capable and turning the platform
	// setting on can never arrive at a container that cannot answer.
	//
	// Cloud Run terminates TLS at its frontend and forwards cleartext to
	// the container — always, for every service. A tako server therefore
	// never speaks TLS itself, and h2c is not a downgrade from "real"
	// HTTP/2: it is the only shape the frontend-to-container hop has,
	// and that hop is encrypted by Google's infrastructure regardless.
	handler := h2c.NewHandler(track(&live, logRequests(h)), &http2.Server{})
	srv := &http.Server{Addr: ":" + port, Handler: handler}

	// signalled is when SIGTERM landed — the moment Cloud Run's 10s clock
	// started. Every budget after this is measured from it, so a drain
	// that finishes early is repaid to the flush instead of being lost.
	var signalled time.Time
	stopping := make(chan os.Signal, 2)
	signal.Notify(stopping, syscall.SIGINT, syscall.SIGTERM)
	if os.Getenv("TAKO_DEV_GUARD") == "1" {
		// tako dev holds this process's stdin: EOF means the dev loop
		// died — however it died — and an orphaned service holding the
		// port and the entity-store lock helps nobody. Stop with it,
		// through the same drain as a signal: a reload that drops the
		// requests it was serving is the bug this path used to have.
		go func() {
			_, _ = io.Copy(io.Discard, os.Stdin)
			fmt.Fprintln(os.Stderr, "tako: dev parent exited — shutting down")
			stopping <- syscall.SIGTERM
		}()
	}

	drained := make(chan struct{})
	go func() {
		defer close(drained)
		<-stopping
		signalled = time.Now()
		go func() {
			<-stopping
			fmt.Fprintln(os.Stderr, "tako: second signal — exiting now")
			os.Exit(130)
		}()
		drain(srv, &live)
	}()

	err := srv.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		<-drained // in-flight requests are done; now the hooks may flush
		runShutdownHooks(flushBudget(signalled))
		say("⏻ stopped (%s)", "stopped (%s)", time.Since(signalled).Round(10*time.Millisecond))
		return
	}
	if err != nil && strings.Contains(err.Error(), "address already in use") {
		// A collision here answers requests with SOMEONE ELSE'S responses
		// — mysterious 404s. Name the fix.
		fmt.Fprintf(os.Stderr, "tako: serve: port %s is already in use by another process — set PORT (e.g. PORT=8090 tako dev) or free it (`lsof -i :%s`)\n", port, port)
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "tako: serve: %v\n", err)
		os.Exit(1)
	}
}

// logRequests emits one record per request. What that record IS depends
// on where the service is running — a terse dev-console line locally, a
// structured httpRequest in the cloud — and logRequest owns that choice
// so this middleware stays about timing and status.
//
// It also records the per-endpoint metrics, and that is not duplication
// of what Cloud Run publishes: Cloud Run's request_count and
// request_latencies carry a `route` label that is NEVER POPULATED for an
// ordinary service (measured: 88 series, empty on every one). Alerting
// on one endpoint is therefore impossible from platform metrics alone,
// and tako writes the route because tako is the only one that knows it.
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		took := time.Since(start)
		recordRequest(r, sw.status, took)
		logRequest(r, sw.status, took)
	})
}

// RequestsMetric and LatencyMetric are the per-endpoint metrics every
// tako service records without being asked. Their names are part of the
// deploy contract: the provisioner writes alert queries against them.
const (
	RequestsMetric = "requests"
	LatencyMetric  = "request_ms"
)

// requestMetrics is the sink, wired by boot. Nil in tests that do not
// care, because recording must never be the reason a handler fails.
var requestMetrics backends.Metrics

// SetRequestMetrics wires the per-endpoint sink.
func SetRequestMetrics(m backends.Metrics) { requestMetrics = m }

// recordRequest counts the request and times it, labelled by the ROUTE
// PATTERN rather than the path.
//
// The pattern is what keeps cardinality bounded: /api/photos/{id}/image
// is one series no matter how many photos exist, where the raw path
// would be one series per photo and would bankrupt the metric. Go 1.22's
// ServeMux reports the matched pattern, so this is the framework's own
// knowledge rather than a guess.
func recordRequest(r *http.Request, status int, took time.Duration) {
	if requestMetrics == nil {
		return
	}
	route := r.Pattern
	if route == "" {
		// No pattern matched: a 404. Bucketing those under one label
		// keeps an unmatched-path flood from minting a series each.
		route = "<unmatched>"
	}
	labels := map[string]string{"route": route, "status": statusClass(status)}
	requestMetrics.Add(r.Context(), RequestsMetric, labels, 1)
	requestMetrics.Observe(r.Context(), LatencyMetric,
		map[string]string{"route": route}, float64(took.Milliseconds()))
}

// statusClass buckets a status into 2xx/3xx/4xx/5xx: five values at
// most, so the label cannot grow without bound.
func statusClass(status int) string {
	switch {
	case status >= 500:
		return "5xx"
	case status >= 400:
		return "4xx"
	case status >= 300:
		return "3xx"
	case status >= 200:
		return "2xx"
	}
	return "1xx"
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
