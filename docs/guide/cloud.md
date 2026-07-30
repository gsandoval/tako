# The cloud story

`tako deploy` reconciles your code onto GCP with no state file: it
reads the live project, labels everything it creates, and converges.

## What one deploy does

```
analyzing → site builds → codegen → preflight → plan → build/push → apply → URLs
```

- **Preflight** probes exactly the APIs and permissions your graph
  needs and prints copy-pasteable remediation when something is
  missing (enable command, grant command, or console step).
- **The plan** classifies every change before touching anything:
  `+ create`, `~ update` (in place), `! replace` (delete+recreate —
  prompts), `? orphaned (kept)` (left your code — never auto-deleted),
  and DRIFT warnings for things edited outside tako.
- **Apply** is additive-first, under a deploy lease, so two deploys
  can't interleave and old+new revisions stay correct mid-rollout.

## What gets created, and who may do what

Package = service. Each service runs as its OWN service account, and
every IAM grant is DERIVED from your code: the analyzer sees which
handlers touch which resources (and which services call which), and
grants exactly those edges — bucket access per bucket, publisher per
topic, `run.invoker` per callee, lease access when you use locks. The
deploy ends with a security summary tracing every grant to the code
that demanded it, plus warnings for anything internet-open.

Removing code removes its grants on the next deploy: revocation is
convergence, not cleanup you remember to do.

## The confirmation ladder

Friction scales with irreversibility (also in `tako --help`):

1. Reversible/derivable — no ceremony (promote/rollback; pruning
   orphaned compute).
2. Risky but recoverable — deploy prompts y/N on replaces and rename
   suspects; `--yes` pre-answers that prompt for CI, and nothing else.
3. Data, gone forever — typed confirmations (`destroy`,
   `prune --data`), never bypassable.

## One-time project setup

Most setup is automated: APIs probed by preflight, Identity Platform
initialized, the email provider enabled when declared, sign-in
domains registered per deploy.

**The one genuinely manual step** — only when your app declares
`tako.WithGoogle()` — is enabling the Google provider, because Google
has no public API for creating external OAuth clients:

> Firebase console → your project → Authentication → Sign-in method →
> Google → Enable (pick a support email) → Save.

That single toggle auto-provisions the OAuth client. The deploy
BLOCKS with this exact instruction (and a hand-built GCP-console
alternative for org-restricted accounts) until it's done, and rejects
placeholder client IDs before your users ever see `invalid_client`.
Secrets are the other human input: `tako.Secret("name")` must exist
in Secret Manager; preflight prints the exact command.

## What destroy deletes — and deliberately leaves

`tako destroy` (typed app name) deletes everything it created for the
app, data included, and unregisters its sign-in domains. It
deliberately leaves project-level, shared, or human-owned things:
Identity Platform initialization, providers and their OAuth client,
user accounts, Secret Manager secrets, enabled APIs, the Datastore
database, and the browser API key. Preview environments
(`--env pr-42`) destroy only their own slice and never shared
machinery.
