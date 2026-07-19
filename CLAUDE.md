# CLAUDE.md — Tako

Tako is a Go framework over Google Cloud Run: users write pure business logic
(APIs, workers, sites) as one or more Go packages; `tako dev` runs it locally,
`tako deploy` analyzes the code, generates services, and provisions GCP.

Module: `github.com/gsandoval/tako` · CLI: `tako` · Handlers are declared as code (constructor vars), not comment directives

Documents (read the section you're pointed at, not the whole file):
- `docs/tako-tech-spec.md` — normative design. Interface rules (Part 1), SDK (Part 2), example app (Part 3), internals (Part 4).
- `docs/tako-implementation-plan.md` — phase milestones and exit criteria.
- `docs/tako-agent-plan.md` — the task you are working on. One task per session.

## Normative API

`pkg/sdk` is the single source of truth for the user-facing API. The spec's
code samples are illustrative; if they disagree with `pkg/sdk`, `pkg/sdk` wins
and the spec gets a fix-up PR. Never add, rename, or re-shape an exported SDK
symbol as a side effect of another task — API changes are their own task.

## Settled decisions — do not revisit or "improve"

1. Six interface rules (spec Part 1) govern every exported symbol. New surface
   must satisfy all six or it doesn't ship. Everything — resources AND handlers —
   is a package-level var via a constructor; there are no comment directives.
   Constructors are side-effect-free records, so init order never matters.
   Sync handlers are always `func(Ctx, Req) (Res, error)` with tagged Req structs.
2. Static analysis + code generation, never reflection in the request path.
3. Generated code lives in `.tako/gen/` and must stay human-readable. Never
   hand-edit generated output; fix the generator.
4. No Terraform, no state file: stateless label-based reconciliation with
   spec-hash annotations (spec §4.6/§4.6a).
5. No Docker for builds: static binaries layered on distroless via
   go-containerregistry (spec §4.5).
6. Package = service. Cross-package handler calls are the RPC (spec §2.12);
   events (Topic/Queue) are the preferred inter-service style; sync-call costs
   are surfaced as analyzer warnings, never hidden.
7. Entity is *an* entity store, never "the database": no global DB/Tx/Query;
   transactions and queries are methods on the store (spec §2.3).
8. `private` endpoints are IAM-only, not network-internal. VPC anything is a
   later explicit opt-in.
9. Local backends are in-process (no emulators, no Docker); the shared
   conformance suite is the contract between `local` and `gcp`.
10. v0 rendering models: static output from any generator + Go SSR. No Node
    runtime services.
11. No admin/CMS primitives in the framework. An admin site is just a site.
12. Data destruction is never automatic (spec §4.6a).
13. SSR is hypermedia-friendly: fragments + htmx-aware redirects are core
    (spec §2.11); dynamic markdown renders sanitized by default (goldmark +
    bluemonday) — trusted-HTML is an explicit opt-out, never the default.

## Working rules

- **One feature at a time, gated.** Work follows `docs/tako-agent-plan.md`:
  vertical slices, three validation tiers (automated → scripted manual →
  human sign-off in `docs/validation-log.md`). STOP at gates — never start
  the next feature without a signed verdict for the previous one.
- **DX-first.** Before implementing a feature, write the DX proposal (user
  code snippet, CLI transcript, error texts) into the task log and stop for
  human approval. The shipped surface must match the approved proposal.
- **Run your own validation.** Execute `validate/<feature>.md` yourself and
  paste the real transcript into the task log before requesting a gate.
- **Test-first, always.** Analyzer work: golden tests in `pkg/analyzer/testdata/`
  (input package + expected AppGraph JSON; error cases assert exact message and
  file:line — error text is product UX and is under test). Backend work: the
  conformance suite. Runtime work: example-app tests.
- **Conformance is law.** Any behavior difference between `local` and `gcp`
  backends: write the conformance case first, then fix. A `gcp` change never
  merges with conformance red.
- **No feature without an example demanding it.** If none of the five examples
  (hello, shortlink, gallery, journal, shop) needs it, it waits.
- **Stay in scope.** Do the task in `tako-agent-plan.md`, including its
  non-goals. If you discover adjacent work, note it under "Discovered work" in
  the task log; do not do it.
- Examples live in `examples/` and are the acceptance suite; their tests must
  pass against `local` on every commit.
- Analyzer errors follow the format `path:line:col: message (tako/RULE-ID)`.
- Commit per task, message prefixed with the task ID (e.g. `A3: handler constructors`).

## Commands

```
go test ./...                    # unit + golden + conformance(local)
go run ./cmd/tako gen graph      # print AppGraph for a target module
go run ./cmd/tako dev            # run an example locally (from its dir)
go test ./pkg/backends/conformance -backend=local
```

(gcp-backend conformance and `tako deploy` arrive in Phase E; update this list
when they do.)

## Repo map

```
cmd/tako/          CLI (dev, deploy, gen, test, logs, destroy, prune)
pkg/sdk/           user-facing API — normative, changes are their own task
pkg/analyzer/      go/packages + go/types → AppGraph; testdata/ goldens
pkg/codegen/       AppGraph → .tako/gen/<service>/main.go
pkg/runtime/       middleware, codecs, auth shim, Ctx implementation
pkg/backends/      interface contracts + conformance suite
  local/           in-process: fs bucket, bbolt entity, channel queues
  gcp/             GCS, Datastore, Pub/Sub, Tasks (Phase E+)
pkg/provision/     stateless GCP reconciler (Phase E+)
pkg/devserver/     tako dev: watch → analyze → generate → go run
examples/          hello, shortlink, gallery, journal, shop
docs/              tech spec, implementation plan, agent plan
```
