# tako.Alert — conditions, channels, and who gets woken up

An alert is two declarations: **where it goes** and **when it holds**.

```go
var (
    oncall = tako.AlertEmail("ops@example.com")

    Stuck  = tako.Alert(tako.QueueBacklogged(thumbs, 1,
        tako.QueueWatch{DeadLetters: true, For: 10 * time.Minute}), oncall)
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
| `tako.AlertEmail(addr)` | one address | none — API-created email channels are pre-verified |
| `tako.AlertWebhook(url)` | POST of Google's incident JSON | none |
| `tako.AlertTopic(topic)` | a declared `tako.Topic` | none |

`ChatSpace` is not here: it needs a real Google Chat space, so no walk
can prove it works, and an unverifiable channel is worse than an absent
one. It returns when something can test it.

`tako.Alert` takes **at least one channel as a parameter**, not a slice
and not a config field, so an alert nobody receives does not compile.
Extra channels are variadic:

```go
var Broken = tako.Alert(tako.RequestsErroring(Photos, 0.05,
    tako.Window{For: 5 * time.Minute}), oncall, room)
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
var Escalate  = tako.Alert(tako.QueueStalled(thumbs, 10*time.Minute,
    tako.QueueWatch{For: 5 * time.Minute}), tako.AlertTopic(incidents))
