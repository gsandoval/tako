# API review 2 — the release pass (2026-07-30)

Baseline: the F10 freeze (api-review-1.md, lock at 384b9cf; the
sdk_lock test still passes at this commit — **zero exported symbols
were added, renamed, or re-shaped across all of Stage 3 and Stage 4**,
which was the freeze's whole promise). This pass re-reviews the
surface now that the stubs behind it are real, and prepares
publication.

## What became real behind the frozen surface

| Surface (frozen at F10) | Realized | Where proven |
|---|---|---|
| `Site` / `Render` / `Fragment` / `Markdown`, `SPA`, `WithBuild`, `WithDevProxy`, `WithRedirect`, `WithTrustedHTML` | W1–W4 | journal |
| `Auth`, `WithGoogle`, `WithEmail`, `WithAuth`, `User`/`MaybeUser`, sessions | W5–W6 | journal, shop |
| `Lock.Do`/`TryDo`, `WithSingleflight` | W8 | gallery |
| `Endpoint.Call` remote (OIDC, deadline budget, session propagation), `WithPrivate` | W9 | shop/billing |
| `Job`/`JobHandler`/`Run`, `WithParallel`, `Singleton` | W10 | shop |
| `WithProfile` | R0 (honored at last) | gallery |

Still deliberate not-yets, shipping as positioned errors with slice
pointers (honest-by-design, reviewed and kept): `WithDomain`,
`WithCDN`.

## Six-rules spot re-check

1. *Package-level vars via constructors* — every Stage 3 addition
   (billing.Charge, settle, Invoicer, relock) reads exactly like F2's
   entities. HOLDS.
2. *Static analysis, no request-path reflection* — remote-call stubs
   and the TS client are generated code; the two constructor-time
   reflections (fnName, pkgOf) predate F10 and run at declaration.
   HOLDS.
3. *Readable generated code* — job/singleton/callee mains reviewed in
   gates; the register-and-serve shape held. HOLDS.
4. *One async shape* — jobs joined `func(Ctx, T) error` without a
   variant. HOLDS.
5. *Errors are product* — 10 new golden-tested analyzer rules since
   F10 (raw-call, mixed-ingress, mixed-profile, job-binding, …) all
   follow `path:line:col: message (tako/rule)`. HOLDS.
6. *Costs visible* — deploy warnings grew (sync-into-scale-to-zero,
   chain depth) rather than absorbing. HOLDS.

**No symbol is recommended for demotion to internal.** The one
surface WART worth recording: `Handler` (the async registration
record) exposes nothing and cannot be constructed usefully by hand —
acceptable, it is the return-type of constructors only.

## Publication readiness

- Module path `github.com/gsandoval/tako` ✓ (root go.mod).
- Examples are separate modules with `replace => ../..` — correct for
  in-repo development; consumers `go get` the published module.
- Generated code, `.tako/`, and build outputs are gitignored ✓.
- **LICENSE: none exists.** Publishing without one leaves the module
  legally unusable; the CHOICE is the owner's (not made here).

## Release checklist (owner actions — nothing below is automated)

1. Pick and commit a LICENSE.
2. `git push origin main`.
3. `git tag v0.1.0 && git push origin v0.1.0`.
4. From a machine that has never seen the repo:
   `go install github.com/gsandoval/tako/cmd/tako@v0.1.0`, then run
   the README quick start VERBATIM against a fresh project — that is
   simultaneously the R2 gate and the R3 published-path validation.
5. Record the R1–R3 verdicts in docs/validation-log.md.
