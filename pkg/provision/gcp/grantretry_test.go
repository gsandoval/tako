package gcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gsandoval/tako/pkg/provision"
)

// The live failure this reproduces, from journal's first deploy to
// tako-test-01:
//
//	tako: update iam grants project: write the IAM policy of project
//	tako-test-01: Service account journal-sa@… does not exist.
//	(HTTP 400 from cloudresourcemanager.googleapis.com)
//
// The service account was created seconds earlier in the same run. IAM
// creation is eventually consistent, and RC3 made a PROJECT-scope grant
// part of every service — so the one target that could not retry became
// the one applied soonest after a fresh member exists.
//
// Re-running fixed it, which is the tell: nothing was wrong, the write
// was just early.
func TestAProjectGrantWaitsForAFreshServiceAccount(t *testing.T) {
	old := grantRetryDelay
	grantRetryDelay = time.Millisecond
	t.Cleanup(func() { grantRetryDelay = old })

	var writes int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ":setIamPolicy") {
			writes++
			if writes == 1 {
				w.WriteHeader(400)
				_, _ = w.Write([]byte(`{"error":{"code":400,"message":"Service account new-sa@p.iam.gserviceaccount.com does not exist."}}`))
				return
			}
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"bindings":[]}`))
			return
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"bindings":[],"etag":"abc"}`))
	}))
	t.Cleanup(srv.Close)

	g := &Grants{C: &Client{HC: srv.Client(), Project: "p", Region: "us-central1", Base: srv.URL}}
	obj := provision.Object{
		Kind: provision.KindIamGrants,
		Name: "project",
		Spec: map[string]any{
			"target": map[string]any{"type": provision.TargetProject, "id": "p"},
			"bindings": []any{map[string]any{
				"role":   "roles/monitoring.metricWriter",
				"member": "serviceAccount:new-sa@p.iam.gserviceaccount.com",
			}},
		},
	}
	if err := g.Create(context.Background(), obj); err != nil {
		t.Fatalf("the project grant gave up on an unpropagated member: %v", err)
	}
	if writes != 2 {
		t.Errorf("%d policy write(s), want 2 — the retry did not happen", writes)
	}
}

// The carve-out that gate protected is still there: a NON-project
// target that no longer reads back is gone, and converging on it is
// what keeps a partial destroy from failing forever.
func TestAGoneTargetStillConverges(t *testing.T) {
	old := grantRetryDelay
	grantRetryDelay = time.Millisecond
	t.Cleanup(func() { grantRetryDelay = old })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Everything about this bucket answers 404: it is gone.
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"error":{"code":404,"message":"Not Found"}}`))
	}))
	t.Cleanup(srv.Close)

	g := &Grants{C: &Client{HC: srv.Client(), Project: "p", Region: "us-central1", Base: srv.URL}}
	obj := provision.Object{
		Kind: provision.KindIamGrants,
		Name: "bucket p-gone",
		Spec: map[string]any{
			"target":   map[string]any{"type": provision.TargetBucket, "id": "p-gone"},
			"bindings": []any{},
		},
	}
	if err := g.Create(context.Background(), obj); err != nil {
		t.Errorf("a gone bucket should converge, not fail: %v", err)
	}
}
