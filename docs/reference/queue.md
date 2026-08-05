# `tako.Queue` + `tako.Worker` — at-least-once work

A queue is competing-consumer work: publish a payload, exactly one
worker processes it. Fan-out belongs to a [topic](topic.md).

## Declare

```go
var clicks = tako.Queue[Click]("clicks")

var CountClick = tako.Worker(clicks, countClick)
```

The worker var is a [`tako.Handler`](handler.md): the exported name is
what the service deploys as and what an alert names.

The name must be a string constant (analyzer-enforced). A queue has
**exactly one** worker — a second one is an analysis error, with a
pointer to the fix (see below). `tako.Worker` accepts `Compute.Profile`
and `Compute.Footprint` to shape the compute its service gets
([footprint.md](footprint.md)); gallery's resizer uses both:

```go
MakeThumb = tako.Worker(thumbs, makeThumb,
    tako.Compute{
        Profile:   tako.ProfileBatch,
        Footprint: tako.Footprint{Memory: "4Gi", Concurrency: 4, MaxInstances: 5},
    })
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

`Publish` takes one option, `PublishAfter(ctx, msg, d)`, deferring first delivery —
a timer (or the test clock) locally, a Cloud Tasks `scheduleTime` in
the cloud (T3).

### The delivery contract

Every queue and topic has one, and `Delivery` overrides it
field-by-field — zero fields inherit the framework's defaults:

```go
var archive = tako.Queue[Expiry]("archive", tako.Delivery{
	RateLimit:   2,                // deliveries per second (1–500)
	MaxAttempts: 5,                // before dead-lettering (5–100)
	MinBackoff:  30 * time.Second, // wait between attempts
	MaxBackoff:  10 * time.Minute,
}))
```

| field | default | range |
|---|---|---|
| `MaxAttempts` | 5 | 5–100 (Pub/Sub's dead-letter policy) |
| `MinBackoff` | 10s | 1s–10m |
| `MaxBackoff` | 300s | 1s–10m |
| `RateLimit` | none | 1–500 (Cloud Tasks' dispatch ceiling) |

Setting `RateLimit`, or publishing with `PublishAfter`, routes the queue's
cloud traffic through a Cloud Tasks queue in front of the worker — same
API, provisioner's choice — with one trade to know: Tasks has no dead
letter (see Not yet). Shortlink's TTL'd links publish their own expiry
with `PublishAfter`, and its archive walks at `RateLimit: 2` with slower,
fewer retries because archiving is idempotent and never urgent.

`RateLimit` is a QUEUE setting only. A topic fans out to subscribers
with independent fates, so there is no single consumer to pace — cap the
queue each subscriber feeds instead. Declaring it on a topic is refused.
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
own) fed by an OIDC push subscription to `/_tako/api/push` — private, so
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

## Replaying a dead letter

`tako dlq <service>` lists what failed, each line starting with an id:

```
  14672041893  2m ago   attempts 5   decode image: unknown format
    {"photo":"izupocvqy7bva6ax","caption":"boom"}
```

`tako dlq <service> --replay 1467` puts it back into delivery and takes
it off the DLQ — **a move, not a copy**. Any unique prefix of the id
works, the way git takes a short SHA. `--replay --all` does the lot.

The message goes back through the **real broker**, which is the point:
it gets the normal attempt count, the normal backoff, and if it fails
again it lands **back on the DLQ**. Replay is not a promise that the
message will succeed — it is a second chance under the same rules.

For a queue that is all there is to it: one worker, one destination.
For a **topic** it is subtler. A topic fans out to every subscriber, but
a dead letter belongs to exactly one of them — the one whose handler
failed. Republishing to the topic would re-run the subscribers that
already succeeded, so tako addresses the replay (`tako-replay-to`) and
every other subscriber acks it without doing work. You will see one
line per uninvolved subscriber in the logs; that is the mechanism, not
a bug.

Locally the same command works against `.tako/data`:

```
tako dlq --local                      # everything on disk
tako dlq --local thumbnails --replay 1699
```

A local replay moves the file into `deadletter/replay/`, which a
running `tako dev` drains within a couple of seconds. If dev is down it
waits for the next boot — the file is the message, so nothing is lost
either way.

`--ack` and `--replay` are refused together: they do the opposite
things to the same message.

## Errors you'll meet

- `queue "jobs" already has worker First (app.go:9) — a queue has exactly one worker; use a Topic for fan-out (tako/one-worker)`
- `tako.Compute takes one configuration value, got 2 — merge them into a single literal (tako/one-config)`
- `tako.Queue name must be a string constant — names are read at analysis time, not run time; got variable "name" (tako/const-name)`
- `queue "x" has no Cloud Tasks route — a PublishAfter site the analyzer did not see? redeploy so the deploy can provision it` — a binary older than its deploy.
- `delivery RateLimit must be 1–500 messages per second (Cloud Tasks' dispatch ceiling); got 501 (tako/delivery)`
- `delivery MaxAttempts must be 5–100 — Pub/Sub's dead-letter policy accepts no other range, and tako will not quietly clamp yours; got 2 (tako/delivery)`
- `delivery MinBackoff (5m0s) is longer than MaxBackoff (1m0s) — backoff grows, it does not shrink (tako/delivery)`
- `delivery RateLimit is a queue setting — a topic fans out to subscribers with independent fates, so there is no single consumer to pace; cap the queue each subscriber feeds instead (tako/delivery)`

At deploy time, after a destroy:

```
cannot recreate the tasks queue "u1-mailer-tako" — Cloud Tasks blocks reusing a
deleted queue's name for 7 days after the delete, and this name is still inside
that window. tako cannot work around it: either wait, or deploy under another
environment, whose queues get fresh names — tako deploy --env <name>
```

This is the one part of a teardown that does not converge: everything
else `tako destroy` deletes comes back on the next deploy, and a
rate-limited queue's NAME does not, for a week. It is Google's block and
there is no API to shorten it.

Because the error above arrives too late to act on, `tako destroy` says
so in its preview, ahead of the confirmation prompt:

```
  NAME REUSE — Cloud Tasks blocks recreating these queue names for 7 days:
    u1-mailer-tako
    redeploying this app sooner needs a different --env
```

## Not yet

- **Tasks-routed queues have no dead letter.** Cloud Tasks retries
  `MaxAttempts` times (mirroring the Pub/Sub contract) and then the task is
  GONE — there is no `tako dlq` for a rate-limited or delayed
  message that exhausts its retries. Put irreplaceable work on a
  plain queue; use Tasks routing for work whose source can re-emit.
- Delay ceiling: Cloud Tasks schedules at most 30 days out.
- `RateLimit` caps at 500/s (Cloud Tasks' dispatch ceiling); the
  local dispatcher paces to the same number under `tako dev`, but
  `NewTestApp` does not pace (tests assert semantics, not timing).
