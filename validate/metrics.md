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
- `TestIdleInstanceDrainsImmediately` and
  `TestFlushGetsWhateverTheDrainDidNotUse` — the drain ends with the
  last request rather than spending a budget, and the time it did not
  use is repaid to the flush.
- `TestServerSpeaksHTTP2`, `TestHTTP11StillWorksAlongsideH2C`, and
  `TestDrainWaitsForHTTP2Requests` — the server answers h2c, still
  answers HTTP/1.1, and the drain covers hijacked HTTP/2 connections
  that `http.Server.Shutdown` stops tracking.

## 2. The drain, locally

```
go run ../../cmd/tako dev
```

First confirm the server answers HTTP/2 — the check Google's docs
prescribe, and the one that has to pass before `HTTP2` is safe to turn
on. Use a route the app actually serves (gallery is API-only, so `/` is
a genuine 404):

```
curl -i --http2-prior-knowledge http://localhost:8080/api/photos
```

→ `HTTP/2 200`.

**Read the protocol in the status line, not the status code.**
`--http2-prior-knowledge` sends an HTTP/2 preface with no fallback, so
any HTTP response at all — 200, 404, 500 — proves the server spoke
HTTP/2. A broken h2c never reaches a status: curl fails in the framing
layer or the connection resets.

### 2a. An in-flight request finishes

Timing a Ctrl-C by hand is a coin flip, so **make the request slow on
purpose** with `curl --limit-rate` and signal the service directly.

Make a payload once — a REAL image, so the thumbnail worker succeeds
and the walk does not fill up with dead letters:

```
cat > /tmp/mkimg.go <<'EOF'
package main

import ("image"; "image/color"; "image/png"; "math/rand"; "os")

func main() {
	m := image.NewRGBA(image.Rect(0, 0, 500, 500))
	for y := 0; y < 500; y++ {
		for x := 0; x < 500; x++ {
			m.Set(x, y, color.RGBA{uint8(rand.Intn(256)), uint8(rand.Intn(256)), uint8(rand.Intn(256)), 255})
		}
	}
	f, _ := os.Create("/tmp/big.png")
	png.Encode(f, m)
	f.Close()
}
EOF
go run /tmp/mkimg.go     # ~750KB of noise, which PNG cannot compress
```

The drain ceiling is **7s**, locally and deployed alike. 750KB at
400k/s is ~1.9s, so signalling at 0.4s leaves ~1.5s — comfortably
inside it:

```
PID=$(pgrep -f ".tako/bin/gallery" | head -1)
curl -s -o /dev/null -w "curl: %{http_code} in %{time_total}s\n" --limit-rate 400k \
  -F caption=drain -F img=@/tmp/big.png http://localhost:8080/api/photos &
sleep 0.4 && kill -TERM $PID
```

Expect, in the dev console:

```
⏻ draining — 1 request in flight
POST /api/photos → 200 (1775.2ms)
📈 uploads +1 (total 1)
⚙ photos.Uploaded → OnUploaded ok (0s)
📈 thumb_ms 26 (n=1)
⚙ thumbnails → MakeThumb ok (27ms)
⏻ stopped (1.06s)
```

and from curl: `curl: 200 in 1.838039s`.

**Everything after the first line happened AFTER SIGTERM**, which is
the whole point. The request was answered **200** rather than cut off
mid-write. Both metrics — the `uploads` counter and the `thumb_ms`
histogram — were recorded by an instance that was already shutting
down, so they are exactly the recordings the final flush has to carry.
And `stopped (1.06s)` is far under the 7s ceiling: the drain ended when
the work did, not when a budget expired.

### 2b. A request that overruns is cut off, and says so

Same payload, eight times slower — 750KB at 50k/s is ~15s, well past
the ceiling:

```
PID=$(pgrep -f ".tako/bin/gallery" | head -1)
curl -s -o /dev/null -w "curl: %{http_code} in %{time_total}s (exit %{exitcode})\n" --limit-rate 50k \
  -F caption=cutoff -F img=@/tmp/big.png http://localhost:8080/api/photos &
sleep 0.5 && kill -TERM $PID
```

```
⏻ draining — 1 request in flight
⏻ 1 request did not finish within 7s — cutting it off
  (Cloud Run allows 10s from SIGTERM; the rest flushes metrics)
⏻ stopped (7s)
```

curl reports `000 in 8.689741s (exit 55)` — no status line, because the
connection died under it. That is the honest outcome, and the second
console line says where the 7s came from rather than leaving a bare
number to wonder about.

### 2c. Idle and impatient

`Ctrl-C` on an idle server: `⏻ draining` / `⏻ stopped` **at once** — the
drain watches the in-flight count, so an idle instance does not sit out
its ceiling. (This is the common case in the cloud: Cloud Run stops
routing to an instance *before* it signals.)

The ceiling is the same 7s locally as deployed, on purpose: a shorter
local cap would hide the deployed behavior at the one moment it
matters, when a request is genuinely hanging.

`Ctrl-C` twice quickly during 2b → `tako: second signal — exiting now`.

**What used to happen in all of these:** nothing. `Serve` called
`http.ListenAndServe` and the process died where it stood.

Restart the dev server before §3 (2a and 2b each stop it), and remove
the uploads the walk created: `rm -rf .tako/data`.

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
