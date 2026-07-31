# tako.Cache — per-instance TTL cache

A typed, in-memory cache. v0 is **per-instance**: no shared backend,
no cross-instance invalidation — which is also why it costs nothing.

## Declare

From [examples/gallery](../../examples/gallery/app.go):

```go
var feed = tako.Cache[[]Photo]("feed", tako.WithTTL(time.Minute))
```

The name must be a string constant (analyzer-enforced). `WithTTL` is
the only Cache option; no declared TTL means entries never expire.

## Use

Keyed like every keyed store: `Get`, `Put`, `Delete`, `Exists` — an
absent or expired entry reports through `tako.IsNotFound`.
`GetOrFill` covers the cold start:

```go
return feed.GetOrFill(ctx, feedKey(owner), func(ctx tako.Ctx) ([]Photo, error) {
    return readyPhotos(ctx, owner)
})
```

A fill error is returned and nothing is cached; concurrent misses may
each fill — it's a cache, the last `Put` wins. Gallery keeps the feed
warm by `Put`ting from an event subscriber (`warmFeed`), so readers
rarely pay the query.

## Local

In-process and in-memory; entries expire on read past their deadline.
`tako dev`'s whole app is one instance, so locally the cache behaves
like a shared one — the cloud is where the per-instance semantics show.

## Cloud

Each Cloud Run instance carries its own copy. A `Put` on one instance
is invisible on the others; after a deploy or scale-to-zero the cache
starts empty. Staleness is bounded only by the TTL — pick it as your
answer to "how stale may this read be?". The honest upside: `tako
deploy` provisions nothing for a cache — no Redis, no IAM, no line in
the plan, no bill. A shared backend becomes a later opt-in without
changing call sites.

**When NOT to use it:** anything correctness depends on. Session or
auth state, counters, idempotency ledgers, "claimed" flags — those are
`tako.Entity`'s job (transactions), and cross-instance mutual
exclusion is `tako.Lock`'s. If you'd need to invalidate it everywhere
at once, it isn't cache material.

## Test it

From [examples/gallery](../../examples/gallery/app_test.go): upload,
`app.DrainQueues()` to let `warmFeed` run, then assert the served feed —
the cache is exercised through the endpoints, never poked directly.

## Errors you'll meet

Passing any option but `WithTTL` fails with, e.g.,
`tako.WithMaxSize is not a Cache option (Cache accepts WithTTL)`.
A non-constant name is the same `tako/const-name` analysis error every
resource gets (see [entity.md](entity.md)). Outside a wired app:
`tako: no cache backend wired — run via tako dev, a generated service,
or tako.NewTestApp`.

## Not yet

- No shared/cross-instance backend (Memorystore-class) — planned as an
  opt-in behind the same call sites.
- No explicit cross-instance invalidation, no size limits or eviction
  policy beyond TTL, no hit/miss metrics.
