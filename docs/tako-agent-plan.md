# Tako — Agent Execution Plan

Companion to `tako-tech-spec.md` (design) and `tako-implementation-plan.md`
(milestones). This document is the unit of daily work: it decomposes the
milestones into **vertical feature slices**, each built slowly, validated in
three tiers, and gated on explicit human sign-off. Agents work exactly one
task at a time, in order.

## How this plan works — read every session

**Vertical slices, not layers.** Each feature cuts through every layer it
needs — analyzer rules, codegen, runtime, local backend, tests, example code,
validation script. The framework is always in a runnable, demoable state; no
"big bang" integration ever occurs. A slice that feels too large is split into
smaller slices, never stretched across sessions with uncommitted state.

**Three validation tiers, every feature, in order:**

1. **Automated.** Unit tests, analyzer goldens, conformance(local), and all
   example test suites — green in CI before a task can end. Written before or
   with the implementation, never after the gate.
2. **Scripted manual.** Every feature ships `validate/<feature>.md`: exact
   commands to run and what a human should observe (output, timing, feel).
   The agent must execute the script itself end-to-end and paste the real
   transcript into the task log before requesting the gate.
3. **Human gate.** A feature is DONE only when the human runs the validation
   script and records a verdict in `docs/validation-log.md` (feature, date,
   pass/notes/requested changes). **Agents stop at gates**: end the session
   with a gate request; never begin the next feature without a log entry for
   the previous one. If the verdict requests changes, those changes are the
   next task.

**Gate scope grows over time.** In Stage 0–1 the human validates **local
only** (`tako dev`, tests, examples). From Stage 2 onward every gate is the
**full cycle**: automated local suite → `tako test` → deploy to the dedicated
test GCP project → scripted cloud validation → `tako destroy` → confirm the
project is clean.

**DX-first: ergonomics are approved before implementation.** The first task of
every feature is a **DX proposal**: the exact user-facing code snippet(s), the
expected CLI transcript, and any new error-message texts, written into the
task log. The human approves or amends the ergonomics *before* implementation
begins, and the shipped surface must match the approved proposal (deviations
require re-approval). This is how "right practices and ergonomics for the
framework's user" stays the driving constraint rather than an afterthought.
The six interface rules (spec Part 1) are the review checklist for every
proposal.

**Session protocol.** Read `CLAUDE.md` → read your task here → if the feature
has no approved DX proposal, write one and stop → otherwise: tests first →
implement → run tiers 1 and 2 yourself → commit per task (task-ID prefix) →
append to `docs/task-log.md` (what shipped, transcript, discovered work — do
not do discovered work) → request the gate if the feature is complete.

---

## Stage 0 — Walking skeleton

*Goal: the thinnest possible end-to-end path, so every later feature extends
working machinery instead of integrating dead layers. Human validates local
only.*

**S0.1 — Repo + normative SDK stubs.**
Spec: Part 1, §2 signatures. Produce: full repo layout; `pkg/sdk` with every
v0 exported type/constructor/method as compilable stubs and doc comments;
`sdk_lock_test.go` snapshotting the exported symbol set; `AppGraph` types.
Done: `go build ./...`, `go vet ./...` green. Not in scope: any behavior.
*(No gate — S0.2 completes the skeleton.)*

**S0.2 — Thin vertical: one endpoint, `tako dev`, hello.**
Spec: §2.8 (API only), §4.1–§4.2 (minimum), §4.7. Produce: analyzer that
extracts `tako.API` handler vars (constant-route enforcement only); codegen
for one service with route registration and JSON encoding (empty-Req
endpoints only); devserver watch→analyze→generate→run; `examples/hello`.
Done: golden for hello's AppGraph; hello's test hits the endpoint.
**Gate G0 (local):** `validate/skeleton.md` — human runs `tako dev`, curls
hello, edits the handler text, observes sub-second reload, reviews the error
message for a non-constant route. This gate approves the *dev-loop feel* —
the product's core sensation — before anything is built on it.

## Stage 1 — Local features, one at a time

*Human validates local only. Each feature follows the same internal task
shape: (a) DX proposal → approval; (b) tests + implementation across layers;
(c) example update; (d) validation script + agent-run transcript; (e) gate.
Those sub-tasks are implied and not restated below.*

**F1 — API endpoints, complete.**
Req-struct binding (`path:`/`query:`/`form:`/JSON body), error helpers
(`Errorf`, `NotFound`, `IsNotFound`), structured request logs. Extends hello
with a parameterized route. Analyzer: route↔`path:` tag matching, option
validity, package-level enforcement; compile-error fixture proving payload
shape is the compiler's job. Gate F1: human exercises good and bad requests,
reviews every new analyzer/HTTP error text.

**F2 — Entity + example shortlink.**
`Get/Put/Delete/Exists`, `Query` builder, `Update`, `Tx`; bbolt local
backend; conformance suite skeleton created here with Entity as its first
citizen; index derivation with unindexed-query analysis error;
`examples/shortlink`. Gate F2: human plays with shortlink end-to-end, kills
and restarts `tako dev` to see persistence, triggers the unindexed-query
error and judges its message.

**F3 — Bucket + Blob.**
Keyed verbs, `List`, streaming Blob through endpoints; fs local backend;
conformance grows. Gate F3: upload/download via curl, inspect `.tako/data/`.

