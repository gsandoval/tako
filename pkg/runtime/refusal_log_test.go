package runtime

// Discovered item 49: a refused trigger was invisible in the logs.
//
// verifyPushCaller and CronDispatch both answered the caller and logged
// nothing, so an operator watching `tako logs` saw only the platform's
// request line — and tako's own 403 is indistinguishable from Cloud
// Run's, which are different problems with different fixes (a stale
// audience after a service URL change versus a missing run.invoker
// grant). Found by spending twenty minutes on exactly that ambiguity
// while validating CR2/CR3 against a real project.
//
// What each refusal has to carry: WARNING severity, so it is findable
// by severity next to the platform's own lines; the reason; and the two
// expectations, because comparing them against the deliverer's own OIDC
// config IS the remediation. Neither is a secret — a service URL and an
// SA email, both readable from the deploy.

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gsandoval/tako/internal/routes"
)

// refusalLine asserts the shape every refusal shares and returns the
// record, so each test below only states what is its own.
func refusalLine(t *testing.T, buf *bytes.Buffer, want map[string]string) map[string]any {
	t.Helper()
	if strings.TrimSpace(buf.String()) == "" {
		t.Fatal("tako refused the request and logged nothing — from outside the " +
			"container this refusal is indistinguishable from the platform's own")
	}
	rec := lastLine(t, buf)
	if rec["severity"] != "WARNING" {
		t.Errorf("a refusal logged at severity %v — a refused delivery is not routine, "+
			"and severity is how an operator finds it", rec["severity"])
	}
	for k, v := range want {
		if got, _ := rec[k].(string); !strings.Contains(got, v) {
			t.Errorf("refusal line %s = %q, want it to contain %q", k, got, v)
		}
	}
	return rec
}

func TestRefusedDeliveryIsLogged(t *testing.T) {
	t.Setenv("TAKO_BACKEND", "gcp")
	t.Setenv("TAKO_PUSH_AUDIENCE", "https://svc-abc.run.app")
	t.Setenv("TAKO_PUSH_INVOKER", "svc-sa@p.iam.gserviceaccount.com")
	buf, restore := cloudLogs(t, "p")
	defer restore()

	r := httptest.NewRequest("POST", "/_tako/push", strings.NewReader(`{"work":1}`))
	r.Header.Set("Authorization", "Bearer not.a.jwt")
	var ran bool
	w := httptest.NewRecorder()
	pushTestHandler(t, &ran).ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Fatalf("a bogus token was answered %d, want 403", w.Code)
	}
	refusalLine(t, buf, map[string]string{
		"handler":           "cr3",
		"reason":            "malformed JWT",
		"expected_audience": "https://svc-abc.run.app",
		"expected_invoker":  "svc-sa@p.iam.gserviceaccount.com",
	})
}

// The status belongs in the line too: a 401 (nobody presented a token)
// and a 403 (a token that did not verify) are different stories, and
// the response body carrying them goes to Pub/Sub, not to the logs.
func TestRefusedDeliveryLogsTheStatus(t *testing.T) {
	t.Setenv("TAKO_BACKEND", "gcp")
	t.Setenv("TAKO_PUSH_AUDIENCE", "https://svc-abc.run.app")
	t.Setenv("TAKO_PUSH_INVOKER", "svc-sa@p.iam.gserviceaccount.com")
	buf, restore := cloudLogs(t, "p")
	defer restore()

	var ran bool
	w := httptest.NewRecorder()
	pushTestHandler(t, &ran).ServeHTTP(w, httptest.NewRequest("POST", "/_tako/push", strings.NewReader(`{}`)))

	rec := refusalLine(t, buf, map[string]string{"reason": "bearer token"})
	if got, ok := rec["status"].(float64); !ok || int(got) != http.StatusUnauthorized {
		t.Errorf("refusal line status = %v, want 401", rec["status"])
	}
}

// The fail-closed refusal is tako's own bug, not the caller's, and it is
// the one an operator is least equipped to guess at from a 500.
func TestMissingPushIdentityIsLogged(t *testing.T) {
	t.Setenv("TAKO_BACKEND", "gcp")
	t.Setenv("TAKO_PUSH_AUDIENCE", "")
	t.Setenv("TAKO_PUSH_INVOKER", "")
	buf, restore := cloudLogs(t, "p")
	defer restore()

	var ran bool
	w := httptest.NewRecorder()
	pushTestHandler(t, &ran).ServeHTTP(w, httptest.NewRequest("POST", "/_tako/push", strings.NewReader(`{}`)))

	refusalLine(t, buf, map[string]string{"handler": "cr3", "reason": "tako deploy"})
}

func TestRefusedCronTriggerIsLogged(t *testing.T) {
	t.Setenv("TAKO_CRON_AUDIENCE", "https://svc-abc.run.app")
	t.Setenv("TAKO_CRON_INVOKER", "svc-sa@p.iam.gserviceaccount.com")
	buf, restore := cloudLogs(t, "p")
	defer restore()

	mux := http.NewServeMux()
	mux.Handle(routes.PatternCron, CronDispatch())
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", routes.CronRoute("nightly"), nil))

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("an unsigned cron trigger was answered %d, want 401", w.Code)
	}
	refusalLine(t, buf, map[string]string{
		"cron":              "nightly",
		"reason":            "bearer token",
		"expected_audience": "https://svc-abc.run.app",
		"expected_invoker":  "svc-sa@p.iam.gserviceaccount.com",
	})
}

// Cron's own fail-closed case. It answers 403 rather than push's 500 —
// the status this route has always answered, left alone here and noted
// as divergence 50 rather than changed inside a logging fix.
func TestMissingCronIdentityIsLogged(t *testing.T) {
	t.Setenv("TAKO_CRON_AUDIENCE", "")
	t.Setenv("TAKO_CRON_INVOKER", "")
	buf, restore := cloudLogs(t, "p")
	defer restore()

	mux := http.NewServeMux()
	mux.Handle(routes.PatternCron, CronDispatch())
	r := httptest.NewRequest("POST", routes.CronRoute("nightly"), nil)
	r.Header.Set("Authorization", "Bearer not.a.jwt")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)

	refusalLine(t, buf, map[string]string{"cron": "nightly", "reason": "no cron trigger identity"})
}
