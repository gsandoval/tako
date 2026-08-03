# tako.Alert — conditions, channels, and who gets woken up

An alert is two declarations: **where it goes** and **when it holds**.

```go
var (
    oncall = tako.Email("ops@example.com")

    Stuck  = tako.Alert(tako.DeadLettered(thumbs, 1), oncall)
    Silent = tako.Alert(tako.CronMissed(Cleanup), oncall)
)
```

Both are package-level vars, like every other tako declaration, and the
**variable name is the identity** — `Stuck` deploys as the policy
`<env>-Stuck`. Nothing here runs in a request path and nothing runs
locally: alerts are evaluated and delivered by Cloud Monitoring. That is
deliberate. An alert routed through your own service goes quiet exactly
when the thing it watches has gone wrong.

## Channels

| constructor | delivers to | human step first? |
|---|---|---|
| `tako.Email(addr)` | one address | none — API-created email channels are pre-verified |
| `tako.ChatSpace("spaces/AAAA1234")` | a Google Chat space | install the Cloud Monitoring app in the space |
| `tako.Webhook(url)` | POST of Google's incident JSON | none |
| `tako.PubSub(topic)` | a declared `tako.Topic` | none |

`tako.Alert` takes **at least one channel as a parameter**, not a slice
and not a config field, so an alert nobody receives does not compile.
Extra channels are variadic:

```go
var Broken = tako.Alert(tako.Erroring(Photos, 0.05), oncall, room)
```

### Pub/Sub destinations

The message body is **Google's incident JSON, not a tako type** — tako
does not own that shape, because a payload frozen into the SDK would
turn a Google change into an API break. Declare a topic over the fields
you want:

```go
type Incident struct {
    Incident struct {
        IncidentID    string `json:"incident_id"`
        PolicyName    string `json:"policy_name"`
        ConditionName string `json:"condition_name"`
        State         string `json:"state"` // "open" | "closed"
        StartedAt     int64  `json:"started_at"`
        Summary       string `json:"summary"`
        URL           string `json:"url"`
    } `json:"incident"`
}

var incidents = tako.Topic[Incident]("incidents")
var Escalate  = tako.Alert(tako.Backlogged(thumbs, 10*time.Minute), tako.PubSub(incidents))
```

Remember what this cannot do: **your subscriber runs in your app**, so it
is unreachable in exactly the outage a page is for. Use it to route and
enrich, and keep one channel that does not depend on you.

## Presets — named situations

Each preset is an ordinary `Condition`, so it composes with `tako.Alert`
like anything else. What it adds is a **name for a failure** rather than
a name for a metric: it derives the query, the reading and the window
from what tako already knows about the thing being watched.

| preset | fires when | default window |
|---|---|---|
| `DeadLettered(c, n)` | ≥ n messages gave up (retries exhausted) | `Every: 5m` |
| `Backlogged(c, age)` | the oldest unacked message is older than `age` | `For: 5m` |
| `CronFailed(cron, times)` | the cron ran and answered 5xx ≥ times | `Every: 1h` |
| `CronMissed(cron)` | the cron has not run at all | **twice its own schedule** |
| `JobFailed(job, times)` | ≥ times executions finished unsuccessfully | `Every: 1h` |
| `Erroring(t, fraction)` | the 5xx share exceeds `fraction` (0–1) | `For: 5m` |
| `SlowerThan(t, d, q)` | the q-quantile of latency exceeds `d` | `For: 5m` |
| `AtCapacity(t)` | the service sits at its instance ceiling | `For: 10m` |
| `SingletonDown(loop)` | the exactly-one instance is not running | `For: 5m` |

Every one takes an optional trailing `Window` that overrides the
default. The defaults are defaults, not a ceiling.

Three of them are worth reading twice:

- **`CronMissed` needs no interval.** A 6-hourly cron gets a 12-hour
  lookback and a daily one 48 hours, read from the schedule in the
  declaration — so there is no second copy of the interval to disagree
  with the first. It catches a schedule that never fired *and* a run
  that never arrived. It does **not** catch a run that arrived and
  failed: that is `CronFailed`.
