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

*Every gate is full-cycle: local suite → `tako test` → deploy to the test
project → scripted validation → destroy. The SDK surface froze at F10 —
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

## Stage 4 — Release

Example-driven docs, quick-start README (< 5 minutes, validated by a human
who is *not* the author following it verbatim), final API review vs. the six
rules, publish `github.com/gsandoval/tako`.

---

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
