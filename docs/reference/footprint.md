# footprint.md — the `Profile` and `Footprint` fields

Two layers of compute intent. `Profile` names it — the framework
owns the values. `Footprint` pins exact values field-by-field over
the profile — the escape hatch for when a workload's needs are known
and specific. Prefer the profile; pin only what the profile can't know.

## Declare

A `Profile` field on `tako.ServiceConfig` or `tako.Compute`
selects its service's scaling profile (one service, one profile). The
knobs (`ProfileKnobs`, spec §4.6):

| profile           | CPU | Memory | Concurrency | Min | Max | Timeout | CPU between requests | Startup boost |
|-------------------|-----|--------|-------------|-----|-----|---------|----------------------|---------------|
| `tako.ProfileDefault`    | 1   | 512Mi  | 80          | 0   | 20  | 300s    | throttled            | no            |
| `tako.ProfileLatency`    | 2   | 1Gi    | 40          | 1   | 100 | 300s    | **always allocated** | **yes**       |
| `tako.ProfileBatch`      | 2   | 2Gi    | 10          | 0   | 10  | 900s    | throttled            | no            |
| *(singleton)*     | 1   | 512Mi  | 1           | 1   | 1   | 3600s   | **always allocated** | no            |

Default scales to zero; Latency keeps an instance warm — min=1 bills
continuously; Batch favors throughput per instance for background work.

**Jobs default to batch and singletons to the singleton profile, but
both are now DEFAULTS you can override** with `Profile` — the
provisioner used to overwrite whatever you asked for, which made the
option accepted and ignored.

**Latency's concurrency is deliberately LOWER than Default's**, which
reads backwards until you see what it buys: fewer requests packed onto
one instance means less queueing behind a busy one, which is the whole
point of the profile. Warmth (min 1), CPU always allocated, and a
startup boost are the other three quarters — an instance that is up but
throttled still stalls on the first request, and a warm floor of 1 does
nothing for the second instance a traffic spike starts.

`Profile` and `Footprint` are declared on
[`tako.Service`](service.md) for a package's app service, and on the
handler itself for a worker, subscriber, job or singleton (where the one
handler IS the service). They are refused on `API` and `Page`
(`tako/service-option`) — a service has many routes and none is an
honest place for a service-wide claim.

A `Footprint` field (S1) overrides the profile field-by-field:

**Go**
```go
type Footprint struct {
	CPU          int    // vCPUs per instance: 1, 2, 4, or 8
	Memory       string // per-instance limit, like "512Mi" or "4Gi"
	Concurrency  int    // concurrent requests per instance (1–1000)
	MinInstances int    // always-warm instances — they bill continuously
	MaxInstances int    // scale-out cap (1–1000)
	CPUAlwaysOn  Toggle // Inherit | On | Off — CPU between requests
	StartupBoost Toggle // Inherit | On | Off — extra CPU until serving
}
```

**cfire**
```cfire
// Footprint settings apply identically to cfire services in AppGraph
type MountOpts struct {
    require_sign_in: bool
    role:            str
    private_route:   bool
    max_age_sec:     i64
}
```

`Traffic` is its sibling, for how requests REACH the service rather
than how much machine runs them:

**Go**
```go
type Traffic struct {
	Timeout         time.Duration // one unit of work: a request, or a job task
	SessionAffinity Toggle        // Inherit | On | Off — sticky routing
	HTTP2           Toggle        // Inherit | On | Off — HTTP/2 to the instance
}
```

**cfire**
```cfire
type ServeOpts struct {
    service_name: str
}
```

The two are separate because they answer separate questions, and you
reach for them at different moments: a footprint is sized against a
workload's appetite, a traffic setting against how clients talk to it.
Both layer over the profile the same way, and both are validated at
analysis time.

`Traffic.Timeout` caps how long ONE unit of work may run — a request
for a service, a task for a job. Cloud Run's ceilings differ, and so do the
refusals: 60m for a service (past that you want a
[job](job.md)), 24h for a job task.

`CPUAlwaysOn` is a three-state `Toggle`, not a `bool`, because a struct
whose zero value means "inherit" cannot otherwise express "explicitly
off". `tako.ToggleOn` forces allocation on a Default-profile service;
`tako.ToggleOff` forces it off on Latency.

`StartupBoost` gives a starting instance extra CPU until it is serving.
It is the one knob that moves cold-start latency, which is why Latency
takes it and the others don't — you pay for it only during startup.

`Traffic.SessionAffinity` asks Cloud Run to send a returning client back to the
instance it reached last. **No profile turns it on**, because it trades
away even load spreading: sticky clients pile onto instances the
autoscaler would rather have balanced. Reach for it when per-client warm
state makes a real difference — a decoded model, a primed cache — and
never for state you cannot lose. It is best-effort, and an instance can
disappear between any two requests.

