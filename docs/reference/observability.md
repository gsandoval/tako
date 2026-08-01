# tako.Secret, metrics, and tako logs

Configuration you must not commit, numbers you want to watch, and the
command that reads a deployed app's logs.

## Declare

From [examples/gallery](../../examples/gallery/app.go):

```go
var (
    notifier = tako.Secret("notifier-key")
    ups      = tako.Counter("uploads")
    thumbMs  = tako.Histogram("thumb_ms")
)
```

Every name must be a string constant (analyzer-enforced). None of the
three take further options today.

## Use

A secret resolves **once at startup** — boot refuses to start a
service with any declared secret unset, which is what lets `Value()`
take no Ctx and never fail at request time:

```go
// Value never fails here: boot refused to start if the secret was unset.
key := notifier.Value()
```

Metrics are fire-and-forget — recording never fails and never blocks
the handler:

```go
ups.Add(ctx, 1)
thumbMs.Observe(ctx, float64(time.Since(start).Milliseconds()))
```

## Local

Secrets come from `.env` in the module dir (`name=value` lines),
loaded once at boot: `echo 'notifier-key=nk_dev_123' > .env` before
`tako dev`. A missing one stops the service before it serves.

Locally every `Add`/`Observe` is one structured log line, plus a 📈
line in the dev console: `📈 uploads +1 (total 3)` for counters,
`📈 thumb_ms 42 (n=7)` for histograms.

## Cloud

Secret values are **data the user owns**: tako never creates, writes,
or deletes secrets. The physical Secret Manager name is env-prefixed
like every logical name — except `prod`, which uses the bare name
(`notifier-key`; a `pr-42` env reads `pr-42-notifier-key`). Deploy's
preflight checks each one exists and, when it doesn't, prints the
exact create command:

```
secret pr-42-notifier-key does not exist in my-project — create it: printf '<value>' | gcloud secrets create pr-42-notifier-key --data-file=- --project my-project
```

Every service resolves every declared secret from Secret Manager at
boot — retrying briefly on a first deploy while IAM grants propagate —
and the deploy derives the accessor grants before any service starts.
Rotation is yours: add a new secret version; instances pick it up as
they restart.

## Metrics in the cloud

Locally, every `Add` and `Observe` is a structured log line and a 📈
line in the dev console. Deployed, the same call sites also export to
Cloud Monitoring as user-defined metrics:

```
custom.googleapis.com/tako/uploads      # tako.Counter("uploads")
custom.googleapis.com/tako/thumb_ms     # tako.Histogram("thumb_ms")
```

Find them in Metrics Explorer by pasting that metric type. From the
command line there is no `gcloud` surface for reading time series —
`gcloud monitoring` covers dashboards, policies and uptime checks, not
points — so read the API:

```
curl -s -G -H "Authorization: Bearer $(gcloud auth print-access-token)" \
  --data-urlencode 'filter=metric.type="custom.googleapis.com/tako/uploads"' \
  --data-urlencode "interval.startTime=$(date -u -v-1H +%Y-%m-%dT%H:%M:%SZ)" \
  --data-urlencode "interval.endTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  https://monitoring.googleapis.com/v3/projects/my-project/timeSeries
```

**Recording is still free at the call site.** `Add` takes a lock and
returns; nothing in a request path ever waits on Cloud Monitoring. A
background flush ships what accumulated **every 60 seconds**, so a
metric you just recorded takes up to a minute to appear.

