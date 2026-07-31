# `tako.Cron` — scheduled handlers

A cron is a `func(Ctx) error` run on a schedule, declared like
everything else. Read the Cloud section before trusting one in
production — this page tells the truth.

## Declare

```go
var Cleanup = tako.Cron("every 24h", cleanupOrphans, tako.WithSingleflight())
```

The schedule must be a string constant (analyzer-enforced), and v0
accepts exactly one form: `"every <Go duration>"` — `every 30s`,
`every 1h`, `every 24h`. Cron expressions are rejected at analysis
time (see Errors); they arrive with cloud scheduling. The analyzer and
the runtime share the parser, so a schedule that analyzes always runs.

The only option is `WithSingleflight`: it wraps the handler in the
§4.4 lease ([lock.md](lock.md)), so overlapping or double-fired runs
collapse to one execution — across all instances, not just one
process. The wrap happens at declaration, so every trigger — dev
ticker, `AdvanceCron`, a future scheduler — inherits it.

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

**The honest limit: nothing schedules crons in the cloud yet.** A
per-instance ticker under autoscaling would fire once per live
instance and never when scaled to zero — worse than nothing — so
deployed crons are deliberately inert until the scheduler feature
lands. `tako deploy` says so, per cron:

```
⚠ cron gallery.Cleanup is not scheduled in the cloud yet (a scheduler feature follows); it still runs under tako dev
```

The deployed service also logs `cron not scheduled in the cloud yet (a
scheduler feature follows)` at startup. Do not believe a daily job is
running in production; it isn't.

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

- `tako.Cron schedule must be "every <duration>" for now (cron expressions arrive with cloud scheduling); got "0 3 * * *" (tako/cron-schedule)`
- `tako.WithParallel is not a tako.Cron option (tako.Cron accepts WithSingleflight) (tako/invalid-option)`
- `tako: AdvanceCron("x"): no such cron — declared crons: cleanupOrphans` — a test naming a cron that isn't declared.

## Not yet

- **Cloud scheduling.** Deployed crons do not run — the ⚠ deploy
  warning above is the contract until the scheduler feature ships.
- **Cron expressions** (`"0 3 * * *"`): they arrive with cloud
  scheduling; only `"every <duration>"` parses today.