**F4 — Queue + Worker.**
`Publish`, `tako.Worker` binding (compile-checked payload), ack/retry/
`Permanent` mapping, dead-letter surfacing in dev output; channel local
backend; conformance grows. Gate F4: human watches a failing worker retry
then dead-letter in the dev console.

**F5 — Topic + Subscribe + bucket events.**
Fan-out semantics (per-handler independence), `bucket.Uploaded`; duplicate-
worker vs. multi-subscriber analysis rules. Gate F5: two subscribers, one
event; human verifies independent retries.

**F6 — Signed URLs (local).**
`SignedPut/SignedGet` with size/content-type constraints via local endpoints.
Gate F6: human uploads oversized and wrong-type files, reviews rejections.

**F7 — Testing as a feature.**
`NewTestApp`, `SignIn`, `DrainQueues`, `AdvanceCron`, chaos mode
(`app.Chaos()`, `tako dev --chaos`, seeded/reproducible); `tako test` with
generation-freshness check; shortlink suite rewritten user-style. Gate F7:
human seeds a deliberately non-idempotent worker, watches chaos catch it with
a printed reproduction seed — validating that the *user's* testing story
actually protects them.

**F8 — Secret, Cache, metrics, WithDelay (local).**
Env-file Secret; TTL Cache + `GetOrFill`; Counter/Histogram logged; timer
delays with fake-clock tests. Gate F8: quick script pass.

**F9 — Gallery, complete (integration feature).**
`examples/gallery` exactly per spec Part 3, full test suite, chaos coverage
on `makeThumb`. Gate F9: human runs the full demo script — signed upload,
event → queue → worker, fan-out, queries, cron — and this gate doubles as the
first review of the *whole* DX in one sitting.

**F10 — API freeze review.**
Human-led: every exported symbol against the six rules; verdicts recorded in
`docs/api-review-1.md`; `sdk_lock_test.go` snapshot regenerated and frozen.
Gate F10 is the human's explicit written approval to lock the pre-cloud API.
*(Last cheap moment to rename anything.)*

## Stage 2 — Cloud, feature by feature

*From here, every gate is the full cycle: local suite green → `tako test` →
deploy to the dedicated test project → scripted cloud validation → `tako
destroy` → clean-project check. First tasks per feature stay small; cloud
behavior always lands behind the already-green conformance contract.*

**C1 — Build + push.** ko-style static binary → distroless → Artifact
Registry by digest. Gate C1 (partial cycle, no deploy yet): human verifies
the pushed image runs locally via `docker run` equivalent and inspects the
digest pinning.

**C2 — Minimal reconciler + hello deploys.** Service accounts, Cloud Run
service, spec-hash annotations, deploy lease, `tako deploy --plan`, `logs`,
`destroy`. **Gate C2 — the first full cycle**: human runs the entire loop
from clean project to live URL to destroyed project, timing it (target
< ~2 min to URL), and reviews the `--plan` output line by line.

**C3 — gcp Bucket + Entity; shortlink graduates.** Conformance(gcp) green is
the task's internal bar; reconciler grows buckets, indexes, derived IAM.
Gate C3: full cycle on shortlink + human reviews the printed IAM summary
against expectations.

**C4 — Pub/Sub push + dead-letter; gallery async graduates.** OIDC push
verification, per-handler subscriptions, retry/backoff config. Gate C4: full
cycle; human kills the worker mid-job in the cloud and observes redelivery
then dead-letter.

**C5 — GCS notifications + signBlob signed URLs + Cloud Tasks selection;
gallery fully graduates.** Gate C5: full cycle running the same demo script
as F9 against the cloud deployment — local and cloud validated with one
script from here on.

**C6 — Lifecycle features.** Change-class plan, prune/`--data`, rename,
rollback, canary/promote, `--env` previews, drift warnings. Gate C6: human
walks the scripted lifecycle (deploy → drift-inject → warn → canary →
promote → rollback → prune → destroy).

## Stage 3 — Web, auth, services (decomposed 2026-07-25, Stage 2 closed)

*Human gates are BATCHED at semantic milestones (user decision,
2026-07-25): W2 gates W1+W2 ("the server-rendered website"), W6 gates
W3–W6 ("journal complete"), W11 gates W7–W11 ("shop complete"). DX
proposals still stop per slice; agent-side scripted + live validation
still runs per slice. Every milestone gate is full-cycle: local suite →
`tako test` → deploy to the test project → scripted validation →
destroy. The SDK surface froze at F10 —
Site/Auth/Job/Lock exist as phase-pointer stubs, so Stage 3 implements
BEHIND the frozen API; new exported symbols reopen api-review-1.md as
their own task. `examples/journal` is grown feature by feature (the way
shortlink and gallery grew through F2–F9) and gated whole at W6;
`examples/shop` likewise through W7–W10, gated at W11.*

**W1 — Site: embedded static, caching headers, WithBuild/WithDevProxy.**
`tako.Site` serves a directory (or a generator's output via `WithBuild`,
re-run on change in dev; `WithDevProxy` for HMR dev servers), embedded
into the service binary at build time. Fingerprinted assets immutable,
HTML no-cache, ETags, security headers (CSP report-only). `SPA()`,
`WithRedirect`. journal is born: a static shell. Gate W1: full cycle —
the same site byte-identical local and cloud, headers judged with curl,
plus the Hugo `WithBuild` variant demoed.

**W2 — Pages, forms, CSRF.** `tako.Page` SSR handlers (`site.Render`,
layouts), tagged-Req form binding, automatic CSRF on unsafe page routes
(template function + meta tag), `tako.Redirect` POST-redirect-GET.
journal gains authored-post pages. Gate W2: full cycle; CSRF rejection
and form flows exercised by script.

