# tako.Handler — what a background declaration returns

Five constructors hand one back: [`Worker`](queue.md),
[`Subscribe`](topic.md), [`Runner`](job.md), [`Cron`](cron.md) and
[`Singleton`](singleton.md). A `Handler` is that declaration's **binding**
— the value you name when you want to say *that* handler somewhere else.

This page bends the rubric like [cli.md](cli.md) does: a `Handler` is not
a feature you declare, it is the value five features return. The HTTP
half of the story is [`Endpoint`](calls.md), which is what `tako.API` and
`tako.Page` return instead.

## Declare

You never write the type — you receive it, always into a package-level
var. From [examples/gallery](../../examples/gallery/app.go) and
[examples/shop](../../examples/shop/app.go):

```go
var (
    WarmFeed = tako.Subscribe(ready, warmFeed)
    Cleanup  = tako.Cron("every 24h", cleanupOrphans, tako.CronConfig{Singleflight: true})
    Settle   = tako.Runner(settle, runSettle, tako.RunnerConfig{Parallel: 4})
    Invoicer = tako.Singleton(assignInvoices)
)
```

Constructors are side-effect-free records, so declaration order never
matters. **The var's name is the identity** — not the function's. That is
the name tako deploys the handler under, the name in `tako gen graph`,
and the name in a log line; `makeThumb` the function is a label, and
renaming it changes nothing outside your own file.

## Use — a Handler is a name, not a callable

It has no exported methods, deliberately. What you do with one is
**name** it:

```go
// alerts, which is where most Handlers get named
Silent  = tako.Alert(tako.CronMissed(Cleanup, tako.Window{For: 8 * time.Minute}), oncall)
Failing = tako.Alert(tako.CronFailed(Cleanup, 1, tako.Window{Every: 5 * time.Minute}), oncall)
Stopped = tako.Alert(tako.SingletonDown(Invoicer, tako.Window{For: 2 * time.Minute}), oncall)
Hungry  = tako.Alert(tako.WhenAbove(
    tako.ServiceMetric(MakeThumb,
        "run.googleapis.com/container/memory/utilizations"), 0.9,
    tako.Window{For: 10 * time.Minute}), oncall)
```

A `Handler` satisfies [`Target`](alerts.md) — anything an alert can be
about — and a `Subscribe` handler also satisfies `Consumer`, because a
subscriber falls behind and dead-letters independently of every other
subscriber on the same event.

There is no `Call`. Invoking a worker directly would be a second way to
run the work — one that skips the queue that gives it its retries, its
dead letter and its at-least-once delivery. You publish to the queue;
you don't call the worker. The same holds for a cron: you wait, or in a
test you advance the clock.

Each Handler carries its **kind**, and the analyzer checks the kind
against what you asked for — naming a worker where a cron belongs is a
refusal, not a surprise at 3am.

## Local

`tako dev` and `tako test` collapse every handler into the one process
that serves the app — workers, subscribers, crons, jobs and singletons
all register in-process. Queues are channels, and crons really tick: on
a real clock under `tako dev`, on the test clock under `AdvanceCron`.

Declaring a Handler is still a side-effect-free record, so declaration
order never matters and the generated main is what wires it — which is
why a handler declared inside a function is invisible to analysis (see
the errors below). The one carve-out: async constructors also append
their record to an in-process registry read **only** by `NewTestApp`, so
a test needs no list of handlers to drain.

## Cloud

`Worker`, `Subscribe`, `Singleton` and `Runner` each become their own
deployable, named `<package>-<var>` lowercased: `MakeThumb` in package
`gallery` deploys as `gallery-makethumb`, `Settle` in `shop` as
`shop-settle`. `Worker`, `Subscribe` and `Singleton` become Cloud Run
**services**; `Runner` becomes a Cloud Run **Job** ([job.md](job.md));
and `Cron` runs inside its package's app service, provisioning a Cloud
Scheduler job that triggers `/_tako/api/cron/<var>` over OIDC
([cron.md](cron.md)). Async services are private, reached over
authenticated push from Pub/Sub or Cloud Tasks and never from the
internet, and each deployable gets its own service account carrying only
the permissions its own code edges imply.

Two consequences worth knowing before you rename a var:

- **Renaming a Handler var renames its deployable.** The old name has
  nothing declaring it any more, so the plan shows an orphan beside the
  create and marks the pair a **rename suspect** rather than quietly
  moving anything. Compute orphans are stateless — `tako prune` removes
  them and a deploy re-derives them — but the plan says so out loud
  because a rename that carried data would not be. See
  [cli.md](cli.md).
- A Handler's own scaling comes from its `Compute`/`RunnerConfig`
  options and their [`Profile`](footprint.md) field — a batch worker and a
  latency-sensitive API do not want the same instance shape.

## Test it

`AdvanceCron` takes the Handler itself, which is the whole reason the
var exists. From [examples/gallery](../../examples/gallery/app_test.go):

```go
app := tako.NewTestApp(t)
app.AdvanceCron(Cleanup)   // fire the schedule once, now
app.DrainQueues()          // let whatever it published run
```

Workers and subscribers need no naming at all — `DrainQueues` runs
whatever is pending — so `Runner`, `Worker` and `Subscribe` handlers are
usually declared into vars for the alerts and the deployed name rather
than for the tests. See [testing.md](testing.md).

## Errors you'll meet

A handler that is not a package-level var cannot be analyzed, so it
cannot be deployed:

```
app.go:6:6: tako.API must initialize a package-level var — handlers declared inside functions are invisible to analysis (tako/package-level)
```

Naming the wrong kind of handler in an alert:

```
app.go:28:41: tako.CronMissed watches a cron, but alerts.Work is a worker (tako/alert-subject)
```

Two queues cannot share one:

```
app.go:11:26: queue "jobs" already has worker First (app.go:9) — a queue has exactly one worker; use a Topic for fan-out (tako/one-worker)
```

## Not yet

- **No exported accessors.** A Handler will not tell you its name, kind
  or schedule at run time; that information is the analyzer's, and
  `tako gen graph` is how you read it. Adding getters would make the
  declaration a run-time object, which is exactly what tako's static
  analysis exists to avoid.
- **No handler-to-handler direct invocation.** Work reaches a handler
  through its queue, topic, schedule or job — the mechanism is what
  carries the delivery guarantees, so there is no shortcut past it.
