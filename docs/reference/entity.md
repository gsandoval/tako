# tako.Entity — typed entity stores

`tako.Entity[T]` is *an* entity store — deliberately not "the
database". No global DB, Tx, or Query: transactions and queries are
methods on the store you declared. Datastore in the cloud, bbolt locally.

## Declare

From [examples/shortlink](../../examples/shortlink/app.go):

```go
var links = tako.Entity[Link]("links")

type Link struct {
    Slug    string    `entity:"id" json:"slug"`
    URL     string    `json:"url"`
    Owner   string    `entity:"index" json:"owner"`
    Created time.Time `entity:"index" json:"created"`
    Hits    int       `json:"hits"`
}
```

The struct is the schema: `entity:"id"` marks the key field (exactly
one, a string), `entity:"index"` the fields you may filter and order
by. The name is the store's stable identity — a string constant.

## Use

Keyed access like every keyed store: `Get`, `Put`, `Delete`, `Exists`.
`Put` stores under the id carried by the `entity:"id"` field; absence
reports through `tako.IsNotFound`; deleting an absent id is not an
error. `Update` is a transactional read-modify-write:
`links.Update(ctx, slug, func(l *Link) { l.Hits++ })`.

Queries build on the store and touch only indexed fields —
`q.Where("Owner", "=", owner).OrderBy("-Created").Limit(100).All()`.
`Where` takes one of `=` `<` `<=` `>` `>=`; `OrderBy` is `-`-prefixed
for descending; `All`, `First`, `Iter`, or `Count` execute.

`Count` returns how many entities match, and **ignores `Limit`**:

```go
list, err := posts.Query(ctx).Where("Published", "=", true).
	OrderBy("-Created").Limit(50).All()
total, err := posts.Query(ctx).Where("Published", "=", true).Count()
// "Showing 50 of 214"
```

That is the decision, not an oversight. `Limit` shapes a page; the total
is not a property of the page, and `Limit(50).Count()` returning 50
would answer a question nobody asked. Ordering is ignored too — it
cannot change how many things match.

On `gcp` this is a Datastore aggregation, billed per index entry scanned
rather than per entity returned, so counting a large match does not read
it. On `local` it walks the same filter the scan uses. The conformance
suite holds the two to the same answers, filters included.

`Tx` runs a multi-entity transaction scoped to this store; an error
rolls back and returns unchanged, so `tako.Errorf` statuses compose —
shortlink claims a slug atomically:

```go
err := links.Tx(ctx, func(tx tako.EntityTx[Link]) error {
    if _, err := tx.Get(req.Slug); err == nil {
        return tako.Errorf(409, "slug %q is taken", req.Slug)
    } else if !tako.IsNotFound(err) {
        return err
    }
    return tx.Put(link)
})
```

## Local

`tako dev` runs entities on a single bbolt file —
`.tako/data/entity.db` — in-process; no Datastore emulator, no Docker.
Data persists across dev reloads, and the shared conformance suite is
the contract that `local` and `gcp` behave alike.

## Cloud

The same declaration becomes Datastore — envs isolated by namespace
(namespace = env), kinds keeping their plain names. Indexes are
**derived from your call sites**, never hand-declared: single-property
indexes from the fields you actually `Where`/`OrderBy`, and a Datastore
**composite index per combined query shape**, grouped per function —
filter fields first (struct order, ascending), then `OrderBy` fields in
call order, `-`-prefixed for descending, in marshaled JSON property
names. Grouping per function can over-index a function running two
separate queries; it never under-indexes a shape. The one query in
[examples/blog](../../examples/blog/app.go) —
`.Where("Post", "=", …).OrderBy("Created")` — derives
`comments (post, created)`; shortlink's derives `links (owner, -created)`.

Composite indexes are built at deploy time and are the slowest part of a
first deploy: **~4 minutes each**, flat, regardless of how much data you
have. tako starts every build at once (Datastore builds them
concurrently) and collects them just before your services go live, so
the wait overlaps the image push rather than adding to it. Two indexes
cost ~4 minutes, not ~8.

`tako deploy` shows each composite as a `+ datastore index` plan entry
and creates it before the services, waiting on the build (first builds
take minutes). Live indexes on kinds tako manages that match no current
query shape are orphans — schema churn's leftovers — shown as
`? orphaned (kept)` and deleted by `tako prune` with no ceremony:
indexes are derivable, never data. Entity data only ever goes behind
`tako prune --data`'s typed confirmation; foreign kinds are invisible to both.

## Test it

`tako.NewTestApp` boots fresh in-process backends per test — two apps
share nothing. From [examples/shortlink](../../examples/shortlink/app_test.go):

```go
app := tako.NewTestApp(t)
ctx := app.Ctx()
_, err := Create.Call(ctx, CreateReq{Slug: "gh", URL: "https://github.com"})
```

Assert against the store directly (`links.Get(ctx, "gh")`) — absence
via `tako.IsNotFound`, statuses via `err.(interface{ HTTPStatus() int })`.

## Errors you'll meet

At analysis time, verbatim:

```
app.go:7:6: entity type Link must have exactly one field tagged entity:"id" (tako/entity-id)
app.go:8:2: entity id field Slug must be a string; got int (tako/entity-id)
app.go:7:31: tako.Entity name must be a string constant — names are read at analysis time, not run time; got variable "n" (tako/const-name)
app.go:17:32: query field must be a string constant — indexes are derived at analysis time; got variable "f" (tako/const-query-field)
app.go:18:9: query filters on Link.URL, but URL is not indexed — tag it entity:"index" to make it queryable (tako/unindexed-query)
```

At run time: `Put` with an empty id fails with `tako: entity "links":
id field Slug is empty`; outside a wired app, calls panic with
`tako: no entity backend wired — run via tako dev, a generated
service, or tako.NewTestApp`.

## Not yet

- Query ops stop at `=`, `<`, `<=`, `>`, `>=` — no `!=`, `IN`, or
  full-text; no cursors (`All`/`First`/`Iter` materialize — bound with `Limit`).
- Ids are strings, never auto-generated — mint with `tako.NewID()`.
- Transactions are per-store; cross-resource sections are
  [tako.Lock](lock.md)'s job.
- No migrations: entities are JSON documents; renamed fields are your
  data to move.