```

Remember what this cannot do: **your subscriber runs in your app**, so it
is unreachable in exactly the outage a page is for. Use it to route and
enrich, and keep one channel that does not depend on you.

## Presets — named situations

Each preset is an ordinary `Condition`, so it composes with `tako.Alert`
like anything else. What it adds is a **name for a failure** rather than
a name for a metric: it derives the query, the reading and the window
from what tako already knows about the thing being watched.

| condition | fires when | window |
|---|---|---|
| `QueueBacklogged(c, n, w)` | ≥ n messages are waiting | `QueueWatch{For}` — **required** |
| `QueueStalled(c, age, w)` | the oldest waiting message is older than `age` | `QueueWatch{For}` — **required** |
| `CronMissed(cron)` | the cron has not run at all | **twice its own schedule** |
| `CronFailed(cron, times)` | the cron ran and answered 5xx ≥ times | `Every: 1h` |
| `JobFailed(job, times)` | ≥ times executions finished unsuccessfully | `Every: 1h` |
| `RequestsErroring(t, fraction)` | the 5xx share exceeds `fraction` (0–1) | `For: 5m` |
| `RequestsSlow(t, d, q)` | the q-quantile of latency exceeds `d` | `For: 5m` |
| `ServiceAtCapacity(t)` | the service sits at its instance ceiling | `For: 10m` |
| `SingletonDown(loop)` | the exactly-one instance is not running | `For: 5m` |

The two queue conditions take a `QueueWatch`, which says **which** queue
as well as how long:

```go
type QueueWatch struct {
    DeadLetters bool          // the dead-letter queue, not the live one
    For         time.Duration // REQUIRED
}
```

`DeadLetters` is a field rather than a symbol because the two queues are
the same two gauges on the same kind of subscription — nothing differs
but which one. It catches **both** ways a message dies: retries
exhausted, and a handler returning `tako.Permanent`. Counting Pub/Sub's
own dead-letter forwards sees only the first, and the second is the
commoner one.

`For` is required because a queue crosses any depth for an instant every
time a burst arrives, so how long it stays there *is* the alert.

Everything but the queue pair takes an optional trailing `Window` that
overrides the default. The defaults are defaults, not a ceiling.

Three are worth reading twice:

- **`CronMissed` needs no interval.** A 6-hourly cron gets a 12-hour
  lookback and a daily one 48 hours, read from the schedule in the
  declaration — so there is no second copy of the interval to disagree
  with the first. It catches a schedule that never fired *and* a run
  that never arrived. It does **not** catch a run that arrived and
  failed: that is `CronFailed`.
- **The four queue situations are two constructors and one boolean.**
  Depth and age, on the live queue or the dead one. Both take a queue
  *or* a `Subscribe` handler, because subscribers fan out with
  independent fates and one falling behind says nothing about the others.
- **`ServiceAtCapacity` takes no threshold.** It reads `MaxInstances` off the
  service's [footprint](footprint.md), so the number lives in one place.

`SingletonDown` is the only check for a primitive whose entire promise is
that one of it exists — a singleton answers nothing but its own health
check, so no request metric can see it.

## The raw vocabulary

Presets cover what tako can name. Your own `Counter` and `Histogram`
recordings need the general form: **a reading, a comparison, and a
window.**

```go
tako.WhenAbove(uploads.Rate(), 100, tako.Window{For: 5 * time.Minute})
tako.WhenBelow(uploads.Count(), 1, tako.Window{For: 30 * time.Minute, Every: 30 * time.Minute})
tako.WhenAbove(thumbMs.Percentile(0.99), 2000, tako.Window{For: 10 * time.Minute})
tako.WhenAbsent(uploads.Count(), tako.Window{For: 26 * time.Hour, Every: time.Hour})
```

Readings are **methods on the handle they read**, not top-level
functions — spec rule 6 says no primitive owns a general concept, and
its own remedy is instance-scoped methods. `tako.Count` and
`tako.Counter` sitting one letter apart, meaning unrelated things, was
the shape being fixed.

| reading | on | answers |
|---|---|---|
| `.Rate()` | counter, histogram | increase per second, summed across instances |
| `.Count()` | counter, histogram | how many in the window |
| `.Sum()` | histogram | total of the observed values (ms of work, bytes) |
| `.Mean()` | histogram | average observed value |
| `.Percentile(q)` | histogram | the q-quantile, any q in (0,1) |

A histogram answers "how fast" and "how many" as well as "how big",
because it counts its observations. Without that you could alert that
thumbnails got slow but not that none were made, which is the louder
failure.

**The reading is mandatory, and that is the point.** Metrics are written
one cumulative series per instance, and an alert that aggregates them
the wrong way looks configured while being noise. `WhenBelow(uploads.Rate(), 1)`
asks for a rate under one *per second* — almost always true. Making
the aggregation explicit at the call site is what stops that from being
the default.

`Percentile` accepts any quantile. Cloud Monitoring's aligner enum
offers exactly four (99, 95, 50, 05); that is a property of one API, not
a fact about metrics. Compiled as PromQL,
`histogram_quantile(0.87, …)` answers on the same data where the aligner
path answers nothing.

### The metrics tako writes for you

Every service records two without being asked, and their names and
labels are **API** — pinned by `internal/metrics/testdata/lock.golden.txt`,
so a rename is a breaking change that announces itself rather than a
query that quietly stops matching.

| metric | kind | labels |
|---|---|---|
| `custom.googleapis.com/tako/requests` | CUMULATIVE INT64 | `route`, `status`, `revision` |
| `custom.googleapis.com/tako/request_ms` | CUMULATIVE DISTRIBUTION | `route`, `revision` |

`route` is the declared PATTERN with its method — `GET /api/photos/{id}`,
never the concrete path, which would mint one series per photo. A request
no pattern matched records `<unmatched>`. `status` is a CLASS and the set
is closed: `1xx`, `2xx`, `3xx`, `4xx`, `5xx` — a query asking for `500`
or `error` is well-formed and matches nothing.

They are written against the `generic_task` monitored resource (Cloud
Run's own is not writable for user-defined metrics), scoped by
`namespace` = `<app>-<env>` and `job` = the tako service name. That
`namespace` is why a matcher over these cannot be written correctly at
declaration time — the value does not exist until a deploy picks an env
— and why the readers below take a tako object instead of a string.

Cloud Run publishes `request_count` and `request_latencies` of its own.
They carry a `route` label that is **never populated** for an ordinary
service — measured across 88 series, empty on every one — so alerting
per endpoint is impossible from platform metrics alone. That is why tako
writes its own.

### Google's own metrics

The metric type is Google's, so it is a string. **Which resource it is
about is not.**

```go
tako.ServiceMetric(Grind, "run.googleapis.com/container/memory/utilizations")
tako.QueueMetric(work, "pubsub.googleapis.com/subscription/push_request_count")
tako.PlatformMetric("pubsub.googleapis.com/topic/byte_cost", `topic_id="prod-incidents"`)
```

The first two take a tako object and write the resource matcher from it.
That is not sugar: the physical name is env-prefixed and only exists
after a deploy, so writing it by hand at declaration time is impossible.
A bare `service_name="siren"` matches nothing; omitting it matches every
Cloud Run service in the project. Both read as healthy.

`PlatformMetric` is the escape hatch for a project-wide reading or a
resource tako does not model.

Optional trailing strings are extra label matchers, verbatim PromQL —
**except on a label tako already writes**, which is refused at analysis
time (`tako/scoped-label`). `ServiceMetric` and `ServicePercentile` own
`service_name`, `QueueMetric` owns `subscription_id`, `ServiceInstances`
owns both plus `state`, and every selector owns `monitored_resource`,
which is derived from the metric type. PromQL would take a second
matcher on the same label and satisfy neither, so the query would come
out well-formed and permanently empty — which reads exactly like a
healthy system. Reaching the dead-letter subscription is the case people
try this for; use `QueueWatch{DeadLetters: true}` instead.

`WhenAbsent` works over these too, and asks a genuinely different
question from a low reading: whether the metric is being written at all.

```go
tako.WhenAbsent(tako.ServiceInstances(Grind), tako.Window{For: 10 * time.Minute})
```

A service that has stopped reporting an instance count is either gone or
not being scraped, and neither shows up as a small number.
There is no list of metric names in these docs on purpose: a project
publishes upwards of eight thousand descriptors, so any list here would
be both incomplete and stale. Search for one instead:

```
$ tako metrics utilizations --project my-project

