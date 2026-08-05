# validate/alerts.md — Y1: an alert fires when it should, and is quiet when it should

The claim has **two halves, and the second one is the whole gate**:

- **Y1a** — a declared alert becomes a Cloud Monitoring policy and a
  notification channel, and delivers to a real destination.
- **Y1b** — the condition is **quiet while the thing it watches is
  healthy**, and fires only once it is not.

> **Read this before running anything.** The first attempt at this gate
> passed Y1a, watched an email arrive, and recorded a pass. The
> condition was a permanent false positive: the query filtered
> `job="x1-gallery"` and the app wrote `job="gallery"`, so it matched
> nothing and `absent_over_time` was true forever. **A firing alert and
> a correct alert are indistinguishable from an inbox.** Only §5 tells
> them apart, and its result arrived after the verdict had been written.
>
> §5 is not optional and does not run last for convenience. Run it
> **before** you wait for absence, while the subject is provably alive.

**Setup:** run the CLI as `go run ../../cmd/tako …` from
`examples/gallery`, which declares the two alerts this walk uses:

```go
oncall = tako.AlertEmail("<your-address>")
Stuck  = tako.Alert(tako.QueueBacklogged(thumbs, 1,
    tako.QueueWatch{DeadLetters: true, For: 10 * time.Minute}), oncall)
Silent = tako.Alert(tako.CronMissed(Cleanup, tako.Window{For: 5 * time.Minute}), oncall)
```

`Silent` overrides the default lookback down to 5 minutes so the walk
takes ten minutes instead of two days. `PROJECT` is the scratch project.

## 1. Offline truth first

```
go test ./...
go test ./pkg/analyzer -run TestErrors/alerts
go test ./pkg/provision -run 'Alert|Preset|JobLabel|Cron'
go test ./pkg/runtime  -run 'WiresTheRequestSink|Route|Request'
```

→ green, and specifically these four, each of which stands for a defect
that reached the cloud once:

- `TestJobLabelMatchesWhatTheServiceWrites` — the query's `job` matcher
  is a value some service actually writes. **This is the false positive
  above, caught offline.**
- `TestBootLocalWiresTheRequestSink` / `TestEveryBootPathWiresTheRequestSink`
  — boot wires the per-endpoint sink. Without it every `tako/requests`
  record is dropped and every route-scoped alert is dead.
- `TestEveryPresetsDefaultWindowSurvivesToTheDeploy` — each preset's
  documented default window is the one the deploy applies. SDK defaults
  are runtime values; the deploy reads source, so they can silently
  differ.
- `TestEveryReconciledKindIsApplied` (`./cmd/tako`) — a kind can be
  planned, printed, and never applied. Both alert kinds were, once.

## 2. Deploy

```
PROJECT=<test-project>
printf 'nk_y1' | gcloud secrets create x1-notifier-key --data-file=- --project $PROJECT
go run ../../cmd/tako deploy --project $PROJECT --env x1 --yes
```

Expect in the plan: `+ create alert channel oncall`,
`+ create alert policy Stuck`, `+ create alert policy Silent` — and
expect them in the **result**, not only the plan. Confirm against the
API rather than the deploy's own summary:

```
policies() {
  curl -s -H "Authorization: Bearer $(gcloud auth print-access-token)" \
    "https://monitoring.googleapis.com/v3/projects/$PROJECT/alertPolicies" \
  | python3 -c "
import json,sys
for p in json.load(sys.stdin).get('alertPolicies',[]):
    print('===', p['displayName'], 'severity=', p.get('severity'))
    for c in p['conditions']:
        q = c.get('conditionPrometheusQueryLanguage', {})
        print('  cond :', c['displayName'])
        print('  query:', q.get('query'))
    d = p.get('documentation', {})
    print('  subject:', d.get('subject'))
    print('  links  :', [l['displayName'] for l in d.get('links', [])])
    print('  body   :', (d.get('content') or '<<< NONE — the mail will say \"A PromQL query was observed at 1\" >>>')[:200])
"
}
policies
```

Six things in that output, each a defect a gate found:

1. **Two policies and a channel exist at all.** Planned-but-never-applied
   exits 0 and creates nothing.
2. **`severity = WARNING`**, not empty — an unset severity mails as
   `[ALERT - No severity]`.
