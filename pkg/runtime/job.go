package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	tako "github.com/gsandoval/tako"
	"github.com/gsandoval/tako/internal/hooks"
	"github.com/gsandoval/tako/pkg/backends"
)

// RegisterJob wires a tako.JobHandler for the LOCAL path: executions
// ride the queue dispatcher in-process (dev and tests). In the cloud
// this is a no-op — Run creates a real Cloud Run Job execution, and
// the job's own generated main (RunJobMain) does the work.
func RegisterJob(name string, h tako.Handler) {
	kind, jobName, invoke := hooks.HandlerInfo(h)
	if kind != "job" {
		panic(fmt.Sprintf("tako: RegisterJob(%s): handler is %q, not a job", name, kind))
	}
	if hooks.RunJob != nil {
		return // cloud: the trigger hook owns it
	}
	fn, ok := invoke.(func(tako.Ctx, []byte) error)
	if !ok || hooks.Queue == nil {
		panic("tako: RegisterJob before Boot")
	}
	par := 1
	if hooks.HandlerParallel != nil {
		par = hooks.HandlerParallel(h)
	}
	hooks.Queue.Consume(hooks.JobQueue(jobName), func(ctx context.Context, payload []byte) error {
		// WithParallel locally: N tasks per execution, joined — the
		// same shard indices Cloud Run hands out, one process over.
		if par == 1 {
			jctx := &requestCtx{Context: ctx, log: slog.Default().With("job", name)}
			return fn(jctx, payload)
		}
		errs := make(chan error, par)
		for i := range par {
			go func(i int) {
				jctx := &requestCtx{Context: ctx, log: slog.Default().With("job", name, "task", i), taskIndex: i, taskCount: par}
				errs <- fn(jctx, payload)
			}(i)
		}
		var first error
		for range par {
			if err := <-errs; err != nil && first == nil {
				first = err
			}
		}
		return first
	})
}

// RunJobMain is the body of a generated Cloud Run Job main: decode the
// params handed over as --tako-job-params, run the handler once, exit.
// A non-nil error exits non-zero — Cloud Run retries the task up to
// its maxRetries.
func RunJobMain(name string, h tako.Handler) {
	kind, _, invoke := hooks.HandlerInfo(h)
	if kind != "job" {
		panic(fmt.Sprintf("tako: RunJobMain(%s): handler is %q, not a job", name, kind))
	}
	fn := invoke.(func(tako.Ctx, []byte) error)
	var raw []byte
	for i, a := range os.Args {
		if a == "--tako-job-params" && i+1 < len(os.Args) {
			raw = []byte(os.Args[i+1])
		}
	}
	// Cloud Run hands each parallel task its shard through the
	// canonical envs; ctx.Task() forwards them to the handler.
	taskIndex, _ := strconv.Atoi(os.Getenv("CLOUD_RUN_TASK_INDEX"))
	taskCount, _ := strconv.Atoi(os.Getenv("CLOUD_RUN_TASK_COUNT"))
	if taskCount < 1 {
		taskCount = 1
	}
	start := time.Now()
	ctx := &requestCtx{Context: context.Background(), log: slog.Default().With("job", name), taskIndex: taskIndex, taskCount: taskCount}
	if err := fn(ctx, raw); err != nil {
		fmt.Fprintf(os.Stderr, "tako: job %s failed after %s: %v\n", name, time.Since(start).Round(time.Millisecond), err)
		os.Exit(1)
	}
	fmt.Printf("tako: job %s ok (%s)\n", name, time.Since(start).Round(time.Millisecond))
}

// RegisterSingleton starts the exactly-one loop for the LOCAL path
// (dev: the app process is the only instance, and the in-process lease
// still guards against doubled registration). Cloud singleton services
// run RunSingletonLoop from their own generated main instead.
func RegisterSingleton(name string, h tako.Handler) {
	if os.Getenv("TAKO_BACKEND") == "gcp" {
		return // the singleton's own min=1 service runs the loop
	}
	go RunSingletonLoop(name, h)
}

// RunSingletonLoop campaigns for the singleton's lease forever and runs
// fn while holding it: exactly one instance across revisions (spec
// §4.4 — min=1/max=1 alone is NOT the guarantee; the lease is). fn
// returning — error or not — is a restart with backoff, narrated; a
// lost lease cancels fn's ctx and the campaign resumes.
func RunSingletonLoop(name string, h tako.Handler) {
	kind, _, invoke := hooks.HandlerInfo(h)
	if kind != "singleton" {
		panic(fmt.Sprintf("tako: RunSingletonLoop(%s): handler is %q, not a singleton", name, kind))
	}
	fn := invoke.(func(tako.Ctx, []byte) error)
	if hooks.Lease == nil {
		panic("tako: RunSingletonLoop before Boot")
	}
	for {
		err := backends.DoLease(context.Background(), hooks.Lease, "singleton-"+name, hooks.LeaseOwner, backends.LeaseTTL,
			func(c context.Context) error {
				sctx := &requestCtx{Context: c, log: slog.Default().With("singleton", name)}
				return fn(sctx, nil)
			})
		if err != nil && !strings.Contains(err.Error(), "lost") {
			fmt.Printf("♾ singleton %s: %v — restarting in 5s\n", name, err)
		} else {
			fmt.Printf("♾ singleton %s returned — restarting in 5s\n", name)
		}
		time.Sleep(5 * time.Second)
	}
}

// SingletonHealth is the bare mux a singleton service serves: Cloud Run
// requires an HTTP port even when the work is the loop.
func SingletonHealth(name string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "singleton %s: the loop runs while this instance holds the lease\n", name)
	})
	return mux
}
