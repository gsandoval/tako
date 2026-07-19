package runtime

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	tako "github.com/gsandoval/tako"
)

// JSONHandler adapts an API endpoint to net/http: build the request Ctx,
// invoke the handler, encode the result as JSON. S0.2 serves empty-Req
// endpoints only; request binding is generated statically from F1 on.
func JSONHandler[Req, Res any](e tako.Endpoint[Req, Res]) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := NewCtx(r.Context())
		var req Req
		res, err := e.Call(ctx, req)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, res)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	_ = enc.Encode(v)
}

// Serve runs the service on PORT (default 8080) with request logging.
// Generated mains call it last.
func Serve(h http.Handler) {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	if err := http.ListenAndServe(":"+port, logRequests(h)); err != nil {
		fmt.Fprintf(os.Stderr, "tako: serve: %v\n", err)
		os.Exit(1)
	}
}

// logRequests prints one plain line per request during dev; structured
// request logs are an F1 feature.
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