**W3 — Fragments + htmx.** `site.Fragment` (no layout), `ctx.IsHTMX`,
dual-mode Redirect (302 vs HX-Redirect), CSRF wired for hx-headers.
journal becomes fragment-updating. Gate W3: full cycle; one route serving
both worlds, judged in the browser and with curl.

**W4 — Markdown, sanitized by default.** goldmark + bluemonday strict;
`{{ markdown }}` + `site.Markdown`; `WithTrustedHTML` the explicit
opt-out. journal renders authored markdown from Entity. Gate W4: full
cycle; an XSS payload in a post is inert by default and live under the
opt-out (judged both ways).

**W5 — Auth: Identity Platform, `_session`, dev users.** `tako.Auth`,
`WithAuth` enforcement, `ctx.User`/`MaybeUser` against real identities,
dev sign-in bar locally, session cookies. Gate W5: full cycle; sign-in
on the deployed journal, WithAuth 401s, ownership enforced.

**W6 — examples/journal, complete (gate).** The htmx + dynamic-markdown
showcase whole: authored posts in Entity, fragment UI, auth, no SPA.
Gate W6: the journal demo script runs local AND cloud (one script, F9
style) — this is Stage 3's mid-point DX review.

**W7 — `tako gen client ts`.** Typed TS client for api endpoints from Go
signatures, session auth pre-wired. shop is born (frontend consumer).
Gate W7: generated client typechecks and drives a deployed api.

**W8 — Coordination: Lock, WithSingleflight, Singleton.** The §4.4 lease
mechanism behind `tako.Lock`/`Singleton`/cron singleflight, local +
gcp backends, conformance chapter. Gate W8: full cycle; two instances
provably serialize.

**W9 — Cross-service Endpoints.** Typed service-to-service calls
(spec §2.12): boot-hook transports, OIDC between services, deadline
budgets, analyzer sync-call warnings. shop splits into services. Gate
W9: full cycle; revoking `run.invoker` breaks the call (judged), dev
breakpoint across the boundary.

**W10 — Jobs.** `tako.Job` long-running compute on the batch profile.
Gate W10: full cycle; a job outliving a request deadline completes.

**W11 — examples/shop, complete (gate).** The multi-service showcase
whole. Gate W11: the shop demo script local and cloud — Stage 3 closes.