- **`DeadLettered` and `Backlogged` are a pair.** The first only fires
  *after* retries are spent, so a consumer that is merely down shows
  nothing there for a while; the second is what catches it. Both take a
  queue *or* a `Subscribe` handler, because subscribers dead-letter
  independently and one giving up says nothing about the others.
- **`AtCapacity` takes no threshold.** It reads `MaxInstances` off the
  service's [footprint](footprint.md), so the number lives in one place.

`SingletonDown` is the only check for a primitive whose entire promise is
that one of it exists — a singleton answers nothing but its own health
check, so no request metric can see it.

## The raw vocabulary

Presets cover what tako can name. Your own `Counter` and `Histogram`
recordings need the general form: **a reading, a comparison, and a
window.**

```go
tako.Above(tako.Rate(uploads),  100, tako.For(5*time.Minute))
tako.Below(tako.Count(uploads), 1,   tako.Window{For: 30*time.Minute, Every: 30*time.Minute})
tako.Above(tako.Percentile(thumbMs, 0.99), 2000, tako.For(10*time.Minute))
tako.Absent(tako.Count(uploads), tako.For(26*time.Hour))
```

Readings:

| reading | takes | answers |
|---|---|---|
| `Rate(r)` | counter or histogram | increase per second, summed across instances |
| `Count(r)` | counter or histogram | how many in the window |
| `Sum(h)` | histogram | total of the observed values (ms of work, bytes) |
| `Mean(h)` | histogram | average observed value |
| `Percentile(h, q)` | histogram | the q-quantile, any q in (0,1) |
| `Platform(type, filters…)` | a Google metric type | whatever it measures |

`Rate` and `Count` take **either** recording, because "how fast" and
"how many" are the same question whichever you ask — a histogram counts
its observations. Without that you could alert that thumbnails got slow
but not that none were made, which is the louder failure.

**The reading is mandatory, and that is the point.** Metrics are written
one cumulative series per instance, and an alert that aggregates them
the wrong way looks configured while being noise. `Below(Rate(uploads),
1)` asks for a rate under one *per second* — almost always true. Making
the aggregation explicit at the call site is what stops that from being
the default.

`Percentile` accepts any quantile. Cloud Monitoring's aligner enum
offers exactly four (99, 95, 50, 05); that is a property of one API, not
a fact about metrics. Compiled as PromQL,
`histogram_quantile(0.87, …)` answers on the same data where the aligner
path answers nothing.

### Platform metrics

`tako.Platform` names a Google metric directly, for what the presets do
not cover. The name is Google's, so it is a string — pretending
otherwise would be a lie — but it must be constant, and tako derives the
rest: the PromQL name mapping and the monitored-resource matcher a bare
selector is rejected without.

```go
tako.Above(tako.Platform("run.googleapis.com/container/network/sent_bytes_count"),
    1<<30, tako.For(10*time.Minute))
```

Optional trailing strings are extra label matchers, verbatim PromQL:
`tako.Platform("run.googleapis.com/container/cpu/utilizations", `state="active"`)`.

There is no list of names in these docs on purpose — a project publishes
upwards of eight thousand descriptors, so any list here would be both
incomplete and stale.

## Windows

```go
type Window struct {
    For   time.Duration // how long the breach must persist
    Every time.Duration // the period each series is reduced into
}
```

`tako.For(d)` is the short spelling of `Window{For: d}`.

`Every` is the rate window for `Rate`, the counting window for `Count`,
the bucket a `Percentile` is computed over. Leaving it zero derives
60s for rate, ratio and percentile readings, where the period only
smooths — **except for `Count`, where the period IS the count**. There
is no defensible default for that, so leaving it zero is an analysis
error rather than a silent guess.

On an `Absent` condition, `For` is the **lookback** instead — "no data
for 26 hours" — because absence has nothing to sustain.

The knobs are exposed rather than hidden because which period a metric
is reduced over changes what the alert *means*.

## Absence

