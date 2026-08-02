# Resume here — U2, U3, U4 (written 2026-08-01)

Companion to `docs/stage7-handoff.md`, which still describes U2/U3/U4
correctly EXCEPT where this file corrects it. Read this first, then that.

## Where things stand

| | state |
|---|---|
| U1a — `WithCDN` retired | done, `010648d` |
| U1 — `WithDomain` on any service | done + **gated pass** (validation-log, 2026-08-01) |
| Stage 8 V0 — Latency actually allocates CPU | done, `51c6c18` |
| Stage 8 V1+V4 — `tako.Service`, service naming | done, `0ec5659` |
| Stage 8 V2 — timeout/CPU/job retries reachable | done, `2f85be1` |
| Stage 8 V3 — `Delivery`, cron timezone/deadline | done, `b787317` |
| index waits halved | done, `52ae1a2` |
| domain replace race | done, `a3d361e` |
| U2 — metrics export + graceful shutdown | done + **gated pass** (validation-log, 2026-08-01); absorbed Stage 8 V0–V4 |
| U3 — dlq replay | done + **gated pass** (validation-log, 2026-08-01) |
| **U4 — shared-cache plan (docs only)** | **NOT STARTED — next** |
| V5 — `destroy --plan` | done + **gated pass** |
| V6 — Footprint/Traffic split | done + **gated pass** |

**Stage 8 is gated** — folded into U2's walk (§6 of
`validate/metrics.md`) at the user's direction, and covered by U2's
verdict row.

## Ground truth for U2, verified in the code (2026-08-01)

The Stage 7 handoff's U2 section is right about intent and wrong about
two mechanics. Corrections:

1. **There is NO shutdown path to hook.** `pkg/runtime/http.go:93`
   `Serve` calls `http.ListenAndServe` and exits on error. No
   `signal.Notify`, no `http.Server.Shutdown`, no SIGTERM handling. The
   stdin leash (`TAKO_DEV_GUARD`) is dev-only and calls `os.Exit(0)`
   directly, which runs no defers.
   **So "flush on shutdown" means first adding graceful shutdown to
   `Serve`** — a runtime change with its own blast radius (it changes
   how every service exits), and arguably its own slice. Cloud Run sends
   SIGTERM and allows ~10s before SIGKILL.
2. **The metrics contract is two methods, fire-and-forget**
   (`pkg/backends/metrics.go`):
   ```go
   type Metrics interface {
       Add(ctx context.Context, name string, delta int64)
       Observe(ctx context.Context, name string, value float64)
   }
   ```
   Neither returns an error, so a flush can never surface failure to the
   caller — drop-and-log is the only honest option, as the handoff says.

Other facts worth not re-deriving:

- **Wiring point:** `pkg/runtime/boot.go:98` — `BootGCP` currently sets
  `hooks.Metrics = local.NewMetricsLog()`. That one line is the switch.
- **Local stays as is:** `pkg/backends/local/metrics.go` logs one
  `slog` line per record and keeps in-process totals; `OnMetric` feeds
  the dev console's 📈 lines. The contract does not change.
- **Backend client to reuse:** `pkg/backends/gcp/client.go` — `Client`
  wraps an ADC `*http.Client` plus `Options{Project, App, Env, Service}`.
  `lease.go` is the closest model for a new store. Note this is
  `pkg/backends/gcp` (runtime), NOT `pkg/provision/gcp` (deploy).
- **Env available at runtime:** `TAKO_PROJECT`, `TAKO_APP`, `TAKO_ENV`,
  `TAKO_SERVICE` are injected by `desired.go`; Cloud Run itself provides
  `K_SERVICE`, `K_REVISION`. Region is NOT injected — check before
  assuming. (This note originally said the `cloud_run_revision`
  monitored resource needs `location`. That resource turned out to be
  UNWRITABLE for user-defined metrics — see the U2 postmortem in the
  task log. The writable type is `generic_task`, which needs `location`
  too, so the conclusion held for the wrong reason.)
- **gallery declares the demanding metrics** since F8: an `uploads`
  counter and a `thumb_ms` histogram.

## U2 open questions to settle in the DX proposal

- **Does graceful shutdown ship inside U2 or before it?** Without it,
  the last ≤60s of metrics are lost on every revision swap. With it, U2
  carries a change to how every service exits.
- **Are metric edges derived?** The handoff flags this: if the analyzer
  derives no usage edges for counters/histograms, grant
  `roles/monitoring.metricWriter` to every service the way secrets are
  package-wide, and say why in the task log.
- **Histogram bucket bounds** — the handoff suggests exponential 2× from
  1ms to ~100s, in one const. Confirm against `thumb_ms`'s real range.

## Standing rules that bit during Stage 8

- **Error texts in `docs/reference/*.md` are golden-verbatim.** A stale
  one is a product bug. Grep for any text you change.
- **Every constant promoted to a knob leaves a sentence that used to be
  true.** Two summary lines were falsified this way ("retries: 3",
  "redelivery: 5 attempts"); both now read the declaration and are
  pinned by `cmd/tako/summary_test.go`.
- **Options in the wrong place breed rules that police the wrong
  place.** Three analyzer rules became unreachable during Stage 8 and
  were deleted. Check reachability before shipping a rule.
- **Never `git push`, never tag.** Feature branch → ff-merge to main →
  delete branch. Exclude `logaster-2013-06.jpg`.
- **`examples/blog/app.go` may hold the user's uncommitted domain
  declaration** — check `git status` before touching it.
- tako-test-01 currently hosts the user's U1 walk (`blog-app` orphan,
  `blog-web`, `blog.ker.gs`). Use tako-test-02, or `--env <scratch>`.

## Discovered work, unscheduled

- **Cloud Tasks blocks reusing a deleted queue name for ~7 days** —
  destroy/redeploy inside that window cannot converge, and tako surfaces
  the raw API message with no remediation.
- **`pkg/provision/gcp` has no HTTP seam**, so no applier can be tested
  against a fake server (`waitGone`, the domain appliers' verbs, and the
  index wait are all live-validated only). Adding an injectable host to
  `gcp.Client` would make the package testable offline.
- `pkg/analyzer/analyzer.go` and `edges.go` are not gofmt-clean
  (pre-existing).
- `docs/tako-agent-plan.md` stops at Stage 4; Stages 5–8 live only in
  the task log and the handoffs.
- A stale `comments` index sat in `CREATING` on tako-test-01 for a week
  (now READY) — worth knowing that state can persist.
