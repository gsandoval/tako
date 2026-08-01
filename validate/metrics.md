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

### 4a. Find the deployed service

`tako deploy` prints the URL in its summary; this re-reads it, so the
walk can be scripted. Note the **env prefix** — `--env metrics` deploys
`gallery` as `metrics-gallery`:

```
PROJECT=<test-project>
URL=$(gcloud run services describe metrics-gallery \
  --project $PROJECT --region us-central1 --format 'value(status.url)')
echo $URL
```

### 4b. Upload photos

Reuse the real PNG from §2a (`/tmp/big.png`) — a decodable image, so the
thumbnail worker succeeds and `thumb_ms` gets samples. Random bytes
would upload fine and then dead-letter, leaving the histogram empty:

```
for i in 1 2 3; do
  curl -s -o /dev/null -w "upload $i → %{http_code}\n" \
    -F caption="metrics-$i" -F img=@/tmp/big.png "$URL/api/photos"
done
```

→ three `200`s. Confirm they landed, and that thumbnails were built
(`ready` flips true once `MakeThumb` runs):

```
curl -s "$URL/api/photos" | head -c 400
```

### 4c. Read the metrics back

Wait **one flush interval (60s)** — recording is in-process and ships on
a timer, so an immediate read finds nothing and that is not a failure.

If a previous walk on this project ever wrote against the old resource
type, delete the descriptors once after redeploying — a descriptor
auto-created by a FAILED write keeps the labels that write used:

```
for m in uploads thumb_ms; do
  curl -s -o /dev/null -w "$m → %{http_code}\n" -X DELETE \
    -H "Authorization: Bearer $(gcloud auth print-access-token)" \
    "https://monitoring.googleapis.com/v3/projects/$PROJECT/metricDescriptors/custom.googleapis.com/tako/$m"
done
```

(Do not URL-encode the slashes — the metric type is part of the resource
path, and encoding them returns 400.)

There is no `gcloud monitoring time-series list` command; read the API
directly (works with the components you already have):

```
NOW=$(date -u +%Y-%m-%dT%H:%M:%SZ)
AGO=$(date -u -v-1H +%Y-%m-%dT%H:%M:%SZ)      # GNU date: -d '1 hour ago'

read_metric() {
  curl -s -G -H "Authorization: Bearer $(gcloud auth print-access-token)" \
    --data-urlencode "filter=metric.type=\"custom.googleapis.com/tako/$1\"" \
    --data-urlencode "interval.startTime=$AGO" \
    --data-urlencode "interval.endTime=$NOW" \
    "https://monitoring.googleapis.com/v3/projects/$PROJECT/timeSeries"
}

read_metric uploads
read_metric thumb_ms
```

Expect from `uploads`:

- `"metricKind": "CUMULATIVE"`, `"valueType": "INT64"`, and a point
  whose `int64Value` is **≥ 3** — a running total, not a delta.
- `resource.type: "generic_task"` — **not `cloud_run_revision`**, which
  Cloud Monitoring refuses for user-defined metrics.
- All four scoping labels filled: `namespace` (`gallery-metrics`),
  `job` (`gallery`), `task_id` (the instance), `location`
  (`us-central1`). An empty `location` means `WhoAmI` could not reach
  the metadata server, in which case export should have been skipped
  entirely — that combination is a bug, not a partial success.
- `metric.labels.revision` present, naming the revision that wrote it.
- More than one `task_id` if more than one instance served — each is
  its own cumulative series, by design.

From `thumb_ms`: `"valueType": "DISTRIBUTION"`, a `distributionValue`
with `count` ≥ 3, and `bucketCounts` non-zero somewhere in the middle
of the range (thumbnails land in the tens-to-hundreds of ms).

If either comes back `{}`, the metric has not flushed yet — wait
another 60s before treating it as a failure.

If a write is being REFUSED rather than merely delayed, the service
says so — check before waiting again:

```
gcloud logging read 'resource.labels.service_name="metrics-gallery" AND textPayload:"metrics flush failed"' \
  --project $PROJECT --limit 3 --freshness=1h --format 'value(textPayload)'
```

Then confirm the same thing visually: **Metrics Explorer → paste
`custom.googleapis.com/tako/uploads`**, and sum across `task_id`.

## 5. The last flush survives a revision swap

The point of U2a. Redeploy to force a new revision:

```
go run ../../cmd/tako deploy --project <test-project> --env metrics
```

Upload one photo, then **immediately** (inside the 60s flush window)
redeploy again. The old instance takes SIGTERM before its ticker fires,
so the upload only reaches Cloud Monitoring if the shutdown flush ran.

