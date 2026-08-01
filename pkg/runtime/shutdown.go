package runtime

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"github.com/gsandoval/tako/internal/hooks"
)

// Cloud Run sends SIGTERM and kills the instance 10 seconds later. That
// window is FIXED — neither the v1 RevisionSpec nor the v2
// RevisionTemplate has a field for it, and gcloud has no flag — so tako
// divides it rather than pretending to set it.
//
// The division is ADAPTIVE, not budgeted. Shutdown does not spend a
// fixed drain and then flush; it waits for the last request to finish,
// however long that actually takes, and hands whatever is left of the
// window to the flush. An instance that is idle at SIGTERM — the common
// case, since Cloud Run stops routing to it before signalling — flushes
// immediately and exits in milliseconds. A busy one gets up to the
// ceiling, and the flush still keeps its floor.
const (
	// shutdownWindow is what Cloud Run gives us, total.
	shutdownWindow = 10 * time.Second
	// killMargin is held back so the process exits before SIGKILL rather
	// than being cut off by it.
	killMargin = 1 * time.Second
	// flushFloor is the least the shutdown hooks are ever given, even if
	// the drain ran right up to the ceiling.
	flushFloor = 1 * time.Second
	// jobFlush is what a job's final flush gets. A job that finishes is
	// not inside Cloud Run's 10s window — that clock only starts if the
	// execution is cancelled — so this is bounded by patience, not by a
	// kill.
	jobFlush = 5 * time.Second
)

// drainCeiling caps the wait for in-flight requests. Reached only when a
// handler genuinely runs that long — the drain ends the moment the last
// one returns.
//
// The SAME ceiling applies locally. Dev used to cut off at 2s, chosen
// for how quickly Ctrl-C should feel, which made local and cloud differ
// exactly where the difference matters most: when a request is actually
// hanging. An idle instance drains instantly either way, so the shorter
// local number bought nothing except a surprise at deploy time. A second
// signal is the escape hatch for a wedged handler, not a lower ceiling.
//
// A var, not a const, only so tests can shorten it — there is no way to
// set it from user code, and deliberately so.
var drainCeiling = 7 * time.Second

// drainBudget caps the wait for in-flight requests — a ceiling, not an
// allowance: the drain returns as soon as the last handler does.
func drainBudget() time.Duration { return drainCeiling }

// flushBudget is what is left of the window once the drain has actually
// finished, which is why it takes the moment shutdown began rather than
// a configured number. Draining fast is repaid as flush time; draining
// to the ceiling still leaves the floor.
func flushBudget(since time.Time) time.Duration {
	left := shutdownWindow - time.Since(since) - killMargin
	if left < flushFloor {
		return flushFloor
	}
	return left
}

// inCloud reports whether this process was deployed, which decides
// whether shutdown narrates to the dev console or to the log stream.
func inCloud() bool { return os.Getenv("TAKO_BACKEND") == "gcp" }

// say prints one shutdown line in the voice of wherever we are running.
func say(dev, cloud string, args ...any) {
	if inCloud() {
		fmt.Printf("tako: "+cloud+"\n", args...)
		return
	}
	fmt.Printf(dev+"\n", args...)
}

// runShutdownHooks gives every registered hook the remaining budget,
// together — one slow hook must not eat another's share. A hook that
// overruns is abandoned, not waited on: the kill is coming either way,
// and holding the process open buys nothing.
func runShutdownHooks(budget time.Duration) {
	fns := hooks.ShutdownHooks()
	if len(fns) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for _, fn := range fns {
			fn(ctx)
		}
	}()
	select {
	case <-done:
	case <-ctx.Done():
		say("⏻ shutdown cleanup did not finish within %s — abandoning it",
			"shutdown cleanup did not finish within %s — abandoning it", budget)
	}
}

// drain stops accepting and waits out the requests already executing.
// It is http.Server.Shutdown with a budget and a narration: the budget
// because Cloud Run's kill does not wait, and the narration because a
// request cut off at the deadline is a real consequence that belongs in
// the log rather than in a mystery 502.
func drain(srv *http.Server, live *inflight) {
	budget := drainBudget()
	if n := live.load(); n > 0 {
		say("⏻ draining — %s in flight", "SIGTERM — draining (%s in flight)", plural(n))
	} else {
		say("⏻ draining", "SIGTERM — draining")
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	err := srv.Shutdown(ctx)
	if err == nil {
		// http.Server.Shutdown waits for HTTP/1.1 requests, but an h2c
		// connection is HIJACKED — Shutdown marks it StateHijacked and
		// stops tracking it, so it returns while HTTP/2 streams are still
		// running. The in-flight COUNTER is at the handler, above the
		// protocol, so it is the only thing that knows. Without this the
		// lame duck would quietly hold only for HTTP/1.1 traffic.
		err = waitIdle(ctx, live)
	}
	if err != nil {
		left := live.load()
		if left < 1 {
			left = 1
		}
		if inCloud() {
			fmt.Printf("tako: %s did not finish within %s — cutting it off (SIGKILL follows in ~%s)\n",
				plural(left), budget, (shutdownWindow - budget).Round(time.Second))
		} else {
			// The number is not arbitrary and not local: it is the same
			// ceiling a deployed instance uses, so say where it comes from
			// rather than leaving the reader to guess at a bare "7s".
			fmt.Printf("⏻ %s did not finish within %s — cutting it off\n", plural(left), budget)
			fmt.Printf("  (Cloud Run allows %s from SIGTERM; the rest flushes metrics)\n", shutdownWindow)
		}
		_ = srv.Close()
	}
}

// waitIdle blocks until no handler is running, or the budget expires.
// It exists for hijacked (HTTP/2) connections, which Shutdown cannot
// account for; on a purely HTTP/1.1 service the counter is already zero
// by the time Shutdown returns and this costs one read.
func waitIdle(ctx context.Context, live *inflight) error {
	for live.load() > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
	return nil
}

// track counts a request for the whole time its handler runs.
func track(live *inflight, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		live.enter()
		defer live.exit()
		next.ServeHTTP(w, r)
	})
}

// inflight counts requests currently executing, so the drain line can
// say what it is actually waiting for instead of a bare "draining".
type inflight struct{ n atomic.Int64 }

func (c *inflight) enter() { c.n.Add(1) }
func (c *inflight) exit()  { c.n.Add(-1) }
func (c *inflight) load() int64 {
	return c.n.Load()
}

// plural renders the drain line's subject without the "1 requests" tell.
func plural(n int64) string {
	if n == 1 {
		return "1 request"
	}
	return fmt.Sprintf("%d requests", n)
}
