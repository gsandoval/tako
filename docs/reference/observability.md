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

Counters and histograms use the same log-line sink in the cloud —
each recording is a structured log entry, so today `tako logs` is
where you read them. Cloud Monitoring export is a later backend
behind the same call sites.

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

- No Cloud Monitoring export — metrics are structured logs everywhere;
  no dashboards, alerts, or histogram percentiles yet.
- No metric labels/dimensions: `Add` and `Observe` take only a value.
- `tako logs` fetches once — no `--follow`/streaming, no severity
  filter.
- No secret versioning surface: services read `latest` at boot, and
  tako will not write versions for you (by design, not a gap).
