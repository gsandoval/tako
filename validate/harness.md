# validate/harness.md — AB2 (app.Handler)

The app's real HTTP surface in its own tests. Run from
`examples/journal` unless a step says otherwise.

## 1. The harness is generated, and it is readable

```
tako test -run TestNothing ./...
cat .tako/gen/journal/harness/tako_harness_test.go
```

**Expect** `generating …/tako_harness_test.go` in the output, a
`harness 1 package(s)` line, and a file that declares `package journal`,
registers itself in `init`, and mounts every declared route with the
same wrappers and options the main uses.

It is in a **subdirectory** beside the main, not next to it: a directory
holding both would be two packages in one place, and `go build
./.tako/gen/journal` — which the image build does — fails with "found
packages main and journal".

## 2. A test asks for it in two lines

```go
app := tako.NewTestApp(t)
srv := httptest.NewServer(app.Handler())
```

That is the whole of what used to be 130 lines of hand-typed mounts and
binders in `pages_test.go`. Read the diff on that file: the tests are
unchanged, the mirror is gone.

## 3. Plain `go test` says what to do

```
go clean -testcache && go test -run TestWritePublishReadRoundTrip ./...
```

**Expect** a failure that is an instruction, not a mystery:

```
tako: app.Handler needs the generated mounts, which `go test` cannot see.
      Run `tako test` (it regenerates first) — or `tako test ./pkg/...` for a subset.
```

An app's tests run under `tako test` now. The framework's own suite is
unaffected — `go test ./...` at the repo root is unchanged.

## 4. The harness cannot drift from the main

From the repo root:

```
go test ./pkg/codegen -run TestTheHarnessMountsWhatTheMainMounts
```

**Expect** green. It compares the two generated files route by route AND
option by option — `RequireAuth`, `RequirePolicy`, `CacheFor`. Both come
from one emitter (`writeMounts`), so this asserts a property rather than
policing a copy.

That is the whole point of the slice. The app that drove it mirrored
codegen in 874 lines, drifted three times in two milestones, and had
four write routes that no test drove because nobody noticed they were
never mounted. Its own guard had to regex `app.go` against
`harness_test.go` because there was nothing better to compare.

## 5. Every example still passes, through the new door

```
for d in examples/*/; do (cd $d && tako test ./...); done
```

**Expect** all seven green. journal is the one converted to
`app.Handler()`; the rest still hand-mount and are unaffected, which is
the point — the change is opt-in per app.

## 6. A route the app does not declare is not in there

`journal`'s `serveWith` mounts three undeclared routes that pin runtime
contracts (a redirect, a permanent redirect, a handler that fails). They
ride the same origin because a session-bound CSRF token is only valid
there. An app should not carry a `/boom` route so that tako can test its
500 page, and the harness will not invent one.

---

## 7. Every example, through the same door

```
for d in examples/*/; do (cd $d && tako test ./...); done
```

**Expect** all seven green. Six now build their HTTP surface with
`app.Handler()`; siren has no HTTP tests, and shortlink uses it in the
one test that needs a request.

What is left hand-mounted is deliberate and worth keeping:

- `gallery/http_test.go` mounts two `http.NotFoundHandler()`s to prove a
  bucket's signed endpoint and a four-segment wildcard route can live on
  separate muxes — a runtime-contract test, not a mirror of codegen.
- `journal/pages_test.go` mounts three routes the app does not declare
  (a redirect, a permanent redirect, a handler that fails), through
  `serveWith`.
- gallery's dead-letter tests still call `runtime.BootLocal` with a dir
  they glob, and register the async handlers themselves. They are about
  the filesystem, so they need to know the directory.

## 8. What the rollout found

Converting is how you learn what the mirror was hiding. Each of these
was a real difference between the tested app and the deployed one:

- **gallery's harness never mounted `POST /api/reindex` or
  `GET /api/reindex/runs`.** Declared, deployed, driven by no test.
- **shop's harness never mounted `GET /api/me`**, and called
  `SessionHandler()` with no providers — so its tests ran against a shop
  with no password door while the deployed one has one.
- **`SignedURLHandler` served a 501 under `app.Handler()`**: it read the
  app `runtime.BootLocal` had set, and `NewTestApp` boots another way.
  With another test's BootLocal in the same run it was worse — it
  verified against that app's signing key and answered "invalid
  signature". Fixed by resolving the signer off the wired backends.
- **`runtime.WaitIdle` was a no-op under `NewTestApp`** — it returned
  nil immediately, so three tests believed they had a barrier and had
  none. Fixed the same way; with nothing local wired it now says so
  instead of returning nil.

The last two are the argument for rolling this out rather than shipping
it once: both were defects in AB2 itself, and both were invisible until
an app with buckets and an app with jobs went through the door.

---

## 9. A fresh clone has the site the harness mounts

The harness mounts the DECLARED site directory. For a site with
`SiteConfig.Build` that directory is a build output, and every example
that has one gitignores it — so this is the state of a clone that has
never run anything.

```
cd examples/blog && rm -rf site/public && tako test -count=1
cd examples/shop && rm -rf web/dist   && tako test -count=1
```

**Expect** a `⛅ site …` line before `generating`, and green. Before
this, blog failed with `the site has no templates/ directory` — a
message about the checkout dressed as a broken app.

**Expect also** that the message, when it is genuinely right, names no
path the app does not have: it says `templates/layout.html inside the
declared site directory`, not `web/templates/layout.html`.

---

## 10. A request through the harness is counted

`app.Handler()` serves through the same middleware the deployed service
does, and that middleware records `requests` and `request_ms`. It read a
copy of the metrics backend that only `runtime.BootLocal` filled in, so
under `NewTestApp` every request through the harness recorded into nil.

```
go test ./pkg/runtime -run RecordsRequests -v
```

**Expect** both `TestALocalBootRecordsRequests` and
`TestATestAppBootRecordsRequests` green. They boot each way that can be
booted and then make a real request, which is the only shape a future
boot path cannot pass by accident.
