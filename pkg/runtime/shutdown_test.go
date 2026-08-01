package runtime

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gsandoval/tako/internal/hooks"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

// serveOn starts an http.Server on a free port and returns it with its
// address, the way Serve would have.
func serveOn(t *testing.T, h http.Handler) (*http.Server, string, *inflight) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var live inflight
	// Mirrors Serve's own wiring — h2c wrapper included, since the
	// hijacking it does is precisely what the drain has to cope with.
	srv := &http.Server{Handler: h2c.NewHandler(track(&live, h), &http2.Server{})}
	go func() { _ = srv.Serve(ln) }()
	return srv, "http://" + ln.Addr().String(), &live
}

// The whole point of the lame duck: a request that is already executing
// when SIGTERM lands runs to completion instead of being cut off.
func TestDrainLetsInFlightRequestsFinish(t *testing.T) {
	started := make(chan struct{})
	var finished atomic.Bool
	srv, addr, live := serveOn(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		time.Sleep(300 * time.Millisecond)
		finished.Store(true)
		fmt.Fprint(w, "done")
	}))

	result := make(chan int, 1)
	go func() {
		resp, err := http.Get(addr + "/slow")
		if err != nil {
			result <- 0
			return
		}
		defer resp.Body.Close()
		result <- resp.StatusCode
	}()

	<-started
	t.Setenv("TAKO_DRAIN", "7")
	drain(srv, live)

	if !finished.Load() {
		t.Error("the in-flight handler did not run to completion — this is the bug the drain exists to fix")
	}
	select {
	case code := <-result:
		if code != http.StatusOK {
			t.Errorf("in-flight request got %d, want 200 — a draining instance must still answer what it accepted", code)
		}
	case <-time.After(2 * time.Second):
		t.Error("the client never got its response")
	}
}

// Lame duck means exactly that: the door closes first, then the room
// empties. A request arriving after the drain begins must not be served.
func TestDrainStopsAcceptingNewRequests(t *testing.T) {
	srv, addr, live := serveOn(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Setenv("TAKO_DRAIN", "7")
	drain(srv, live)

	if _, err := http.Get(addr + "/after"); err == nil {
		t.Error("the listener still accepted a request after the drain — new work must be refused, not queued behind a dying instance")
	}
}

// A handler that outlives the budget is cut off rather than held past
// Cloud Run's kill. The drain must RETURN, not wait forever.
func TestDrainGivesUpAtItsBudget(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	started := make(chan struct{})
	srv, addr, live := serveOn(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
	}))
	go func() {
		resp, err := http.Get(addr + "/forever")
		if err == nil {
			resp.Body.Close()
		}
	}()
	<-started

	t.Setenv("TAKO_DRAIN", "1")
	done := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		drain(srv, live)
		done <- time.Since(start)
	}()
	select {
	case took := <-done:
		if took > 3*time.Second {
			t.Errorf("drain took %s for a 1s budget — the kill would have landed first", took)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("drain never returned; a wedged handler must not hold the process past SIGKILL")
	}
}

// The flush is why the drain has a budget at all.
func TestShutdownHooksRun(t *testing.T) {
	var ran atomic.Bool
	hooks.OnShutdown(func(ctx context.Context) { ran.Store(true) })
	runShutdownHooks(time.Second)
	if !ran.Load() {
		t.Error("a registered shutdown hook did not run — buffered metrics would be lost on every revision swap")
	}
}

// A hook that overruns is abandoned. The kill is coming either way, and
// holding the process open buys nothing.
func TestSlowShutdownHookIsAbandoned(t *testing.T) {
	hooks.OnShutdown(func(ctx context.Context) { <-ctx.Done() })
	start := time.Now()
	runShutdownHooks(200 * time.Millisecond)
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("runShutdownHooks took %s for a 200ms budget", took)
	}
}

