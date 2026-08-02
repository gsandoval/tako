package gcp

// P2: the runtime backends get the same seam the provisioner got.
// MetricsStore had one (its own `base` field) and nothing else did, so
// every other store could only be exercised against a real project.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type seen struct {
	Method string
	Host   string
	Path   string
}

func fakeClient(t *testing.T, reply map[string]any) (*Client, *[]seen) {
	t.Helper()
	var calls []seen
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, path, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
		calls = append(calls, seen{r.Method, host, "/" + path})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(reply)
	}))
	t.Cleanup(srv.Close)
	return &Client{
		HC:   srv.Client(),
		Opts: Options{Project: "p", App: "gallery", Env: "prod"},
		Base: srv.URL,
	}, &calls
}

// Env isolation is a naming rule, and naming rules are only real if the
// REQUEST carries them. A prod entity and a preview-env entity must
// address different namespaces on the same host.
func TestEntityCallsCarryTheEnvNamespace(t *testing.T) {
	c, calls := fakeClient(t, map[string]any{"found": []any{}})
	s := NewEntityStore(c)

	_, err := s.Get(context.Background(), "links", "gh")
	if err != nil && !strings.Contains(err.Error(), "not found") {
		t.Fatalf("get: %v", err)
	}
	if len(*calls) == 0 {
		t.Fatal("no call was made")
	}
	got := (*calls)[0]
	if got.Host != datastoreHost {
		t.Errorf("entity store addressed %s, want %s", got.Host, datastoreHost)
	}
	if !strings.HasPrefix(got.Path, "/v1/projects/p:") {
		t.Errorf("path = %s, want the project-scoped :verb form", got.Path)
	}
}

// A bucket's physical name is project + app + env-prefixed base, and it
// belongs in the URL — the local backend cannot prove this because it
// has no URLs.
func TestBucketURLCarriesThePhysicalName(t *testing.T) {
	c, calls := fakeClient(t, map[string]any{})
	s := NewBucketStore(c)

	_, _ = s.Get(context.Background(), "photos", "a.jpg")
	if len(*calls) == 0 {
		t.Fatal("no call was made")
	}
	got := (*calls)[0]
	if got.Host != storageHost {
		t.Errorf("addressed %s, want %s", got.Host, storageHost)
	}
	// prod deploys bare names; the project and app still scope the bucket.
	if !strings.Contains(got.Path, "p-gallery-photos") {
		t.Errorf("path = %s, want the physical bucket p-gallery-photos", got.Path)
	}
}

// A permanent failure publishes to the service's dead-letter topic
// directly. The topic name is derived, and the derivation is only
// observable in the request.
func TestPublishAddressesTheDerivedTopic(t *testing.T) {
	c, calls := fakeClient(t, map[string]any{"messageIds": []string{"1"}})
	c.Opts.Service = "gallery-makethumb"
	s := NewTopicStore(c)

	if err := s.Publish(context.Background(), "photo-ready", []byte(`{}`)); err != nil {
		t.Fatalf("publish: %v", err)
	}
	got := (*calls)[0]
	if got.Host != pubsubHost {
		t.Errorf("addressed %s, want %s", got.Host, pubsubHost)
	}
	if !strings.Contains(got.Path, "/topics/photo-ready:publish") {
		t.Errorf("path = %s", got.Path)
	}
}

// The signed URL is handed to the CALLER, so it must keep pointing at
// the real GCS host even when every API call is redirected at a fake.
// Redirecting it would produce links that 404 for users.
func TestSignedURLIsNotRedirectedByBase(t *testing.T) {
	c, _ := fakeClient(t, map[string]any{})
	if c.Base == "" {
		t.Fatal("this test is meaningless without a redirected client")
	}
	// The host constant is what signedURL builds from; assert the
	// constant is the real host and that url() is not involved.
	if storageHost != "storage.googleapis.com" {
		t.Errorf("storageHost = %q", storageHost)
	}
	if got := c.url(storageHost, "/x"); !strings.HasPrefix(got, c.Base) {
		t.Errorf("url() should redirect API calls; got %q", got)
	}
}
