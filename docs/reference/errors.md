# Analysis errors, by rule

Every refusal `tako` makes at analysis time carries a rule ID:

```
app.go:11:5: tako.API route must be a string constant — routes are read at analysis time, not run time; got variable "route" (tako/const-route)
```

`path:line:col: message (tako/rule-id)` — the same shape as a compiler
diagnostic, and for the same reason: **these are your app's build
errors.** `tako dev`, `tako test`, `tako build` and `tako deploy` all
refuse to proceed while any of them stands. The text is golden-tested,
so what you read below is what you will see.

This page is the index: what each rule is protecting, and where the
feature page explains it in full. Most texts below are copied verbatim
from the golden corpus in `pkg/analyzer/testdata/errors/`; a few rules
have no golden fixture, and theirs are shown with illustrative names
filled into the real message template.

**Why a rule exists at all.** Tako derives services, IAM and wiring
from declarations by *reading* them — never by running them. A rule
fires where reading would have to become guessing (a route built at run
time), where one service would have to be two things at once (a package
claiming two footprints), or where the cloud would reject the shape
later and more expensively (a cron schedule Cloud Scheduler cannot
express). None of them are style.

## Declarations must be readable

Names and routes are read from source, so they have to be *in* the
source.

| Rule | Fires when | Page |
|---|---|---|
| `package-level` | a handler is declared inside a function | [api.md](api.md) |
| `const-route` | a route is a variable or a call | [api.md](api.md) |
| `const-name` | a resource name is a variable | [entity.md](entity.md) |
| `const-query-field` | a query field name is a variable | [entity.md](entity.md) |
| `unexported-req` | a request type is unexported | [api.md](api.md) |

```
tako.API must initialize a package-level var — handlers declared inside functions are invisible to analysis
tako.API route must be a string constant — routes are read at analysis time, not run time; got variable "route"
tako.Entity name must be a string constant — names are read at analysis time, not run time; got variable "n"
query field must be a string constant — indexes are derived at analysis time; got variable "f"
request type greetReq must be exported — generated services construct it outside the package
```

## Request binding

The Req struct and the route are one contract; these rules keep the two
from disagreeing.

| Rule | Fires when | Page |
|---|---|---|
| `route-params` | a `{param}` and a `path:` tag don't match up | [api.md](api.md) |
| `bind-type` | a tagged field is a type the binder can't parse | [api.md](api.md) |
| `body-on-get` | a GET's Req has untagged fields (a body it can't receive) | [api.md](api.md) |
| `blob-binding` | a `tako.Blob` field is on a GET, untagged, or mixed with untagged fields | [bucket.md](bucket.md) |
| `page-binding` | a Page's Req has an untagged field | [pages.md](pages.md) |

```
route "GET /greet/{name}" has path parameter {name} but GreetReq has no field tagged path:"name"
GreetReq field Nick is tagged path:"nick" but route "GET /greet/{name}" has no {nick} parameter
field When: query:-tagged fields must be string, []string, bool, int, int64, or float64; got time.Time
field Tag: path:-tagged fields cannot be []string — a route segment is one value; a field that repeats binds with query: or form:
GET route "GET /search" cannot bind a JSON body, but SearchReq field Extra has no path:/query: tag
tako.Blob field Img needs a form:"…" tag — blobs bind from multipart form fields
GET route "GET /b" cannot receive an upload — requests with tako.Blob fields need a non-GET route
CReq mixes a tako.Blob field with untagged field Extra — when a blob is present every other field needs a path:/query:/form: tag
PageReq field Title has no path:/query:/form: tag — pages receive browser forms, not JSON bodies
```

## Handlers bind to the thing they serve

A worker serves one queue, a job has one body, and the binding is the
edge the analyzer follows to derive IAM.

| Rule | Fires when | Page |
|---|---|---|
| `worker-binding` | `tako.Worker` binds something that isn't a package-level queue | [queue.md](queue.md) |
| `subscribe-binding` | `tako.Subscribe` binds something that isn't a topic or bucket event | [topic.md](topic.md) |
| `job-binding` | `tako.Runner` binds no package-level job, or a job has no body | [job.md](job.md) |
| `one-worker` | two workers claim one queue | [queue.md](queue.md) |
| `one-job-handler` | two handlers claim one job | [job.md](job.md) |
| `raw-call` | a cross-service call goes to the function, not the Endpoint | [calls.md](calls.md) |

