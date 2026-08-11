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
| `tako.Secret` | `.env` in the module directory (`tako secrets set`) |

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
- Site `SiteConfig.Build` commands re-run on change; their output never
  retriggers the reload loop.
- The spawned service binary dies with the dev process, however the
  dev process dies — no orphaned binaries holding ports or the
  entity-store lock.
- `tako dev --chaos` injects seeded duplicate/reordered deliveries so
  at-least-once bugs surface on your laptop, reproducibly
  (`TAKO_CHAOS_SEED` replays a failure exactly).
- Signing in locally: the dev bar's test users, or — when the app
  declares `AuthConfig{Password: true}` — an email and password against
  an account you made with `tako users create ana@example.com
  --password`. A dev sign-in creates a real local account, because in
  the cloud a session cannot exist without one.
- **Secrets are a command too.** `tako secrets` lists what the app
  declares and which have a value — so you find all three missing names
  at once instead of one boot failure at a time — and `tako secrets set
  <name>` writes into `.env`, keeping its comments and its order. It
  refuses to write into a `.env` that is not gitignored. A value is
  never printed and never taken from a flag; it comes from stdin or an
  echo-off prompt. `--project <id>` targets Secret Manager instead.
- **The accounts are a command, not a program you have to write.**
  `tako users list|show|create|grant|revoke|disable|enable|delete`
  works on `.tako/data` — the same accounts `tako dev` signs in — and
  on a deployed app's Identity Platform when you add `--project`. A
  grant reaches sessions that are already signed in: the role lands on
  their next request, nobody is signed out. One catch: `tako dev` holds
  `.tako/data` while it runs, so stop it first (the command says so if
  you forget).
- `tako.Environment() == tako.EnvLocal` is how your code knows it is
  here rather than deployed — for a seeded sign-in, a visible fixture,
  an extra log line. It is not a feature flag: branch a development
  **affordance** on it, never the product, or you have two products and
  test one. Details in
  [reference/service.md](../reference/service.md).

## Testing

`tako.NewTestApp(t)` boots the same in-process backends, fresh per
test: `app.Ctx(app.SignIn("alice"))` for identities,
`Endpoint.Call` to invoke handlers, `app.DrainQueues()` to settle
async work (jobs included), `app.AdvanceCron(Cleanup)` to fire a cron,
`app.Chaos()` for delivery chaos, and `app.Handler()` for the app's
real HTTP surface. `tako test` regenerates stale code, builds any
declared site, maps in the generated test harness, and runs `go test
./...` — an app's tests want `tako test` rather than `go test`,
because the overlay carrying that harness is what `go test` does not
apply.
