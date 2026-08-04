package gcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gsandoval/tako/pkg/provision"
)

// Cloud Tasks blocks reusing a deleted queue's name for 7 days, so a
// destroy-then-redeploy inside that window cannot converge. tako used to
// surface Google's raw message, which says the name existed too recently
// and nothing else — not how long the wait is, not that another
// environment sidesteps it. Both are things tako knows and the user does
// not. Found on the T3 walk (2026-08-01) and worked around with --env
// for the rest of it.
func TestQueueNameReuseNamesTheWaitAndTheWayOut(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"error":{"code":400,"status":"FAILED_PRECONDITION",` +
			`"message":"The queue cannot be created because a queue with this name existed too recently."}}`))
	}))
	t.Cleanup(srv.Close)
	q := &TasksQueues{C: &Client{HC: srv.Client(), Project: "p", Region: "us-central1", Base: srv.URL}}

	err := q.Create(context.Background(), provision.Object{Name: "u1-mailer-tako",
		Spec: map[string]any{"rate": 10, "attempts": 3}})
	if err == nil {
		t.Fatal("a blocked recreate must be an error")
	}
	for _, want := range []string{
		`cannot recreate the tasks queue "u1-mailer-tako"`,
		"7 days",
		"tako cannot work around it",
		"tako deploy --env <name>",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q\n  missing %q", err, want)
		}
	}
	// No countdown: the API carries no delete timestamp, so any remaining
	// time would be invented.
	for _, banned := range []string{"days left", "remaining", "until"} {
		if strings.Contains(err.Error(), banned) {
			t.Errorf("error %q promises a countdown tako cannot compute (%q)", err, banned)
		}
	}
}

// The arm must not swallow other failures: a 403 on the same call is a
// permission problem with a different remediation, and reporting it as a
// name-reuse wait would send the user to wait out a week for nothing.
func TestQueueCreateKeepsTheOtherFailures(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		_, _ = w.Write([]byte(`{"error":{"code":403,"message":"Permission 'cloudtasks.queues.create' denied on resource."}}`))
	}))
	t.Cleanup(srv.Close)
	q := &TasksQueues{C: &Client{HC: srv.Client(), Project: "p", Region: "us-central1", Base: srv.URL}}

	err := q.Create(context.Background(), provision.Object{Name: "u1-mailer-tako", Spec: map[string]any{}})
	if err == nil {
		t.Fatal("a 403 must still be an error")
	}
	if strings.Contains(err.Error(), "existed too recently") || strings.Contains(err.Error(), "7 days") {
		t.Errorf("a permission failure reported as a name-reuse wait: %q", err)
	}
	if !strings.Contains(err.Error(), "roles/cloudtasks.admin") {
		t.Errorf("error %q lost its remediation", err)
	}
}
