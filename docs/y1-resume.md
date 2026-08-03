# Y1 (alerting) — resume brief

Written 2026-08-02 at the end of a long session, immediately before a
context compaction. Everything here is verified state, not recollection.

**Status: alerting is NOT shippable. The live gate's headline claim is
false and `docs/task-log.md` records it as true.** Fix that first.

---

## 0. FIRST: correct the record

`docs/task-log.md`, entry **"Y1e — the live gate: nine defects, and a
delivered alert"**, says the alert "reached the owner's inbox … that is
the one claim nothing offline could make", and presents the walk as a
pass.

The email did arrive. **The alert was a permanent false positive.** The
delayed half of the gate script reported, after the write-up was
already committed:

```
right after a run (cron alive → must be QUIET):  ABSENT (would fire)
after 7 minutes of silence:                      ABSENT (would fire)
```

It never went quiet. A firing alert and a correct alert are
indistinguishable from an inbox; only the "must be quiet" assertion
separates them, and its result arrived after the conclusion had been
written.

Append a correction to the task log (do not rewrite history — the
project's convention is that historical entries record what was
believed then). Say plainly: nine defects were real and fixed, the
delivery path is proven, and the CONDITION was wrong, so Y1e is a
FAIL pending re-validation.

**No `docs/validation-log.md` row was written.** Do not write one until
the quiet half passes.

---

## 1. Defect 10 — the per-endpoint metrics sink is never wired

`pkg/runtime/http.go` defines `SetRequestMetrics(m backends.Metrics)`
and `recordRequest` no-ops when the sink is nil. **Nothing calls it.**

```
$ grep -rn "SetRequestMetrics" --include="*.go" . | grep -v _test
pkg/runtime/http.go:219:// SetRequestMetrics wires the per-endpoint sink.
```

So every `tako/requests` and `tako/request_ms` record vanishes. Proof
from the live project — the only series that existed was the deploy
seed, never the app:

```
series written by the app: 1
  route='' status='' job='gallery' namespace='gallery-x1'   ← the seed
```

The five tests in `pkg/runtime/request_metrics_test.go` pass because
they call `SetRequestMetrics` themselves. They prove the function works
and never ask whether production calls it.

**Fix:** call `SetRequestMetrics` in `pkg/runtime/boot.go` beside the
existing sink wiring — `hooks.Metrics = local.NewMetricsLog()` at
line ~115 (BootLocal) and `hooks.Metrics = metrics` at ~119 (BootGCP).
Pass the same sink.

**Add the test that would have caught it**: assert that after
`Boot*`, the package-level `requestMetrics` is non-nil. A unit test
that injects its own dependency cannot catch a missing injection.

---

## 2. Defect 11 — the `job` label disagrees three ways

The `generic_task.job` resource label must be identical in all three
places. It is not:

| written by | value | where |
|---|---|---|
| the app | `os.Getenv("TAKO_SERVICE")` = `svc.Name` — the **logical** name, e.g. `gallery-makethumb` | `pkg/backends/gcp/metadata.go:59`, set at `pkg/provision/desired.go:254` |
| the deploy seed | `app` — e.g. `gallery` | `pkg/provision/gcp/descriptors.go`, `Seed(...)` |
| every alert query | `cfg.Name(serviceOf(...))` — the **env-prefixed** name, e.g. `x1-gallery` | `pkg/provision/alerts.go`, `takoRequests` and the `latency` case |

Confirmed live: the app/seed wrote `job='gallery'` while the query
filtered `job="x1-gallery"`, so nothing ever matched and
`absent_over_time` was permanently true.

**Decide once and apply everywhere.** Recommendation: the LOGICAL
service name (what `TAKO_SERVICE` already carries), because the
`namespace` label already carries `<app>-<env>` and env-prefixing the
job too is redundant. Then:

- `alerts.go`: drop `cfg.Name(...)` around the job matcher (keep it for
  `subscription_id` and `job_name`, which ARE physical GCP names).
- `descriptors.go` `Seed`: write the same logical name — but note the
  seed has no single service, so consider seeding without a `job` label
  or with one series per service.

---

## 3. Re-validate (the gate, properly this time)

`examples/gallery/app.go` already declares `oncall`, `Stuck`, `Silent`
(near the `Cleanup` cron). Keep them for the walk.

