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

`Publish` takes one option, `WithDelay(d)`, deferring first delivery
(local only for now — see Not yet). Payloads that fail to decode are
`Permanent` automatically: redelivering malformed JSON can't help.

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
- `tako.WithDelay on the gcp backend needs Cloud Tasks — that arrives with the delayed-delivery feature (no shipped example demands it yet); remove the delay or run locally`

## Not yet

- **`WithDelay` in the cloud.** It works locally (and in tests, on a
  fake clock `Advance` moves); on the gcp backend `Publish` errors
  with the text above until the Cloud Tasks-backed feature ships.
- **`WithRateLimit`.** Refused at analysis time with
  `rate-limited queues arrive with the Cloud Tasks feature — declare
  the queue without it for now (tako/not-yet)` — it limits nothing
  until that feature ships, and says so instead of pretending.
