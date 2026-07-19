# Tako — Implementation Plan

Companion to `tako-tech-spec.md` (the tech spec; section references below point there).
Name and module are decided: `tako`, `github.com/gsandoval/tako`. Handlers are declared as code (constructor vars), not comment directives.

## Strategy

Execution happens through `tako-agent-plan.md`: vertical feature slices with
three-tier validation (automated tests → scripted manual runs → human
sign-off gates), local-only validation first, full local→test→cloud cycles
from the cloud stage onward. This document remains the milestone map; phases
below correspond to agent-plan stages (A–D ≈ Stages 0–1, E–F ≈ Stage 2,
G–H ≈ Stage 3, I–J ≈ Stages 2/4).

Two ordering principles govern this plan:

1. **Local development and testing come first.** The framework is a working local web framework — with its full test story — before any GCP API is called. This front-loads the highest-risk component (the analyzer), makes every later phase testable without cloud access, and means the conformance suite exists *before* the GCP backends that must pass it.

2. **Cloud capability is pulled in by examples, not pushed by features.** Each example app is built locally first, then "graduates" to the cloud; whatever the graduation is missing is the next work item. This keeps the reconciler honest (every feature it grows is demanded by a running app), produces the documentation corpus as a side effect, and gives a crisp definition of done per phase: *the example deploys and its tests pass against both backends.*

The examples, in graduation order, and what each one forces into existence:

| Example | Locally exercises | Graduation forces |
|---|---|---|
| `hello` | api, one endpoint | build pipeline, minimal reconciler, one Cloud Run service |
| `shortlink` | api + Entity (Get/Put/Query) | Datastore backend, index derivation, derived IAM for data |
| `gallery` (spec Part 3) | Bucket, Queue, Topic, signed URLs, worker retry semantics | Pub/Sub push + OIDC verification, dead-letter, GCS notifications, signBlob |
| `journal` | Site, pages, Auth, sessions, CSRF, forms | Identity Platform, `_session`, static serving, domains |
| `shop` | multi-package services, sync call edges, Lock, cron singleflight, Job | cross-service OIDC stubs, lease bucket, Scheduler, Cloud Run Jobs |

Every phase below lists deliverables and exit criteria. No calendar estimates — phases are scoped so each ends in something runnable, and the order is the commitment.

---

## Phase A — Analyzer and AppGraph (the risk phase)

The analyzer (spec §4.1) is the component most likely to force design changes, so it goes first and gets the deepest tests.

Deliverables:
- Repo skeleton per spec Part 4 layout: `cmd/tako`, `pkg/sdk`, `pkg/analyzer`, `pkg/codegen`, `pkg/runtime`, `pkg/backends/{local,gcp}`, `pkg/provision`, `pkg/devserver`.
- `pkg/sdk`: declaration types only — `Bucket`, `Entity[T]`, `Queue[T]`, `Topic[T]`, `Ctx`, `Blob`, error helpers. Constructors record declarations; no behavior yet.
- `pkg/analyzer`: module loading (`go/packages`), resource-var extraction with constant-name enforcement, handler-constructor extraction (`tako.API`, `Page`, `Worker`, `Subscribe`, `Cron` vars, same mechanism as resources), route/Req-tag validation per spec §2.8, binding cardinality validation (one Worker per queue; exported-Endpoint-only cross-package calls), per-function resource-usage edges with read/write verbs (AST reference scan, transitive within module), Datastore index derivation from `Query` call sites.
- `tako gen graph`: prints the AppGraph as JSON + a human summary (services, endpoints, edges, would-be IAM).

Testing approach — this phase sets the pattern for the repo:
- **Golden tests**: a `testdata/` tree of small input packages, each paired with an expected AppGraph JSON; includes an `errors/` set asserting exact analysis-error text and file:line (bad signatures, non-constant names, duplicate workers, unindexed queries, undeclared bindings).
- Error-message quality is a tested feature, not a nicety — these messages are the product's primary UX.

Exit criteria: analyzer produces correct graphs for hand-written miniatures of all five examples; all error-path goldens pass; `tako gen graph` output is stable across runs.

## Phase B — Local runtime: codegen + local backends + `tako dev`

