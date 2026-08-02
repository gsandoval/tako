package gcp

// P2: the seam. Before Client.Base every URL in this package was a
// literal, so nothing here could be exercised without a real project —
// and four defects in one stage reached production because of it. These
// tests are the ones that were impossible to write.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gsandoval/tako/pkg/provision"
)

// fakeAPI serves every Google API at once. The host is the first path
// segment (Client.url puts it there when Base is set), so a test can
// assert WHICH api was addressed, not just that something was.
type fakeAPI struct {
	*httptest.Server
	mu    struct{ calls []call }
	reply map[string]any // "HOST/path prefix" → JSON body
	code  map[string]int // same key → status, default 200
}

type call struct {
	Method string
	Host   string
	Path   string
	Body   map[string]any
}

func newFakeAPI(t *testing.T) *fakeAPI {
	t.Helper()
	f := &fakeAPI{reply: map[string]any{}, code: map[string]int{}}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, path, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.calls = append(f.mu.calls, call{r.Method, host, "/" + path, body})

		key := host + "/" + path
		status := 200
		for k, c := range f.code {
			if strings.HasPrefix(key, k) {
				status = c
			}
		}
		// Long-running operations answer done by default. Without this
		// waitOperation polls a never-finishing operation for its full
		// three-minute budget, which turns every test into a hang.
		var reply any = map[string]any{}
		if strings.Contains(key, "/operations/") {
			reply = map[string]any{"name": path, "done": true}
		}
		for k, v := range f.reply {
			if strings.HasPrefix(key, k) {
				reply = v
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(reply)
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeAPI) client() *Client {
	return &Client{HC: f.Server.Client(), Project: "p", Region: "us-central1", Base: f.Server.URL}
}

func (f *fakeAPI) lastTo(t *testing.T, host string) call {
	t.Helper()
	for i := len(f.mu.calls) - 1; i >= 0; i-- {
		if f.mu.calls[i].Host == host {
			return f.mu.calls[i]
		}
	}
	t.Fatalf("nothing was sent to %s; calls were %+v", host, f.mu.calls)
	return call{}
}

// find returns the one call matching method and a path substring. A
// create is followed by an operation poll and a setIamPolicy, so
// "the last call to this host" is the wrong request to assert on.
func (f *fakeAPI) find(t *testing.T, method, pathHas string) call {
	t.Helper()
	for _, c := range f.mu.calls {
		if c.Method == method && strings.Contains(c.Path, pathHas) {
			return c
		}
	}
	t.Fatalf("no %s call whose path contains %q; calls were %+v", method, pathHas, f.mu.calls)
	return call{}
}

// THE regression. U2 shipped a container port carrying a name and no
// number; Cloud Run reads the absent field as 0 and refuses with
// "must be 0 < port_number < 65536". EVERY service in EVERY app failed
// to create, and no unit test could catch it because nothing could see
// the request body. Now it can.
func TestCreatedServiceSendsAPortNumber(t *testing.T) {
	f := newFakeAPI(t)
	f.reply[runHost+"/v2/projects/p/locations/us-central1/services"] = map[string]any{"name": "operations/1", "done": true}
	r := &RunServices{C: f.client()}

	err := r.Create(context.Background(), provision.Object{
		Kind: provision.KindRunService,
		Name: "gallery",
		// The spec desired.go actually produces — a thinner one would
		// test the fixture rather than the builder.
		Spec: map[string]any{
			"image":          "gcr.io/p/gallery@sha256:abc",
			"serviceAccount": "gallery-sa@p.iam.gserviceaccount.com",
			"env":            map[string]any{"TAKO_BACKEND": "gcp"},
			"public":         true,
			"scaling": map[string]any{
				"min": 0, "max": 100, "concurrency": 80,
				"cpu": "1", "memory": "512Mi",
				"cpuAlwaysOn": false, "startupBoost": false,
				"affinity": false, "http2": true, "timeout": 300,
			},
		},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	sent := f.find(t, "POST", "/services")
	tmpl, _ := sent.Body["template"].(map[string]any)
	containers, _ := tmpl["containers"].([]any)
	if len(containers) == 0 {
		t.Fatalf("no containers in %+v", sent.Body)
	}
	ports, _ := containers[0].(map[string]any)["ports"].([]any)
	if len(ports) == 0 {
		t.Fatal("no ports declared")
	}
	p, _ := ports[0].(map[string]any)
	num, ok := p["containerPort"].(float64)
	if !ok {
		t.Fatalf("containerPort is absent from %+v — Cloud Run reads that as 0 and refuses the service", p)
	}
	if num <= 0 || num >= 65536 {
		t.Errorf("containerPort = %v, must be 0 < n < 65536", num)
	}
	if p["name"] != "h2c" {
		t.Errorf("port name = %v, want h2c for an http2 service", p["name"])
	}
}

// Domain mappings live on a REGIONAL host. Sending them to the global
// one 404s, and the difference was previously invisible to tests.
func TestDomainMappingsUseTheRegionalHost(t *testing.T) {
	f := newFakeAPI(t)
	r := &DomainMappings{C: f.client()}
	_, _ = r.List(context.Background())

	want := "us-central1-" + runHost
	if got := f.mu.calls[len(f.mu.calls)-1].Host; got != want {
		t.Errorf("domain mappings addressed %s, want %s", got, want)
	}
}

// The taxonomy end to end: a real 403 body from a real (fake) server,
// through do → parseAPIError → Classify, arrives as the remediation a
// user can paste.
func TestDisabledAPIBecomesTheEnableCommand(t *testing.T) {
	f := newFakeAPI(t)
	f.code[runHost] = 403
	f.reply[runHost] = map[string]any{"error": map[string]any{
		"message": "Cloud Run Admin API has not been used in project p before or it is disabled. Enable it by visiting run.googleapis.com then retry.",
		"details": []any{map[string]any{"reason": "SERVICE_DISABLED"}},
	}}
	r := &RunServices{C: f.client()}

	_, err := r.List(context.Background())
	if err == nil {
		t.Fatal("a 403 SERVICE_DISABLED must not read as success")
	}
	if !strings.Contains(err.Error(), "gcloud services enable run.googleapis.com --project p") {
		t.Errorf("err = %q — a disabled API must arrive as the exact enable command", err)
	}
}

// A 403 that is NOT a disabled API is a permission problem, and names
// the narrowest role rather than the API.
func TestForbiddenNamesTheRole(t *testing.T) {
	f := newFakeAPI(t)
	f.code[runHost] = 403
	f.reply[runHost] = map[string]any{"error": map[string]any{"message": "caller lacks permission"}}
	r := &RunServices{C: f.client()}

	_, err := r.List(context.Background())
	if err == nil || !strings.Contains(err.Error(), "roles/run.viewer") {
		t.Errorf("err = %v — a plain 403 must name the role that unblocks it", err)
	}
}

// Observation converges past an API the app never enabled: destroy must
// not demand cloudtasks from an app that has no queues.
func TestDisabledAPIObservesAsEmpty(t *testing.T) {
	f := newFakeAPI(t)
	f.code[tasksHost] = 403
	f.reply[tasksHost] = map[string]any{"error": map[string]any{
		"message": "Cloud Tasks API has not been used in project p before or it is disabled.",
		"details": []any{map[string]any{"reason": "SERVICE_DISABLED"}},
	}}
	r := &TasksQueues{C: f.client()}

	got, err := r.List(context.Background())
	if err != nil {
		t.Fatalf("a disabled API is an empty inventory, not a failure: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("observed %d queues from a disabled API", len(got))
	}
}

// Google answers a malformed URL with an HTML page. Dumping it helps
// nobody, so the taxonomy replaces it with the actual diagnosis.
func TestHTMLErrorPageBecomesADiagnosis(t *testing.T) {
	f := newFakeAPI(t)
	f.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		_, _ = w.Write([]byte("<!DOCTYPE html><html><title>Error 404</title>…"))
	})
	r := &RunServices{C: f.client()}

	_, err := r.List(context.Background())
	if err == nil || !strings.Contains(err.Error(), "HTML (non-JSON) error page") {
		t.Errorf("err = %v — an HTML answer should be named as a malformed URL, not pasted", err)
	}
}

// Every host constant must be a real Google API host: the whole point
// of routing through url() is that a typo cannot hide in one adapter.
func TestHostConstantsAreWellFormed(t *testing.T) {
	c := &Client{Project: "p", Region: "us-central1"}
	for _, h := range []string{runHost, iamHost, crmHost, storageHost, pubsubHost,
		datastoreHost, schedulerHost, tasksHost, artifactHost, secretHost,
		serviceuseHost, loggingHost, identityHost, monitoringHost, iamCredsHost} {
		if !strings.HasSuffix(h, ".googleapis.com") {
			t.Errorf("%q is not a googleapis.com host", h)
		}
		if got := c.url(h, "/v1/x"); got != "https://"+h+"/v1/x" {
			t.Errorf("url(%q) = %q", h, got)
		}
	}
}

// The release walk's destroy stopped halfway on a one-off
// "503 from pubsub.googleapis.com" deleting a subscription. Reconciling
// converges, so re-running finished it — but the user had to know that.
// A transient answer on an idempotent call is now retried.
func TestTransientFailureIsRetriedOnIdempotentCalls(t *testing.T) {
	old := retryBackoff
	retryBackoff = []time.Duration{time.Millisecond, time.Millisecond}
	t.Cleanup(func() { retryBackoff = old })

	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(503)
			_, _ = w.Write([]byte(`{"error":{"message":"The service was unable to fulfill your request."}}`))
			return
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	c := &Client{HC: srv.Client(), Project: "p", Region: "us-central1", Base: srv.URL}

	if err := c.do(context.Background(), http.MethodDelete, c.url(pubsubHost, "/v1/x"), nil, nil); err != nil {
		t.Fatalf("a 503 on a DELETE should have been retried: %v", err)
	}
	if calls != 2 {
		t.Errorf("%d call(s), want 2 — the retry did not happen", calls)
	}
}

// A create is NOT retried: repeating a POST can make two of something,
// and a deploy that has re-observed the world is the safe way back.
func TestCreatesAreNotRetried(t *testing.T) {
	old := retryBackoff
	retryBackoff = []time.Duration{time.Millisecond}
	t.Cleanup(func() { retryBackoff = old })

	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(503)
		_, _ = w.Write([]byte(`{"error":{"message":"unavailable"}}`))
	}))
	t.Cleanup(srv.Close)
	c := &Client{HC: srv.Client(), Project: "p", Region: "us-central1", Base: srv.URL}

	if err := c.do(context.Background(), http.MethodPost, c.url(runHost, "/v2/x"), map[string]any{"a": 1}, nil); err == nil {
		t.Fatal("a 503 on a POST must surface, not retry")
	}
	if calls != 1 {
		t.Errorf("%d call(s), want 1 — a create was retried", calls)
	}
}

