# tako.NewTestApp: testing an app

Every other page in this reference ends with a "Test it" section. This
page is the surface those sections use: what `tako.NewTestApp` boots,
what each method does, and where the seams are.

The shape of a Tako test is: **boot an app, call handlers directly,
settle the async work, assert.** No HTTP, no emulators, no Docker, no
cloud credentials — the same in-process backends `tako dev` runs, one
fresh set per test.

## Declare

Nothing. `NewTestApp` reads the declarations your package already made
and wires every one of them.

## Use

From [examples/shortlink](../../examples/shortlink/app_test.go):

```go
func TestCreateResolveList(t *testing.T) {
    app := tako.NewTestApp(t)
    u := app.SignIn("memo")
    ctx := app.Ctx(u)

    if _, err := Create.Call(ctx, CreateReq{Slug: "gh", URL: "https://github.com"}); err != nil {
        t.Fatal(err)
    }
    // Resolving enqueues a click; the count lands once queues drain.
    if _, err := Resolve.Call(ctx, ResolveReq{Slug: "gh"}); err != nil {
        t.Fatal(err)
    }
    app.DrainQueues()

    if l, _ := links.Get(app.Ctx(), "gh"); l.Hits != 1 {
        t.Fatalf("hits = %d", l.Hits)
    }
}
```

Three things are load-bearing there:

- **`Endpoint.Call` invokes the handler**, not an HTTP round trip. It
  is the same method cross-service calls use, so the request type is
  checked at compile time and a failure is a Go error, not a status
  code you have to decode.
- **`app.Ctx()` is how a test reaches resources.** The Ctx carries this
  app's backends, which is what makes tests isolated — `links.Get`
  above reads the entity store belonging to *this* `app`. Two
  `NewTestApp` calls share nothing, so `t.Parallel()` is safe.
- **Async work is not instant.** `Resolve` publishes to a queue; the
  worker runs on the dispatcher. `DrainQueues` is the barrier.

### The full surface

| Call | Does |
|---|---|
| `tako.NewTestApp(t)` | fresh backends, every worker/subscriber/job wired, cleaned up at test end |
| `app.Ctx(user…)` | a Ctx bound to this app; pass a `User` to authenticate it |
| `app.HTMX(user…)` | the same, with `ctx.IsHTMX()` true — drives a Page down its fragment branch |
| `app.SignIn(name)` | a stub `User{ID: name, Email: name+"@example.test"}` — identity injection, not auth enforcement |
| `app.DrainQueues()` | block until every published message has acked or dead-lettered |
| `app.Advance(d)` | move the fake clock, releasing `PublishAfter` messages that came due |
| `app.AdvanceCron(cron)` | run one cron body once, synchronously — pass the cron itself |
| `app.SetSecret(name, v)` | provide a declared secret; removed when the test ends |
| `app.Chaos()` | duplicate, reorder and redeliver messages — seeded and reproducible |

### Time is fake, and only for delays

`app.Advance(d)` moves *scheduled* time: a `PublishAfter(ctx, msg, 4 * time.Hour)`
publish becomes testable in microseconds.

```go
app.DrainQueues()          // idle — nothing is due yet, the worker has NOT run
app.Advance(4 * time.Hour)
app.DrainQueues()          // the worker ran exactly once
```

The subtlety worth knowing: **`DrainQueues` treats a not-yet-due
message as idle** rather than waiting out its delay. Without that,
every `PublishAfter` test would hang for the real duration. Retry backoff
and chaos reordering keep their real (millisecond) timers, because
those are the contract under test.

**Crons do not ride that clock.** You fire one directly, because a cron
test means *"this cron does the right thing when it runs"*, not *"time
passed and whatever was due fired"*:

```go
var Cleanup = tako.Cron("every 24h", cleanupOrphans)

app.AdvanceCron(Cleanup)   // the cron itself, not a name
```

**You pass the cron, not a string.** The declaring variable is the
cron's identity — it is what tako deploys it as, and what Cloud
Scheduler addresses at `/_tako/api/cron/Cleanup` — so naming it any other
way would be naming something else. It also means the compiler checks
it: a typo or a renamed var is a build error, never a test that
silently runs nothing. This is the same way every other declaration is
reached in a test (`Create.Call`, `links.Get`, `thumbs.Publish`).

### Chaos mode

`app.Chaos()` makes the queue misbehave on purpose — duplicate
delivery, redelivery after ack, reordering — which is what at-least-once
delivery actually does in production and almost never does on your
laptop.