```
read_metric uploads      # the helper from §4c
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

Never gated; validated agent-side on tako-test-02. Confirm live.

**Read the v2 API, not `gcloud run services describe`.** tako writes
`cpuIdle` and `startupCpuBoost` through the v2 API; gcloud's describe
renders the v1/Knative view, where they appear — if at all — as
`run.googleapis.com/*` annotations. Grepping the v2 field names against
v1 YAML matches nothing, and looks like a failure when it is a
mismatch of representations.

```
knobs() {
  curl -s -H "Authorization: Bearer $(gcloud auth print-access-token)" \
    "https://run.googleapis.com/v2/projects/$PROJECT/locations/us-central1/services/$1" \
  | python3 -c "
import json,sys
d=json.load(sys.stdin); t=d['template']; c=t['containers'][0]
print('  resources      :', json.dumps(c.get('resources')))
print('  ports          :', json.dumps(c.get('ports')))
print('  sessionAffinity:', t.get('sessionAffinity'))
print('  timeout        :', t.get('timeout'))
print('  scaling        :', json.dumps(t.get('scaling')))
print('  concurrency    :', t.get('maxInstanceRequestConcurrency'))
"
}
```

### V4 — every service carries its package name

No edit needed; the deployed set is the evidence:

```
gcloud run services list --project $PROJECT --format 'value(metadata.name)' | grep metrics-
```

→ `metrics-gallery` (the app service — **undeclared keeps `<package>`**),
plus `metrics-gallery-onuploaded`, `-makethumb`, `-notifyowner`,
`-warmfeed`: one per worker/subscriber, each `<package>-<varname>`.

### V0/V2 — the defaults, and the footprint layered over them

```
knobs metrics-gallery              # profile default
knobs metrics-gallery-makethumb    # profile batch + footprint
```

```
metrics-gallery (profile default):
  resources      : {"limits": {"cpu": "1", "memory": "512Mi"}, "cpuIdle": true}
  ports          : [{"name": "http1", "containerPort": 8080}]
  sessionAffinity: None
  timeout        : 300s
  scaling        : {"maxInstanceCount": 20}
  concurrency    : 80

metrics-gallery-makethumb (profile batch + footprint):
  resources      : {"limits": {"cpu": "2", "memory": "4Gi"}, "cpuIdle": true}
  ports          : [{"name": "http1", "containerPort": 8080}]
  sessionAffinity: None
  timeout        : 900s
  scaling        : {"maxInstanceCount": 5}
  concurrency    : 4
```

Read across those two:

- **V2 (timeout reachable):** 300s for a service, 900s for batch — the
  profile's value, on the wire.
- **Footprint layering:** makethumb takes `cpu 2` and `timeout 900s`
  from the batch PROFILE, and `memory 4Gi`, `concurrency 4`, `max 5`
  from its FOOTPRINT. That interleaving is the whole design.
- **`cpuIdle: true`** on both — neither profile allocates CPU between
  requests, and tako does not set it spuriously.
- **No `startupCpuBoost` key at all**, and `sessionAffinity: None`.
  Both are omitted-when-false by the API; absent IS false. Same for
  `minInstanceCount`, which is why `scaling` shows only the max.
- **`ports: [{"name": "http1", "containerPort": 8080}]`** — the port
  carries the protocol, and carries a NUMBER. A name without a number
  is rejected outright (it was, once).

### V0 — the positive case needs a Latency service

**No gallery service uses the latency profile** (four default, one
batch), so nothing in the app as shipped sets `cpuIdle: false` or
`startupCpuBoost: true`. Declare one for the walk.

Put it on a SUBSCRIBER, not on `tako.Service`: declaring `tako.Service`
renames the app service to `<package>-<var>`, which would orphan
`metrics-gallery` in the middle of the walk. A subscriber already owns
its own service, so this is an in-place update.

In `examples/gallery/app.go`, add the option to `NotifyOwner`:

```go
NotifyOwner = tako.Subscribe(photoReady, notifyOwner,
	tako.WithProfile(tako.Latency))
```

Redeploy, then:

```
knobs metrics-gallery-notifyowner
```

```
  resources      : {"limits": {"cpu": "2", "memory": "1Gi"}, "startupCpuBoost": true}
  timeout        : 300s
  scaling        : {"minInstanceCount": 1, "maxInstanceCount": 100}
  concurrency    : 40
```

Three things to check, because they are the V0 bug and its sequel:

1. **`cpuIdle` is GONE.** Absent means false means CPU stays allocated
   between requests. Before V0, `Latency` promised this in its doc
   comment and set it nowhere, so a Latency service ran throttled
   exactly like Default.
2. **`startupCpuBoost: true`** — the other half. A warm floor of 1 does
   nothing for the second instance a spike starts.
3. **`minInstanceCount: 1`** — the warm floor itself, and the one that
   bills continuously. The plan warned about it.

Revert the edit and redeploy before §7.

### V1 — `tako.Service` carries service-wide options

Analysis-only, no deploy:

```
go test ./pkg/analyzer -run 'TestErrors/serviceoption|TestErrors/oneservice'
```

→ `WithProfile`/`WithFootprint`/`WithDomain` are refused on `API` and
`Page` with `tako/service-option`, and a package may declare at most one
`tako.Service` (`tako/one-service`).

### V3 — `Delivery` and cron timezone/deadline

The deploy summary reads the declaration rather than reciting a
constant (pinned by `cmd/tako/summary_test.go`). In §3's output, the
redelivery line for a queue with a declared `MaxAttempts` shows that
number, not the framework default of 5.

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
