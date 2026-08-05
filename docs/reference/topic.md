# `tako.Topic` + `tako.Subscribe` — fan-out events

A topic is broadcast: every subscriber gets every event, and each one
retries and dead-letters on its own. Where a [queue](queue.md) has
exactly one worker (competing consumers, one job done once), a topic
has any number of subscribers — including zero: events broadcast into
silence, legally.

## Declare

```go
var ready = tako.Topic[PhotoReady]("photo-ready") // fan-out: MANY subscribers

var NotifyOwner = tako.Subscribe(ready, notifyOwner)
var WarmFeed    = tako.Subscribe(ready, warmFeed)
```

Each subscriber var is a [`tako.Handler`](handler.md), and each is its
own deployable with its own dead letter — which is what lets two
subscribers on one topic fail independently.

The name must be a string constant (analyzer-enforced). `Subscribe`
binds a handler to any **event source** — a topic, or a bucket's
`Uploaded` event ([bucket.md](bucket.md)) — and accepts `WithProfile`
and `WithFootprint` for the service it runs in
([footprint.md](footprint.md)).

Payloads are compile-checked: `ready` is a `*TopicHandle[PhotoReady]`,
so `notifyOwner` must be `func(tako.Ctx, PhotoReady) error` — a
subscriber with the wrong payload type does not build. No stringly
typed event names on the consuming side, no runtime surprises.

## Use

```go
return ready.Publish(ctx, PhotoReady{PhotoID: p.ID, Owner: p.Owner, Caption: p.Caption})
```

Every subscriber is the universal async shape: return `nil` to ack,
an error to retry with backoff, `tako.Permanent(err)` to give up and
dead-letter. The point of fan-out is **independent fates**: gallery's
`NotifyOwner` can retry into its own dead letter while `WarmFeed`
processes the same event untouched — one subscriber failing never
delays another.

This is the preferred inter-service style: gallery's whole pipeline is
upload → bucket event → queue → worker → topic → two subscribers, and
nobody calls anybody ([gallery chapter](../guide/03-gallery.md)).

## Local

`tako dev` fans out in-process: one delivery loop per
(topic, subscriber) pair, each with the local policy — 5 attempts,
fixed 200ms backoff, then a dead letter at
`<data>/deadletter/<topic>/<subscriber>/<id>.json` (note the
per-subscriber directory; queues have no such level). The console
narrates each subscriber's attempts and acks separately, and
`tako dev --chaos` injects duplicates and redeliveries per delivery,
just as for queues.

## Cloud

One Pub/Sub topic; one OIDC push subscription **per subscriber
service** (each subscriber is its own private Cloud Run service),
each carrying local's failure semantics — as the deploy summary puts
it, *"redelivery: 5 attempts with backoff, then dead-letter"* — into
its own per-service dead-letter topic. `tako dlq` lists and drains
them per service, exactly as for workers ([queue.md](queue.md)).

Idle topics and scale-to-zero subscribers cost nothing to sit there;
each event pays per-subscriber delivery, which is the honest price of
fan-out.

## Test it

`tako.NewTestApp` wires every declared subscriber; `DrainQueues`
settles the fan-out. Gallery's suite proves independence directly: a
caption containing `"boom"` makes `notifyOwner` fail permanently, and
the test asserts the dead letter lands in
`deadletter/photo-ready/NotifyOwner/` while `WarmFeed`'s directory
stays empty and the feed still warms
([examples/gallery/http_test.go](../../examples/gallery/http_test.go)).
Chaos mode (`app.Chaos()`) covers the at-least-once side: subscribers
must tolerate duplicates like any async handler.

## Errors you'll meet

- `tako.Compute must be written as a literal with constant fields — configuration is read at analysis time, not run time (tako/config-literal)`
- `tako.Topic name must be a string constant — names are read at analysis time, not run time; got variable "name" (tako/const-name)`
- `queue "jobs" already has worker First (app.go:9) — a queue has exactly one worker; use a Topic for fan-out (tako/one-worker)` —
  the error that sends you here from a queue.
- `tako: no topic backend wired — run via tako dev, a generated service, or tako.NewTestApp` — a panic from `Publish` in a bare
  test without a booted app.
- On the wire, minutes after a fresh deploy: `POST /_tako/api/push → 403 …
  lacks {run.routes.invoke} permission`. IAM propagation, not a
  misconfiguration — Pub/Sub retries and it stops. See
  [the cloud story](../guide/cloud.md#the-first-two-minutes-after-a-deploy).

## Not yet

- No ordering guarantee across events, no filtering, no replay of
  past events to a new subscriber — a subscriber sees what is
  published after it exists.
- No per-subscriber delivery options beyond compute shape
  (`WithProfile` / `WithFootprint`); retry pacing and attempt counts
  are framework policy, not knobs.
