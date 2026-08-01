# job.md — `tako.Job` + `tako.JobHandler`

Long-running compute (§2.7): work that outlives request timeouts. In the
cloud an execution is a Cloud Run **Job** — no request deadline exists;
a six-minute settlement just finishes. A job is compute you walk away
from: `Run` hands off and returns, and the result lands wherever the
body writes it (shop's settle job writes a `reports` entity).

## Declare

Two vars, from [examples/shop](../../examples/shop/app.go):

```go
// settle is long-running compute (§2.7): a Cloud Run JOB in the
// cloud — no request timeout to outlive — and a tracked in-process
// execution locally.
settle = tako.Job[SettleParams]("settle")

Settle = tako.JobHandler(settle, runSettle)
```

`tako.Job[T](name)` declares the job and its parameter type; the name
must be a string constant (analyzer-enforced). `tako.JobHandler` binds
the body — like every async handler it is `func(Ctx, T) error`, and a
job has exactly one body. Options on `JobHandler`:

- `tako.WithParallel(n)` — fans an execution out across n parallel
  tasks (Cloud Run's `taskCount`; the same n tasks run joined
  locally). Inside the body, `ctx.Task()` reports `(index, count)` —
  shard by them (hash the key, take your share); everywhere else it
  is `(0, 1)`.
- `tako.WithFootprint(tako.Footprint{CPU: …, Memory: …})` — task
  compute; only CPU and Memory apply (see [footprint.md](footprint.md)).

## Use

Trigger it from any handler — api, worker, cron — with
`handle.Run(ctx, params)`:

```go
// startSettle HANDS OFF and returns immediately — the job is compute
// you walk away from; the report entity is where its result lands.
func startSettle(ctx tako.Ctx, req SettleReq) (SettleStarted, error) {
	if err := settle.Run(ctx, SettleParams{Day: req.Day, ThrottleMs: req.ThrottleMs}); err != nil {
		return SettleStarted{}, err
	}
	return SettleStarted{Started: true}, nil
}
```

`Run` returns once the execution is handed off — queued locally, a
Cloud Run Job execution created in the cloud. It never waits for
completion.

### Retries and timeout

```go
var Settle = tako.JobHandler(settle, runSettle,
	tako.WithParallel(4),
	tako.WithRetries(1),
	tako.WithFootprint(tako.Footprint{Memory: "4Gi", Timeout: 2 * time.Hour}))
```

`WithRetries(n)` sets how many times a failed task is retried before the
execution fails (default 3, range 0–10). Jobs are compute, not
messaging: **there is no dead letter** to catch a task that runs out of
attempts, which is why the ceiling is low — a task that fails ten times
will not pass on the eleventh.

`Timeout` on the footprint caps one TASK (default 900s from the batch
profile, up to 24h) — not the execution, which is as long as its slowest
task.

## Local

An execution rides the local queue dispatcher as a tracked in-process
run — the ⚙ line in the dev console narrates it, `DrainQueues`/`WaitIdle`
cover it in tests, retries follow the local queue policy (5 attempts,
fixed 200ms backoff), and a failed run dead-letters inspectably under
`.tako/data/deadletter/` — no invisible goroutines.

## Cloud

`tako deploy` creates a `run job <pkg>-<var>` (shop: `shop-settle`) —
a Cloud Run Job v2, not a service. Executions run on demand: `Run`
calls the Jobs API's `runWithOverrides`, handing params over as the
`--tako-job-params` container arg the generated job main decodes. The
spec is fixed: `maxRetries: 3`, `timeout: "3600s"`, and there is no
dead-letter — this is compute, not messaging; Cloud Run retries a
failed task, then the execution is simply Failed. Task compute comes
from the batch profile unless a footprint pins CPU/Memory.

The wiring is derived, never hand-configured: every service that calls
`Run` gets the job's full resource name injected as `TAKO_JOB_<NAME>`,
and its service account gets `roles/run.developer` **on that job only**
— `runWithOverrides` (the params channel) sits in `run.developer`, not
`run.invoker`. Cost: an execution bills CPU and memory only while it
runs; an idle job costs nothing.

## Test it

The shop pattern ([api_test.go](../../examples/shop/api_test.go),
`TestSettleJobRunsToCompletion`): trigger through the api, wait for the
tracked execution, read the result entity:

```go
started, res := postJSON[SettleStarted](t, alice, srv.URL+"/api/settle", SettleReq{Day: "2026-07-27"})
if err := runtime.WaitIdle(30 * time.Second); err != nil {
	t.Fatal(err)
}
```

## Errors you'll meet

- `a job footprint sets CPU, Memory and Timeout only — executions are
  tasks, not serving instances, so there is no concurrency, no scaling,
  no idle CPU to throttle, no cold start to boost and no in-flight
  requests to drain; WithParallel sets the fan-out and WithRetries the
  attempts (tako/footprint)`
- `tako.WithRetries takes a constant 0–10 attempts after the first — a
  task that fails 10 times is not going to pass on the 11th, and jobs
  have no dead letter to catch it (tako/retries)`
- A second `JobHandler` on the same job:
  `job "settle" already has handler shop.Settle (app.go:99) — a job has
  exactly one body (tako/one-job-handler)`
- `tako.WithTTL is not a tako.JobHandler option (tako.JobHandler
  accepts WithParallel, WithRetries, WithProfile, WithFootprint)
  (tako/invalid-option)`
- `Run` in a bare test: `tako: jobs are not wired — Run works in booted
  services (tako dev / deploy) and NewTestApp tests, not bare tests`
- Missing env in the cloud: `job "settle" has no execution target —
  tako deploy injects TAKO_JOB_* from the graph's Run edges, so this is
  a tako bug (re-run tako deploy, and file it if it persists)`

## Not yet

- `Run` never waits and there is no status API from code — watch an
  execution with `gcloud run jobs executions list --job shop-settle`.
- (T4 removed the old shard gap: `ctx.Task()` reports (index, count)
  inside a parallel task — `(0, 1)` everywhere else — and the local
  runtime runs the full fan-out; shop's settle shards orders by ID
  hash and merges shard subtotals idempotently.)
- `maxRetries` (3) and the 3600s task timeout are framework-owned, not
  knobs. For a scheduled job, trigger `handle.Run` from a
  [cron](cron.md) handler — scheduled everywhere since T2.
