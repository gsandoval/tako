package devserver

// P7: the dev loop had no tests at all — 447 lines, and the first thing
// a new user meets. These cover the pure decisions inside it: what the
// banner says, which file writes are ignored, and which service the
// loop runs. The process half (watch → build → run → restart) stays on
// the manual gate scripts; it needs a real toolchain and a real port.

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gsandoval/tako/internal/routes"
	"github.com/gsandoval/tako/pkg/analyzer"
)

// The banner is the first sentence tako ever says about your app, and
// it is derived — a miscount here is a user reading the wrong shape of
// their own system.
func TestCountLine(t *testing.T) {
	cases := []struct {
		name  string
		graph *analyzer.AppGraph
		want  string
	}{
		{
			name: "one endpoint, singular throughout",
			graph: &analyzer.AppGraph{
				Services: []analyzer.Service{{Name: "hello"}},
				Handlers: []analyzer.Handler{{Kind: "api"}},
			},
			want: "1 service, 1 endpoint",
		},
		{
			name: "zero endpoints still pluralizes correctly",
			graph: &analyzer.AppGraph{
				Services: []analyzer.Service{{Name: "worker"}},
				Handlers: []analyzer.Handler{{Kind: "worker"}},
			},
			want: "1 service, 0 endpoints, 1 worker",
		},
		{
			name: "each async kind counted under its own name",
			graph: &analyzer.AppGraph{
				Services: []analyzer.Service{{Name: "gallery"}, {Name: "gallery-makethumb"}},
				Handlers: []analyzer.Handler{
					{Kind: "api"}, {Kind: "page"},
					{Kind: "worker"},
					{Kind: "subscribe"}, {Kind: "subscribe"},
					{Kind: "cron"},
				},
			},
			want: "2 services, 2 endpoints, 1 worker, 2 subscribers, 1 cron",
		},
		{
			name: "a site is named, and only sites count as sites",
			graph: &analyzer.AppGraph{
				Services:  []analyzer.Service{{Name: "journal"}},
				Handlers:  []analyzer.Handler{{Kind: "page"}},
				Resources: []analyzer.Resource{{Kind: "site"}, {Kind: "entity"}},
			},
			want: "1 service, 1 endpoint, 1 site",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := CountLine(c.graph); got != c.want {
				t.Errorf("CountLine = %q, want %q", got, c.want)
			}
		})
	}
}

// The loop writes into .tako/ every cycle. If those writes counted as
// source changes the server would reload forever, so this filter is
// load-bearing rather than cosmetic.
func TestHiddenPathIgnoresTheLoopsOwnWrites(t *testing.T) {
	dir := "/app"
	hidden := []string{
		"/app/.tako/gen/hello/main.go",
		"/app/.tako/data/entities.db",
		"/app/.git/index",
		"/app/.env",
		"/app/pkg/.hidden/x.go",
	}
	for _, p := range hidden {
		if !hiddenPath(dir, filepath.FromSlash(p)) {
			t.Errorf("hiddenPath(%s) = false — the loop would reload on its own write", p)
		}
	}
	visible := []string{
		"/app/app.go",
		"/app/web/index.html",
		"/app/internal/thumbs/thumbs.go",
	}
	for _, p := range visible {
		if hiddenPath(dir, filepath.FromSlash(p)) {
			t.Errorf("hiddenPath(%s) = true — a real edit would not reload", p)
		}
	}
	// A path outside the watched tree is not "hidden", it is irrelevant;
	// reporting it hidden would be the right answer for the wrong reason.
	if hiddenPath(dir, filepath.FromSlash("/elsewhere/.secret/x")) {
		t.Error("a path outside dir was judged by dir's rules")
	}
}

// tako dev runs ONE service — the app service. Jobs and singletons run
// in-process, and async-only services have no traffic to serve, so
// picking the wrong one means the dev server answers nothing.
func TestAppServiceIsTheOneThatServesTraffic(t *testing.T) {
	g := &analyzer.AppGraph{
		Services: []analyzer.Service{
			{Name: "gallery", Package: "gallery"},
			{Name: "gallery-makethumb", Package: "gallery"},
			{Name: "gallery-settle", Package: "gallery", Job: true},
			{Name: "gallery-invoicer", Package: "gallery", Singleton: true},
		},
		Handlers: []analyzer.Handler{
			{Kind: "api", Service: "gallery"},
			{Kind: "worker", Service: "gallery-makethumb"},
		},
	}
	svc, err := appService(g)
	if err != nil {
		t.Fatalf("appService: %v", err)
	}
	if svc.Name != "gallery" {
		t.Errorf("appService = %q, want gallery", svc.Name)
	}
}

// An app with nothing public is a real thing to write — a repo of only
// workers — and the dev server has to say so rather than pick one at
// random or serve nothing silently.
func TestAppServiceRefusesWhenNothingServes(t *testing.T) {
	g := &analyzer.AppGraph{
		Services: []analyzer.Service{{Name: "jobs-makethumb", Package: "jobs"}},
		Handlers: []analyzer.Handler{{Kind: "worker", Service: "jobs-makethumb"}},
	}
	if _, err := appService(g); err == nil {
		t.Error("an app with no public routes was accepted; dev would serve nothing")
	}
}

// A private-only package is not an app service either: WithPrivate means
// no public traffic, which is the whole point of the option.
func TestPrivateRoutesDoNotMakeAnAppService(t *testing.T) {
	g := &analyzer.AppGraph{
		Services: []analyzer.Service{{Name: "billing", Package: "billing"}},
		Handlers: []analyzer.Handler{{Kind: "api", Service: "billing", Private: true}},
	}
	if _, err := appService(g); err == nil {
		t.Error("a private-only service was treated as the app service")
	}
}