`tako.Absent` compiles to PromQL's `absent_over_time`, not to Cloud
Monitoring's `conditionAbsent`, which caps at 23h30m — shorter than a
daily cron's own period, so it could never watch one. A PromQL lookback
has no such ceiling: `[26h]` and `[7d]` are both accepted.

If you are tempted to write the same thing as `increase(x[26h]) == 0`:
it parses, it is accepted, and it never fires. With no data there are no
series to compare against zero, so the expression yields nothing —
where `absent_over_time` yields exactly one series. Measured, not
assumed.

One consequence to know before you are woken at 3am: **an absence
notification cannot name a resource**, because there is no data to carry
labels. Google prints `__missing__` where the instance would go. The
condition's own sentence is what makes it readable, so tako writes one:

```
[ALERT - WARNING] gallery/prod: cron Cleanup has not run on __missing__
```

## What alerts can watch

Requests, latency and error rates are read from **tako's own
per-endpoint metrics**, not Cloud Run's `request_count`. Cloud Run
publishes a `route` label and leaves it empty on every series, so its
metric cannot answer a question about one endpoint at all. Tako writes
`route` and `status` itself — see
[observability.md](observability.md#the-two-metrics-you-never-declare) —
which is what makes `Erroring(oneEndpoint, …)` mean one endpoint.

`Erroring`, `SlowerThan` and `AtCapacity` take a `Target`: a
`tako.Service`, one async handler, a single `Api`/`Page` route, or a
`Site`.

## Local

`tako dev` lists declared alerts and evaluates none. An evaluator with
different windowing than Cloud Monitoring's would be worse than none,
because it would disagree with production about when you get woken up.

## Cloud

Policies and channels are reconciled like every other object: created,
updated in place when the spec hash changes, and deleted by
`tako destroy` for that env. The policy's display name carries the tako
identity — **not a label**, because GCP label values forbid uppercase
and a tako identity is an exported Go name.

Deploy needs two roles beyond the usual set;
[tako-cloud-permissions.md](../tako-cloud-permissions.md) has the full
table.

Alerting also requires the metric to **exist** before a policy can name
it, and a custom metric does not exist until something writes it — so
deploy declares the descriptors and seeds one zero point per metric.
Without that, alerting would work on the second deploy and not the
first.

## Errors you'll meet

At analysis time, verbatim:

```
app.go:15:21: tako.Email takes a string constant — alert destinations are read at analysis time, not run time; got a call expression (tako/const-name)
app.go:28:41: tako.CronMissed watches a cron, but alerts.Work is a worker (tako/alert-subject)
app.go:31:64: tako.SlowerThan quantile must be between 0 and 1 exclusive — 0.99 is the 99th percentile, not 99; got 99 (tako/quantile)
app.go:32:59: tako.Percentile quantile must be between 0 and 1 exclusive — 0.99 is the 99th percentile, not 99; got 99 (tako/quantile)
app.go:35:52: tako.Alert destinations must be channels declared as package-level vars in this module (tako.Email, tako.ChatSpace, tako.Webhook, tako.PubSub) (tako/channel-binding)
app.go:47:55: a Window's Every must be between 1m and 25h — Cloud Monitoring refuses anything else; got 30s (tako/window)
app.go:48:55: a threshold Window's For cannot exceed 24h — Cloud Monitoring refuses longer; got 172800s (tako/window)
app.go:49:56: tako.Count needs a period — write tako.Window{Every: …}; how long you are counting over IS the count, so there is no default worth guessing (tako/window)
```

`tako.Alert` with something that is not a condition is a compile error
before the analyzer sees it — `Condition` is not constructible outside
the package.

## Not yet

- **No log-based alerting.** Conditions read metrics only, so "page me
  when this line appears" has no expression.
- **No local evaluation**, by design (above).
- **No severity or routing rules.** Every policy is created `WARNING`,
  and every channel on an alert gets every incident — there is no
  escalation ladder and no per-condition severity.
- **No `tako metrics` command yet**, so finding a `Platform` metric type
  means the Metrics Explorer or the `metricDescriptors` API.
- **No dashboards.**
- No `tako.Latency`/`Instances`/`Saturation` shorthands for the common
  platform metrics — write them as `tako.Platform` for now.
