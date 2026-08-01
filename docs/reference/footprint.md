# footprint.md — `WithProfile` and `WithFootprint`

Two layers of compute intent. `WithProfile` names it — the framework
owns the values. `WithFootprint` pins exact values field-by-field over
the profile — the escape hatch for when a workload's needs are known
and specific. Prefer the profile; pin only what the profile can't know.

## Declare

`tako.WithProfile(p)` on an API, Page, Worker, or Subscribe handler
selects its service's scaling profile (one service, one profile). The
knobs (`ProfileKnobs`, spec §4.6):

| profile           | CPU | Memory | Concurrency | Min | Max | Timeout | CPU between requests | Startup boost |
|-------------------|-----|--------|-------------|-----|-----|---------|----------------------|---------------|
| `tako.Default`    | 1   | 512Mi  | 80          | 0   | 20  | 300s    | throttled            | no            |
| `tako.Latency`    | 2   | 1Gi    | 40          | 1   | 100 | 300s    | **always allocated** | **yes**       |
| `tako.Batch`      | 2   | 2Gi    | 10          | 0   | 10  | 900s    | throttled            | no            |
| *(singleton)*     | 1   | 512Mi  | 1           | 1   | 1   | 3600s   | **always allocated** | no            |

Every profile drains for 7s (see [Drain](#drain)).

Default scales to zero; Latency keeps an instance warm — min=1 bills
continuously; Batch favors throughput per instance for background work.

**Jobs default to batch and singletons to the singleton profile, but
both are now DEFAULTS you can override** with `WithProfile` — the
provisioner used to overwrite whatever you asked for, which made the
option accepted and ignored.

**Latency's concurrency is deliberately LOWER than Default's**, which
reads backwards until you see what it buys: fewer requests packed onto
one instance means less queueing behind a busy one, which is the whole
point of the profile. Warmth (min 1), CPU always allocated, and a
startup boost are the other three quarters — an instance that is up but
throttled still stalls on the first request, and a warm floor of 1 does
nothing for the second instance a traffic spike starts.

`WithProfile` and `WithFootprint` are declared on
[`tako.Service`](service.md) for a package's app service, and on the
handler itself for a worker, subscriber, job or singleton (where the one
handler IS the service). They are refused on `API` and `Page`
(`tako/service-option`) — a service has many routes and none is an
honest place for a service-wide claim.

`tako.WithFootprint(tako.Footprint{…})` (S1) overrides field-by-field:

```go
type Footprint struct {
	CPU          int    // vCPUs per instance: 1, 2, 4, or 8
	Memory       string // per-instance limit, like "512Mi" or "4Gi"
	Concurrency  int    // concurrent requests per instance (1–1000)
	MinInstances int    // always-warm instances — they bill continuously
	MaxInstances int    // scale-out cap (1–1000)
	Timeout      time.Duration // one unit of work: a request, or a job task
	CPUAlwaysOn  Toggle // Inherit | On | Off — CPU between requests
	Drain        time.Duration // finishing in-flight requests at shutdown
	StartupBoost Toggle // Inherit | On | Off — extra CPU until serving
}
```

`Timeout` caps how long ONE unit of work may run — a request for a
service, a task for a job. Cloud Run's ceilings differ, and so do the
refusals: 60m for a service (past that you want a
[job](job.md)), 24h for a job task.

`CPUAlwaysOn` is a three-state `Toggle`, not a `bool`, because a struct
whose zero value means "inherit" cannot otherwise express "explicitly
off". `tako.On` forces allocation on a Default-profile service;
`tako.Off` forces it off on Latency.

`StartupBoost` gives a starting instance extra CPU until it is serving.
It is the one knob that moves cold-start latency, which is why Latency
takes it and the others don't — you pay for it only during startup.

### Drain

`Drain` is how long a shutting-down instance may spend finishing the
requests already in flight. **It divides a window it does not set.**
Cloud Run sends `SIGTERM` and kills the instance 10 seconds later, and
that 10 seconds is fixed — no API field, no `gcloud` flag. `Drain`
buys the requests; what is left pays for flushing metrics and closing
cleanly.

So `Timeout` and `Drain` are not the same clock and the gap between
them is the point: a service may hold a request for 300s, but at
shutdown it gets 7. A request still running when the drain expires is
cut off. If that matters for your workload, the answer is a
[job](job.md), not a bigger number here.

Values above 9s are **refused, not clamped** — above the ceiling the
kill lands first, so a longer drain would be a promise tako cannot
keep:

```
app.go:14:43: footprint Drain must be between 1s and 9s; got 30s — Cloud Run kills the instance 10s after SIGTERM and that window is not configurable, so a longer drain is a promise tako cannot keep (tako/footprint)
```

The whole shutdown sequence is in
[observability.md](observability.md#shutdown).

Zero fields inherit from the profile; set fields override it. The
min=0 nuance: a zero field means *inherit*, so an explicit
always-scale-to-zero floor is expressed by choosing a min=0 base
profile (Default or Batch), not by `MinInstances: 0`. On job handlers
only CPU, Memory and Timeout apply — an execution has no concurrency,
no scaling, no idle CPU, no cold start to boost and no in-flight
requests to drain. Singletons take `Drain` too (they serve), but not
the shape that defines the primitive.

## Use

The layering, from [examples/gallery](../../examples/gallery/app.go):

```go
// Batch gives the resizer throughput knobs; the footprint pins what
// the profile can't know: image decodes are memory-hungry per
// request, so more headroom, fewer per instance, capped fan-out.
MakeThumb = tako.Worker(thumbs, makeThumb,
	tako.WithProfile(tako.Batch),
	tako.WithFootprint(tako.Footprint{Memory: "4Gi", Concurrency: 4, MaxInstances: 5}))
```

CPU and min-instances are NOT pinned — they inherit from batch. That
layering is the whole design.

## Local

No effect on `tako dev` or `tako test` — everything runs in one
process. The claims are still fully validated at analysis time, and
`tako gen graph` shows them side by side:
`service gallery-makethumb (package gallery, profile batch + footprint (memory 4Gi, concurrency 4, max 5))`.

## Cloud

Footprint fields must be constants — they are read at analysis time,
and validated twice: each field against Cloud Run's limits, then the
RESOLVED combination (profile base plus overrides) against Cloud Run's
cross-field rules, so a footprint that only pins CPU cannot silently
pair it with too little inherited memory. Impossible combinations fail
the build, not the deploy.

`tako deploy` prints what the footprint resolves to, so the plan
carries the price, not just the override:
`compute: 2 cpu / 4Gi / concurrency 4 / instances 0–5 (profile batch + footprint memory 4Gi, concurrency 4, max 5)`
(jobs get a `task compute:` line). Standing money is priced, never
absorbed — raising min-instances above the profile's warns:

```
⚠ footprint on <service> keeps 2 instance(s) always on — they bill continuously whether traffic comes or not
```

(Gallery's footprint never triggers this — it inherits batch's min 0.
The warning fires exactly when YOUR MinInstances exceeds the
profile's.)

## Test it

The claims live in golden tests —
`go test ./pkg/analyzer -run TestErrors/footprint` — and the full walk
(declaration → summary → live `gcloud` values → pricing warning) is
[validate/footprint.md](../../validate/footprint.md).

## Errors you'll meet

- `footprint CPU must be 1, 2, 4, or 8 vCPUs; got 3 (tako/footprint)`
- `footprint Memory must be like "512Mi" or "4Gi", between 128Mi and 32Gi; got "1GB" (tako/footprint)`
- `footprint Memory must be a constant — footprints are read at analysis time, not run time (tako/footprint)`
- `footprint sets nothing — drop tako.WithFootprint or pin at least one field (tako/footprint)`
- Resolved judgments: `service footprintresolve-bigcpu resolves to
  8 vCPUs with 512Mi memory — 8 vCPUs need at least 4Gi; pin Memory too
  (tako/footprint)` — likewise memory-over-CPU and
  `resolves to MinInstances 30 over MaxInstances 20 (profile default) — pin MaxInstances too`
- Two handlers, two claims, one package: `service mixedfootprint cannot
  carry both footprint (cpu 2) from Fast and footprint (cpu 4, memory
  2Gi) from Slow — one service scales one way; split the packages or
  align the footprints (tako/mixed-footprint)`
- `service mixedprofile cannot be both latency (Fast) and batch (Slow)
  — one service scales one way; split the packages or align the
  profiles (tako/mixed-profile)`
- Scoping: `a job footprint sets CPU, Memory and Timeout only —
  executions are tasks, not serving instances, so there is no
  concurrency, no scaling, no idle CPU to throttle, no cold start to
  boost and no in-flight requests to drain; WithParallel sets the
  fan-out and WithRetries the attempts (tako/footprint)`, and for a
  singleton `a singleton footprint cannot set MaxInstances — the lease
  is what makes a singleton exactly-one … (tako/singleton-max)`
- Ceilings: `service slow resolves to a 2h0m0s request timeout — Cloud
  Run caps a request at 60m; for work that outlives a request use a
  tako.Job (docs/reference/job.md) (tako/footprint)`

## Not yet

- Three named profiles; no custom or per-app profiles.
- No GPU.
- An explicit `MinInstances: 0` cannot be written as a field (zero
  means inherit) — pick a min=0 profile instead.