✓ run.googleapis.com/container/cpu/utilizations
    DELTA DISTRIBUTION · on cloud_run_job, cloud_run_revision, cloud_run_worker_pool
    Container CPU utilization distribution across all container instances.
    tako.ServicePercentile(svc, "run.googleapis.com/container/cpu/utilizations", 0.99)

✓ nameable — a policy can be created against it today
```

It prints the constructor because the metric's SHAPE decides it — a
DISTRIBUTION has no bare name, so `ServiceMetric` on one produces a
policy Cloud Monitoring refuses. And it marks whether the name is
resolvable in your project today, because a policy naming something
Cloud Monitoring cannot see is refused at creation.

## Windows

```go
type Window struct {
    For   time.Duration // how long the breach must persist
    Every time.Duration // the period each series is reduced into
}
```

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

### The same trap, everywhere else

**An empty result set satisfies no comparison.** That one sentence has
been the diagnosis three times in this feature, and it is worth knowing
as a rule rather than meeting as a surprise: any condition meaning "less
than expected" is silent when the metric stops, unless something makes
the missing case explicit.

`WhenBelow` handles it for you. It compiles

```promql
(sum(rate(…)) or vector(0)) < 0.01
```

— `or vector(0)` being PromQL's idiom for reading missing data as zero.
So below covers both "slowed down" and "stopped". `SingletonDown` is a
below and gets the same treatment, which matters more than it sounds:
before this, a deleted service stopped publishing an instance count, so
the alert fired for a few minutes and then **resolved itself while the
service was still gone**.

The cost is worth stating: a metric nothing has ever written reads as
zero, so a fresh deploy with no traffic fires once `Window.For` elapses.
That is usually correct — there really is no traffic — and `For` is the
knob for it.

`WhenAbsent` remains the sharper tool when zero is a legitimate reading
and only the series vanishing is news.

One consequence to know: **an absence has no series, so nothing can be
read off its labels.** Google's own default subject appends the
resource that fired and prints `__missing__` there, and any `${…}`
variable in a policy's documentation resolves to `null` for the same
reason. Tako sidesteps both by writing the subject and the body itself,
at deploy time, out of what it already knows:

```
[ALERT - Warning] gallery/prod: cron Cleanup has not run
```

## What the notification says

Cloud Monitoring writes its own body for a PromQL condition, and that
body is `A PromQL query was observed at 1`. True, and no help at 3am: it
knows a query returned a number, and nothing about what the number
meant.

Tako knows the rest at deploy time, so it writes the body:

```
Subject: [ALERT - Warning] gallery/prod: cron Cleanup has not run

**No run of cron `Cleanup` has reached `gallery` in the last 48h.**

Either the schedule never fired, or it fired and the service never
answered. A run that *did* arrive and then failed is a different alert
(`CronFailed`) — this one means nothing arrived at all.

Schedule: every 24h. Run it by hand:

    gcloud scheduler jobs run gallery-cron-cleanup --project my-project --location us-central1

---
app `gallery` · env `prod` · declared in tako as `Silent` · service `gallery`

This policy is generated by `tako deploy` from the declaration above.
Editing it in the console works until the next deploy overwrites it —
change the declaration instead.

Quick Links:  Cloud Scheduler · Service logs
```

Each preset writes its own: `DeadLettered` prints the `tako dlq` peek
and drain commands and links the subscription; `Backlogged` says it
fires *before* anything gives up; `AtCapacity` names the ceiling it hit
and the three knobs that raise it; `SingletonDown` tells you to look for
a crash loop.

Two deliberate choices in there. The command names the **deployed**
scheduler job, derived by the same function the scheduler reconciler
uses — a name derived twice is a name that can be derived differently.
And the last paragraph exists because these policies are reconciled: a
console edit survives exactly until the next deploy, which is worth
knowing before you make one.

Links land in the email's **Quick Links** section; Google caps them at
three. A dead-letter alert links the **subscription that consumes the
queue**, not the queue: a `tako.Queue` deploys as a Pub/Sub *topic*, and
the subscription draining it is named after its consumer, because
subscribers fan out and each needs its own.

## What alerts can watch

Requests, latency and error rates are read from **tako's own
per-endpoint metrics**, not Cloud Run's `request_count`. Cloud Run
publishes a `route` label and leaves it empty on every series, so its
metric cannot answer a question about one endpoint at all. Tako writes
`route` and `status` itself — see
[observability.md](observability.md#the-two-metrics-you-never-declare) —
which is what makes `Erroring(oneEndpoint, …)` mean one endpoint.

`RequestsErroring`, `RequestsSlow` and `ServiceAtCapacity` take a
`Target`: a
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

## Reading what you wrote

`tako alerts` prints what every declared alert actually compiles to. It
touches no cloud — an alert is a property of the code, and only the
env-prefixed names inside it depend on where it would deploy, which is
what `--env` selects.

```
$ tako alerts Slowing --env prod

