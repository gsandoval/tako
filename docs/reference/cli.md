# The tako CLI — the command surface and the confirmation ladder

A CLI has no Declare/Use, so this page bends the rubric: the surface
by side-effect tier (exactly as `tako` help groups it), the
confirmation ladder, flag resolution, errors, and honest limits.

## Local — nothing outside this directory is touched

```
tako new <name> [--kind api|site] [--module <path>] [--replace <dir>]
                         write a new app: go.mod, one handler, a
                         .gitignore — and for a site, the layout and page
tako dev [--chaos]       run the app locally with reload (--chaos: seeded
                         duplicate/reordered deliveries)
tako test [args]         analyze, refresh generated code and site builds,
                         then go test ./...
tako gen graph [--json | --mermaid]
tako gen client ts [--out <path>]
tako alerts [name] [--env prod]
                         what each declared alert compiles to — PromQL,
                         notification sentence, and the call it came from
tako docs [--port 8090] [--open]
                         the guide, the reference and the SDK's exported
                         signatures, served from this binary
tako build [--push <repo>]
```

`tako docs` serves the documentation that shipped inside the binary —
no checkout, no network, nothing fetched. Three sections: the
[guide](../guide/README.md), this [reference](README.md), and an **api**
page generated from the same lock file the API-freeze test compares
against, so it cannot quietly lose a symbol. The pages are as old as the
binary, which is the point: they describe the SDK you are compiling
against rather than whatever is on main.

Serving is all it does, so there is no subcommand and no `--serve`
flag — a flag that must always be passed is a flag that should not
exist. It listens on `127.0.0.1` only.

`tako new` writes the smallest app that runs, then resolves it and
generates, so `tako dev` works on the next line rather than the one
after. `--kind site` adds the pieces a site cannot start without — the
site declaration, `templates/layout.html`, a page — because their
absence surfaces as `the site has no templates/ directory`, a message
about a checkout wearing the costume of a broken app. The `.gitignore`
carries `.env` from the first commit, which is what `tako secrets set`
requires before it will write a value.

It refuses a directory that already exists, and removes what it wrote
if resolution fails: half a project is worse than none.

**Until tako is tagged and published, `--replace <dir>` is required** —
a binary built from a checkout has no version a `go.mod` can depend on,
and the refusal says so and prints the command with the flag in it.

`tako test` never runs against stale generated code — it regenerates
first and calls out drift (`regenerated: shop was stale`). It runs
declared site builds first too, for the same reason: the harness
mounts the declared site directory, and for a site with
`SiteConfig.Build` that directory is a build output nobody commits.
Bare
`tako gen` prints the gen namespace (one line per generator), not the
full usage.

`build --push` is **the one flag that crosses into the cloud**: without
it, `build` produces digest-pinned distroless images locally (no
Docker daemon); with it, they upload to `<repo>/<service>` on Artifact
Registry. Held as acceptable because the flag name carries it — and
help sits `build` at the boundary line with the consequence spelled
out.

## Your data — local unless you type a project

```
tako users list                       every account and its roles
tako users show   <email|id>
tako users create <email> [--name N] [--password] [--role R]...
tako users grant|revoke <email|id> <role>
tako users disable|enable <email|id>  sign-in refused / allowed, record kept
tako users delete <email|id>          permanent
```

A door onto `auth.Users()` — the same surface an app calls, so a grant
marks live sessions stale and a disable ends them, exactly as from Go.
Works on `.tako/data` (the accounts `tako dev` signs in) unless
`--project <id> [--env <env>]` sends it at a deployed app's Identity
Platform. A role the app does not declare is refused with the declared
set and a did-you-mean; deleting against a project asks you to type the
email, and deleting locally does not (`.tako/data` is the reset story).

`tako dev` holds `.tako/data` while it runs — stop it first. Against a
project you need `roles/identitytoolkit.admin`; the refusal prints the
grant.

```
tako secrets                          what this app declares, and which have a value
tako secrets set <name>               value read whole from stdin, or an
                                      echo-off prompt
tako secrets rm  <name>               delete it
```