// Failure output is indented under its heading; a message that loses its
// indentation runs into the console's own lines.
func TestIndentKeepsEveryLine(t *testing.T) {
	got := indent("first\nsecond\n")
	if got != "  first\n  second" {
		t.Errorf("indent = %q", got)
	}
	if strings.HasSuffix(got, "\n") {
		t.Error("indent left a trailing newline, which doubles the blank line after it")
	}
}

// --- FL16: the readiness probe proves it reached THIS child ----------
//
// The process half of the loop stays on the manual gates, but the
// probe is not the process half: it is a decision about an HTTP
// response, and it can be held to a real listener without a toolchain.

// A stranger on the port is not readiness.
//
// This is the defect FL16 fixed, and it is worth stating as a test
// because the old probe passed it in the worst way: a TCP dial to a
// port another process holds SUCCEEDS, so `tako dev` printed `ready`
// about someone else's server while its own child was exiting with an
// address-in-use error. The browser then answered from the other app.
func TestWaitReadyRefusesAListenerThatIsNotOurs(t *testing.T) {
	// A foreign server that answers everything, including the dev path
	// — a second tako dev is exactly this, with a different nonce.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "some other tako app")
	}))
	defer srv.Close()

	err := waitReady(portOf(t, srv), "our-nonce", make(chan struct{}), 300*time.Millisecond)
	var foreign *foreignListenerError
	if !errors.As(err, &foreign) {
		t.Fatalf("waitReady = %v, want a foreign-listener error", err)
	}
	// The text has to name the port and the fix: this is read by someone
	// whose app appears to be broken.
	for _, want := range []string{"ANOTHER tako dev", "PORT="} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error text is missing %q:\n%s", want, err)
		}
	}
}

// Item 35, as a test: a listener that answers the app but 404s the
// identity path is NOT proof of a stranger.
//
// It was read as one, and the consequence was the worst kind: `tako dev`
// killed its own healthy child. Every service answers the probe now
// (Serve wraps it, not Reserved), so a 404 means something tako did not
// start — worth a message, never worth a verdict before the child has
// had its say.
func TestAListenerThat404sTheProbeIsNotDeclaredForeign(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r) // a service with no dev route, or any other server
	}))
	defer srv.Close()

	err := waitReady(portOf(t, srv), "our-nonce", make(chan struct{}), 200*time.Millisecond)

	var foreign *foreignListenerError
	if errors.As(err, &foreign) {
		t.Fatalf("a 404 was called a collision: %v", err)
	}
	var unidentified *unidentifiedListenerError
	if !errors.As(err, &unidentified) {
		t.Fatalf("waitReady = %v, want an unidentified-listener error", err)
	}
	// And it must not repeat the advice that could not be followed:
	// the old text sent people to free a port tako had already released.
	if strings.Contains(err.Error(), "another process has it") {
		t.Errorf("the weaker case still claims a collision:\n%s", err)
	}
}

// The child exiting outranks anything the probe saw. A real collision
// IS the child failing to bind, and it explains itself on the way out.
func TestTheChildsExitOutranksTheProbe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	exited := make(chan struct{})
	close(exited)
	if err := waitReady(portOf(t, srv), "our-nonce", exited, time.Second); !errors.Is(err, errExited) {
		t.Fatalf("waitReady = %v, want errExited", err)
	}
}

// Our own child, answering with its nonce, IS readiness.
func TestWaitReadyAcceptsOurOwnChild(t *testing.T) {
	const nonce = "3f1c0b9a"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != routes.DevReady {
			http.NotFound(w, r)
			return
		}
		io.WriteString(w, nonce)
	}))
	defer srv.Close()

	if err := waitReady(portOf(t, srv), nonce, make(chan struct{}), 2*time.Second); err != nil {
		t.Fatalf("waitReady = %v, want nil", err)
	}
}

// A child that dies during startup ends the wait at once.
//
// Before FL16 nothing watched the process, so a service that failed
// immediately still cost the full five-second timeout before the loop
// said anything — five seconds of a terminal that looks like it is
// working.
func TestWaitReadyStopsWhenTheChildExits(t *testing.T) {
	exited := make(chan struct{})
	close(exited)

	start := time.Now()
	// Port 1 is not in use and not usable: nothing will ever answer, so
	// only the exit signal can end this.
	err := waitReady("1", "our-nonce", exited, 5*time.Second)
	if !errors.Is(err, errExited) {
		t.Fatalf("waitReady = %v, want errExited", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("waited %s for a child already gone; want it to end at once", elapsed)
	}
}

// Nothing listening at all is still a timeout, and says so — the case
// where the app is genuinely slow or genuinely broken.
func TestWaitReadyTimesOutWhenNothingAnswers(t *testing.T) {
	err := waitReady("1", "our-nonce", make(chan struct{}), 100*time.Millisecond)
	var foreign *foreignListenerError
	if errors.As(err, &foreign) || errors.Is(err, errExited) {
		t.Fatalf("waitReady = %v, want a plain timeout", err)
	}
	if !strings.Contains(err.Error(), "did not start listening") {
		t.Errorf("timeout text = %q", err)
	}
}

// Two starts must not share an identity: a nonce that repeated would
// let the PREVIOUS child vouch for the current one across a restart.
func TestNoncesDiffer(t *testing.T) {
	a, err := newNonce()
	if err != nil {
		t.Fatal(err)
	}
	b, err := newNonce()
	if err != nil {
		t.Fatal(err)
	}
	if a == b || a == "" {
		t.Fatalf("nonces %q and %q", a, b)
	}
}

func portOf(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Port()
}
