# validate/queue.md — Gate F4: Queue + Worker (local only)

What this gate approves: the async story's feel — publish → worker in
one process, and above all *watching a failing worker retry then
dead-letter in the dev console*.

Prerequisite: `PORT=8090`; three files:

```
printf 'FAKEJPEG' > /tmp/cat.jpg
printf 'notes' > /tmp/notes.txt
: > /tmp/empty.png
```

## 1. Start

```
cd examples/gallery && rm -rf .tako
PORT=8090 go run ../../cmd/tako dev
```

**Observe:** `2 services, 5 endpoints, 1 worker`; the worker line
`worker MakeThumb ← queue thumbnails (app.go:35)`; `queue thumbnails`
in resources; and the honesty line about local in-process collapse.

## 2. Happy path

```
curl -s -F caption=cat -F 'img=@/tmp/cat.jpg;type=image/jpeg' localhost:8090/api/photos
```

**Observe** in the dev console, moments later: `⚙ thumbnails → MakeThumb
ok (…ms)`. Then:

```
curl -s localhost:8090/api/photos          # …"ready":true
curl -s localhost:8090/api/photos/<id>/thumb   # the worker's output streams
```

## 3. Retry → dead-letter (the centerpiece)

```
curl -s -F caption=doc -F 'img=@/tmp/notes.txt;type=text/plain' localhost:8090/api/photos
```

**Watch the console**: attempts 1/5 … 4/5, each saying why and when it
retries, then:

```
✗ dead-letter thumbnails: MakeThumb gave up after 5 attempts: unsupported content type "text/plain"
  payload → .tako/data/deadletter/thumbnails/
```

Judge: could you debug a production-shaped async failure from this
output alone?

## 4. Permanent — no retries

```
curl -s -F caption=void -F 'img=@/tmp/empty.png;type=image/png' localhost:8090/api/photos
```

**Observe:** one line, no attempts: `✗ dead-letter thumbnails: MakeThumb
permanent failure: empty upload`.

## 5. Inspect the dead letters

```
cat .tako/data/deadletter/thumbnails/*.json
```

**Observe:** payload, attempts, lastError, permanent, time — enough to
replay by hand. The photo that dead-lettered never became `ready`.

## 6. One worker per queue

Add `var Second = tako.Worker(thumbs, makeThumb)` to app.go, save.

**Observe:** `queue "thumbnails" already has worker MakeThumb (app.go:35)
— a queue has exactly one worker; use a Topic for fan-out
(tako/one-worker)`. Revert.

## 7. Payload shape is still the compiler's job

Change `makeThumb`'s parameter to `job string` — a Go compile error, not
a tako error. Revert.

## 8. Conformance + tests

```
cd ../.. && go test ./pkg/backends/conformance -backend=local
go test ./... && cd examples/gallery && go test ./...
```

Record the verdict in `docs/validation-log.md` (feature F4, gate F4).
