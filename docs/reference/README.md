# The feature reference

One page per feature, every page the same shape — enter anywhere,
leave with the whole story of that one thing:

- **Declare** — the package-level var and its options.
- **Use** — the handler-side calls.
- **Local** — what `tako dev` / `tako test` actually run.
- **Cloud** — what `tako deploy` creates, the IAM it derives, what
  it costs.
- **Test it** — the pattern the examples use.
- **Errors you'll meet** — the real texts, verbatim.
- **Not yet** — honest limits.

The [guide](../guide/README.md) teaches by example; this reference
answers by feature. The [README](../../README.md) is the five-minute
path.

## What is API, and what is not

**The root `tako` package is the API.** Every symbol in it is locked
(`testdata/sdk_lock.golden`, 148 entries) and reviewed; changing one is
its own task with its own written review, never a side effect. That is
the surface your application code imports, and the one this reference
documents.

**Everything under `pkg/…` is tako's internals** — the analyzer, the
code generator, the runtime, the provisioner, the backends. They are
importable because Go has no other way to let the CLI and the generated
services share them, not because they are for you. They may change in
any release.

Two consequences worth knowing:

- **Generated services import `pkg/runtime`.** The `.tako/gen/` files in
  your repository name about thirty of its symbols, so upgrading tako
  can make committed generated code stale. `tako test`, `tako dev` and
  `tako deploy` all regenerate before they do anything else, so the fix
  is automatic — but a bare `go build ./.tako/...` after an upgrade may
  fail until you run one of them. (`pkg/runtime` carries its own lock
  test, so such a change is deliberate and lands in the release notes.)
- **Example tests import `pkg/runtime` directly** to drive contracts
  whose surface really is the wire — multipart decoding, signed-URL
  rejection texts. That is a framework-testing technique, not a pattern
  to copy; [testing.md](testing.md) covers what your tests should use.

## HTTP

- [api.md](api.md) — `tako.API`: JSON endpoints, request binding
- [pages.md](pages.md) — `tako.Page`: server-rendered HTML, forms,
  CSRF, htmx fragments
- [site.md](site.md) — `tako.Site`: static serving, templates,
  frontend builds (`WithBuild`), SPA, redirects
- [service.md](service.md) — `tako.Service`: the app service declared —
  its name and its service-wide settings
- [domains.md](domains.md) — `ServiceConfig.Domain`: a custom domain with
  managed TLS on any service, site or API-only

## Data

- [entity.md](entity.md) — `tako.Entity`: get/put/query,
  transactions, derived indexes
- [cache.md](cache.md) — `tako.Cache`: per-instance TTL cache
- [observability.md](observability.md) — `tako.Secret`,
  `tako.Counter`, `tako.Histogram`, `tako logs`

## Async

- [queue.md](queue.md) — `tako.Queue` + `tako.Worker`: at-least-once
  work, retries, dead letters, chaos testing
- [topic.md](topic.md) — `tako.Topic` + `tako.Subscribe`: fan-out
  events
- [bucket.md](bucket.md) — `tako.Bucket`: blobs, signed URLs, upload
  events
- [cron.md](cron.md) — `tako.Cron` (+ `WithSingleflight`)
- [lock.md](lock.md) — `tako.Lock`: leases, `Do`/`TryDo`

## Compute

- [job.md](job.md) — `tako.Job` + `tako.Runner`: work that
  outlives requests, `WithParallel`
- [singleton.md](singleton.md) — `tako.Singleton`: exactly-one loops
- [calls.md](calls.md) — cross-service `Endpoint.Call`,
  `WithPrivate`, identity propagation
- [footprint.md](footprint.md) — `WithProfile` and `WithFootprint`:
  scaling, explicit compute

## Edges

- [auth.md](auth.md) — `tako.Auth`: providers, sessions, `WithAuth`,
  the dev bar
- [testing.md](testing.md) — `tako.NewTestApp`: the test surface, the
  fake clock, chaos mode
- [client-ts.md](client-ts.md) — `tako gen client ts`: the generated
  TypeScript client
- [cli.md](cli.md) — the command surface and the confirmation ladder
- [errors.md](errors.md) — every analysis rule tako can refuse with,
  and what each one protects