Refused on jobs and singletons: an execution serves nobody, and a
singleton is one instance answering its own health check.

### HTTP2

`Traffic.HTTP2` keeps HTTP/2 all the way to your instance instead of letting
Cloud Run downgrade it at the edge.

**Read that carefully, because the thing it does not do is the thing
people expect.** Browsers already reach Cloud Run over HTTP/2 with TLS,
whether you set this or not. Cloud Run terminates TLS at its frontend —
**always, for every service; a container can never terminate TLS
itself** — and by default it then forwards HTTP/1.1 over the last hop.
`HTTP2` changes only that last hop, to HTTP/2 cleartext (`h2c`). The hop
is still encrypted by Google's infrastructure; "cleartext" describes the
application protocol, not the wire.

So this is not a TLS setting and not a knob for browser performance. It
is worth turning on for **request multiplexing and streaming between the
frontend and your handler** — long-lived streams, many concurrent small
requests, gRPC-shaped traffic. For ordinary request/response JSON the
last hop gains close to nothing, which is why it is off by default,
matching Cloud Run's own default.

**A tako server always speaks HTTP/2**, deployed with this on or not:
`Serve` wraps every handler in `h2c`, which upgrades an HTTP/2 preface
and passes HTTP/1.1 straight through. That matters because Cloud Run
stops downgrading the moment the setting is on — a container that could
not answer h2c would fail *every* request rather than falling back. You
can confirm it locally against `tako dev`, the same check Google's docs
prescribe:

```
curl -i --http2-prior-knowledge http://localhost:8080/
```

Refused on jobs and singletons, for the same reason affinity is.

Shutdown is not a knob and deliberately so — see
[observability.md](observability.md#shutdown). Cloud Run's 10s window is
fixed, and tako divides it by watching rather than by budgeting: the
drain ends when the last request does, and whatever is left goes to the
flush. There is no number a service could usefully set.

Zero fields inherit from the profile; set fields override it. The
min=0 nuance: a zero field means *inherit*, so an explicit
always-scale-to-zero floor is expressed by choosing a min=0 base
profile (Default or Batch), not by `MinInstances: 0`. On job handlers
only CPU and Memory apply — an execution has no concurrency, no
scaling, no idle CPU and no cold start to boost. Their one traffic
setting is `Timeout`. Singletons take `StartupBoost` too, but not the
shape that defines the primitive.

## Use

The layering, from [examples/gallery](../../examples/gallery/app.go):

**Go**
```go
// Batch gives the resizer throughput knobs; the footprint pins what
// the profile can't know: image decodes are memory-hungry per
// request, so more headroom, fewer per instance, capped fan-out.
MakeThumb = tako.Worker(thumbs, makeThumb,
	tako.Compute{
		Profile:   tako.ProfileBatch,
		Footprint: tako.Footprint{Memory: "4Gi", Concurrency: 4, MaxInstances: 5},
	})
```

**cfire**
```cfire
MakeThumb := tako.worker(Thumbs, make_thumb)
```

CPU and min-instances are NOT pinned — they inherit from batch. That
layering is the whole design.

## Local

No effect on `tako dev` or `tako test` — everything runs in one
process. The claims are still fully validated at analysis time, and
`tako gen graph` shows them side by side:
`service gallery-makethumb (package gallery, profile batch + footprint (memory 4Gi, concurrency 4, max 5))`.

## Cloud

Footprint and profile settings apply identically whether the service is compiled from Go (`gcr.io/distroless/static-debian12:nonroot`) or `cfire` (`gcr.io/distroless/cc-debian12:nonroot`).

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
- `footprint sets nothing — drop the Footprint field or pin at least one setting (tako/footprint)`
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
- Scoping: `a job footprint sets CPU and Memory only — executions are
  tasks, not serving instances, so there is no concurrency, no scaling,
  no idle CPU to throttle and no cold start to boost;
  RunnerConfig.Parallel sets the fan-out and RunnerConfig.Attempts the
  attempts (tako/footprint)`, and for a singleton `a singleton
  footprint cannot set MaxInstances — … (tako/singleton-max)`.

  The traffic twin of that rule is gone, and structurally:
  `RunnerConfig` and `SingletonConfig` carry a plain `Timeout` and no
  `Traffic` at all, so a job or singleton cannot claim session affinity
  or HTTP/2 in the first place.
- Ceilings: `service slow resolves to a 2h0m0s request timeout — Cloud
  Run caps a request at 60m; for work that outlives a request use a
  tako.Job (docs/reference/job.md) (tako/footprint)`

## Not yet

- Three named profiles; no custom or per-app profiles.
- No GPU.
- An explicit `MinInstances: 0` cannot be written as a field (zero
  means inherit) — pick a min=0 profile instead.
