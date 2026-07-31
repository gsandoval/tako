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

Two references frame everything:

- **[The local story](local.md)** — what `tako dev` and `tako test`
  actually run (spoiler: your process, no emulators, no Docker).
- **[The cloud story](cloud.md)** — what `tako deploy` creates, the
  permissions model, the ONE manual setup step, and what `destroy`
  deliberately leaves behind.