**Both kinds are written cumulatively** — the running total since the
instance started, not the delta since the last flush. That is what
makes a failed flush harmless: the total lives in the instance, so the
next flush carries the truth rather than leaving a gap. The one flush
that cannot be recovered is the last one, which is why a service
[drains before it exits](#shutdown).

**Series are per instance.** Cloud Run's `cloud_run_revision` monitored
resource identifies a revision — `service_name`, `revision_name`,
`location` — and nothing narrower, so every point also carries an
`instance` metric label. Without it, two instances of one revision
would write the same cumulative series and fight over it. **Aggregate
across `instance` when you chart:** sum for counters, and for
histograms let the percentile aggregation do it.

Histogram buckets are exponential, doubling, from 1 to 262144 — one
layout for every histogram, because `tako.Histogram` takes a name and a
`float64` and no unit. Samples below 1 and above 262144 land in the
under/overflow buckets rather than being dropped. At 2× per bucket this
is honest about magnitude and coarse about precision: read it for "did
thumbnails get slower", not for a tight p99.

Cost: user-defined metrics bill on ingestion, and one instance writing
two metrics once a minute is negligible. It grows with metrics ×
instances, so it is worth a glance if you declare many metrics on a
service that scales wide.

## Shutdown

Cloud Run replaces a revision by sending the old instance `SIGTERM` and
killing it **10 seconds later**. That window is fixed — there is no API
field for it and no `gcloud` flag — so tako divides it instead:

1. The listener closes. New requests go to the new revision; this
   instance accepts nothing more.
2. Requests **already executing run to completion** — the drain ends
   the moment the last one returns, with a 7-second ceiling for the
   ones that take their time.
3. Shutdown hooks flush, which is where the final metrics write goes.
4. The process exits.

**Step 2 is adaptive, and there is no setting.** tako watches the
in-flight count rather than spending a budget: an instance that is idle
at SIGTERM — the common case, because Cloud Run stops routing to it
*before* signalling — drains in milliseconds and hands nearly the whole
window to the flush. A busy one takes what it needs, up to the ceiling,
and the flush keeps a one-second floor regardless. Time the drain does
not use is repaid, not lost.

**Locally, Ctrl-C does exactly the same, with the same 7-second
ceiling** — a hanging request behaves in dev the way it will in Cloud
Run, which is the one moment the difference would matter. An idle server
still stops instantly. A second Ctrl-C exits immediately, and that, not
a shorter local cap, is the escape hatch for a wedged handler.

A handler that outlives the ceiling is cut off, and says so rather than
leaving you to find it as a mystery 502:

```
tako: 1 request did not finish within 7s — cutting it off (SIGKILL follows in ~3s)
```

and locally, where there is no SIGKILL to name, it says where the
number came from instead:

```
⏻ 1 request did not finish within 7s — cutting it off
  (Cloud Run allows 10s from SIGTERM; the rest flushes metrics)
```

The count covers HTTP/2 as well as HTTP/1.1. That is not free:
`http.Server.Shutdown` stops tracking a connection once h2c hijacks it,
so the drain waits on tako's own counter, which sits above the
protocol.

Jobs flush on exit too, on both the success and the failure path — a
failing task's metrics are usually the ones worth having.

`tako logs [service] [--since 15m]` (plus the standard
`--project`/`--region`/`--env`) is cloud read-only: it analyzes the
app to map logical service names to deployed ones, accepts either
form, fetches up to 200 recent entries, and prints
`time service severity text` per line. No matching service lists the
real names; no entries prints `no log entries in the last 15m0s`.

## Test it

From [examples/gallery](../../examples/gallery/app_test.go) — tests
provide secrets on the app, no `.env` involved:

```go
app := tako.NewTestApp(t)
app.SetSecret("notifier-key", "nk_test_123")
```

Metrics need no test wiring: the test app carries its own sink.

## Errors you'll meet

At analysis time, verbatim:

```
app.go:7:28: tako.Secret name must be a string constant — names are read at analysis time, not run time; got variable "keyName" (tako/const-name)
```

At boot with a secret unset:
`secret "notifier-key" is not set — add it to .env (local) or Secret
Manager (cloud)`. Reading an unset secret outside boot's guarantee
(tests without `SetSecret`) panics:
`tako: secret "notifier-key" is not set — add it to .env (tako dev,
generated services) or call app.SetSecret (tests)`.

From `tako logs`: `--since wants a duration like 15m, got "soon"`, and
`no service named "web" — services: gallery, gallery-makethumb, …`.

## Not yet

- No metric labels/dimensions: `Add` and `Observe` take only a value,
  so you cannot break `uploads` down by user or status. (The `instance`
  label the exporter adds is tako's, not yours — it exists to keep
  instances from colliding, not to slice by.)
- No dashboards or alert policies are created for you; tako writes the
  metrics, you build the views.
- Histogram bucket bounds are fixed (doubling, 1 to 262144) and not
  declarable.
- `tako logs` fetches once — no `--follow`/streaming, no severity
  filter.
- No secret versioning surface: services read `latest` at boot, and
  tako will not write versions for you (by design, not a gap).