3. **The condition displayName is a sentence**, not a Go variable:
   `gallery/x1: cron Cleanup has not run`. It is what the notification's
   subject line is built from, read at 3am.
4. **The tako identity is on `displayName`** (`x1-Silent`), never a
   label — GCP label values forbid uppercase and tako identities are
   exported Go names.
5. **`documentation.content` is not empty.** Empty means the mail falls
   back to Google's `A PromQL query was observed at 1`.
6. **`documentation.subject` is set**, which is what removes the
   ` on __missing__` tail from the subject line.

## 3. Fire the subject

```
gcloud scheduler jobs run x1-gallery-cron-cleanup --project $PROJECT --location us-central1
```

**That command exits 0 when the trigger is ACCEPTED, not when the run
succeeds.** On a fresh deploy the invocation that follows is usually a
403 while the scheduler's invoker grant propagates, and the exit code
says nothing about it. Verify the run landed:

```
gcloud scheduler jobs describe x1-gallery-cron-cleanup \
  --project $PROJECT --location us-central1 --format='value(status.code)'
```

Empty is success. `7` is PERMISSION_DENIED — retry, for up to a few
minutes. Then confirm the service answered rather than trusting the
scheduler's own view:

```
gcloud logging read 'resource.labels.service_name="x1-gallery" httpRequest.requestUrl:"/_tako/api/cron/Cleanup"' \
  --project $PROJECT --limit 3 --freshness=20m --format='value(timestamp,httpRequest.status)'
```

This is not pedantry. A walk that skipped it reported §5 as FIRING and
looked like a feature defect; the cron had 403'd, so **the alert was
correct** and the gate script was wrong — it had checked an exit code
where it needed a status. Which is the same mistake, one level up, that
this whole walk exists to catch.

## 4. Wait for the flush

Three minutes: recording is in-process, ships on a **60s** timer, and
ingestion adds its own lag. A read before that finds nothing, and
nothing is exactly what an absence condition is looking for — so
reading too early manufactures the false positive this gate exists to
catch.

## 5. THE QUIET HALF — run the policy's own query

Ask the query the policy is actually storing (paste it from §2's output,
do not retype it):

```
Q='absent_over_time(custom_googleapis_com:tako_requests{monitored_resource="generic_task",namespace="gallery-x1",job="gallery",route="/_tako/api/cron/Cleanup"}[300s])'

ask() {
  curl -s -H "Authorization: Bearer $(gcloud auth print-access-token)" \
    "https://monitoring.googleapis.com/v1/projects/$PROJECT/location/global/prometheus/api/v1/query" \
    --data-urlencode "query=$Q" \
  | python3 -c "
import json,sys
r = json.load(sys.stdin).get('data',{}).get('result',[])
print('FIRING (absent)' if r else 'QUIET (data present)')
"
}
ask
```

→ **`QUIET (data present)`**.

**If this says FIRING while the cron is alive, stop. The feature is
broken**, whatever any email says. Do not continue to §7, and do not
write a verdict row.

Then look at the series themselves — not the count, the **labels**:

```
curl -s -G -H "Authorization: Bearer $(gcloud auth print-access-token)" \
  --data-urlencode 'filter=metric.type="custom.googleapis.com/tako/requests"' \
  --data-urlencode "interval.startTime=$(date -u -v-1H +%Y-%m-%dT%H:%M:%SZ)" \
  --data-urlencode "interval.endTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  "https://monitoring.googleapis.com/v3/projects/$PROJECT/timeSeries" \
| python3 -c "
import json,sys
for t in json.load(sys.stdin).get('timeSeries',[]):
    m, r = t['metric'].get('labels',{}), t['resource'].get('labels',{})
    print(f\"route={m.get('route','')!r} status={m.get('status','')!r} job={r.get('job','')!r} ns={r.get('namespace','')!r} task={r.get('task_id','')!r}\")
"
```

Require, explicitly:

- a series with **`route='/_tako/api/cron/Cleanup'`** — a populated route
  label, the thing Cloud Run's own metric never has;
- its **`job`** equal to the value the query filters on;
- more than one route present once the app has served requests, which
  proves the label is per-endpoint rather than a constant.

A descriptor that *declares* `route` proves nothing. The first gate
confirmed the schema and never checked a value — every one of the 88
series it looked at had `route=''`.