Deliverables:
- `pkg/codegen`: per-service generated mains (spec §4.2) — route registration on `net/http` 1.22 patterns, static param decoding (path/query/JSON/multipart), async envelope handling with the ack/retry/`Permanent` mapping, resource boot-hook wiring from env.
- `pkg/backends/local`: Bucket → `.tako/data/` directory; Entity → bbolt-backed store with in-process query/index scans; Queue/Topic → channel dispatch invoking handlers in-process; signed URLs → local upload/download endpoints.
- **Backend conformance suite** (spec §4.7): one shared test suite per resource interface, run against `local` now and `gcp` in Phase E — written in this phase so the interface contract is fixed before a second implementation exists.
- `pkg/runtime` middleware minus auth: recovery, request ID, structured logs, decode/encode.
- `pkg/devserver`: `tako dev` — analyze → generate → `go run`, file watcher with <1s target reload, fidelity-gap banner.
- Examples `hello` and `shortlink` running locally end to end.

Exit criteria: `tako dev` on both examples; conformance suite green on `local`; kill-and-restart preserves shortlink data (bbolt persistence).

## Phase C — The testing story

Testing is a headline feature (spec §2.10) and ships before the cloud so framework development itself can lean on it.

Deliverables:
- `tako.NewTestApp(t)`: handlers callable directly against fresh in-memory backends; `app.SignIn(name)` stub identities; `app.DrainQueues()` synchronous worker/subscriber execution; `app.AdvanceCron(name)`.
- **Chaos mode** for async fidelity: `tako dev --chaos` and `app.Chaos()` — random redelivery, reordering, and duplicate delivery on local queues/topics, so at-least-once bugs (the most common async bug class) are catchable locally instead of only in production.
- `tako test`: wraps `go test` with generation freshness ensured.
- Test suites for `hello` and `shortlink` written *as a user would* — these double as dogfood and docs.

Exit criteria: shortlink's test suite catches a deliberately seeded non-idempotent-worker bug under `app.Chaos()`; `NewTestApp` requires no cloud credentials or network.

## Phase D — `gallery` locally: the full async + storage surface

Deliverables:
- Bucket events (`photos.Uploaded`) in the local backend; signed-URL local flow; `Blob` streaming through api handlers; dead-letter surfacing in `tako dev` output; `WithDelay` semantics locally (timer-based).
- `Secret` (env-file backed locally), `Cache` (in-memory + TTL), `Counter`/`Histogram` (logged locally).
- The `gallery` example complete with its test suite, including chaos-mode tests on `MakeThumb` idempotency.

Exit criteria: gallery fully functional and tested with zero cloud dependencies; this is the "already a pleasant framework" milestone — worth a pause for API-feel review before the cloud work locks interfaces in.

## Phase E — Cloud minimum: `hello` and `shortlink` graduate

First GCP code. Scope is pulled by the two simplest examples only.

Deliverables:
- Build pipeline (spec §4.5): static binaries, distroless layering via `go-containerregistry`, digest-pinned pushes to Artifact Registry.
- `pkg/provision` v1 (spec §4.6): stateless label-based reconcile for service accounts, buckets, Datastore indexes, Cloud Run services, IAM bindings; spec-hash annotations; additive-first ordering; deploy lease.
- `pkg/backends/gcp` for Bucket + Entity; **conformance suite green on `gcp`** — the phase's real exit gate.
- `tako deploy` (with `--plan`), `tako logs`, `tako destroy`; derived-IAM security summary output.

Exit criteria: both examples deploy from a clean GCP project to a live URL in under ~2 minutes; their test suites pass against the `gcp` backend; a no-op redeploy is near-instant; `tako destroy` leaves the project clean.

## Phase F — Async in the cloud: `gallery` graduates

Deliverables:
- GCP backends for Queue/Topic: Pub/Sub topics, per-handler push subscriptions, OIDC push verification, dead-letter topics, retry/backoff config; Cloud Tasks backend selection for `WithDelay`/`WithRateLimit`; GCS notification wiring for bucket events; signBlob-based signed URLs + the `tokenCreator` self-grant.
- Reconciler grows: topics, subscriptions (post-service-URL ordering), notification configs.
- Chaos-mode findings from Phase C validated against real redelivery behavior.

