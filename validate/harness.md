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