```
export PROJECT=tako-test-01
cd examples/gallery
printf 'nk_y1' | gcloud secrets create x1-notifier-key --data-file=- --project $PROJECT
go run ../../cmd/tako deploy --project $PROJECT --env x1 --yes
```

Then, and this is the part that must not be skipped:

1. Fire the cron (`gcloud scheduler jobs run x1-gallery-cron-cleanup
   --project $PROJECT --location us-central1`), retrying past the
   IAM-propagation 403.
2. Wait ~3 minutes for the 60s metric flush plus ingestion.
3. Run the policy's OWN query and require **no series** (healthy):
   ```
   absent_over_time(custom_googleapis_com:tako_requests{
     monitored_resource="generic_task",namespace="gallery-x1",
     job="<whatever §2 settles on>",route="/_tako/cron/Cleanup"}[300s])
   ```
   **If this returns a series while the cron is alive, stop — the
   feature is still broken.** This is the assertion that was skipped.
4. Confirm a real series exists with `route="/_tako/cron/Cleanup"` —
   query `custom.googleapis.com/tako/requests` over v3 `timeSeries` and
   check the labels, not just the count.
5. Wait out the 5-minute window with no further runs; the same query
   must now return 1 series.
6. Confirm the mail: subject should now read
   `[ALERT - WARNING] gallery/x1: cron Cleanup has not run on __missing__`
   (`__missing__` is unavoidable on an absence — no resource labels to
   attribute).
7. `echo "gallery" | go run ../../cmd/tako destroy --project $PROJECT --env x1`,
   delete `x1-notifier-key`, and confirm zero policies and channels.

Owner-approved destination: **darchitect@gmail.com**. Email channels
need no verification (`verificationStatus` absent on API-created ones —
settled at the gate).

---

## 4. Then finish Y1's paperwork

- **`docs/reference/alerts.md`** — the feature page. Must carry: the
  preset catalogue with each default window, the `Window{For, Every}`
  mapping, the copy-pasteable incident struct for a `PubSub` channel,
  and the honest limits (no local evaluation; `__missing__` on
  absences; `Erroring`/`SlowerThan` on a route need tako's own metrics).
- **`docs/tako-cloud-permissions.md`** — add
  `roles/monitoring.alertPolicyEditor` and
  `roles/monitoring.notificationChannelEditor` to the deployer table at
  line ~56.
- **`validate/alerts.md`** — the repeatable gate script, built from §3.
  Include the quiet-half assertion prominently; its absence is why this
  session shipped a false pass.
- **`docs/reference/observability.md`** — "No metric labels/dimensions"
  under *Not yet* is now false for tako's own metrics. Correct it and
  document `tako/requests` and `tako/request_ms`.

---

## 5. Not built, proposed and approved (Y2a)

- `tako.Latency(svc, q)`, `tako.Instances(svc)`, `tako.Saturation(svc)`
  — the derived platform-metric constructors. Only `tako.Platform` and
  the nine presets shipped.
- **`tako metrics <search>`** — the CLI that lists metric descriptors so
  users can find a `Platform` metric type. Nothing exists. This is the
  "reference that cannot rot" answer to platform-metric discovery
  (a project has ~8,667 descriptors; a static doc is hopeless).

---

## 6. Known-good, do not redo

- Nine earlier defects are fixed and committed (`4d28606` and before);
  the task log lists them.
- Three completeness tests exist and were mutation-checked:
  apply-coverage (`cmd/tako/apply_coverage_test.go`), no-uppercase
  labels, preset-defaults-where-the-deploy-reads-them.
- `Stuck` (DeadLettered) was created live but **never fired** — nothing
  dead-lettered. Its transition is unproven; a poisoned message through
  `thumbs` would close it.
- Cloud Monitoring has **no public v3 incident-listing API**, so
  "exactly once" cannot be asserted programmatically. Running the
  policy's own query at both moments is the honest proxy.

## 7. Standing constraints

Never `git push`, never tag — commit on a feature branch, ff-merge to
main, delete the branch; the owner pushes. Exclude
`logaster-2013-06.jpg` from staging. Commit trailer
`Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>`.
Full suite green before every commit. Error texts in
`docs/reference/*.md` are golden-verbatim.