```
tako.Worker must bind a queue declared as a package-level var in this module
tako.Subscribe must bind a topic or bucket event declared as a package-level var in this module
tako.Runner must bind a job declared as a package-level var in this module
job "reindex" has no body — bind one with tako.Runner
queue "jobs" already has worker First (app.go:9) — a queue has exactly one worker; use a Topic for fan-out
job "reindex" already has handler Reindex (app.go:9) — a job has exactly one body
ChargeFn is a handler function — cross-service calls go through the Endpoint var (billing.Charge.Call(ctx, req)) so the RPC boundary stays visible
```

## One package is one service

Package = service is the partitioning rule, so anything a package can
only have one of is enforced at the package.

| Rule | Fires when | Page |
|---|---|---|
| `one-service` | two `tako.Service` declarations in a package | [service.md](service.md) |
| `one-site` | two `tako.Site` declarations in a package | [site.md](site.md) |
| `mixed-ingress` | public and `Route{Private: true}` routes share a package | [calls.md](calls.md) |

```
a package declares one service — oneservice.api is already declared; remove oneservice.admin or merge the two
a package serves one site — onesite.site is already declared; remove onesite.admin or merge the two
service mixedingress mixes private and public routes — Cloud Run enforces Route.Private at the service level, so private endpoints need their own package
```

## Configuration applies where it means something

Every constructor takes at most one config struct — `tako.Route`,
`tako.Compute`, `tako.CronConfig` and friends. A setting can only be
written where it has a field, so "this option does not belong here" is
a **compile** error now, not an analysis one, and the two rules that
used to say it (`invalid-option`, `service-option`) are gone.

What the analyzer still owns is everything a struct field cannot check:
that the value is written as a literal, and that it is in range.

| Rule | Fires when | Page |
|---|---|---|
| `config-literal` | a config is held in a variable, or a field is computed | each feature page |
| `one-config` | two config values are passed where one is read | each feature page |
| `retries` | `RunnerConfig.Attempts` is outside 0–10, or not constant | [job.md](job.md) |
| `timezone` | `CronConfig.Timezone` is not a constant IANA zone, or unknown | [cron.md](cron.md) |
| `deadline` | `CronConfig.Deadline` is outside 15s–30m | [cron.md](cron.md) |
| `cron-schedule` | a schedule can't map onto unix-cron | [cron.md](cron.md) |
| `delivery` | a `Delivery` field is out of range or wrong for the carrier | [queue.md](queue.md) |
| `auth-provider` | `tako.Auth` declares no provider | [auth.md](auth.md) |
| `max-age` | `Route.MaxAge` is not a constant between 1s and a year | [pages.md](pages.md) |
| `cache-signin` | `Route.MaxAge` on a route that requires sign-in | [pages.md](pages.md) |
| `one-status` | a package declares two status pages | [site.md](site.md) |
| `unexported-status` | a status page var is not exported | [site.md](site.md) |
| `site-dir` | a site's directory doesn't exist and nothing builds it | [site.md](site.md) |

```
tako.Compute must be written as a literal with constant fields — configuration is read at analysis time, not run time; got variable "shared"
tako.CronConfig.Timezone must be a string constant — it is read at analysis time, not run time; got a call expression
tako.Compute takes one configuration value, got 2 — merge them into a single literal
tako.Queue takes one tako.Delivery, got 2 — merge them into a single literal
tako.RunnerConfig.Attempts takes a constant 0–10 attempts after the first — a task that fails 10 times is not going to pass on the 11th, and jobs have no dead letter to catch it
tako.CronConfig.Timezone does not know the zone "Mars/Olympus" — use an IANA name like "America/Mexico_City" or "Europe/Madrid"
tako.CronConfig.Deadline takes a constant 15s–30m — Cloud Scheduler's range for an HTTP target; a cron that needs longer wants a tako.Job
tako.Cron schedule "0 25 * * *": field 2 (hour): value "25" out of range 0–23
"every 7m0s" cannot map onto Cloud Scheduler's unix-cron grammar — use minutes dividing 60, hours dividing 24, exactly 24h, or write a cron expression
delivery MaxAttempts must be 5–100 — Pub/Sub's dead-letter policy accepts no other range, and tako will not quietly clamp yours; got 2
delivery MinBackoff (5m0s) is longer than MaxBackoff (1m0s) — backoff grows, it does not shrink
delivery RateLimit is a queue setting — a topic fans out to subscribers with independent fates, so there is no single consumer to pace; cap the queue each subscriber feeds instead
tako.Auth needs at least one provider (AuthConfig{Google: true} or AuthConfig{Password: true})
tako.Site directory ./web does not exist (add it, or set SiteConfig.Build if a generator creates it)
```

