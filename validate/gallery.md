# validate/gallery.md — Gallery, complete (gate F9 local · gate C5 cloud)

What this gate approves: the whole DX in one sitting — the agent
plan's full demo script (signed upload, event → queue → worker, fan-out,
queries, cron) on the finished gallery. Gate F9 ran it locally ("already
a pleasant framework", Phase D exit); gate C5 runs the SAME §§2–4
against a cloud deployment — one script, both worlds, from here on.

**Cloud runs (C5+):** deploy first (`tako deploy --project
<test-project>`, plan reviewed), set `BASE=<the ✓ gallery URL>` instead
of localhost:8090, and skip §1 and §5 (tako test and AdvanceCron are
local by nature — cloud cron now runs via Cloud Scheduler (T2; validate/scheduler.md is its gate), and
the deploy says so). Dead letters appear in `tako dlq <service>`
instead of `.tako/data/deadletter/`; pipeline logs in `tako logs`.
Finish with §7. Conformance bar: `-backend=gcp -project <test-project>`
runs Entity, Bucket, TestTopicGCP, TestBucketNotificationGCP, and
TestSignedURL (platform params pin what a bare PUT URL cannot enforce:
MaxSize and wildcard types fall through to the pipeline safety net —
§4 IS that net, live).

## 0. A real test image

```
cd examples/gallery
printf 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==' | base64 -d > /tmp/dot.png
```

(makeThumb really decodes since F9 — fake bytes dead-letter as
`decode image: …`, permanently. That's §4.)

## 1. The suite, the product way

```
tako test        # (or: go run ../../cmd/tako test)
```

**Observe:** the analyzing line now ends `…, 1 worker, 3 subscribers,
1 cron`; the suite passes — including `TestThumbPipelineSurvivesChaos`
(the Phase D exit criterion) and `TestCronCleansNeverCompletedUploads`.
**Judge:** open `app_test.go` — pure user code: `NewTestApp`, `SignIn`,
`SetSecret`, `Call`, `DrainQueues`, `AdvanceCron`. HTTP/disk contracts
live in `http_test.go` on the runtime harness, deliberately.

## 2. The full demo

```
rm -rf .tako && PORT=8090 tako dev     # local; cloud: tako deploy, then BASE=<url>
BASE=${BASE:-localhost:8090}
```

**Observe the banner (local):** `cron Cleanup — every 24h (app.go:…)`,
`entity photos (indexes: Owner, Ready)`, and the `⏰ cron Cleanup: every
24h` boot line. Then the signed-upload flow end to end:

```
TICKET=$(curl -s -X POST $BASE/api/uploads -d '{"caption":"sunset"}')
URL=$(echo "$TICKET" | jq -r .uploadURL)
curl -s -o /dev/null -w "PUT %{http_code}\n" -X PUT -H 'Content-Type: image/png' --data-binary @/tmp/dot.png "$URL"
curl -s $BASE/api/photos | jq '[.[] | {caption, ready}]'
ID=$(curl -s $BASE/api/photos | jq -r '.[0].id')
DL=$(curl -s $BASE/api/photos/$ID/download | jq -r .url)
curl -s -o /dev/null -w "download %{http_code}\n" "$DL"
curl -s -o /dev/null -w "thumb %{http_code} %{content_type}\n" $BASE/api/photos/$ID/thumb
```

**Observe:** ticket → direct PUT (200) → `📈 uploads +1`, ⚙ pipeline,
`📈 thumb_ms` → feed shows `ready: true` (warmed, not queried) → shaped
`{"url": …}` download (200) → a real resized PNG thumb. In the cloud
the PUT goes straight to storage.googleapis.com (a V4 URL the service
signed as itself via signBlob) and the event that starts the pipeline
is a real GCS notification — allow the feed a few seconds.

## 3. Fan-out, independent fates

```
curl -s -o /dev/null -F 'caption=boom goes' -F 'img=@/tmp/dot.png;type=image/png' $BASE/api/photos
```

**Observe:** MakeThumb and WarmFeed ack; NotifyOwner retries 5× then
dead-letters. Local: `✗ dead-letter photo-ready: NotifyOwner …`, JSON
under `.tako/data/deadletter/photo-ready/NotifyOwner/`. Cloud: watch
`attempt n/5` in `tako logs gallery-notifyowner` (backoff-spaced, a
few minutes), then `tako dlq gallery-notifyowner`.

## 4. Real decoding has real failure modes

```
printf 'NOTANIMAGE' > /tmp/junk.bin
curl -s -F 'caption=junk' -F 'img=@/tmp/junk.bin;type=image/jpeg' $BASE/api/photos | jq .id
```

**Observe:** ONE attempt, then a permanent dead letter with
`decode image: …` — undecodable bytes never retry (contrast §3's
transient 5×). Local: `✗ dead-letter thumbnails: MakeThumb permanent
failure: …`; cloud: `tako dlq gallery-makethumb` within seconds. This
is also the cloud's enforcement story for what a bare signed PUT URL
cannot check (wildcard types, size caps): junk lands, the pipeline
dead-letters it, the cron reaps the orphan. Ctrl-C when done (local).

## 5. Cron fires (tests own scheduled time)

A 24h cron never fires in a demo session — that's what AdvanceCron is
for. Watch it work:

```
go test -run TestCronCleansNeverCompletedUploads -v -count=1 .
```

**Judge:** the test issues a ticket that never uploads, drains, calls
`app.AdvanceCron("cleanupOrphans")`, and the ghost is gone while the
completed photo survives. Try `app.AdvanceCron("nope")` in a scratch
edit if you want the unknown-cron error listing declared crons.

## 6. Everything else still green

```
cd ../.. && go test ./pkg/backends/conformance -backend=local   # 58 cases (CORS case is new)
go test ./... && (cd examples/shortlink && go test ./...) && (cd examples/hello && go test ./...)
```

## 7. Cloud brackets (C5+ runs only)

```
tako deploy --project <test-project>    # again: all skips, ✓ no changes
tako destroy --project <test-project>   # DATA section, typed name, clean project
```

Record the verdict in `docs/validation-log.md` (F9 for local, C5+ for
cloud). The F9 gate doubled as the whole-DX review; cloud runs judge
the plan, the security summary, and that this script needed no
cloud-specific surgery beyond BASE.
