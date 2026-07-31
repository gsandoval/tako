# footprint.md — `WithProfile` and `WithFootprint`

Two layers of compute intent. `WithProfile` names it — the framework
owns the values. `WithFootprint` pins exact values field-by-field over
the profile — the escape hatch for when a workload's needs are known
and specific. Prefer the profile; pin only what the profile can't know.

## Declare

`tako.WithProfile(p)` on an API, Page, Worker, or Subscribe handler
selects its service's scaling profile (one service, one profile). The
knobs (`ProfileKnobs`, spec §4.6):

| profile           | CPU | Memory | Concurrency | Min | Max |
|-------------------|-----|--------|-------------|-----|-----|
| `tako.Default`    | 1   | 512Mi  | 80          | 0   | 20  |
| `tako.Latency`    | 2   | 1Gi    | 40          | 1   | 100 |
| `tako.Batch`      | 2   | 2Gi    | 10          | 0   | 10  |

(Jobs base on batch; singletons use a fixed internal profile —
CPU 1 / 512Mi / concurrency 1 / min 1 / max 1.) Default scales to
zero; Latency keeps an instance warm — min=1 bills continuously; Batch
favors throughput per instance for background work.

`tako.WithFootprint(tako.Footprint{…})` (S1) overrides field-by-field:

```go
type Footprint struct {
	CPU          int    // vCPUs per instance: 1, 2, 4, or 8
	Memory       string // per-instance limit, like "512Mi" or "4Gi"
	Concurrency  int    // concurrent requests per instance (1–1000)
	MinInstances int    // always-warm instances — they bill continuously
	MaxInstances int    // scale-out cap (1–1000)
}
```

Zero fields inherit from the profile; set fields override it. The
min=0 nuance: a zero field means *inherit*, so an explicit
always-scale-to-zero floor is expressed by choosing a min=0 base
profile (Default or Batch), not by `MinInstances: 0`. On job handlers
and singletons only CPU and Memory apply.

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
- Scoping: `a job footprint sets CPU and Memory only — executions are
  tasks, not serving instances; WithParallel sets the fan-out` and
  `a singleton footprint sets CPU and Memory only —
  min=1/max=1/concurrency=1 plus the lease is what makes it exactly-one`

## Not yet

- Three named profiles; no custom or per-app profiles.
- No GPU, no request-timeout knob, no CPU-throttling toggle — the knob
  set is exactly the five Footprint fields.
- An explicit `MinInstances: 0` cannot be written as a field (zero
  means inherit) — pick a min=0 profile instead.
