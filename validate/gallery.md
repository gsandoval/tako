# validate/gallery.md — Gate F9: Gallery, complete (local)

What this gate approves: the whole local DX in one sitting — the agent
plan's full demo script (signed upload, event → queue → worker, fan-out,
queries, cron) on the finished gallery. This is the "already a pleasant
framework" milestone review (implementation plan, Phase D exit).

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

## 2. The full demo (dev)

```
rm -rf .tako && PORT=8090 tako dev
```

**Observe the banner:** `cron Cleanup — every 24h (app.go:…)`,
`entity photos (indexes: Owner, Ready)`, and the `⏰ cron Cleanup: every
24h` boot line. Then the signed-upload flow end to end:

```
TICKET=$(curl -s -X POST localhost:8090/api/uploads -d '{"caption":"sunset"}')
URL=$(echo "$TICKET" | jq -r .uploadURL)
curl -s -o /dev/null -w "PUT %{http_code}\n" -X PUT -H 'Content-Type: image/png' --data-binary @/tmp/dot.png "$URL"
curl -s localhost:8090/api/photos | jq '[.[] | {caption, ready}]'
ID=$(curl -s localhost:8090/api/photos | jq -r '.[0].id')
DL=$(curl -s localhost:8090/api/photos/$ID/download | jq -r .url)
curl -s -o /dev/null -w "download %{http_code}\n" "$DL"
curl -s -o /dev/null -w "thumb %{http_code} %{content_type}\n" localhost:8090/api/photos/$ID/thumb
```

**Observe:** ticket → direct PUT (200) → `📈 uploads +1`, ⚙ pipeline,
`📈 thumb_ms` → feed shows `ready: true` (warmed, not queried) → shaped
`{"url": …}` download (200) → a real resized PNG thumb.

## 3. Fan-out, independent fates

```
curl -s -o /dev/null -F 'caption=boom goes' -F 'img=@/tmp/dot.png;type=image/png' localhost:8090/api/photos
```

**Observe:** MakeThumb and WarmFeed ack; NotifyOwner retries 5× then
`✗ dead-letter photo-ready: NotifyOwner …` — inspect the JSON under
`.tako/data/deadletter/photo-ready/NotifyOwner/`.

## 4. Real decoding has real failure modes

```
printf 'NOTANIMAGE' > /tmp/junk.bin
curl -s -F 'caption=junk' -F 'img=@/tmp/junk.bin;type=image/jpeg' localhost:8090/api/photos | jq .id
```

**Observe:** ONE attempt, then
`✗ dead-letter thumbnails: MakeThumb permanent failure: decode image: …`
— undecodable bytes never retry (contrast §3's transient 5×). Ctrl-C
when done.

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

Record the verdict in `docs/validation-log.md` (feature F9, gate F9).
This gate doubles as the whole-DX review — notes beyond pass/fail are
welcome in the log; F10 (API freeze) is where they become renames.
