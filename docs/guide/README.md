# The Tako guide

Tako is a Go framework over Google Cloud Run: you write pure business
logic — APIs, workers, websites — as ordinary Go packages, and the
`tako` CLI runs it locally and provisions it onto GCP. No Terraform,
no Dockerfiles, no YAML: the analyzer reads your declarations and
derives the infrastructure, the IAM, and the wiring.

The guide is the six examples, in the order the framework grows:

1. **[hello](01-hello.md)** — APIs, request binding, the dev loop,
   your first deploy.
2. **[shortlink](02-shortlink.md)** — entities, transactions, queues
   and workers, exactly-once effects under chaos.
3. **[gallery](03-gallery.md)** — buckets, signed URLs, event
   fan-out, dead letters, cron, cache/secret/metrics, locks.
4. **[journal](04-journal.md)** — a server-rendered website: pages,
   forms, CSRF, htmx fragments, sanitized markdown, Google sign-in.
5. **[shop](05-shop.md)** — many services from one repo: a generated
   TypeScript client, private services and typed calls, background
   jobs, an exactly-one singleton.
6. **[blog](06-blog.md)** — a Hugo-built static site with htmx
   islands: the generator owns the pages, tako runs the build and
   serves the dynamic fragments.

Two stories frame everything:

- **[The local story](local.md)** — what `tako dev` and `tako test`
  actually run (spoiler: your process, no emulators, no Docker).
- **[The cloud story](cloud.md)** — what `tako deploy` creates, the
  permissions model, the ONE manual setup step, and what `destroy`
  deliberately leaves behind.

Two pages answer the questions that cut across every feature:
**[testing](../reference/testing.md)** (how you write tests against an
app) and **[errors](../reference/errors.md)** (every refusal the
analyzer can make, and why it exists).

And the **[feature reference](../reference/README.md)** answers by
feature instead of by example: one page per feature, every page the
same shape (declare · use · local · cloud · test · errors · not yet).