## 6. Deliver

The channel needs no verification step: email channels created through
the API come back with no `verificationStatus` and deliver immediately.
Confirm the channel exists and is enabled:

```
curl -s -H "Authorization: Bearer $(gcloud auth print-access-token)" \
  "https://monitoring.googleapis.com/v3/projects/$PROJECT/notificationChannels" \
| python3 -c "
import json,sys
for c in json.load(sys.stdin).get('notificationChannels',[]):
    print(c['displayName'], c['type'], c['labels'], 'enabled=', c.get('enabled'))
"
```

## 7. THE FIRING HALF

Let the 5-minute window pass with **no further cron runs** (seven
minutes is comfortable), then ask again:

```
ask
```

→ **`FIRING (absent)`**, and within a couple of minutes the mail:

```
[ALERT - Warning] gallery/x1: cron Cleanup has not run
```

No ` on __missing__` tail — tako sets the policy's `documentation.subject`,
which replaces Google's generated subject line. (An earlier version of
this walk called that tail unavoidable. It is unavoidable in the
*default* subject; the field exists.)

**Read the body, not just the subject.** It must say what broke, what it
is not, and what to run:

```
**No run of cron `Cleanup` has reached `x1-gallery` in the last 5m.**

Either the schedule never fired, or it fired and the service never answered.
A run that *did* arrive and then failed is a different alert (`CronFailed`) …

    gcloud scheduler jobs run x1-gallery-cron-cleanup --project <p> --location us-central1
```

with a **Quick Links** section carrying *Cloud Scheduler* and *Service
logs*. If the body instead reads `A PromQL query was observed at 1`, the
documentation did not reach the policy — check §2's readback.

**Paste the scheduler command and run it.** It is the one line in the
mail a responder will act on without thinking, so a wrong name here is a
worse failure than a missing body. It must run the job, not 404.

## 8. Destroy clean

```
echo "gallery" | go run ../../cmd/tako destroy --project $PROJECT --env x1
gcloud secrets delete x1-notifier-key --project $PROJECT --quiet
policies    # → empty
```

Both policies and the channel go. **Metric descriptors and series stay**
— they are data with a retention period, and tako never deletes data
(§4.6a). Say so in the verdict rather than reading leftover descriptors
as an incomplete destroy.

## 7b. Click the links

Not optional, and not something a unit test can stand in for. Two of the
four console link shapes in the first version were wrong: Cloud Run's
detail pages moved under `/observability/`, so
`/run/detail/<region>/<svc>/logs` answers **URL not found**; and the
Pub/Sub link named the queue, which is a *topic*, so it answered
**Unable to find the resource you requested**.

That second one was not just a bad link. The alert's own query carried
the same wrong name in `subscription_id`, so `QueueBacklogged` matched
nothing and could never fire — found by opening the link, after the
policy had been created, read back, and gated.

Open every link the mail carries. Each must land on the named resource,
not a 404 and not a list page you have to search. Then **paste the
command from the body and run it** — it must run the job.

## Known gaps in this walk

- **`QueueBacklogged` sees retry exhaustion only.** Proven firing in Z2
  (quiet → `value=1`), driven by a `text/plain` upload that fails five
  times until Pub/Sub forwards it. It is **blind to `tako.Permanent`**:
  the runtime publishes those to the dead topic itself and ACKs, so
  Pub/Sub records a successful delivery and the metric never moves. A
  walk that poisons with an undecodable image will watch a dead letter
  arrive and the alert stay quiet — correctly, for now. Fix proposed and
  awaiting approval (`task-log.md`, Z2 DX proposal).
- **This preset's metric has no series until the first forward.**
  Distinguish *no series* from *series at zero*; only the second means
  "measured, and nothing is wrong". Allow ~5 minutes.
- **Nothing detects Google moving a console page again.** The link
  shapes are verified by hand, here, once per walk.
- **"Fires exactly once" is not asserted.** Cloud Monitoring has no
  public v3 incident-listing API, so there is nothing to count. Running
  the policy's own query at both moments (§5, §7) is the honest proxy.

## Verdict

Record in `docs/validation-log.md`. A row is only a pass if **§5 said
QUIET and §7 said FIRING** — one without the other is not a partial
pass, it is no evidence at all.