The values behind `tako.Secret`. `set` writes `.env` locally (keeping
its comments and order, and refusing a `.env` that is not gitignored)
and adds a Secret Manager version with `--project`. **A value is never
printed** — there is no `get` and no `--show` — **and never taken from a
flag**, because a flag lands in shell history, in `ps`, and in CI logs.

A piped value is taken WHOLE, minus one trailing newline — a PEM key or
a service-account JSON is one value, not its first line. `.env` is
`name=value` per line with no quoting, so a multi-line value is refused
locally and named as a `--project` one, where Secret Manager stores the
bytes as they are.

`rm` against a project destroys the secret and every version,
permanently: the name is typed, and `--yes` does not apply. It says
whether the secret is still declared (its services will refuse to boot)
or an orphan (the safe case). Locally it just drops the line.

## Cloud, read-only

```
tako logs [service] [--since 15m]     recent logs for the deployed services
tako dlq [service]                    peek dead-lettered messages per async service
tako dlq <service> --replay <id>      put one dead letter back into delivery
tako dlq --local [carrier]            the same, against .tako/data
tako metrics <search> [--limit 20]    find a metric type to alert on
```

`tako metrics` is a substring search over the project's metric
descriptors — around eight thousand of them, which is why
[alerts.md](alerts.md) carries no list. It prints two things a raw
descriptor listing does not, and each of them is a failure that
otherwise costs a deploy to discover:

```
$ tako metrics container/startup --project my-project

✓ run.googleapis.com/container/startup_latencies
    DELTA DISTRIBUTION · on cloud_run_job, cloud_run_revision, cloud_run_worker_pool
    Distribution of time spent starting a new container instance in milliseconds.
    tako.ServicePercentile(svc, "run.googleapis.com/container/startup_latencies", 0.99)

✓ nameable — a policy can be created against it today
```

**The constructor**, because the metric's shape decides it: a
DISTRIBUTION is published only as `_bucket`/`_count`/`_sum`, so naming
one the way its own documentation writes it produces a policy Cloud
Monitoring refuses. **And whether the name resolves in your project
today** — a policy naming something Cloud Monitoring cannot see is
refused when it is created, and a service you have never used
contributes no names.

## Cloud, mutating

```
tako setup [--member <principal>] [--plan] [--yes]
                       enable the APIs and grant the roles THIS graph needs
tako datastore create --region <region>
                       the project's datastore-mode database (location is permanent)
tako deploy [--plan] [--canary <pct>] [--yes]
tako promote           send 100% of every service's traffic to latest
tako rollback          return traffic to the previous revision set
tako prune [--data]    delete orphans tako once managed
tako dlq [service] --ack   drain the printed dead-letter messages for good
tako dlq <service> --replay <id>|--all   re-deliver, then remove from the DLQ (a MOVE)
tako destroy           delete everything labeled for this app
tako destroy --plan    show what it would delete, delete nothing
```

`deploy --plan` stops after the classified plan; `--canary` holds new
revisions at a traffic percentage. `rollback` is instant for traffic;
infra shape rolls back via git revert + `tako deploy`.

**`tako setup` is the applying half of preflight.** Preflight has always
found every gap and printed its fix; setup grants them — the APIs and
the permission→role pairs *this graph* needs, and no more, so an app
with no queues asks for nothing about Pub/Sub. It is idempotent and
meant to be re-run as the app grows a resource. `--member` sets up
somebody else (a CI service account) instead of you; `--plan` prints
what it would grant and stops.

The one step it cannot do for you is the first one: granting yourself
the permission to grant. When setup lacks that, it prints the `gcloud`
command for whoever owns the project to run — handing the job over
rather than failing at it.

**`tako datastore create` is separate because its choice is permanent.**
A datastore-mode database cannot be moved once it exists, so `--region`
is required here and is neither defaulted nor inherited from
`TAKO_REGION`, unlike every other region in this CLI — those default
because deploying again can change them. Pick the region your services
run in.

