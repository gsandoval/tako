package runtime

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	tako "github.com/gsandoval/tako"
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
		reqID := newRequestID()
		session := loadSession(r)
		if conf.requireAuth && session == nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": authRequired})
			return
		}
		ctx := &requestCtx{
			Context: r.Context(),
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
func Serve(h http.Handler) {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	err := http.ListenAndServe(":"+port, logRequests(h))
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