// Google's read-only custom methods are POSTs, and the X2 release walk
// died on exactly that: preflight's :testIamPermissions took a 429
// "Quota exceeded for quota metric 'Read requests'" and could not
// retry, killing a deploy that had touched nothing. A call that reads
// is safe to re-send whatever verb carries it.
func TestReadOnlyPostsAreRetried(t *testing.T) {
	old := retryBackoff
	retryBackoff = []time.Duration{time.Millisecond}
	t.Cleanup(func() { retryBackoff = old })

	for _, url := range []string{
		"/v1/projects/p:testIamPermissions",
		"/v1/projects/p:getIamPolicy",
		"/v1/projects/p:runQuery",
	} {
		t.Run(url, func(t *testing.T) {
			var calls int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls == 1 {
					w.WriteHeader(429)
					_, _ = w.Write([]byte(`{"error":{"message":"Quota exceeded for quota metric 'Read requests'"}}`))
					return
				}
				_, _ = w.Write([]byte(`{"permissions":["run.services.create"]}`))
			}))
			t.Cleanup(srv.Close)
			c := &Client{HC: srv.Client(), Project: "p", Region: "us-central1", Base: srv.URL}

			var out struct {
				Permissions []string `json:"permissions"`
			}
			if err := c.do(context.Background(), http.MethodPost, c.url(crmHost, url), map[string]any{"permissions": []string{"x"}}, &out); err != nil {
				t.Fatalf("a 429 on a read-only POST must be retried: %v", err)
			}
			if calls != 2 {
				t.Errorf("%d call(s), want 2 — the read was not retried", calls)
			}
		})
	}
}

