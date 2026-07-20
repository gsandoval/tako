# validate/topic.md — Gate F5: Topic + Subscribe + bucket events (local only)

What this gate approves: event fan-out's feel — one event, two
subscribers, *independent fates* — and the bucket event driving the
pipeline end to end with no hand-published messages.

Prerequisite: `PORT=8090`; two files:

```
printf 'FAKEJPEG' > /tmp/cat.jpg
```

## 1. Start

```
cd examples/gallery && rm -rf .tako
PORT=8090 go run ../../cmd/tako dev
```

**Observe:** `5 services, 5 endpoints, 1 worker, 3 subscribers`; the
subscriber lines
`subscriber OnUploaded ← bucket photos.Uploaded (app.go:44)`,
`subscriber NotifyOwner ← topic photo-ready (app.go:46)`,
`subscriber WarmFeed ← topic photo-ready (app.go:47)`;
`topic photo-ready` in resources; and the honesty line
`local: workers and subscribers run in-process`.

## 2. The event spine, one clean upload

```
curl -s -F caption=cat -F 'img=@/tmp/cat.jpg;type=image/jpeg' localhost:8090/api/photos
```

**Watch the console narrate the whole chain** — no handler publishes the
ThumbJob anymore; the bucket event does:

```
⚙ photos.Uploaded → OnUploaded ok (…)     ← the upload's write
⚙ thumbnails → MakeThumb ok (…)
⚙ photos.Uploaded → OnUploaded ok (…)     ← the worker's _thumb write (filter acks it)
⚙ photo-ready → NotifyOwner ok (…)
⚙ photo-ready → WarmFeed ok (…)
```

Note OnUploaded firing twice: bucket events see every write, including
the worker's own output — the `_thumb` filter in app.go is the standing
idiom. Then `curl -s localhost:8090/api/photos` shows `"ready":true`.

## 3. Fan-out with independent fates (the centerpiece)

```
curl -s -F caption=boom -F 'img=@/tmp/cat.jpg;type=image/jpeg' localhost:8090/api/photos
```

**Watch:** the pipeline succeeds (thumb ok), then ONE PhotoReady event
splits into two fates — WarmFeed acks instantly while NotifyOwner
retries 1/5 … 4/5 and finally:

```
✗ dead-letter photo-ready: NotifyOwner gave up after 5 attempts: notification service rejected "boom"
  payload → .tako/data/deadletter/photo-ready/NotifyOwner/
```

Judge: is it obvious that WarmFeed was never touched by its sibling's
failure?

## 4. Per-subscriber dead letters

```
cat .tako/data/deadletter/photo-ready/NotifyOwner/*.json
ls .tako/data/deadletter/photo-ready/ 2>/dev/null
```

**Observe:** the letter carries `"subscriber": "NotifyOwner"`; there is
no WarmFeed directory — dead letters are per subscriber. The boom photo
IS `"ready": true` (the pipeline succeeded; only the notification
failed).

## 5. Subscribe binding is analyzed

Change `NotifyOwner`'s binding to an inline constructor:
`tako.Subscribe(tako.Topic[PhotoReady]("inline"), notifyOwner)`, save.

**Observe:** `tako.Subscribe must bind a topic or bucket event declared
as a package-level var in this module (tako/subscribe-binding)`. Revert.

## 6. Fan-out is legal where a second worker is not

Recall F4's one-worker error, then note the gallery itself has two
subscribers on `photo-ready` and analyzes clean — this asymmetry (queue:
exactly one worker; topic: any number of subscribers) is the Queue/Topic
distinction the framework teaches.

## 7. Payload shape is still the compiler's job

Change `notifyOwner`'s parameter to `e string` — a Go compile error, not
a tako error. Revert.

## 8. Conformance + tests

```
cd ../.. && go test ./pkg/backends/conformance -backend=local
go test ./... && cd examples/gallery && go test ./...
```

Record the verdict in `docs/validation-log.md` (feature F5, gate F5).
