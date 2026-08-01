package runtime

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
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
			log:     slog.Default().With("req", reqID),
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
		budget := flushBudget(drainBudget())
		start := time.Now()
		runShutdownHooks(budget)
		say("⏻ stopped (%s)", "stopped (%s)", time.Since(start).Round(10*time.Millisecond))
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

// logRequests prints one terse line per request in dev; structured JSON
// request logs become the default when the cloud runtime lands (Phase E).
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		fmt.Printf("%s %s → %d (%.1fms)\n",
			r.Method, r.URL.Path, sw.status, float64(time.Since(start).Microseconds())/1000)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