Slowing · shop/prod: GET /slow is slow
  when   evaluated every 1m0s, sustained 2m0s
  query  histogram_quantile(0.99, sum(rate(custom_googleapis_com:tako_request_ms_bucket{monitored_resource="generic_task",namespace="shop-prod",job="shop",route="GET /slow"}[60s])) by (le)) > 2000
  notify oncall
  from   tako.RequestsSlow
```

The `from` line is the point. A preset is a starting point, not a
ceiling: read what it compiles to, then write your own version with the
threshold, window or label you actually wanted. You cannot fork what you
cannot identify.

The query is printed whole and unwrapped so it can be pasted into the
metrics explorer and run as-is.

## Errors you'll meet

At analysis time, verbatim:

```
app.go:15:26: tako.AlertEmail takes a string constant — alert destinations are read at analysis time, not run time; got a call expression (tako/const-name)
app.go:28:41: tako.CronMissed watches a cron, but alerts.Work is a worker (tako/alert-subject)
app.go:31:66: tako.RequestsSlow quantile must be between 0 and 1 exclusive — 0.99 is the 99th percentile, not 99; got 99 (tako/quantile)
app.go:32:57: tako.Percentile quantile must be between 0 and 1 exclusive — 0.99 is the 99th percentile, not 99; got 99 (tako/quantile)
app.go:35:52: tako.Alert destinations must be channels declared as package-level vars in this module (tako.AlertEmail, tako.AlertWebhook, tako.AlertTopic) (tako/channel-binding)
app.go:47:55: a Window's Every must be between 1m and 25h — Cloud Monitoring refuses anything else; got 30s (tako/window)
app.go:48:55: a threshold Window's For cannot exceed 24h — Cloud Monitoring refuses longer; got 172800s (tako/window)
app.go:49:56: a Count reading needs a period — write tako.Window{Every: …}; how long you are counting over IS the count, so there is no default worth guessing (tako/window)
app.go:56:60: tako.QueueBacklogged needs a sustain window — write tako.QueueWatch{For: …}; a queue crosses any depth for an instant, so how long it stays there IS the alert (tako/window)
app.go:64:4: tako.QueueMetric already matches subscription_id for the consumer you named — a second matcher on the same label matches nothing, and the deployed subscription is env-prefixed so it cannot be written by hand; for the dead-letter subscription use tako.QueueBacklogged or tako.QueueStalled with tako.QueueWatch{DeadLetters: true} (tako/scoped-label)
app.go:68:4: tako.ServiceMetric already matches service_name for the target you named — a second matcher on the same label matches nothing, and the deployed name is env-prefixed so it cannot be written by hand; drop it (tako/scoped-label)
app.go:72:4: tako.PlatformMetric already matches monitored_resource — it is derived from the metric type, and a second matcher on the same label matches nothing; drop it (tako/scoped-label)
```

`tako/scoped-label` is refused rather than merged because PromQL accepts
both matchers and satisfies neither: `{subscription_id="x",
subscription_id="x-dead"}` is a well-formed selector that returns no
series, which is indistinguishable from a healthy queue.

`tako.Alert` with something that is not a condition is a compile error
before the analyzer sees it — `Condition` is not constructible outside
the package. The same is true of a reading on the wrong handle: only a
histogram has `.Percentile`.

## Not yet

- **No log-based alerting.** Conditions read metrics only, so "page me
  when this line appears" has no expression.
- **No local evaluation**, by design (above).
- **No severity or routing rules.** Every policy is created `WARNING`,
  and every channel on an alert gets every incident — there is no
  escalation ladder and no per-condition severity.

- **No dashboards.**
- No `tako.ServiceLatency`/`ServiceInstances`/`ServiceSaturation`
  shorthands for the common platform metrics — write them as
  `tako.ServiceMetric` for now.