**Some mistakes now belong to the compiler.** Putting a service-wide
setting on one route used to be `tako/service-option`; today
`tako.Route` has no `Footprint` field, so Go answers first:

```
unknown field Footprint in struct literal of type tako.Route
```

That is caught earlier and says less — it does not point you at
`tako.Service` the way the analyzer's message did. The trade was
deliberate: a whole class of silently-dropped configuration disappears
with it (see [api-review-3](../api-review-3.md)).

```
Dashboard sets MaxAge on a route that requires sign-in — a shared cache would serve one signed-in visitor's page to another; drop MaxAge, or drop SignIn
status page status must be exported — the generated service registers it by name from outside the package; rename it Status
```

`cache-signin` is the one worth reading twice. Nothing about that
failure is visible from the app: the tests pass, the page renders, and
one day a reader is served someone else's page by a cache neither of
them can see. It is refused rather than documented as a caution.

## Sizing has to resolve to one answer

A service scales one way, so every handler in a package must agree —
and the resolved numbers have to be ones Cloud Run will accept. See
[footprint.md](footprint.md) for all of these.

| Rule | Fires when |
|---|---|
| `footprint` | a resolved footprint is illegal, or sets fields its kind has no use for |
| `traffic` | a `Traffic` field is out of range, empty, or wrong for the kind |
| `singleton-max` | a singleton pins `MaxInstances` |
| `mixed-footprint` | two handlers in a package pin different footprints |
| `mixed-profile` | two handlers in a package claim different profiles |
| `mixed-traffic` | two handlers in a package pin different traffic |

```
service footprintresolve-bigcpu resolves to 8 vCPUs with 512Mi memory — 8 vCPUs need at least 4Gi; pin Memory too
service footprintresolve-bigmem resolves to 8Gi memory with 1 vCPU — over 4Gi needs at least 2 vCPUs; pin CPU too
service footprintresolve-bigmin resolves to MinInstances 30 over MaxInstances 20 (profile default) — pin MaxInstances too
a job footprint sets CPU and Memory only — executions are tasks, not serving instances, so there is no concurrency, no scaling, no idle CPU to throttle and no cold start to boost; RunnerConfig.Parallel sets the fan-out and RunnerConfig.Attempts the attempts
a singleton footprint sets CPU, Memory and StartupBoost only — min=1 and concurrency=1 are what keep the loop's host alive and alone
a singleton footprint cannot set MaxInstances — the lease is what makes a singleton exactly-one, so a max above 1 only buys instances that pay to lose the race; CPU, Memory and StartupBoost are yours
traffic Timeout must be at least one second; got 500ms
traffic SessionAffinity must be tako.ToggleInherit, tako.ToggleOn, or tako.ToggleOff
traffic sets nothing — drop the Traffic field or pin at least one setting
service mixedfootprint cannot carry both footprint (cpu 2) from Sweep and footprint (cpu 4, memory 2Gi) from Purge — one service scales one way; split the packages or align the footprints
service X cannot be both latency (Web) and batch (Sweep) — one service scales one way; split the packages or align the profiles
service X cannot carry both traffic (timeout 45s) from Web and traffic (http2) from Api — one service answers one way; split the packages or align the settings
```

## Entities

| Rule | Fires when | Page |
|---|---|---|
| `entity-id` | the id field is missing, duplicated, or not a string | [entity.md](entity.md) |
| `unindexed-query` | a query filters or orders on an untagged field | [entity.md](entity.md) |
| `query-shapes` | one function assembles a query from too many conditional filters | [entity.md](entity.md) |
| `query-escapes` | a query is built somewhere tako cannot follow it | [entity.md](entity.md) |