// The allowlist is a list, not a pattern: a colon-suffixed POST that
// MUTATES must still fail fast, or a retried :commit writes twice.
func TestMutatingCustomMethodsAreNotRetried(t *testing.T) {
	old := retryBackoff
	retryBackoff = []time.Duration{time.Millisecond}
	t.Cleanup(func() { retryBackoff = old })

	for _, url := range []string{
		"/v1/projects/p:commit",
		"/v1/projects/p:setIamPolicy",
		"/v1/projects/p/topics/t:publish",
	} {
		t.Run(url, func(t *testing.T) {
			var calls int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.WriteHeader(429)
				_, _ = w.Write([]byte(`{"error":{"message":"rate limited"}}`))
			}))
			t.Cleanup(srv.Close)
			c := &Client{HC: srv.Client(), Project: "p", Region: "us-central1", Base: srv.URL}

			if err := c.do(context.Background(), http.MethodPost, c.url(datastoreHost, url), map[string]any{"a": 1}, nil); err == nil {
				t.Fatal("a 429 on a mutating POST must surface, not retry")
			}
			if calls != 1 {
				t.Errorf("%d call(s), want 1 — a mutation was retried", calls)
			}
		})
	}
}

// A permanent refusal must not be retried into a slow failure: 403 is
// an answer, not a hiccup.
func TestPermanentFailuresAreNotRetried(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(403)
		_, _ = w.Write([]byte(`{"error":{"message":"caller lacks permission"}}`))
	}))
	t.Cleanup(srv.Close)
	c := &Client{HC: srv.Client(), Project: "p", Region: "us-central1", Base: srv.URL}

	if err := c.do(context.Background(), http.MethodDelete, c.url(pubsubHost, "/v1/x"), nil, nil); err == nil {
		t.Fatal("a 403 must surface")
	}
	if calls != 1 {
		t.Errorf("%d call(s), want 1 — a permanent refusal was retried", calls)
	}
}

