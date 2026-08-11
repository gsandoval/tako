package runtime

// CR3: /_tako/push accepted anything.
//
// PushHandler's own doc said "Phase F adds the envelope + OIDC
// verification". The envelope landed; the verification did not, while
// CronDispatch — the other route Google calls — verifies. The only thing
// between the internet and an app's workers was that async services
// happen to deploy non-public, which is a fact computed in another
// package and one graph change away from being false.
//
// Both deliverers (Pub/Sub push and Cloud Tasks) carry a Google-signed
// OIDC token whose audience is the worker's URL and whose identity is
// the worker's own service account — the same pair the cron route
// already checks.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tako "github.com/gsandoval/tako"
)

type cr3Job struct {
	Work int `json:"work"`
}

// ran records whether the delivery reached the handler at all, which is
// the question every test below is really asking.
func pushTestHandler(t *testing.T, ran *bool) http.Handler {
	t.Helper()
	h := tako.Worker(tako.Queue[cr3Job]("cr3"), func(ctx tako.Ctx, msg cr3Job) error {
		*ran = true
		return nil
	})
	return PushHandler("cr3", h)
}

func TestPushRefusesAnUnauthenticatedDeliveryInTheCloud(t *testing.T) {
	t.Setenv("TAKO_BACKEND", "gcp")
	t.Setenv("TAKO_PUSH_AUDIENCE", "https://svc-abc.run.app")
	t.Setenv("TAKO_PUSH_INVOKER", "svc-sa@p.iam.gserviceaccount.com")

	var ran bool
	w := httptest.NewRecorder()
	pushTestHandler(t, &ran).ServeHTTP(w, httptest.NewRequest("POST", "/_tako/push", strings.NewReader(`{"work":1}`)))

	if w.Code != http.StatusUnauthorized {
		t.Errorf("an unsigned POST to /_tako/push was answered %d — anyone on the "+
			"internet can inject work into this app's handlers", w.Code)
	}
	if ran {
		t.Error("the handler ran on an unauthenticated delivery")
	}
}

func TestPushRefusesAGarbageBearerToken(t *testing.T) {
	t.Setenv("TAKO_BACKEND", "gcp")
	t.Setenv("TAKO_PUSH_AUDIENCE", "https://svc-abc.run.app")
	t.Setenv("TAKO_PUSH_INVOKER", "svc-sa@p.iam.gserviceaccount.com")

	r := httptest.NewRequest("POST", "/_tako/push", strings.NewReader(`{"work":1}`))
	r.Header.Set("Authorization", "Bearer not.a.jwt")
	var ran bool
	w := httptest.NewRecorder()
	pushTestHandler(t, &ran).ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("a bogus token was answered %d, want 403", w.Code)
	}
	if ran {
		t.Error("the handler ran on a delivery whose token did not verify")
	}
}

// Fail CLOSED: a deployed service whose identity the deploy did not
// inject refuses rather than falling back to accepting everything. The
// old behaviour is exactly what that fallback would restore.
func TestPushRefusesWhenTheDeployInjectedNoIdentity(t *testing.T) {
	t.Setenv("TAKO_BACKEND", "gcp")
	t.Setenv("TAKO_PUSH_AUDIENCE", "")
	t.Setenv("TAKO_PUSH_INVOKER", "")

	var ran bool
	w := httptest.NewRecorder()
	pushTestHandler(t, &ran).ServeHTTP(w, httptest.NewRequest("POST", "/_tako/push", strings.NewReader(`{}`)))

	if w.Code < 400 {
		t.Errorf("a service with no push identity accepted the delivery (%d)", w.Code)
	}
	if !strings.Contains(w.Body.String(), "tako deploy") {
		t.Errorf("the refusal does not say how to fix it: %q", w.Body.String())
	}
	if ran {
		t.Error("the handler ran without the service knowing who may deliver to it")
	}
}

// Locally there is no Google to sign anything: the dispatcher posts
// straight to this endpoint, and tako dev must keep working with no GCP
// anywhere near it.
func TestPushAcceptsLocalDeliveryUnsigned(t *testing.T) {
	t.Setenv("TAKO_BACKEND", "local")

	var ran bool
	w := httptest.NewRecorder()
	pushTestHandler(t, &ran).ServeHTTP(w, httptest.NewRequest("POST", "/_tako/push", strings.NewReader(`{"work":1}`)))

	if w.Code != http.StatusNoContent {
		t.Errorf("local delivery was answered %d, want 204: %s", w.Code, w.Body.String())
	}
	if !ran {
		t.Error("local delivery never reached the handler")
	}
}