```go
func TestClicksCountExactlyOnceUnderChaos(t *testing.T) {
    app := tako.NewTestApp(t)
    app.Chaos()
    // …publish, DrainQueues, assert the tally is still exact
}
```

The seed prints on every run, and `TAKO_CHAOS_SEED=<n>` replays a
failure exactly. A handler that is not idempotent fails here rather
than in production; that is the entire point. `tako dev --chaos` is
the same behavior for a manual walk.

## Local

`NewTestApp` is `tako dev`'s boot path with a `t.TempDir()` for state —
the same bbolt entity store, filesystem bucket, and channel-backed
queues, configured for tests rather than for a session:

- **retries are fast, not production-paced** — 5 attempts with a 5ms
  backoff, because a test asserts the retry *contract*, not the timing;
- **the clock is manual**, per above;
- **cleanup settles first** — in-flight deliveries drain before the
  temp dir goes away, so a worker mid-write never races teardown.

Secrets are the one process-wide exception: `Value()` takes no Ctx, so
`SetSecret` sets it for the process and unsets it at test end. Tests
that share a secret name across parallel apps share that value.

## Cloud

Nothing. `NewTestApp` never authenticates, never reaches the network,
and does not need a project. `tako test` runs analysis, refreshes
generated code so a stale checkout can't pass, and then runs
`go test ./...`; every example's suite is green against the local
backends on every commit.

The local/cloud agreement is held by a different mechanism: the
[conformance suite](../../pkg/backends/conformance), which runs the
same cases against `local` and `gcp` backends. Your tests assert your
app; conformance asserts the backends underneath it.

## When a bare `go test` is the right tool

Not everything wants an app. Pure functions — a slug generator, a price
calculation — are ordinary Go and need no boot. What *does* need boot is
anything that touches a declared resource, and calling one without an
app is a panic naming the fix:

```
tako: no entity backend wired — run via tako dev, a generated service, or tako.NewTestApp
```

A handful of contracts are the wire or the filesystem rather than app
logic — multipart decoding, signed-URL rejection texts, on-disk
dead-letter layout. Those live on `httptest` against the generated
service's shape (see `examples/gallery/http_test.go`), deliberately
kept apart from the `NewTestApp` suite next to it.

## Errors you'll meet

```
tako: AdvanceCron: that is a worker, not a cron — workers run when their queue delivers, so publish to the queue and DrainQueues
tako: AdvanceCron: the handler is empty — pass the var a tako.Cron was assigned to, like app.AdvanceCron(Cleanup)
tako: DrainQueues: queues still busy after 30s
tako: secret "notifier-key" is not set — add it to .env (tako dev, generated services) or call app.SetSecret (tests)
tako: ctx.User on an unauthenticated context — pass a user to app.Ctx, or use MaybeUser
tako: TAKO_CHAOS_SEED must be a seed integer, got "yesterday"
```

**Using a resource with no app booted panics, always** — one condition,
one mechanism, one shape of sentence:

```
tako: no entity backend wired — run via tako dev, a generated service, or tako.NewTestApp
tako: no lease backend wired — run via tako dev, a generated service, or tako.NewTestApp
```

It panics rather than returning an error because these methods' error
returns already mean something: an unwired `Entity.Get` reporting an
error would be mistaken for not-found, and an unwired `Lock.Do` for the
function failing. A panic cannot be mistaken for either.

`Site` is the one exception, and returns an error, because it is the
one case that is not a wiring mistake — templates are compiled INTO a
generated service, so there is nothing `NewTestApp` could have wired:

```
tako: site templates are not wired — Render works in generated services (tako dev / deploy), not bare tests
```

That is the real limit worth internalizing: **`Render` and
`Fragment` need a generated service**, because templates are compiled
into it. Page handlers are still testable — assert the data path and
the redirect/fragment branch via `app.Ctx()` / `app.HTMX()` — but
asserting rendered HTML means the `httptest` route above.

## Not yet

- No HTTP-level test helper: there is no `app.Get("/path")`. Handlers
  are called directly, and anything that must be tested over the wire
  goes through `httptest` with the runtime harness.
- `NewTestApp` does not pace anything — no rate limits, no delivery
  timing, no cold starts. Tests assert semantics; timing is a cloud
  property.
- No fake for cross-service `Call` to a *remote* service: in tests the
  callee's handler runs in-process, so a test proves the contract, not
  the network between them.
- `SignIn` injects an identity; it does not exercise a provider's
  sign-in flow.