```
entity type Link must have exactly one field tagged entity:"id"
entity id field Slug must be a string; got int
query filters on Link.URL, but URL is not indexed — tag it entity:"index" to make it queryable
query orders by Link.Hits, but Hits is not indexed — tag it entity:"index" to make it orderable
search assembles one query from 6 conditional filters — that is 64 composite indexes for a single function, and Datastore allows 200 in a project; split it into separate queries whose filters are unconditional
```

`unindexed-query` is the one rule that pays off entirely in the cloud:
the local backend would happily scan, and Datastore would fail the
query at run time in production. Indexes are derived from the tags, so
the tag is both the fix and the provisioning input.

`query-shapes` is the same bargain at a different scale. A query built
across branches has one shape per combination of branches taken, and
each needs its own index; past four conditional filters that powerset
stops fitting in a project. Refusing at analysis time is the whole
point — the alternative is discovering it as a 500 from Datastore in
production, which is exactly how this rule came to exist.

`query-escapes` is the boundary of what "derived from the call sites"
can mean. A helper that takes and returns an `*EntityQuery[T]` is
followed — its filters belong to whoever calls it — but a query stored
in a struct field, sent on a channel, or handed to a function value
cannot be traced to the code that runs it:

```
this query is stored in a struct field, so the composite indexes it needs cannot be derived — assemble it in one function, or in helpers that take and return *tako.EntityQuery[Article]
```

Refusing beats guessing here for the same reason as above: guessing low
is a 500 in production, and nothing local would ever show it.

## Domains

| Rule | Fires when | Page |
|---|---|---|
| `domain` | the domain isn't a bare hostname | [domains.md](domains.md) |
| `domain-unreachable` | the package holding the domain serves no public traffic | [domains.md](domains.md) |

```
tako.ServiceConfig.Domain needs a bare domain name — no scheme, port, path, or trailing dot; got "https://blog.example.com/"
this package declares the domain "blog.example.com", but nothing here serves public traffic — queue workers, topic subscribers, and jobs are reached over authenticated push or not at all, so the name would answer nothing; declare an API, Page or Site route here, or move the domain to the package that has one
```

## Errors that are not analysis errors

Four other kinds of message look similar and are not this:

- **Warnings** — printed by `tako dev` and `tako deploy`, marked `⚠`,
  never failing anything. They wear a rule id and a source position
  like an error because the fix is at the same place, but they describe
  a cost or a surprise rather than something with no correct meaning.
  `tako/site-shadow` is one: a declared route matching URLs the site
  has files at is legitimate — an app may own that space deliberately —
  so tako says what it will do and serves it.

  ```
  app.go:73:2: ⚠ GET /{year}/{slug} shadows 3 files the site serves — /about/index.html, /assets/htmx.min.e209dda5.js, /assets/site.9f86d081.css reach Perma instead of the site, and answer 404; move them deeper than the route's 2 segments, or narrow the pattern (tako/site-shadow)
  ```

  This one is worth reading closely when you see it. A shadowed
  stylesheet is not a failure anywhere: the route answers, honestly,
  404 — and the only symptom is an unstyled site nobody notices until
  it is deployed.


- **Boot refusals** — an unset secret, a missing site directory at run
  time. These stop a service from serving rather than from building;
  the feature pages list them under "Errors you'll meet".
- **Deploy preflight** — a disabled API, a missing permission, an
  unverified domain. These carry a copy-pasteable remediation
  (`gcloud services enable …`) and are described in
  [the cloud story](../guide/cloud.md).
- **Panics from unwired backends** — `tako: no entity backend wired —
  run via tako dev, a generated service, or tako.NewTestApp`. You get
  these calling a resource from a bare `go test` with no app booted;
  [testing.md](testing.md) explains what boots what.

## Not yet

- No `//tako:ignore` escape hatch, and none planned: every rule refuses
  something that has no correct meaning downstream, so suppressing one
  would move the failure to deploy or to run time.
- Rules are not configurable — no per-project opt-outs, and no severity
  to turn down: a rule refuses, a warning informs, and which one a
  given id is was decided when it was written.
- Analysis stops at the first *load* failure (the module has to compile
  before rules can run), but rule violations are collected and reported
  together, in source order.
