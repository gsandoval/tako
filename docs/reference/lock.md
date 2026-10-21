# `tako.Lock` — distributed leases

A lock guards critical **sections** — cross-entity rebuilds, calls
into external systems, sequential cursors — across all instances. It
is NOT for single-entity atomicity: that is `Tx`'s job, cheaper and
contention-free. Most apps never touch Lock directly: Cron's
`CronConfig.Singleflight` and Singleton are built on the same mechanism.

## Declare

```go
var relock = tako.Lock("reindex")
```

The name must be a string constant (analyzer-enforced) — lock names
ARE the identity; two declarations of `"reindex"` are the same lock.
Lock accepts **no options**: its signature is `tako.Lock(name string)`,
and the lease policy is framework-owned in v0.

The mechanism is the spec's §4.4 lease: a named compare-and-swap
record `{owner, expiry}` with a **30s TTL**, heartbeated every ttl/3
(10s), campaigns polling every ~2s with jitter. Tokens are fencing
generations — extension and release succeed only for the exact
holder, so a usurper can never be double-held. Crash safety needs no
cleanup: the TTL expires and the next campaigner takes over. Locks
are **not reentrant** — a holder's own re-acquire is refused like
anyone else's.

## Use

```go
err := relock.Do(ctx, func(ctx tako.Ctx) error { ... })      // blocks until held
ran, err := relock.TryDo(ctx, func(ctx tako.Ctx) error { ... }) // one attempt
```

`Do` campaigns until the lock is held, runs `fn` holding it, releases
on return. `TryDo` runs `fn` only if the lock is immediately
available; `(false, nil)` means someone else holds it — often the
right answer, not an error. Gallery's reindex uses `TryDo` because a
caller finding a rebuild mid-flight should collapse into it, not
queue another:

```go
ran, err := relock.TryDo(ctx, func(ctx tako.Ctx) error {
    var err error
    run, err = rebuild(ctx)
    return err
})
return ReindexRes{Started: ran, Run: run}, err
```

**Loss cancels ctx**: while `fn` runs the lease is heartbeated, and a
definitive loss cancels `fn`'s Ctx mid-flight. If `fn` itself
succeeded anyway, the loss still surfaces:
`lease "reindex" was lost while held (ttl expired or forcibly taken) — work after the loss point may not have run`.

## Local

`tako dev` and tests use `MemoryLease`, an in-process CAS table with
counter tokens — same TTL, same heartbeats, same semantics. The
campaigning, heartbeat, and loss-cancellation logic is shared code
above the backend interface, so local and cloud cannot diverge in
behavior, only in storage.

## Cloud

Each lease is one object in the framework bucket (`<project>-tako`)
at `leases/run-<app>-<env>-<name>`, written with
`ifGenerationMatch` — the atomic compare-and-swap GCS gives away for
free. The object body is `{owner, expires}`; the object **generation
is the fencing token**. Same bucket, same trick as the deploy lease,
different namespace — and env-scoped, so pr-42's reindex never
contends with prod's. Cost: a handful of GCS operations per held
window; no dedicated infrastructure.

## Test it

The lease backends pass the shared conformance suite
(`pkg/backends/conformance/lease.go`) — the contract between `local`
and `gcp`. App-level, contention is deterministic: occupy the very
lease the code campaigns for, then assert the collapse — see
`TestReindexCollapsesWhileHeld` in
[examples/gallery/coordination_test.go](../../examples/gallery/coordination_test.go),
which holds `tako.Lock("reindex")` in a goroutine, calls the reindex
endpoint, and asserts `Started == false` and an unchanged ledger.

## Errors you'll meet

- `tako.Lock name must be a string constant — names are read at analysis time, not run time; got variable "name" (tako/const-name)`
- `tako: no lease backend wired — run via tako dev, a generated service, or tako.NewTestApp; a tool that reaches a deployed app calls runtime.BootGCPWith first` — a panic from `Do` or `TryDo` in a bare test, the same shape every other unwired resource uses.
- `lease "reindex" was lost while held (ttl expired or forcibly taken) — work after the loss point may not have run`

## Not yet

- No tunable TTL, heartbeat, or campaign cadence — the policy is the
  framework's until an example demands otherwise.
- No reentrancy: holding a lock and taking it again deadlocks `Do`
  (and `TryDo` reports false), by design.
- No lock introspection or forced release from the CLI; a stuck
  holder resolves itself within the 30s TTL.
