# `tako.Cron` — scheduled handlers

A cron is a `func(Ctx) error` run on a schedule, declared like
everything else — a ticker locally, a Cloud Scheduler job in the
cloud (T2), one declaration for both.

## Declare

**Go**
```go
var Cleanup = tako.Cron("every 24h", cleanupOrphans, tako.CronConfig{Singleflight: true})
```

**cfire**
```cfire
PruneExpired := tako.cron("0 * * * *", prune_expired)
```

`Cleanup` is a [`tako.Handler`](handler.md) — the value you hand to
`CronMissed`, `CronFailed` and `app.AdvanceCron`.

The schedule must be a string constant (analyzer-enforced), in either
form: `"every <Go duration>"` (`every 10m`, `every 6h`, `every 24h`)
or a 5-field unix-cron expression (`"0 3 * * *"`, `"0 9-17 * * 1-5"`).
Both must map onto Cloud Scheduler's grammar — an `every` interval
that unix-cron cannot express (`every 7m`, `every 90s`) is refused at
analysis time with the mapping rule. The analyzer, the runtime, and
the provisioner share one parser, so a schedule that analyzes runs
locally AND provisions.

`CronConfig.Singleflight` wraps the handler in the §4.4 lease
([lock.md](lock.md)), so overlapping or double-fired runs collapse to
one execution — across all instances, not just one process. The wrap
happens at declaration, so every trigger — dev ticker, `AdvanceCron`,
Cloud Scheduler — inherits it. `CronConfig.Profile` and `CronConfig.Footprint`
([footprint.md](footprint.md)) shape the HOSTING service, the escape
hatch a cron-only package would otherwise lack.

## Use

The handler is plain: do the work, return `nil` or an error. Cron
handlers run inside the package's app service. Gallery's daily
cleanup deletes never-completed uploads
([examples/gallery/app.go](../../examples/gallery/app.go)); failures
are narrated and the next run stays on schedule — there is no retry
of a failed run, the schedule itself is the retry.

### Timezone and deadline

**Go**
```go
var Cleanup = tako.Cron("0 9 * * *", cleanupOrphans,
	tako.CronConfig{
		Singleflight: true,
		Timezone:     "America/Mexico_City",
		Deadline:     10 * time.Minute,
	})
```

**cfire**
```cfire
Cleanup := tako.cron("0 9 * * *", cleanup_orphans)
```

Schedules run in **UTC** unless `CronConfig.Timezone` says otherwise — nine
o'clock means nine o'clock somewhere, and the zone follows daylight
saving. The name is an IANA zone, checked against the tz database **at
analysis time**, so a misspelling is a build error rather than a 3am
surprise.

`CronConfig.Deadline` caps one run (default 180s, range 15s–30m) before Cloud
Scheduler abandons the attempt. There is no retry config: the schedule
IS the retry policy for an idempotent cron. Work that needs longer than
30 minutes wants a [job](job.md).

## Local

`tako dev` runs each cron on a real ticker and narrates it:

```
⏰ cron Cleanup: every 24h
⏰ cron Cleanup ok (312ms)
⏰ cron Cleanup failed: <err> — next run on schedule
```

Runs are serialized per cron in-process — a tick landing mid-run is
skipped with a `⏰ … run still in progress — tick skipped` line. With
`CronConfig.Singleflight` the collapse is cross-instance and lease-based,
with its own narration — the feature working, never an error:

```
↻ cron cleanupOrphans skipped — an overlapping run holds the lease
```

## Cloud

Every cron provisions a **Cloud Scheduler job**
(`+ create scheduler job <svc>-cron-<name>`) firing
`POST /_tako/api/cron/<name>` on the service with an OIDC token minted as
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

> Immediately after a fresh deploy, a cron that fires can get a 403
> from Cloud Run's front door while the Scheduler job's invoker grant
> propagates — a minute or two, self-healing, and the next scheduled
> run is the recovery. See
> [the cloud story](../guide/cloud.md#the-first-two-minutes-after-a-deploy).

**The cron's identity is its variable.** `var Cleanup = tako.Cron(...)`
deploys as the Scheduler job for `/_tako/api/cron/Cleanup`, leases as
`cron-Cleanup` under `CronConfig.Singleflight`, and is fired in a test as
`app.AdvanceCron(Cleanup)` — one name, from one place. Renaming the
variable renames the cron; renaming its function does not.

> Upgrading from a tako that keyed the singleflight lease on the handler
> function's name: during the one deploy that rolls the change out, old
> and new instances hold different keys, so a single overlapping run can
> fire twice. Afterwards it is stable.

Tests own scheduled time: `app.AdvanceCron(Cleanup)` fires one run
synchronously, by handler function name. Singleflight is testable by
occupying the cron's lease — named `cron-<handler>` — and firing:

**Go**
```go
go func() {
    done <- tako.Lock("cron-cleanupOrphans").Do(ctx, func(tako.Ctx) error {
        close(holding)
        <-release
        return nil
    })
}()
<-holding
app.AdvanceCron(Cleanup) // collapses into the held run
```

**cfire**
```cfire
app := tako.new_test_app()
try prune_expired(app.ctx())
```

([examples/gallery/coordination_test.go](../../examples/gallery/coordination_test.go))

## Errors you'll meet

- `tako.Cron schedule "0 25 * * *": field 2 (hour): value "25" out of range 0–23 (tako/cron-schedule)`
- `"every 7m0s" cannot map onto Cloud Scheduler's unix-cron grammar — use minutes dividing 60, hours dividing 24, exactly 24h, or write a cron expression (tako/cron-schedule)`
- `tako.CronConfig.Timezone does not know the zone "Mars/Olympus" — use an IANA name like "America/Mexico_City" or "Europe/Madrid" (tako/timezone)`
- `tako: AdvanceCron: that is a worker, not a cron — workers run when their queue delivers, so publish to the queue and DrainQueues` — passing a handler that isn't a cron. Passing a var that was never assigned a constructor gives `tako: AdvanceCron: the handler is empty — pass the var a tako.Cron was assigned to, like app.AdvanceCron(Cleanup)`. A *mistyped* cron is no longer possible: it does not compile.

## Not yet

- No retryConfig surface — a failed attempt waits for the next tick;
  idempotency is the handler's job (singleflight guards overlap, not
  replay).
- unix-cron subset: `*`, `*/N`, values, ranges, and comma lists — no
  names (`MON`), no `L/W/#` extensions.