Exit criteria: gallery deploys and passes its suite on `gcp`; killing the worker mid-job demonstrates redelivery then dead-letter; the security summary matches spec Part 3's example output.

## Phase G — Web and auth: `journal` graduates

Deliverables:
- `Site` (spec §2.11): embedded static serving, `WithBuild`/`WithDevProxy`, fingerprint-aware caching, security headers, redirects; `Page` handlers, layouts, form binding, automatic CSRF, `tako.Redirect`; htmx-first-class fragments (`site.Fragment`, `IsHTMX`, HX-aware Redirect, CSRF hx-headers) and sanitized dynamic markdown (goldmark + bluemonday).
- `Auth`: Identity Platform provisioning, JWKS-validating shim, `_session` endpoint, `tako.WithAuth()`, dev-mode test users.
- Custom domain mapping; `tako gen client ts` (v1: types + fetch wrappers).
- The `journal` example: a small authored-content site (posts, editor sign-in, public SSR pages) — deliberately CMS-shaped to exercise the patterns from that discussion without adding framework surface.

Exit criteria: journal deploys with Google sign-in working, CSRF verified by test, Hugo-built static example variant served via `WithBuild`.

## Phase H — Multi-service and coordination: `shop` graduates

Deliverables:
- Package=service partitioning; cross-service call edges → generated OIDC stubs with deadline-budget propagation; analyzer warnings (sync-into-cold-service, chain depth) per spec §2.12; `private` IAM-only endpoints.
- Lease mechanism (spec §4.4) → `Lock`, `cron singleflight`, `singleton`; Cloud Scheduler; `Job` → Cloud Run Jobs.
- The `shop` example: gallery's frontend + a separate `billing` package, a reindex Job, a singleflight cron.

Exit criteria: shop deploys as two services; dev-mode breakpoint steps across the service boundary; revoking the caller's `run.invoker` breaks the call (proving IAM is load-bearing); cron double-fire test shows singleflight collapse.

## Phase I — Deployment lifecycle polish

Deliverables (spec §4.6a): change-class-aware `--plan` output; `tako prune` / `--data` confirmations; `tako rename`; digest-pinned `tako rollback`; `--canary`/`tako promote`; `--env` preview environments with CI recipe; drift warnings.

Exit criteria: a scripted lifecycle test on `gallery` walks deploy → drift-inject → warn → canary → promote → rollback → prune → destroy without manual intervention.

## Phase J — Release readiness

- Docs generated around the examples (each example = a tutorial chapter); README with the <5-minute quick start (`hello` local, then deployed).
- API-stability pass: every exported symbol reviewed against the six interface rules (spec Part 1); anything doubtful goes behind `internal/` rather than shipping.
- Module published at `github.com/gsandoval/tako`.

---

## Cross-cutting rules

- **Conformance suite is law**: no `gcp` backend merges without green conformance; any behavioral difference discovered later becomes a new conformance case first, fix second.
- **Analysis errors are UX**: every new analyzer rule lands with an error-message golden test.
- **Examples are the acceptance suite**: CI runs all example test suites against `local` on every commit and against `gcp` nightly + pre-release.
- **No feature without an example demanding it**: if none of the five examples needs it, it waits (this killed the admin scaffold and keeps the surface honest).

## Risks and early warnings

- *Analyzer precision* (Phase A): the AST-scan usage analysis may over-grant IAM through helper indirection; acceptable v0, but if goldens show egregious over-grants, pull the SSA/RTA upgrade forward.
- *Local/cloud fidelity* (Phases D→F): the first real divergence found in Phase F tells you whether the conformance suite has the right granularity; treat any Phase F surprise as a Phase C test gap.
- *Reconciler edge cases* (Phase E+): GCP API eventual consistency (IAM propagation delays especially) will need retry-with-backoff in the reconciler; budget for it rather than discovering it in demos.
- *Interface lock-in* (end of Phase D): the pause-and-review before cloud work is the last cheap moment to change the SDK; after Phase E, renames ripple through generated code, docs, and examples.
