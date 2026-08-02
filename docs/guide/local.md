# The local story

`tako dev` runs your app as one ordinary Go process. There are **no
emulators and no Docker** anywhere in local development:

| Declared | Local backing |
|---|---|
| `tako.Entity[T]` | a bbolt file, `.tako/data/entity.db` |
| `tako.Bucket` | directories under `.tako/data/buckets/` |
| `tako.Queue` / `tako.Topic` | in-process channels calling your handlers |
| `tako.Lock` / singleflight / `Singleton` | an in-process lease table |
| `tako.Job` | executions ride the local queue dispatcher |
| signed URLs | served by your own process |
| `tako.Auth` | a dev bar: sign in as alice/bob, no GCP anywhere |
| `tako.Secret` | `.env` in the module directory |

Because queues are channels calling your functions, a breakpoint
steps from a publish straight into the worker — and a cross-service
`Endpoint.Call` is a direct function call locally, so the debugger
steps across "service" boundaries too.

## The database is a directory

`.tako/data` IS your local state. Deleting it is the reset story:
next boot starts empty (an empty shop has an empty shelf — stock it).
Nothing else persists.

## Fidelity

Local and cloud backends pass one shared **conformance suite** — that
is the contract keeping them honest with each other, instead of an
emulator's imitation. Where v0 behavior differs by design (the cache
is per-instance; local queue retries pace in milliseconds), the doc
comment on the declaration says so.

## Dev-loop details worth knowing

- The dev process serves ONE port (default 8080; `PORT=8090 tako dev`).
  Everything — workers, subscribers, crons, jobs, singleton loops,
  private callee services — runs inside it.
- Site `WithBuild` commands re-run on change; their output never
  retriggers the reload loop.
- The spawned service binary dies with the dev process, however the
  dev process dies — no orphaned binaries holding ports or the
  entity-store lock.
- `tako dev --chaos` injects seeded duplicate/reordered deliveries so
  at-least-once bugs surface on your laptop, reproducibly
  (`TAKO_CHAOS_SEED` replays a failure exactly).

## Testing

`tako.NewTestApp(t)` boots the same in-process backends, fresh per
test: `app.Ctx(app.SignIn("alice"))` for identities,
`Endpoint.Call` to invoke handlers, `app.DrainQueues()` to settle
async work (jobs included), `app.AdvanceCron(Cleanup)` to fire a cron,
`app.Chaos()` for delivery chaos. `tako test` regenerates stale code
first, then runs `go test ./...`.
