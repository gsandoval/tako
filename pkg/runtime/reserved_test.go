package runtime

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gsandoval/tako/internal/routes"
)

// FL16: the dev-readiness answer exists only under `tako dev`.
//
// It is how the dev loop tells its own child from whatever else holds
// the port. That makes it a URL that answers under a condition, so
// both halves of the condition are worth pinning: a deployed service
// has no nonce and must not answer, and a dev child must.
func TestDevReadyAnswersOnlyWithANonce(t *testing.T) {
	app := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot) // "the app ran", distinctly
	})
	framework := http.NewServeMux()
	// devReadyFirst is what Serve wraps every service with; Reserved is
	// only there for a service that declares auth, a bucket or a cron.
	// Both layers are in play here because a route inside /_tako/ must
	// still reach the framework mux rather than the app.
	h := devReadyFirst(Reserved(framework, app))

	// The regression this file did not have: a service with NO reserved
	// mux — no auth, no bucket, no cron, which is `hello` — still has to
	// answer the probe. It did not, so `tako dev` read the 404 as a
	// stranger holding the port and killed its own healthy child.
	t.Run("a service with no framework mux still answers", func(t *testing.T) {
		defer swapNonce(t, "abc123")()
		bare := devReadyFirst(app) // Serve's wrapping, and nothing else
		rec := httptest.NewRecorder()
		bare.ServeHTTP(rec, httptest.NewRequest("GET", routes.DevReady, nil))
		if rec.Code != http.StatusOK || rec.Body.String() != "abc123" {
			t.Fatalf("dev probe on a bare service = %d %q, want 200 abc123", rec.Code, rec.Body)
		}
	})

	t.Run("deployed: no nonce, no endpoint", func(t *testing.T) {
		defer swapNonce(t, "")()
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", routes.DevReady, nil))
		// Reserved, so the framework mux answers — 404, not the app.
		if rec.Code == http.StatusOK {
			t.Errorf("a deployed service answered the dev probe: %d %q", rec.Code, rec.Body)
		}
		if rec.Code == http.StatusTeapot {
			t.Error("the dev path fell through to the app's router")
		}
	})

	t.Run("under tako dev: the nonce, and nothing else", func(t *testing.T) {
		defer swapNonce(t, "abc123")()
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", routes.DevReady, nil))
		if rec.Code != http.StatusOK || rec.Body.String() != "abc123" {
			t.Fatalf("dev probe = %d %q, want 200 abc123", rec.Code, rec.Body)
		}
		// A proof-of-identity response is not a cacheable one.
		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("Cache-Control = %q, want no-store", got)
		}
	})

	t.Run("it claims one path, not the namespace", func(t *testing.T) {
		defer swapNonce(t, "abc123")()
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", routes.Z+"something-else", nil))
		if rec.Body.String() == "abc123" {
			t.Error("the dev answer leaked onto another reserved path")
		}
	})

	t.Run("the app's own routes are untouched", func(t *testing.T) {
		defer swapNonce(t, "abc123")()
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
		if rec.Code != http.StatusTeapot {
			t.Errorf("app route = %d, want the app to have answered", rec.Code)
		}
	})
}

// swapNonce sets the package's dev nonce for one test and returns the
// restore. It is read from the environment once at init, so a test that
// wants both states has to reach the variable.
func swapNonce(t *testing.T, v string) func() {
	t.Helper()
	prev := devNonce
	devNonce = v
	return func() { devNonce = prev }
}