// The retried body must be re-sent, not consumed by the first attempt.
func TestRetryResendsTheBody(t *testing.T) {
	old := retryBackoff
	retryBackoff = []time.Duration{time.Millisecond}
	t.Cleanup(func() { retryBackoff = old })

	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(raw))
		if len(bodies) == 1 {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	c := &Client{HC: srv.Client(), Project: "p", Region: "us-central1", Base: srv.URL}

	if err := c.do(context.Background(), http.MethodPut, c.url(pubsubHost, "/v1/x"), map[string]any{"topic": "t"}, nil); err != nil {
		t.Fatalf("put: %v", err)
	}
	if len(bodies) != 2 || bodies[0] != bodies[1] || !strings.Contains(bodies[1], `"topic":"t"`) {
		t.Errorf("bodies = %q — the retry must re-send what the first attempt sent", bodies)
	}
}

// Every colon-suffixed POST in the tree must be CLASSIFIED — either as
// a read that may be retried or as a mutation that may not. The
// allowlist was first written by eyeballing a few call sites and missed
// two reads (logging's entries:list, datastore's :lookup); the second
// one surfaced as a 429 that killed `tako logs` mid-investigation, the
// same failure the allowlist had just been created to prevent.
//
// This scans the source rather than trusting a reviewer to notice, so a
// new custom method cannot be added without a decision about retrying it.
func TestEveryCustomMethodPostIsClassified(t *testing.T) {
	classified := map[string]bool{}
	for _, v := range readOnlyVerbs {
		classified[v] = true
	}
	for _, v := range mutatingVerbs {
		classified[v] = true
	}

	// URLs are built from string literals; find the colon verbs among them.
	verb := regexp.MustCompile(`"[^"]*(:[a-zA-Z][a-zA-Z0-9]*)"`)
	roots := []string{".", "../../backends/gcp"}
	seen := map[string]string{}
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			t.Fatalf("reading %s: %v", root, err)
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			path := filepath.Join(root, name)
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(src), "MethodPost") {
				continue
			}
			for _, m := range verb.FindAllStringSubmatch(string(src), -1) {
				// Skip time literals and the like: a verb is alphabetic
				// and follows a path segment.
				if strings.Contains(m[0], "://") {
					continue
				}
				seen[m[1]] = path
			}
		}
	}
	if len(seen) == 0 {
		t.Fatal("scanned no custom methods — the scan itself broke, which would make this test vacuous")
	}
	for v, where := range seen {
		if !classified[v] {
			t.Errorf("custom method %q (%s) is neither in readOnlyVerbs nor mutatingVerbs — "+
				"decide whether re-sending it is safe; an unclassified POST silently gets no retry", v, where)
		}
	}
	t.Logf("classified %d custom methods", len(seen))
}
