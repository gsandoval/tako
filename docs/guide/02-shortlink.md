# 2 · shortlink — state, work, and exactly-once

[examples/shortlink](../../examples/shortlink/app.go) adds the two
primitives most apps need next: durable state and background work.

```go
var links  = tako.Entity[Link]("links")
var clicks = tako.Queue[Click]("clicks")

type Link struct {
    Slug    string    `entity:"id" json:"slug"`
    URL     string    `json:"url"`
    Owner   string    `entity:"index" json:"owner"`
    Created time.Time `entity:"index" json:"created"`
    Hits    int       `json:"hits"`
}

var CountClick = tako.Worker(clicks, countClick)
```

## Entities

`tako.Entity[T]` is *an* entity store (Datastore in the cloud, bbolt
locally) — deliberately not "the database". The struct is the schema:
`entity:"id"` is the key, `entity:"index"` marks what you may filter
and order by. Querying an unindexed field is an ANALYSIS error, not a
production surprise; combined query shapes derive their Datastore
composite indexes automatically, per query.

Claiming a slug atomically is a transaction on the store:

```go
err := links.Tx(ctx, func(tx tako.EntityTx[Link]) error {
    if _, err := tx.Get(req.Slug); err == nil {
        return tako.Errorf(409, "slug %q is taken", req.Slug)
    }
    ...
    return tx.Put(link)
})
```

## Queues and workers

`clicks.Publish(ctx, Click{...})` hands work off; the queue's ONE
worker (two is an analysis error — fan-out is what topics are for)
processes with the universal async contract: return `nil` to ack, an
error to retry with backoff, `tako.Permanent(err)` to give up. After
retries exhaust, the message lands in a dead letter you can inspect
(`tako dlq`) and drain (`--ack`).

Delivery is at-least-once — so shortlink counts hits through an
idempotency ledger keyed by the click's ID. The test suite proves it
the honest way: `app.Chaos()` injects seeded duplicates and
redeliveries, and the count must still be exact. Gut the ledger and
the test fails with a seed that reproduces the run.

## Run it

```
cd examples/shortlink
tako dev
curl -X POST localhost:8080/api/links -d '{"slug":"go","url":"https://go.dev","owner":"ada"}'
curl localhost:8080/api/links/go        # hits count exactly once per resolve
tako test                               # incl. the chaos test
```

In the cloud, the same declarations become Datastore, Pub/Sub with an
OIDC-authenticated push worker (private — curl gets 403 at Google's
front door), a composite index, and per-service IAM — all visible in
the deploy plan first.
