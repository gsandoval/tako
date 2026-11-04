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
	"github.com/gsandoval/tako/internal/trace"
	"github.com/gsandoval/tako/pkg/backends"
)

// RegisterJob wires a tako.Runner for the LOCAL path: executions
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
	recordAsyncHandler(name, fn)
	par := 1
	attempts := 0
	var timeout time.Duration
	if hooks.HandlerJobConfig != nil {
		par, attempts, timeout = hooks.HandlerJobConfig(h)
	} else if hooks.HandlerParallel != nil {
		par = hooks.HandlerParallel(h)
	}
	if attempts > 0 {
		SetQueueDelivery(hooks.JobQueue(jobName), attempts, 0, 0)
	}
	runTask := fn
	if timeout > 0 {
		base := fn
		runTask = func(ctx tako.Ctx, b []byte) error {
			c, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			taskIdx, taskCnt := ctx.Task()
			return base(&requestCtx{Context: c, log: ctx.Log(), taskIndex: taskIdx, taskCount: taskCnt}, b)
		}
	}
	hooks.Queue.Consume(hooks.JobQueue(jobName), func(ctx context.Context, payload []byte) error {
		// WithParallel locally: N tasks per execution, joined — the
		// same shard indices Cloud Run hands out, one process over.
		if par == 1 {
			ctx, _, finish := trace.StartRoot(ctx, "job", name, os.Getenv("TAKO_SERVICE"), "", "", "")
			ctx = pinFlagsOnCtx(ctx)
			jctx := &requestCtx{Context: ctx, log: slog.Default().With("job", name)}
			err := runTask(jctx, payload)
			finish(0, err)
			return err
		}
		errs := make(chan error, par)
		for i := range par {
			go func(i int) {
				tctx, _, finish := trace.StartRoot(ctx, "job", name, os.Getenv("TAKO_SERVICE"), "", "", "")
				tctx = pinFlagsOnCtx(tctx)
				jctx := &requestCtx{Context: tctx, log: slog.Default().With("job", name, "task", i), taskIndex: i, taskCount: par}
				err := runTask(jctx, payload)
				finish(0, err)
				errs <- err
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
	if rawSpec := os.Getenv("TAKO_ONESHOT"); rawSpec != "" {
		recordAsyncHandler(name, fn)
		runOneShot(http.NotFoundHandler(), rawSpec)
		return
	}
	var raw []byte
	var traceHdr, reqID, parentSpan, flagsHdr string
	for i, a := range os.Args {
		if i+1 >= len(os.Args) {
			continue
		}
		switch a {
		case "--tako-job-params":
			raw = []byte(os.Args[i+1])
		case "--tako-job-trace":
			traceHdr = os.Args[i+1]
		case "--tako-job-req":
			reqID = os.Args[i+1]
		case "--tako-job-parent-span":
			parentSpan = os.Args[i+1]
		case "--tako-job-flags":
			flagsHdr = os.Args[i+1]
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
	baseCtx, _, finishRoot := trace.StartRoot(context.Background(), "job", name, os.Getenv("TAKO_SERVICE"), reqID, traceHdr, parentSpan)
	if flags := trace.DecodeFlagsHeader(flagsHdr); len(flags) > 0 {
		baseCtx = trace.WithPropagatedFlags(baseCtx, flags)
	}
	baseCtx = pinFlagsOnCtx(baseCtx)
	log := slog.Default().With("job", name)
	if reqID != "" {
		log = log.With("req", reqID)
	}
	if tAttrs := traceAttrsFromHeader(traceHdr); len(tAttrs) > 0 {
		log = log.With(tAttrs...)
	}
	ctx := &requestCtx{Context: baseCtx, log: log, taskIndex: taskIndex, taskCount: taskCount}
	err := fn(ctx, raw)
	finishRoot(0, err)
	// A job's every exit is a process exit, and os.Exit runs no defers —
	// so the flush goes HERE, on both paths. A failing task's metrics are
	// the ones most worth having, and until this line they were the ones
	// most reliably lost.
	runShutdownHooks(jobFlush)
	if err != nil {
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
	hooks.SetHandlerIdentity(hooks.HandlerID(h), name)
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
