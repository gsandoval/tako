# `tako.Queue` + `tako.Worker` — at-least-once work

A queue is competing-consumer work: publish a payload, exactly one
worker processes it. Fan-out belongs to a [topic](topic.md).

## Declare

```go
var clicks = tako.Queue[Click]("clicks")

var CountClick = tako.Worker(clicks, countClick)
```

The name must be a string constant (analyzer-enforced). A queue has
**exactly one** worker — a second one is an analysis error, with a
pointer to the fix (see below). `tako.Worker` accepts `WithProfile`
and `WithFootprint` to shape the compute its service gets
([footprint.md](footprint.md)); gallery's resizer uses both:

```go
MakeThumb = tako.Worker(thumbs, makeThumb,
    tako.WithProfile(tako.Batch),
    tako.WithFootprint(tako.Footprint{Memory: "4Gi", Concurrency: 4, MaxInstances: 5}))
```

## Use

`Publish` enqueues; the worker is the universal async shape —
`func(Ctx, T) error` — with the universal contract: return `nil` to
ack, an error to retry with backoff, `tako.Permanent(err)` to give up
immediately and dead-letter. From gallery's worker:

```go
func makeThumb(ctx tako.Ctx, job ThumbJob) error {
    img, err := photos.Get(ctx, job.PhotoID)
    if err != nil {
        return err // transient (object may lag the message): retry
    }
    if img.Size == 0 {
        return tako.Permanent(errors.New("empty upload")) // never retriable
    }
    ...
}
```

Delivery is **at-least-once** — duplicates are a fact, not a bug, so
idempotency is the worker's job. Shortlink claims each click's ID in a
transaction before counting; see the
[shortlink chapter](../guide/02-shortlink.md).

`Publish` takes one option, `WithDelay(d)`, deferring first delivery —
a timer (or the test clock) locally, a Cloud Tasks `scheduleTime` in
the cloud (T3). Declaring `WithRateLimit(n)` on the queue caps
dispatch at n/second everywhere. Either one routes the queue's cloud
traffic through a Cloud Tasks queue in front of the worker — same
API, provisioner's choice — with one trade to know: Tasks has no
dead letter (see Not yet). Shortlink's TTL'd links publish their own
expiry with `WithDelay`, and its archive walks at `WithRateLimit(2)`.
Payloads that fail to decode are `Permanent` automatically:
redelivering malformed JSON can't help.

## Local

`tako dev` runs an in-process dispatcher: **5 attempts, fixed 200ms
backoff, then dead-letter** to `<data>/deadletter/<queue>/<id>.json`.
The console narrates every delivery — `⚙` per attempt and ack, `✗` per
dead letter, with the payload's path.

`tako dev --chaos` sets `TAKO_CHAOS=<seed>` in the app: seeded
duplicate delivery, redelivery after ack, and reordering, each
injection narrated with a `☢` line. At-least-once bugs surface on your
laptop instead of in production, and the seed reproduces the run.

## Cloud

Each queue becomes a Pub/Sub topic; the worker becomes its own Cloud
Run service (the partition rule: each async handler is alone in its
own) fed by an OIDC push subscription to `/_tako/push` — private, so
curl gets 403 at Google's front door. Failure semantics match local,
as the deploy summary states: *"redelivery: 5 attempts with backoff,
then dead-letter"* (exponential, 10s–300s). `tako.Permanent`
short-circuits straight to the service's dead-letter topic, which
keeps messages on a pull subscription:

```
tako dlq                    # list every async service's dead letters
tako dlq <service>          # peek the messages (nothing is consumed)
tako dlq <service> --ack    # drain what it printed, for good
```

Cost-wise this is scale-to-zero: an idle queue is a free topic and a
worker service at zero instances (unless your footprint pins
`MinInstances` — warm instances bill continuously).

## Test it

`tako.NewTestApp` wires every declared worker; `DrainQueues` settles
async work; `Chaos()` is the honest proof of idempotency:

```go
func TestClicksCountExactlyOnceUnderChaos(t *testing.T) {
    app := tako.NewTestApp(t)
    app.Chaos()
    ...
    app.DrainQueues()
    // hits must be exactly 20 despite duplicates and redeliveries
}
```

The seed prints on every chaos run; rerun with `TAKO_CHAOS_SEED=<n>`
to replay a failure exactly.

## Errors you'll meet

- `queue "jobs" already has worker First (app.go:9) — a queue has exactly one worker; use a Topic for fan-out (tako/one-worker)`
- `tako.WithTTL is not a tako.Worker option (tako.Worker accepts WithProfile, WithFootprint) (tako/invalid-option)`
- `tako.Queue name must be a string constant — names are read at analysis time, not run time; got variable "name" (tako/const-name)`
- `tako.WithMaxSize is not a Publish option (Publish accepts WithDelay)` — at runtime, from `Publish`.
- `queue "x" has no Cloud Tasks route — a WithDelay publish site the analyzer did not see? redeploy so the deploy can provision it` — a binary older than its deploy.
- `tako.WithRateLimit takes a constant 1–500 messages per second (Cloud Tasks' dispatch ceiling) (tako/rate-limit)`

## Not yet

- **Tasks-routed queues have no dead letter.** Cloud Tasks retries 5
  attempts (mirroring the Pub/Sub contract) and then the task is
  GONE — there is no `tako dlq` for a rate-limited or delayed
  message that exhausts its retries. Put irreplaceable work on a
  plain queue; use Tasks routing for work whose source can re-emit.
- Delay ceiling: Cloud Tasks schedules at most 30 days out.
- `WithRateLimit` caps at 500/s (Cloud Tasks' dispatch ceiling); the
  local dispatcher paces to the same number under `tako dev`, but
  `NewTestApp` does not pace (tests assert semantics, not timing).
