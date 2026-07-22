# validate/extras.md — Gate F8: Secret, Cache, metrics, WithDelay (local)

What this gate approves: the config-and-observability primitives — a
secret that cannot fail at request time (because boot refuses to start
without it), a TTL cache the event spine keeps warm, metrics you can see,
and delayed messages a test never sleeps for.

## 1. Gallery wears all four (the product way)

```
cd examples/gallery
tako test        # (or: go run ../../cmd/tako test)
```

**Observe:** the analyzing banner now counts gallery's full shape, and the
suite — including `TestFeedCacheServesReadyPhotos` — passes. **Judge:** open
`examples/gallery/app.go`: `feed`/`notifier`/`ups`/`thumbMs` declarations,
`list` via `GetOrFill`, `warmFeed` re-Putting the feed, `notifyOwner`
reading `notifier.Value()` with no error path.

## 2. Watch the metrics and the warm feed

```
rm -rf .tako && PORT=8090 tako dev
curl -s -F 'caption=cat' -F 'img=@go.mod;type=image/jpeg' localhost:8090/api/photos | jq .
curl -s localhost:8090/api/photos | jq '[.[] | {caption, ready}]'
```

**Observe:** the banner lists `cache feed`, `secret notifier-key`,
`counter uploads`, `histogram thumb_ms`; the upload narrates
`📈 uploads +1 (total 1)` and `📈 thumb_ms <n> (n=1)` between the ⚙ lines;
the second curl shows `ready: true` — served from the feed warmFeed just
wrote, not a fresh query. Leave the server running for §3.

## 3. A missing secret refuses to boot

Ctrl-C the server, then:

```
mv .env .env.bak
rm -rf .tako && PORT=8090 tako dev
```

**Observe** (exact text, in the app's output):

```
tako: boot: secret "notifier-key" is not set — add it to .env (local) or Secret Manager (cloud)
```

The service never starts serving — that is the contract that lets
`Value()` take no ctx and no error. Restore: `mv .env.bak .env`. Ctrl-C.

## 4. WithDelay rides the fake clock (the centerpiece)

```
cd ../.. && go test -run 'WithDelay|GetOrFill|Secret' -v -count=1 .
```

**Judge:** `TestWithDelayRidesTheFakeClock` publishes with
`WithDelay(4*time.Hour)` and finishes in milliseconds: `DrainQueues`
treats the parked message as idle, `Advance(3h)` isn't enough, `Advance(1h)`
more delivers exactly once. Also passing here: GetOrFill fills once then
hits, `SetSecret` resolution, and the unset-secret panic text pointing at
`app.SetSecret`.

## 5. Everything else still green

```
go test ./pkg/backends/conformance -backend=local   # 57 cases incl. Cache + QueueDelay chapters
go test ./... && (cd examples/shortlink && go test ./...) && (cd examples/gallery && go test ./...)
```

Record the verdict in `docs/validation-log.md` (feature F8, gate F8).
