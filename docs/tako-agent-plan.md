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

**Z4 — `tako metrics <search>`.** The CLI that lists metric descriptors
so a `Platform` metric type can be found. A project publishes upwards of
8,000 descriptors, which is why the reference deliberately carries no
list — and why, without this, `Platform` is only usable by someone who
already knows the name. Gate Z4: find a metric neither of us knew,
from the CLI alone.

**Z5 — Housekeeping (no gate, one commit).** Nothing user-facing except
one error text, which needs its wording approved like any other:
Cloud Tasks' 7-day name-reuse block currently surfaces Google's raw
message and should name the wait and suggest `--env`; `analyzer.go` and
`edges.go` are not gofmt-clean; this plan's stage table is now current.

**Z6 — Publish.** R3 completed the API review and the publish *prep*.
The remaining act is the owner's: `git push origin main`, `git tag
v0.1.0`, push the tag. Gate Z6: a fresh-project full cycle against the
PUBLISHED module path, which is the one thing no local run can fake.

## Stage 11 — The consumer's gaps (proposed 2026-08-04) — AWAITING APPROVAL

*The first stage driven by an outside consumer rather than by an example.
Source: flauta's friction log (sibling repo, `docs/friction.md`, entries
dated 2026-08-02), kept under its ADR 0008 — an app-side workaround is
the default, promotion to a tako task is the exception. Four entries:
two promoted by flauta, one reopened here on evidence flauta did not
have, one closed with no task.*

*Slice IDs are FL because Z is spent. The standing rules are unchanged —
one feature, one gate, DX proposal first. **No feature without an example
demanding it still holds**: each slice below names the example that will
demand it, and nothing ships on an outside consumer's word alone.*

**FL1 — `tako.RedirectPermanent`.** A Page handler can only redirect
303 (`pkg/runtime/page.go`, hard-wired), and the SDK's only permanent
redirect is `SiteConfig.Redirects` — a map literal read at analysis
time, so it cannot carry data decided per request. The gap is real and
has no clean workaround: the three flauta found are build-time-only
308s, an honest-but-temporary 303, or both mechanisms at once. Add a
`status` to `HTML` (widening the `hooks.HTMLParts` tuple), keep
`Redirect` at 303, serve 301 from the new constructor, and leave the
htmx path identical — `HX-Redirect` + 200 either way, because
permanence means nothing to a client that is not a crawler. Example
demanding it: journal grows a slug rename that mints a moved-URL
record and serves 301 from the store — a redirect decided per request
from entity data, which is the whole of the gap. Gate FL1: rename a
post, watch the old URL answer 301, and the same route answer
`HX-Redirect` under htmx.

**FL2 — `SiteConfig{NotFound: "404.html"}`.** Unparks the styled error
page. Two paths 404 today and both answer in plain text: `pageError`
for a handler's `tako.NotFound()`, and the site handler for a static
miss — which is why a handler-side result type cannot close this alone
and the config field can. Cheaper than it looks:
`RegisterSiteTemplates` already receives `SiteOptions` and installs
`renderSite` as package state, so both paths can render the site's own
layout with no codegen restructuring. A missing or broken template
falls back to today's plain text — a 404 never becomes a 500. Example:
journal has both paths already (`read` returns `tako.NotFound()`, its
site 404s on misses). Three calls for the proposal: whether `NotFound`
resolves as a template first and falls back to a static file in the
site dir so generators that already emit `404.html` just work
(recommended); whether 500s are in scope (recommended: no); and
whether htmx requests get the rendered document like everyone else
(recommended: yes — one behavior, no mode switch). Gate FL2: a
mistyped URL and a missing post both answer 404 wearing the site.

**FL3 — `Query.Count()`.** flauta rated this workaround-fine and it was
headed for the not-scheduled list; reopened on a fact flauta could not
see from outside. The gcp backend is a hand-rolled Datastore v1 REST
client, not the Go library, and `runAggregationQuery` is a sibling
endpoint of the `runQuery` it already speaks: the same query object we
build today, wrapped in `{aggregations: [{count: {}}]}`. It is one new
verb and a response field — server-side, billed per index scan rather
than per matched entity, and no new dependency. Local counts what it
already filters. That is small enough to be worth having rather than
deferring. One call for the proposal: `Count` ignores `Limit`
(recommended — `Limit` shapes a page, `Count` answers the total, which
is the question a numbered pager asks). Example: journal's home listing
renders "page N of M" instead of a bare first-50. Gate FL3: the total
is right against both backends, including with filters.

**FL4 — `Query.Page(cursor)`.** The one the consumer flagged as
definitely important, and the one with a sharp edge. `Page(after
string) (EntityPage[T], error)` joins `All`/`First`/`Iter` as an
executor, returning items, an opaque cursor, and whether more remain;
the empty string starts at the beginning. On gcp the cursor is nearly
in hand already — `Query` reads `endCursor` on every batch and uses it
internally to drain them, then throws it away at the API boundary.
The edge is that it cannot always be handed back honestly: the backend
deliberately falls back to a client-side sort when the order is the
default id order or cannot push down beside an inequality filter, and
in that case the server's cursor is not a position in the order the
caller asked for. Handing it back would silently skip or repeat rows.
So the rule is uniform across both backends: **`Page` requires an
explicit `OrderBy`**, and says so in a runtime error rather than
guessing — never a query that pages locally and lies in cloud. This is
a backend-interface change (`backends.EntityQuery` gains a start
cursor; `Query` must return an end cursor), so local implements a
**keyset** cursor — the last row's `(sort value, id)` resumed as a
compound predicate — not an offset, because only keyset matches
Datastore's behavior when rows are written between pages. That
agreement is the conformance case, written first: insert and delete
between page 1 and page 2, both backends observe the same thing.
Example: gallery's `GET /api/photos` takes `?cursor=` and returns the
next one. Gate FL4: page through gallery on both backends, with a
write landing mid-walk.

**Closed with no task — the site domain through the config seam.**
flauta asked for nothing here and it is right: `tako/config-literal`
refusing a variable is what lets the deploy plan be computed without
running the app. One duplicated string held by a test is the smaller
cost. Recorded so the question is not reopened from outside later.

**Sequencing — the open call.** FL2–FL4 are additive and can land as
v0.1.x after Z6 without hurting anyone. FL1 is the one worth arguing
about: it is small, and it is the difference between an outside
project's redirect design being honored or needing to be rewritten
around a 303 before its own M4. Publishing v0.1.0 without it means the
first published API is one a real consumer already cannot build on.
**Recommendation: pull FL1 into Stage 10 ahead of Z6; leave FL2–FL4
here.** Not yet decided.

**Not promoted.** flauta's log also carries four gaps known from
planning and not yet hit in code — no full-text query, no image
processing, no roles in auth, no configurable markdown pipeline. Two
are standing non-goals (admin primitives; anything that turns Entity
into "the database"), and all four are unhit. They stay in the
consumer's log until an example here demands them.

### Deliberately NOT scheduled

Designed and argued, waiting on a trigger rather than on time:

- **Load balancer + Firebase Hosting** for domains — `docs/domain-options.md`
  carries the four-way comparison, live pricing, and the trigger
  conditions for each. Domain mappings ship today.
- **Parked, logged, unscheduled:** multi-service dev routing, the
  migrating `tako rename` (rename is a data migration; detection
  shipped, the command did not), session cache, email
  account-creation UI, VPC, per-command `--help`. *(Styled error pages
  left this list on 2026-08-04 — scheduled as FL2, Stage 11.)*
- **Console-link rot.** Two link shapes were wrong in Y2 and were found
  by hand. Detecting a future move would need network access and a
  deployed project in CI; it stays a per-walk human check rather than a
  test that pretends.

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
