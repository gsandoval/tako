# validate/metrics.md — U2: metrics reach Cloud Monitoring, and nothing is lost on the way out

The claim, in two halves that need each other:

- **U2a** — a service shuts down as a lame duck. `SIGTERM` closes the
  door, the requests already inside finish, and only then does the
  process go. Nothing is cut off, and the last metrics get written.
- **U2b** — `tako.Counter` and `tako.Histogram` appear in Cloud
  Monitoring under `custom.googleapis.com/tako/`, per instance, without
  a request ever waiting on a write.

**This gate also covers Stage 8 (V0–V4)**, which was built under a
"do this first" instruction and never got a verdict row of its own.
Section 6 is that walk.

**Setup:** run the CLI as `go run ../../cmd/tako …` from
`examples/gallery` — the demanding example: an `uploads` counter and a
`thumb_ms` histogram, declared since F8.
**`<test-project>`** is the scratch project (tako-test-02).

## 1. Analysis and unit truth, before anything is built

```
go test ./... 
go test -race ./pkg/backends/gcp ./pkg/runtime ./pkg/backends/conformance
go test ./pkg/analyzer -run 'TestErrors/drainrange'
go test ./pkg/backends/conformance -backend=local -run TestMetrics
```

→ green, and specifically:

- `TestCounterWritesCumulativeTotal` — the wire carries the **running
  total**, not the delta, and the `instance` metric label is present.
  Both are what keep two instances of one revision from corrupting one
  series.
- `TestBucketPlacement` — samples below 1 and above 262144 land in the
  under/overflow buckets instead of vanishing.
- `TestDrainLetsInFlightRequestsFinish` / `TestDrainStopsAcceptingNewRequests`
  — the lame duck, both halves.
- `TestDrainGivesUpAtItsBudget` — a wedged handler does not hold the
  process past SIGKILL.
- `drainrange` golden — the refusal above 9s, verbatim:

```
app.go:14:43: footprint Drain must be between 1s and 9s; got 30s — Cloud Run kills the instance 10s after SIGTERM and that window is not configurable, so a longer drain is a promise tako cannot keep (tako/footprint)
```

## 2. The drain, locally

```
go run ../../cmd/tako dev
```

Upload a photo so a handler is live, then `Ctrl-C` **while a request is
in flight** (the gallery upload is slow enough; if not, hit a page and
interrupt immediately).

Expect the request to **complete**, then:

```
⏻ draining — 1 request in flight
⏻ stopped (0.0s)
```

Then `Ctrl-C` on an idle server: `⏻ draining` / `⏻ stopped`, promptly —
the dev drain is 2s, and an idle server does not spend it.

Press `Ctrl-C` twice fast on a busy server → `tako: second signal —
exiting now`.

**What used to happen here:** nothing. `Serve` called
`http.ListenAndServe` and the process died where it stood.

## 3. Deploy

```
go run ../../cmd/tako deploy --project <test-project> --env metrics
```

Expect in the **plan**, before anything runs:

- `monitoring.googleapis.com` in preflight when it is not yet enabled,
  with the copy-pasteable enable command.
- In the security summary, per service:
  `roles/monitoring.metricWriter on the project (counters and histograms derive no usage edges, so every service writes metrics — package-wide, like secrets)`

That parenthetical is load-bearing: it is a **wider** grant than a
per-service one, and the summary has to say so rather than borrowing
Datastore's explanation.

## 4. Metrics arrive

Upload two or three photos through the deployed app, then wait for one
flush (≤60s) and read them back:

```
gcloud monitoring time-series list \
  --project <test-project> \
  --filter 'metric.type = "custom.googleapis.com/tako/uploads"' \
  --format 'value(metric.labels.instance, points[0].value.int64Value)'

gcloud monitoring time-series list \
  --project <test-project> \
  --filter 'metric.type = "custom.googleapis.com/tako/thumb_ms"' \
  --format 'value(metric.labels.instance, points[0].value.distributionValue.count)'
```

Expect:

- `uploads` ≥ the number of photos, as a **cumulative** value.
- `thumb_ms` with a non-zero distribution count and buckets populated
  in the low-hundreds-of-ms range.
- An `instance` label on both. **Check the resource labels too** —
  `service_name`, `revision_name` and `location` must be filled, and
  `location` in particular comes off the metadata server rather than
  the env, so an empty one means `WhoAmI` fell back and export should
  have been skipped entirely.

Then confirm the same thing in the console: Metrics Explorer →
paste `custom.googleapis.com/tako/uploads`.

## 5. The last flush survives a revision swap

The point of U2a. Redeploy to force a new revision:

```
go run ../../cmd/tako deploy --project <test-project> --env metrics
```

Upload one photo, then **immediately** (inside the 60s flush window)
redeploy again. The old instance takes SIGTERM before its ticker fires,
so the upload only reaches Cloud Monitoring if the shutdown flush ran.

```
gcloud monitoring time-series list --project <test-project> \
  --filter 'metric.type = "custom.googleapis.com/tako/uploads"'
```

→ the count includes that photo, under the **old** revision's series.
In `tako logs`, the old instance's last lines:

```
tako: SIGTERM — draining
tako: stopped (0.4s)
```

**This is the regression to watch.** Before U2a there was no shutdown
path at all, so up to 60s of metrics died with every revision.

## 6. Stage 8 (V0–V4), folded in

Never gated; validated agent-side on tako-test-02. Confirm live:

**V0 — Latency actually allocates CPU, and now boosts startup.**

```
gcloud run services describe gallery --project <test-project> --region us-central1 --format yaml | grep -i "cpu-idle\|startup-cpu-boost\|cpuIdle\|startupCpuBoost"
```

→ a Latency-profile service reads back CPU always-on **and** startup
boost; a Default one reads back neither. (`cpuIdle` and
`startupCpuBoost` are both omitted-when-false by the v2 API — the same
drift trap, pinned by `TestRunServiceEmitsCPUIdle`.)

**V1 — `tako.Service` carries service-wide options.** `WithProfile`,
`WithFootprint` and `WithDomain` on `tako.Service`; refused on `API`
and `Page` with `tako/service-option`.

**V2 — timeout, CPU and job retries are reachable.** Set
`Timeout` in a footprint, redeploy, and read it back:

```
gcloud run services describe gallery --project <test-project> --region us-central1 --format 'value(spec.template.spec.timeoutSeconds)'
```

**V3 — `Delivery` and cron timezone/deadline.** Declared attempts show
in the deploy summary rather than the old hardcoded `redelivery: 5
attempts` (pinned by `cmd/tako/summary_test.go`).

**V4 — service naming.** Every service carries its package name;
an undeclared one keeps `<package>`.

## 7. Destroy clean

```
go run ../../cmd/tako destroy --project <test-project> --env metrics
```

→ every object goes. Metrics already written **stay** in Cloud
Monitoring — they are data with a retention period, not infrastructure,
and tako never deletes data (§4.6a). Say so in the verdict rather than
treating leftover series as an incomplete destroy.

Confirm the project is pristine: no leftover services, SAs, or scratch
secrets.

## 8. Put gallery back

Revert any footprint edits made for §6. `go test ./...` green.

## Verdict

Record in `docs/validation-log.md`: U2 (U2a + U2b) **and** Stage 8
V0–V4, one row, with anything the walk caught.