*Standing deferrals slot when an example forces them: Cloud Tasks /
WithDelay (shop's order flow is the likely demand), Cloud Scheduler for
cron (journal or shop publishing schedules), the migrating `tako
rename`, shared cache, metrics backend.*

## Stage 4 — Release (decomposed 2026-07-30, Stage 3 closed)

*Gating returns to the standing rule: ONE feature, ONE gate — the
Stage 3 batching amendment expired with W11. Every slice: DX proposal
→ approval → implement → agent-side scripted+live validation → human
verdict in docs/validation-log.md.*

**R0 — Fit & finish the gates demanded.** The Stage 3 walks left four
example-demanded debts: per-QUERY composite derivation (the aggregate
tuple under-indexed shop's split query shapes — listOrders gets its
OrderBy back), orphaned-index detection (stale composites from schema
churn are invisible to destroy; prune learns to delete them —
indexes are derivable, never data), WithProfile extraction (accepted
everywhere, honored nowhere — gallery's batch worker finally gets
batch knobs), and honest security-summary ingress text for jobs and
singletons. Gate R0: goldens + shop/gallery suites + one full cycle
on a fresh project.

**R1 — Example-driven docs.** Each example a tutorial chapter
(hello → shortlink → gallery → journal → shop = the feature arc);
the cloud story (permissions model, the Identity Platform manual
step — the W6 standing requirement lands HERE, in user-facing docs);
the local story (no emulators, .tako/data is the database, deleting
it is the reset). Gate R1: the human reads as a newcomer.

**R2 — Quick-start README.** < 5 minutes, hello local then deployed.
Gate R2: a human who is NOT the author follows it VERBATIM.

**R3 — API review 2 + publish.** Every exported symbol re-reviewed
against the six rules (api-review-2.md; the F10 lock plus what Stage
3 made real behind it); anything doubtful goes internal; module
tagged and published at github.com/gsandoval/tako. Gate R3: the
review verdict + a fresh-project full cycle against the PUBLISHED
module path.

---

## Stages 5–9 (closed; decomposed in `task-log.md`, not here)

The plan stopped at Stage 4 while five stages ran out of the task log
and the Stage 7 handoff — a gap logged as discovered work on 2026-08-01
and closed here so the plan is again the place to look.

| Stage | What it was | Slices | State |
|---|---|---|---|
| 5 | Operator surface | S1–S3 | closed, verdicts recorded |
| 6 | Close the gaps | T1–T4 | closed, verdicts recorded |
| 7 | The operator era | U1–U4 | closed, verdicts recorded |
| 8 | The explicit service | V0–V6 | closed, verdicts recorded |
| 9 | DX round | U3, V5, V6, P1, X2 | closed, verdicts recorded |

Alerting (Y1, Y2) ran after Stage 9 without a stage of its own. Stage 10
adopts it and closes it out.

## Stage 10 — Close alerting, then ship (proposed 2026-08-03) — AWAITING APPROVAL

*Standing rule applies: one feature, one gate. Every slice is DX
proposal → approval → implement → agent-side scripted + live validation
→ human verdict in `docs/validation-log.md`.*

**Z1 — Y2 delivery, and the two rows.** No new surface: run the
`validate/alerts.md` walk end to end against the CURRENT policy spec and
record the verdicts Y1 and Y2 are both waiting on. Y1 has been sitting
at *pass (agent-side)* since 2026-08-02; Y2 has no row at all.
Gate Z1: the human signs both rows.

**Z2 — `DeadLettered` observed firing.** The only preset whose
transition has never been seen, and the one whose query was wrong for
the whole of Y1 — the fix is proven by test and readback, never by an
incident. Drive a poisoned message through `thumbs` until retries are
exhausted, watch the policy go from quiet to firing, read the mail.
Gate Z2: the transition, both directions, in a transcript.

**Z2b — The alerting namespace, and `examples/siren`.** Spec rule 6 says
no primitive owns a general concept; alerting claimed `Count`, `Rate`,
`Sum`, `Above`, `For`, `Email`, `PubSub` and nine more. Readings become
methods on their handle (rule 6's own remedy), comparisons take `When`,
channels take `Alert`, conditions become `<Subject><Situation>`, and the
dead-letter queue stops being a symbol and becomes a field on
`QueueWatch`. `DeadLettered` and `For` are deleted. `examples/siren` —
the sixth example, whose entire job is to break on command — is what
demands every surviving condition, since the standing rule is no feature
without an example demanding it and the walk found 24 exported symbols
against five in use. **Lands before Z3**, so Z3's three constructors are
born under the scheme rather than renamed a week later. Full proposal in
`task-log.md`. Gate Z2b: the renamed surface, and siren firing every
condition live.

**Z3 — `tako.ServicePercentile` / `ServiceInstances`. DONE 2026-08-04.** The derived
platform-metric constructors, approved in the Y2a proposal and never
built. Today the same questions need a raw `tako.Platform` with a
Google metric type spelled by hand. Gate Z3: one of each on gallery,
live.

**Z4 — `tako metrics <search>`. DONE 2026-08-04.** The CLI that lists metric descriptors
so a `Platform` metric type can be found. A project publishes upwards of
8,000 descriptors, which is why the reference deliberately carries no
list — and why, without this, `Platform` is only usable by someone who
already knows the name. Gate Z4: find a metric neither of us knew,
from the CLI alone.

**Z5 — Housekeeping. DONE 2026-08-04.** Nothing user-facing except one
error text, whose wording was approved before shipping: Cloud Tasks'
7-day name-reuse block now names the wait and suggests `--env`, and
`tako destroy` warns about it in its preview — the error alone arrives
after the clock has started. The gofmt item and the stage table were
already current and closed by verification. One item was not on the
list: the custom-method retry guard had been failing since Z2b, which
is why `:generateServiceIdentity` is now classified as an idempotent
mutation.

**Z6 — Publish. REMOVED from Stage 10 (owner, 2026-08-04).** Blocked on
the `flautas` friction logs: the first real application built on tako is
the only source of evidence about what using it is actually like, and
publishing before that evidence is in would tag a version whose
ergonomics nobody outside this repo has tested. R3 completed the API
review and the publish prep; what remains is the owner's `git push` /
`git tag v0.1.0` plus a fresh-project cycle against the published module
path, and it is unscheduled until the friction logs land. (Note: the
repo has NO git remote configured yet — `git remote -v` is empty — so
the push step needs one added first.)

**Z7 — `tako docs`. DONE 2026-08-04** (approved and built after Stage 11;
listed here because Stage 10 is where it was proposed). Serves the guide,
the reference and the SDK's exported signatures from a `go:embed`ed copy
inside the binary — no checkout, no network. The api page is generated
from the same lock file the API-freeze test compares against, and a
symbol with no reference page fails a test. Proposal in `task-log.md`.

## Stage 11 — Alerting you can fork, then the flautas gaps (2026-08-04)

Two groups. FA came first by the owner's direction; FL is the release
gate and Z6 waits on it.

**FA1 — the metric contract, locked. DONE 2026-08-04.** `internal/metrics`
is the one definition of every metric tako writes; the runtime records
through it, descriptors derive from it, and the provisioner's matchers
are spelled from it. A lock golden pins the names, and a test asserts the
rendered queries only match labels the runtime writes — verified by
mutation. Found one drift: `1xx` was missing from the documented status
set.

**FA2 — `tako alerts`. DONE 2026-08-04.** What each declared alert
compiles to: PromQL, notification sentence, and the constructor it came
from. No cloud, no project. `DescribeAlerts` is one renderer shared with
the siren golden.

**FA3 — the request readings. DONE 2026-08-04.** `tako.Requests(t)` and
`tako.RequestMs(t)` carry the same reading methods a declared Counter or
Histogram carries, plus `Status(class)` and `Fraction()`. Shipped
`Fraction` in place of the proposal's general `Ratio` — no example
divides unrelated metrics; deviation recorded in `task-log.md`.

**FA4 — `WhenAtLeast`. DONE 2026-08-04.** `>= n`, so a hand-written
counting condition is not off by one against the preset it replaced.

Gate FA (batched, agent-side): `pkg/provision/testdata/twins` asserts
each preset and its hand-written twin compile to identical PromQL. Six
do. A preset with no twin is reported, so coverage cannot erode quietly.

**FL1–FL4 — the flautas gaps. ALL DONE 2026-08-04**, in the suggested
order FL1 → FL3 → FL4 → FL2.

- **FL1 `tako.RedirectPermanent`** — 301 from a per-request target;
  htmx unchanged. journal renames a slug and forwards from the store.
- **FL3 `Query.Count()`** — a Datastore aggregation on gcp, a filtered
  walk on local, ignoring both `Limit` and `Order`. journal's home
  renders "Showing 50 of 214".
- **FL4 `Query.Page(cursor)`** — keyset on both backends, refusing
  without an explicit `OrderBy` and `Limit`. gallery's feed takes
  `?cursor=`.
- **FL2 `SiteConfig{NotFound}`** — one field for both 404 paths, with
  a plain-text fallback so a 404 never becomes a 500. journal has both.

Gate FL (agent-side): every example suite green against `local`,
conformance included, with the write-between-pages case pinning FL4.
Live gates for FL1/FL4 remain part of the batched human walk.

**FL5 — the `/_tako/` namespace. DONE 2026-08-05.** Framework routes
moved under a reserved prefix with rooms — `api/` for everything a
program talks to, `z/` reserved for diagnostics, the namespace root for
tako's own pages (`signin`) — and onto a mux of their own, answered
before the app's router. `internal/routes` is the one definition of the
six places that used to spell these paths by hand; `runtime.Reserved` is
the dispatch; `tako/reserved-prefix` refuses a route declared inside the
namespace.

It closed a startup panic, not a bad response: `tako.Auth` beside a
two-segment wildcard page, or a `tako.Bucket` beside a deeper one, made
Go's `ServeMux` refuse to build the router. The scope grew from the
friction log's one entry to the class once `/_tako/signed/` turned out
to be a second instance already in the tree. Depth alone does not fix
it — measured, the path move rescues journal and leaves gallery
panicking, because a deeper mount only moves the colliding arity — which
is why the fix is two muxes.

Gate FL5 (agent-side): `validate/reserved-namespace.md` run end to end,
transcripts in `task-log.md`; journal boots with auth + `GET
/{year}/{slug}`, gallery with a bucket + `GET
/{owner}/{album}/{photo}/{size}`; `PUT /_tako/api/session` still answers
405 from the handler; a route inside the namespace fails analysis. Cloud
half unvalidated by design — three deployed targets move (Scheduler uri,
push endpoint, Tasks target) and belong to the next release-cycle walk.

Also in the log and **not promoted**: a `NotFoundData` provider so a
config-driven shell can render on the 404 (today `writeNotFoundPage`
passes nil, `pkg/runtime/page.go:397`, so a shell built from config
renders its bands empty) and an analyzer warning when a wildcard Page
route shadows site files at depth 2 (cost flauta a silently unserved
stylesheet). Both are flagged "workaround-fine, worth asking for" by
flauta and neither blocks. The rest of the log — 403 pages, the dev bar
on htmx fragments, one root template per site, the domain through the
config seam — is settled workaround-fine on both sides.

**Z7 — `tako docs`. DONE 2026-08-04**, after FL. Details in the Stage 10
entry above; gate (agent-side) is `validate/docs.md`, including the step
that matters most — a binary built into `/tmp`, run from `/tmp`, serving
every page.

**Z6 — Publish. Unblocked: FL1–FL5 are all in.** FL5 landing before the
tag matters more than the others did — it moved six public URLs, and
after a v0.1.0 those paths are API that someone's sign-in code is
written against. Z6 itself is still the owner's:
`git remote` (none is configured), `git push`, `git tag v0.1.0`, plus a
fresh-project cycle against the published module path. The verdicts also
need to travel back to `flautas/docs/friction.md` under its ADR 0008 —
a flauta-session job; tako never patches flauta.

**FL5b–FL9 and RC1–RC3 — the friction log, worked down (2026-08-05/06).**
Details and transcripts in `task-log.md`; this is the map.

- **FL5b** — shop's browser code follows the `/_tako/` move. FL5 moved
  six public URLs and left one hand-written `app.ts` behind; the guard
  is a test in `internal/routes` that checks examples against the
  constants, not against a copy.
- **FL6 / FL6a** — `tako/site-shadow`, a decision-time warning, and
  journal's assets out from under `GET /{year}/{slug}`. FL6 found
  journal serving its own stylesheet as a 404 on its first run.
- **FL7** — `ServiceHandle.Domain()`: a service reads back the domain
  it declared.
- **RC1** — index derivation follows the branches (gallery's production
  500). **RC2** — `tako logs` prints the error, not the label.
  **RC3** — every deployed service is observable: `runtime.Boot()`
  unconditionally, and `metricWriter` granted to match what the
  middleware actually writes.
- **FL8** — the app identifier is derived where Google constrains the
  characters. Blocked every deploy of every app whose module path names
  a host, with no app-side workaround; no example could reach it, so
  `examples/hello` moved to a host-shaped module path. That move
  immediately found a second wall in codegen's import alias.
- **FL9** — an index note belongs to its index (RC1's attribution, one
  row off).

Live-verified against tako-test-01 through RC3; FL8's create is the one
step `--plan` cannot exercise and belongs to the next release-cycle
walk.

### Deliberately NOT scheduled

Designed and argued, waiting on a trigger rather than on time:

- **Load balancer + Firebase Hosting** for domains — `docs/domain-options.md`
  carries the four-way comparison, live pricing, and the trigger
  conditions for each. Domain mappings ship today.
- **Parked, logged, unscheduled:** multi-service dev routing, the
  migrating `tako rename` (rename is a data migration; detection
  shipped, the command did not), session cache, email
  account-creation UI, VPC, per-command `--help`. *(Styled error pages
  left this list on 2026-08-04 and shipped as FL2; FL11 later made
  every status tako answers render through the app's own page.)*
- **Console-link rot.** Two link shapes were wrong in Y2 and were found
  by hand. Detecting a future move would need network access and a
  deployed project in CI; it stays a per-walk human check rather than a
  test that pretends.

## Stage 12 — the fourth friction review (2026-08-08) — ALL SHIPPED

**FL10–FL14 shipped between the third review and this one** and are not
mapped here; `task-log.md` carries each one. In short: FL10 derives a
query assembled across functions, FL11 `tako.StatusPage`, FL12
`Route.MaxAge`, FL13 the project IAM grant retries through propagation,
FL14 a declared provider is a scoped door. Z8 rode along — the docs
check catches a field that no longer exists.

**Where FL15+ comes from.** A fourth read of `flautas/docs/friction.md`,
with every open entry re-checked against tako at `7a40353` rather than
carried forward. Two things the read found before any of the entries:

- **The log's own index is stale.** Its table lists six open entries;
  the file holds **eight**. Two dated 2026-08-07 — the dev bar's missing
  hook, and scalar-only form binding — sit below the resolved entries
  and never reached the table. The second of them is the most valuable
  item in the log. A flauta-side correction under ADR 0008; noted here
  because tako read the file, not the table.
- **Nothing open blocks.** Every one of the eight has a working app-side
  answer, which is the friction gate passing rather than stalling — the
  contrast is FL8, whose only app-side answer was renaming the module.

Six entries are taken below as five tasks, one is deferred. Proposals
are in `task-log.md` under "FL15–FL19 — DX proposals"; all five were
approved together on 2026-08-08 and shipped the same day.

**FL15 — a form may repeat a field. DONE 2026-08-08** (`a3753dd`).
`bindableTypes`
(`pkg/analyzer/handlers.go:18`) binds `string`, `bool`, `int`, `int64`,
`float64`; anything else is `tako/bind-type` at `handlers.go:326`. So a
group of checkboxes named alike — the native HTML shape of "select some
rows" — cannot reach a handler: `FormString` returns the first value and
the other N-1 are dropped before the app runs. flauta's workaround
travels the selection as one hidden scalar of space-separated
`slug@token` entries, which forces the boxes to be JS-built and costs
the no-JS baseline for bulk selection.

The shape: `[]string` for `form:` and `query:` tags. The decode helper
is `r.Form[name]`; the analyzer already has the type table and the error
path.

**This one goes before the tag, and it is the only one that does.** It
changes what a Req struct may *declare* — normative surface, so its own
DX proposal per the API rule. Pre-tag that is just the v0 surface;
post-tag it is an API change against a published version.

It also has a corpus problem: none of the five examples asks for it, so
the standing rule says it waits. Override it the way FL8 already did —
FL8 moved `examples/hello` to a host-shaped module path *because* the
corpus could not reach the bug. Here a list screen with selection is
part of the task, not a follow-up. Gate FL15 (agent-side): golden for
the new binding and for the unchanged `bind-type` error on a
still-unbindable type; the example's suite green with N checkboxes
named alike and no JS.

**Shipped in `journal`, not `shop` as proposed.** shop is the
SPA-plus-JSON-API showcase — no `tako.Page`, no templates — so a no-JS
checkbox form would have meant giving it a server-rendered surface it
deliberately does not have. journal is the pages example and already
carries the forms, the auth and the htmx seam, so the bulk unpublish
sits where a reader of the guide meets it.

**FL16 — the readiness probe answers for the wrong process. DONE
2026-08-08** (`1d231c3`). flauta filed this as "`tako dev` says ready
before it has
bound the port" and asked tako to bind before printing. **The
diagnosis is wrong and the fix does not follow from it**: `tako dev`
never binds — the child does. `waitReady` (`pkg/devserver/devserver.go:342`)
dials `127.0.0.1:PORT` and returns nil the moment *anything* accepts, so
when a stale example holds 8080 the probe succeeds against the stranger
and `ready in 590ms` (`devserver.go:214`) is printed truthfully about
someone else's server. The child then fails to bind and prints the good
error from `pkg/runtime/http.go:171` — after the success line, which is
the ordering flauta saw.

The sharp detail: `http.go:168` already carries the comment *"A
collision here answers requests with SOMEONE ELSE'S responses —
mysterious 404s."* tako wrote this failure mode down and then built a
readiness probe that walks into it.

The shape: `waitReady` must confirm it reached *this* child — watch the
child's exit alongside the dial, or probe a `/_tako/` path carrying a
per-run nonce. Two tako apps on one machine is the normal case for
anyone developing tako and an app together. Gate FL16 (agent-side): a
devserver test that holds the port with a foreign listener and asserts
`tako dev` fails with the collision text and never prints `ready`.

**FL17 — a blob carries its headers, and so does a stylesheet. DONE
2026-08-08** (`fc31684`).
`BlobHandler` (`pkg/runtime/blob.go:20`) sets `Content-Type` and
`Content-Length` (`blob.go:57-59`) and stops: no `X-Content-Type-Options`,
no `Cache-Control`, no validator. A blob is the one response kind whose
bytes came from outside the program, which makes it the response that
needs nosniff most. flauta's workaround is to constrain what can be
*declared* — uploads refuse anything that is not one of four image types
— so a stored type can never be `text/html`.

**tako's read found a second instance flauta could not see.**
`securityHeaders` (`pkg/runtime/status.go:141`) is the single writer of
nosniff, and the site handler calls it **only when the content type is
`text/html`** (`site.go:174-176`). Every non-HTML file the site serves —
`.css`, `.js`, `.svg` — goes out without it. `contentTypeOf`'s own
comment two hundred lines later says *"nosniff is set — types must be
right"* (`site.go:229-232`): an invariant asserted about exactly the
files that do not hold it.

The shape: hoist nosniff out of the HTML-only branch, set it
unconditionally in `BlobHandler`, and wire `Route.MaxAge` — the field
FL12 already added, `handlerConf.maxAge` at `page.go:34` — through blob
endpoints. Freshness stays opt-in per route, so replace-in-place keeps
working for apps that have not asked for caching. Gate FL17
(agent-side): a header test per response kind — page, fragment, HTML
site file, non-HTML site file, blob, status — asserting the set each one
carries, so a future writer cannot drop one silently. That table is the
thing FL11 found missing when it counted four writers and three header
sets.

**FL18 — the dev bar behaves. DONE 2026-08-08** (`3c29f50`). Two
entries, one task, both local-only.
The bar is a `<div>` with inline styles and no id or class
(`pkg/runtime/session.go:438`), so an app with its own fixed bottom bar
— flauta's admin shell, and any app-shaped surface — has nothing stable
to hide. flauta targets it by *behaviour* instead, finding the
fixed-position child of `<body>` holding a `[name="dev_user"]` control,
because an inline-style substring match would be a guess at something
tako never promised. Separately, `barWriter.flush` (`session.go:414`)
branches on `b.html` alone, so every htmx fragment carries a copy of the
bar into whatever it swapped into.

The shape: `id="tako-dev-bar"`, and skip injection for fragments. The
runtime already knows fragment-ness one layer down — `securityHeaders(h,
fragment)` at `page.go:359` branches on exactly that. Gate FL18
(agent-side): a fragment response under `tako dev` contains no bar; a
page response contains one and it carries the id.

**FL19 — every call is billed to the project it was given. DONE
2026-08-08** (`476c7a0`).
Nothing in the tree sets a quota project or `x-goog-user-project`, so
Logging reads meter against whatever the ADC file carries — for a fresh
`gcloud auth application-default login` that is the project behind
gcloud's shared OAuth client, rate-limited across every gcloud user on
earth. flauta met HTTP 429 on its first deploy. `gcloud logging read`
against the same project works, because gcloud bills its own reads
correctly, so tako's log reader looks broken in the one place it cannot
afford to.

**Smaller in code than in design, which makes it a cloud task**:
`x-goog-user-project` requires the caller to hold
`serviceusage.services.use` on the target, so a naive fix trades a 429
for a 403 on some setups. It carries the "Cloud errors & permissions"
section per the standing rule — both failure texts, the fallback, and
whether `docs/tako-cloud-permissions.md` grows a line for the deployer.

**Not promoted: multi-layout rendering.** flauta wants two shells — a
public masthead and an admin workspace — and `renderSite` fixes the root
at `layout.html` (`pkg/runtime/page.go:87-90`). It is the only open
entry that would add real API surface (`RenderIn`, or a `SiteConfig`
that names more than one layout), flauta calls it "neither is urgent",
and its workaround is a two-line branching root. Widening the render
contract immediately before a version tag is the wrong order. Revisit
after v0.1.0 if a second app asks.

**How it went.** All five approved together (owner, 2026-08-08) and
built in order, each on its own commit and gate. Transcripts and
per-task detail are in `task-log.md`; the scripted walk is
`validate/friction-4.md`. Two turned out larger than filed and one
smaller: FL17 grew a second instance (the site handler set nosniff only
inside an HTML-only branch, so every stylesheet went without one),
FL16 changed shape (the filed diagnosis does not hold — tako dev never
binds, so it is an identity check now), and FL19 shrank (the permission
it needs is already required by the preflight, so its cloud surface is
one Classify case rather than a design).

FL15 also found the example suite red on main since FL11: journal's
page harness never registered the status page, so every 404 in the
example fell back to plain text. Fixed in that commit.

**Z6 impact.** FL15 was the one that had to land before a tag, and it
has. The friction gate itself is met — eight open at the review, none
blocking, none without an app-side answer, and five now closed.

## Stage 13 — the abstraction review (2026-08-09) — AWAITING APPROVAL

**Where this comes from.** The owner asked a different question from the
four friction reviews: not "what did flauta hit" but "what has flauta
*built* that a second tako app would build again". So this read went at
flauta's 25,563 lines of Go (`flautas@2d23ca8`) looking for **shape
repeated by construction**, and checked each candidate against this tree
(`5e2ca3a`) and against settled decision 11 — *no admin/CMS primitives;
an admin site is just a site.*

The distinction that decides every row below: a construct is
framework-shaped when it is about **how a tako app is wired** (routes,
identity, stores, tests, environment) and app-shaped when it is about
**what a CMS is** (sections, revisions, publish states, media libraries).
Decision 11 bars the second list, and this review sustains it — the four
promoted items are all in the first.

| # | The construct | Evidence in flauta | Verdict |
|---|---|---|---|
| **AB1** | **Authorization is declared, not repeated** — `Route{Allow: policy}` | `requireEditor(ctx)` at **49 call sites in 12 files**, `renderNoAccess` at 48; the same four lines opening every admin handler | **DONE 2026-08-09** (`107ac03`) |
| **AB2** | **An app's real HTTP surface in tests** — `app.Handler()` | `harness_test.go` is **874 lines** hand-mirroring codegen's binders and mounts; `harnessguard_test.go` regexes `app.go` against it because nothing else could; four write routes were tested by nothing at all until M4-04. tako's own `examples/journal/pages_test.go` is **1,052 lines** of the same | **approved, not yet built** — wiring decided: `tako test` + `-overlay`, and `tako test` becomes the documented command |
| **AB3a** | **A handler can see its client** — `ctx.Client()` | the log's #1 open entry; `contact.go` caps site-wide (30/h) because per-IP is inexpressible | **promote — small** |
| **AB4** | **An app can ask where it is running** — `tako.Env` | `admin.go:85` `deployed()` reads `os.Getenv("TAKO_BACKEND")`, tako's private boot switch, at 5 call sites — and `editorAllowed` **authorizes on it** | **DONE 2026-08-09** (`efcedec`) |
| AB3b | A declared rate limit — `Route{Limit: …}` | same entry as AB3a | **designed, blocked**: honest enforcement needs a cross-instance counter, and `tako.Cache` is per-instance in-memory (`cache.go:11`) even on GCP. A backend decision, not a patch |
| AB5 | Optimistic concurrency in the store — `UpdateIf` / `entity:"version"` | `guardedSave` (`autosave.go:33`) — a generic CAS the app had to write once and route six write paths through; a backlog entry notes a misspelled token field silently disables it | **second wave** — real, and it reshapes `EntityHandle`, so it is its own task after AB1/AB2 |
| AB6 | Capability links — signed share URLs | `share.go`: mint/rotate/revoke/expire, ~100 lines | **park** — flauta needs revocation, so a stateless signed link does not cover it and a stored one is 100 lines it already understands. One app asking |
| AB7 | Pagination view-model | `listing.go:149-190` `pageURL`/`pageLinks`/`perPage` | **park** — every list screen wants it, but it is view furniture and decision-11-adjacent. Promote when a second app asks |
| AB8 | Audit fields stamped by the store | `touchArticle`/`touchPage`/`touchMedia` + a test policing stray `Updated =` | **refuse, and write down why** — flauta's `stampMedia` split exists *because* an automatic stamp erased the uploader on every background write (M3-08's walk). A store that stamps what the app did not set would ship that bug to everyone |
| AB9 | Versioned entities — snapshots, restore, prune | `revision.go` + `history.go` + `autosave.go`, ~1,000 lines | **refuse** — the framework-shaped kernel inside it is AB5; the rest (causes, throttling, pinning, drift-vs-published) is what a CMS is, and decision 11 owns that |
| AB10 | A user store, registration, roles | there is none: identity is Identity Platform's, authorization is `config.json`'s `editors` list, and adding an editor is a code edit plus a deploy | **AB1 is its floor** — do not build a user store before the enforcement seam exists. Revisit after AB1 with a second app's requirements in hand |

**The ordering argument.** AB1 and AB2 are coupled and the coupling
decides the order: a policy moved off the handler and onto the route is
only enforced where the route is mounted, so **AB1 without AB2 would make
authorization invisible to the fast test path**. AB1's proposal answers
that inside AB1 — declared policies are enforced by `Endpoint.Call` as
well as by the mount, because a policy is about the caller's identity and
a cross-service call is exactly where a trust boundary is crossed — but
AB2 is what lets an app assert the *whole* refusal (status, page, nav)
rather than an error value.

AB4 rides with AB1: the reason flauta authorizes on a private env var is
that `editorAllowed` admits dev identities locally and must not deployed.
That check is authorization, and it is currently reading a string tako
never promised.

**What this review deliberately does not propose.** Nothing that would
make tako a CMS. No sections, no publish states, no media library, no
editor screens, no revision store. Decision 11 was re-read against every
row above and it holds: the four promoted constructs would each be worth
shipping if flauta were a shop, a dashboard, or a docs site instead.

Proposals: `task-log.md`, "AB1–AB4 — DX proposals". Gate: one per task,
per the standing rules.

**Status (2026-08-09).** AB1 and AB4 are shipped and validated —
outcome, transcripts and three discovered items in `task-log.md` under
"AB1 + AB4 — shipped". AB2 is approved in shape (the overlay wiring) and
unbuilt; AB3a is unscheduled. AB2 got a third piece of evidence while
AB1 was built: the moderation desk's two mounts had to be hand-typed
into journal's harness to match codegen, and a drift there would have
passed every moderation test against a door that was not mounted.

## Standing rules

- **No gate skipping, no gate batching.** One feature, one gate. If the human
  is unavailable, the agent stops; there is no "provisional continue."
- **Ergonomics regressions are release blockers.** If a later feature makes an
  approved DX transcript worse (more ceremony, noisier output, vaguer error),
  that's a failing state even with green tests.
- **Conformance is law**; error-message texts are golden-tested UX; examples
  are the acceptance suite; no feature without an example demanding it.
- **Standing non-goals:** SQL/Postgres, shared Redis, Node runtimes,
  websockets, admin primitives, VPC networking, multi-region, SSA-precision
  IAM (unless the F1 characterization test shows egregious over-grants).
