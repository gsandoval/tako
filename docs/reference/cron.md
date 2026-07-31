# `tako.Cron` — scheduled handlers

A cron is a `func(Ctx) error` run on a schedule, declared like
everything else — a ticker locally, a Cloud Scheduler job in the
cloud (T2), one declaration for both.

## Declare

```go
var Cleanup = tako.Cron("every 24h", cleanupOrphans, tako.WithSingleflight())
```

The schedule must be a string constant (analyzer-enforced), in either
form: `"every <Go duration>"` (`every 10m`, `every 6h`, `every 24h`)
or a 5-field unix-cron expression (`"0 3 * * *"`, `"0 9-17 * * 1-5"`).
Both must map onto Cloud Scheduler's grammar — an `every` interval
that unix-cron cannot express (`every 7m`, `every 90s`) is refused at
analysis time with the mapping rule. The analyzer, the runtime, and
the provisioner share one parser, so a schedule that analyzes runs
locally AND provisions.

`WithSingleflight` wraps the handler in the §4.4 lease
([lock.md](lock.md)), so overlapping or double-fired runs collapse to
one execution — across all instances, not just one process. The wrap
happens at declaration, so every trigger — dev ticker, `AdvanceCron`,
Cloud Scheduler — inherits it. `WithProfile` and `WithFootprint`
([footprint.md](footprint.md)) shape the HOSTING service, the escape
hatch a cron-only package would otherwise lack.

## Use

The handler is plain: do the work, return `nil` or an error. Cron
handlers run inside the package's app service. Gallery's daily
cleanup deletes never-completed uploads
([examples/gallery/app.go](../../examples/gallery/app.go)); failures
are narrated and the next run stays on schedule — there is no retry
of a failed run, the schedule itself is the retry.

## Local

`tako dev` runs each cron on a real ticker and narrates it:

```
⏰ cron Cleanup: every 24h
⏰ cron Cleanup ok (312ms)
⏰ cron Cleanup failed: <err> — next run on schedule
```

Runs are serialized per cron in-process — a tick landing mid-run is
skipped with a `⏰ … run still in progress — tick skipped` line. With
`WithSingleflight` the collapse is cross-instance and lease-based,
with its own narration — the feature working, never an error:

```
↻ cron cleanupOrphans skipped — an overlapping run holds the lease
```

## Cloud

Every cron provisions a **Cloud Scheduler job**
(`+ create scheduler job <svc>-cron-<name>`) firing
`POST /_tako/cron/<name>` on the service with an OIDC token minted as
the service's own SA. The route trusts nobody: it verifies the
Google-signed token's audience (this service) and identity (that SA) —
injected at deploy as `TAKO_CRON_AUDIENCE`/`TAKO_CRON_INVOKER` — so
the route is safe even on a public service, and locally it just
refuses strangers (the ticker drives dev). In-process tickers stay off
in the cloud: one schedule, not one per instance. A failed run answers
500 and the schedule itself is the retry; Scheduler's attempt deadline
(180s) bounds a run — put longer work behind a [job](job.md) the cron
triggers. Scheduler bills per job at trivial rates; `destroy` deletes
the jobs with everything else.

## Test it

Tests own scheduled time: `app.AdvanceCron(name)` fires one run
synchronously, by handler function name. Singleflight is testable by
occupying the cron's lease — named `cron-<handler>` — and firing:

```go
go func() {
    done <- tako.Lock("cron-cleanupOrphans").Do(ctx, func(tako.Ctx) error {
        close(holding)
        <-release
        return nil
    })
}()
<-holding
app.AdvanceCron("cleanupOrphans") // collapses into the held run
```

([examples/gallery/coordination_test.go](../../examples/gallery/coordination_test.go))

## Errors you'll meet

- `tako.Cron schedule "0 25 * * *": field 2 (hour): value "25" out of range 0–23 (tako/cron-schedule)`
- `"every 7m0s" cannot map onto Cloud Scheduler's unix-cron grammar — use minutes dividing 60, hours dividing 24, exactly 24h, or write a cron expression (tako/cron-schedule)`
- `tako.WithParallel is not a tako.Cron option (tako.Cron accepts WithSingleflight, WithProfile, WithFootprint) (tako/invalid-option)`
- `tako: AdvanceCron("x"): no such cron — declared crons: cleanupOrphans` — a test naming a cron that isn't declared.

## Not yet

- Schedules run in UTC (`Etc/UTC`); no per-cron timezone knob.
- No retryConfig surface — a failed attempt waits for the next tick;
  idempotency is the handler's job (singleflight guards overlap, not
  replay).
- unix-cron subset: `*`, `*/N`, values, ranges, and comma lists — no
  names (`MON`), no `L/W/#` extensions.
