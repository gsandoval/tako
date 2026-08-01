package runtime

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/gsandoval/tako/internal/hooks"
)

// Cloud Run sends SIGTERM and kills the instance 10 seconds later. That
// window is FIXED — neither the v1 RevisionSpec nor the v2
// RevisionTemplate has a field for it, and gcloud has no flag — so tako
// divides it rather than pretending to set it. WithFootprint's Drain
// picks the split; these are the fallbacks and the ceiling.
const (
	// shutdownWindow is what Cloud Run gives us, total.
	shutdownWindow = 10 * time.Second
	// cloudDrain is the default share for finishing in-flight requests
	// (analyzer.DefaultDrainSeconds — duplicated rather than imported,
	// because the runtime must not depend on the analyzer).
	cloudDrain = 7 * time.Second
	// devDrain is the local default. Nothing kills a dev server, so this
	// is chosen for how long Ctrl-C should feel, not for safety.
	devDrain = 2 * time.Second
	// flushFloor is the least time shutdown hooks are ever given. The
	// last metrics flush is the whole reason the drain has a budget at
	// all, so it never gets squeezed to nothing.
	flushFloor = 1 * time.Second
	// jobFlush is what a job's final flush gets. A job that finishes is
	// not inside Cloud Run's 10s window — that clock only starts if the
	// execution is cancelled — so this is bounded by patience, not by a
	// kill.
	jobFlush = 5 * time.Second
)

// drainBudget is how long in-flight requests get. TAKO_DRAIN is injected
// per service by the deploy (the resolved footprint); its absence means
// nobody deployed this, so it is a dev run.
func drainBudget() time.Duration {
	v := os.Getenv("TAKO_DRAIN")
	if v == "" {
		return devDrain
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return cloudDrain
	}
	return time.Duration(n) * time.Second
}

// flushBudget is what remains of the window after the drain, keeping a
// second in hand for the kill to land late. In dev there is no window to
// divide, so hooks get a flat allowance.
func flushBudget(drain time.Duration) time.Duration {
	if os.Getenv("TAKO_DRAIN") == "" {
		return devDrain
	}
	left := shutdownWindow - drain - time.Second
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
	if err := srv.Shutdown(ctx); err != nil {
		left := live.load()
		if left < 1 {
			left = 1
		}
		if inCloud() {
			fmt.Printf("tako: %s did not finish within %s — cutting it off (SIGKILL follows in ~%s)\n",
				plural(left), budget, (shutdownWindow - budget).Round(time.Second))
		} else {
			fmt.Printf("⏻ %s did not finish within %s — cutting it off\n", plural(left), budget)
		}
		_ = srv.Close()
	}
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