// The budget split has to fit inside the window Cloud Run actually
// gives, with room for the kill to land late.
func TestBudgetsFitTheShutdownWindow(t *testing.T) {
	t.Setenv("TAKO_DRAIN", "7")
	d := drainBudget()
	f := flushBudget(d)
	if d+f >= shutdownWindow {
		t.Errorf("drain %s + flush %s does not fit in Cloud Run's %s window", d, f, shutdownWindow)
	}
	if f < flushFloor {
		t.Errorf("flush budget %s is below the floor %s — the last metrics flush needs room", f, flushFloor)
	}

	// A footprint that claims nearly the whole window still leaves the
	// floor for flushing, because analysis capped it at 9s.
	t.Setenv("TAKO_DRAIN", "9")
	if got := flushBudget(drainBudget()); got < flushFloor {
		t.Errorf("flush budget %s under a 9s drain, want at least %s", got, flushFloor)
	}

	// No TAKO_DRAIN means nobody deployed this: a dev run, where Ctrl-C
	// should feel immediate rather than safe.
	t.Setenv("TAKO_DRAIN", "")
	if got := drainBudget(); got != devDrain {
		t.Errorf("undeployed drain = %s, want the dev default %s", got, devDrain)
	}
}

// h2cClient speaks HTTP/2 with prior knowledge over a plain TCP socket
// — exactly what Cloud Run's frontend does to a container when the port
// is named h2c, and what `curl --http2-prior-knowledge` does locally.
func h2cClient() *http.Client {
	return &http.Client{Transport: &http2.Transport{
		AllowHTTP: true,
		DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
	}}
}

// The server must answer real HTTP/2, not merely tolerate the setting:
// Cloud Run stops downgrading once the port is named h2c, so a container
// that cannot speak it fails every request instead of falling back.
func TestServerSpeaksHTTP2(t *testing.T) {
	var gotProto string
	srv, addr, live := serveOn(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotProto = r.Proto
		fmt.Fprint(w, "ok")
	}))
	defer func() { t.Setenv("TAKO_DRAIN", "1"); drain(srv, live) }()

	resp, err := h2cClient().Get(addr + "/h2")
	if err != nil {
		t.Fatalf("h2c request failed — Cloud Run would send exactly this once HTTP2 is on: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d", resp.StatusCode)
	}
	if gotProto != "HTTP/2.0" {
		t.Errorf("handler saw %q, want HTTP/2.0 — h2c did not take effect", gotProto)
	}
}

// HTTP/1.1 must keep working unchanged: h2c.NewHandler wraps every
// service, deployed with the setting or not, and Cloud Run's default is
// still to downgrade.
func TestHTTP11StillWorksAlongsideH2C(t *testing.T) {
	var gotProto string
	srv, addr, live := serveOn(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotProto = r.Proto
		fmt.Fprint(w, "ok")
	}))
	defer func() { t.Setenv("TAKO_DRAIN", "1"); drain(srv, live) }()

	resp, err := http.Get(addr + "/plain")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if gotProto != "HTTP/1.1" {
		t.Errorf("handler saw %q, want HTTP/1.1", gotProto)
	}
}

// The interaction that would have gone unnoticed: an h2c connection is
// HIJACKED, and http.Server.Shutdown stops tracking hijacked conns — so
// Shutdown returns while HTTP/2 streams are still running. The lame duck
// has to hold for them too, or it silently only covers HTTP/1.1.
func TestDrainWaitsForHTTP2Requests(t *testing.T) {
	started := make(chan struct{})
	var finished atomic.Bool
	srv, addr, live := serveOn(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		time.Sleep(400 * time.Millisecond)
		finished.Store(true)
		fmt.Fprint(w, "done")
	}))

	done := make(chan int, 1)
	go func() {
		resp, err := h2cClient().Get(addr + "/slow-h2")
		if err != nil {
			done <- 0
			return
		}
		defer resp.Body.Close()
		done <- resp.StatusCode
	}()

	<-started
	t.Setenv("TAKO_DRAIN", "7")
	drain(srv, live)

	if !finished.Load() {
		t.Error("an in-flight HTTP/2 request was abandoned — Shutdown does not track hijacked connections, so the counter must")
	}
	select {
	case code := <-done:
		if code != http.StatusOK {
			t.Errorf("HTTP/2 request got %d, want 200", code)
		}
	case <-time.After(2 * time.Second):
		t.Error("the HTTP/2 client never got its response")
	}
}
