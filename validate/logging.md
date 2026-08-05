# validate/logging.md — P1: logs arrive as records, not as text

The claim: a **deployed** tako service writes Cloud Logging records —
`severity` that filters, a `httpRequest` that renders as a request, and
a trace that nests a handler's lines under the request that caused them
— while **local** output does not change at all.

The thing to disprove: before this slice every line landed as severity
`DEFAULT`, so an error and an info line were indistinguishable once
they arrived.

**Setup:** run the CLI as `go run ../../cmd/tako …` from
`examples/gallery`. `<test-project>` is the scratch project
(tako-test-01). Export `PROJECT=<test-project>` first — several
commands below use it.

## 1. Unit truth first

```
go test ./...
go test ./pkg/runtime -run 'TestSeverity|TestCloud|TestRequest|TestNoTrace|TestParseTrace|TestLocal|TestLogRequests' -v
```

→ green, and specifically:

- `TestSeverityUsesCloudLoggingNames` — slog says `WARN`, Cloud Logging
  wants `WARNING`. This is the rename the whole slice exists for.
- `TestCloudLinesCarryTheRenamedKeys` — `level`/`msg` are gone,
  `severity`/`message` are there, and the handler's own attributes
  survived.
- `TestRequestLoggerCorrelatesWithTheTrace` — the exact
  `projects/P/traces/T` string.
- `TestNoTraceKeyWithoutCloudRun` — no project, no trace key. A trace
  field pointing at nothing is worse than none.
- `TestLocalRequestLogStaysTheDevLine` — the half that must NOT change.

## 2. Local: nothing changed

```
go run ../../cmd/tako dev
```

In another terminal:

```
curl -s -o /dev/null localhost:8080/api/photos
```

**Observe** in the dev console — the terse line, exactly as before, no
JSON anywhere:

```
GET /api/photos → 200 (14.2ms)
```

If you see a JSON line here, the slice broke the dev console and the
gate fails.

## 3. Cloud: deploy

```
go run ../../cmd/tako deploy --project $PROJECT --env logging
```

Keep `--env logging` on every command below; services are
`logging-gallery`, `logging-gallery-makethumb`, and so on.

## 4. Produce an INFO, an ERROR, and a 4xx

```
URL=$(gcloud run services describe logging-gallery --project $PROJECT \
  --region us-central1 --format 'value(status.url)')

# a good request (INFO) — build a real PNG first if you have none:
#   printf '\x89PNG\r\n\x1a\n' > /tmp/tiny.png  (see validate/metrics.md §2a
#   for one Go actually decodes)
curl -s -o /dev/null -w "%{http_code}\n" -F caption=hello -F img=@/tmp/big.png "$URL/api/photos"

# a miss (WARNING)
curl -s -o /dev/null -w "%{http_code}\n" "$URL/api/photos/nope/image"
```

Give it ~15s for the logs to land.

## 5. The severity is real

**This is the gate.** Ask Cloud Logging for errors only:

```
gcloud logging read \
  'resource.type="cloud_run_revision" AND resource.labels.service_name="logging-gallery" AND severity>=WARNING' \
  --project $PROJECT --limit 5 --freshness 10m --format json
```

→ entries come back, each with `"severity": "WARNING"` or `"ERROR"`.

**Before this slice the same query returned nothing at all**, no matter
how many errors the service had logged — everything was `DEFAULT`.
Confirm that contrast is real by asking for the opposite and seeing the
successful requests instead:

```
gcloud logging read \
  'resource.type="cloud_run_revision" AND resource.labels.service_name="logging-gallery" AND severity="INFO"' \
  --project $PROJECT --limit 3 --freshness 10m --format 'value(jsonPayload.message)'
```

## 6. The handler's own lines are records, and each request is logged ONCE

Read a line the HANDLER wrote — one with attributes of its own, which is
what distinguishes it from a bare message:

```
gcloud logging read \
  'resource.type="cloud_run_revision" AND resource.labels.service_name="logging-gallery"
   AND logName:"stdout" AND jsonPayload.req!=""' \
  --project $PROJECT --limit 1 --freshness 10m --format json
```

**Observe:** a **`jsonPayload`** object carrying the handler's own keys
(`req`, and whatever the call site attached), plus a `trace` of the form
`projects/<project>/traces/<id>`.

A line whose only content is a message arrives as `textPayload` with
`severity` and `trace` alongside — that is Cloud Logging lifting the
special fields out, not a failure.

**Then check for duplicates**, which is what this section is really for:

```
gcloud logging read \
  'resource.type="cloud_run_revision" AND resource.labels.service_name="logging-gallery"
   AND httpRequest.requestUrl:"/api/photos"' \
  --project $PROJECT --limit 10 --freshness 10m \
  --format 'value(logName, httpRequest.requestMethod, httpRequest.status)'
```

**Every request must appear exactly once**, from
`run.googleapis.com%2Frequests` — Cloud Run's own entry. A second line
for the same request from `%2Fstdout` means tako is writing a request
log the platform already writes (the defect this gate caught the first
time it ran).

## 7. Handler lines nest under their request

Open the Logs Explorer for the service:

```
https://console.cloud.google.com/logs/query?project=<test-project>
```

Query `resource.labels.service_name="logging-gallery"`, find the
request line for the upload, and **expand it**.

**Observe:** the lines the handler wrote during that request — the
thumbnail metric, any `ctx.Log()` call — appear *underneath* it rather
than as unrelated siblings. That nesting is the trace field doing its
job; without it the same lines are scattered through the stream in
arrival order.

## 8. `tako logs` reads the severities back

```
go run ../../cmd/tako logs gallery --project $PROJECT --env logging
```

**Observe:** the severity column now carries `INFO` / `WARNING` /
`ERROR` per line rather than `DEFAULT` on every one.

## 8b. Expected noise: the post-deploy 403 window

Within the first couple of minutes you will likely see 403s on
`/_tako/api/push` for the subscriber services, with *"The IAM principal
lacks {run.routes.invoke} permission"*. **That is IAM propagation, not a
defect** — the grant is applied before the subscription is created, but
Cloud Run's front door takes a moment to see it, and Pub/Sub retries.

Confirm it recovered rather than assuming:

```
gcloud logging read \
  'resource.labels.service_name="logging-gallery-onuploaded" AND httpRequest.status=204' \
  --project $PROJECT --limit 3 --freshness 15m
go run ../../cmd/tako dlq gallery-onuploaded --project $PROJECT --env logging
```

→ 204s present, and no dead letters citing the 403. If the 403s never
stop, that IS a defect: report it.

## 9. Destroy clean

```
go run ../../cmd/tako destroy --plan --project $PROJECT --env logging
go run ../../cmd/tako destroy --project $PROJECT --env logging
```

Confirm no services, topics, subscriptions, service accounts or
scheduler jobs remain for this env.

## Verdict

Record in `docs/validation-log.md`: P1.