```
$ tako datastore create
tako datastore create needs --region, and will not pick one for you
      a Datastore database's location is permanent: it cannot be moved,
      and every other --region in this CLI defaults because it can be
      changed by deploying again. Pick the region your services run in.
```

`destroy` deliberately leaves the database behind ([the cloud
story](../guide/cloud.md) lists everything in that category), so this is
a once-per-project command in practice.

## The confirmation ladder

One rule, stated in `tako` help: **friction scales with
irreversibility** (from [cli-review-1](../cli-review-1.md)).

1. **Reversible or derivable** — no ceremony. `rollback` undoes
   `promote`; a deploy re-derives what `prune` removed (stateless
   orphans: compute, messaging, indexes).
2. **Risky but recoverable** — `deploy` prompts y/N on replaces and
   rename suspects. `--yes` exists ONLY to pre-answer this prompt in
   CI; it never touches data confirmations.
3. **Data, gone forever** — typed confirmations, never bypassable.
   `destroy` types the app name, always (you must know what you are
   killing). `prune --data` prints counts, then types a literal word
   (the counts are the object, the word is the seal). `dlq --ack` is
   an explicit single-purpose flag whose name says it. `--yes`
   deliberately does not reach this tier — data destruction is never
   automatic.

The tier-3 mechanics differ on purpose: unifying destroy's typed name
with prune's counts-then-word would weaken the stronger ceremony.

`destroy`'s preview also names the one thing it deletes that a redeploy
cannot bring back: Cloud Tasks blocks reusing a deleted queue's name for
7 days, so those names are listed ahead of the prompt rather than
discovered a week later on the redeploy that fails. See
[queue.md](queue.md#errors-youll-meet).

## Project, region, env

Every cloud command resolves its target identically, through one
parser:

- `--project` → else `GOOGLE_CLOUD_PROJECT` → else gcloud config
- `--region` → else `TAKO_REGION` → else `us-central1`
- `--env` → default `prod` (preview environments like `--env pr-42`
  touch only their own slice)

`--flag value` and `--flag=value` both work, everywhere.

**`tako users` and `tako secrets` are the exception, deliberately.**
They read `--project` and `--env` from the command line and nowhere
else — no `GOOGLE_CLOUD_PROJECT`, no gcloud config.

The rule is which way a mistake goes. Commands that **converge
infrastructure** (`setup`, `deploy`, `prune`) may infer a project,
because re-running fixes a wrong guess. Commands that **write your
data** — a person, a secret — may not: nothing re-derives those, and an
inherited project is how a dev fixture or a test key lands in
production. `--env` without `--project` is refused rather than silently
meaning "local".

## Errors you'll meet

- Unknown flags name their command's real surface, e.g.
  `unknown flag "--x" (tako gen graph accepts --json or --mermaid)`,
  `unknown flag "--x" (tako gen client ts accepts --out <path>)`,
  `unknown flag "--x" (tako docs accepts --port <n> and --open)`.
- `port 8090 is already in use — pass --port to pick another`
- `tako gen client generates TypeScript today — run: tako gen client ts`
- Bare `tako` (or an unknown command) prints the full usage — which
  ends with the confirmation ladder and the flag-resolution rule.
- Deploy preflight failures print copy-pasteable remediation (enable
  command, grant command, or console step) — see
  [the cloud guide](../guide/cloud.md).

## Not yet

- `destroy --plan` observes and prints the same block destroy prints —
  including the `DATA — deleted with the app, gone forever` list — then
  exits before the prompt. It is the safe way to inspect a teardown, and
  the only one that does not involve starting it.
- No `--yes` on `destroy` / `prune --data` — tier 3 is non-bypassable
  by design, not a missing feature.
- No per-command `--help`: the single usage screen still fits;
  revisit if the surface grows past one screen.
- `tako docs` has no search. Find-in-page over one reference page is
  enough for twenty-four of them, and a search index is a real feature
  that should wait for a demand. It also does not publish: there is no
  `--build` that emits HTML for hosting, and nothing about it touches an
  eventual public docs site.
- `tako gen client` speaks TypeScript only.
