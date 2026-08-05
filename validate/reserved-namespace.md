# validate/reserved-namespace.md — FL5

What this checks: that tako's own endpoints live under `/_tako/`, are
answered before the app's routes, and no longer make a service refuse to
start when the app declares a wildcard route.

The bug being closed is a **startup panic**, so the first thing to watch
is a service that starts. Everything after §2 is only meaningful because
§1 got past `ready`.

---

## 1. The combination that could not boot

```
cd examples/journal
rm -rf .tako/gen
go run ../../cmd/tako dev
```

**Observe:** the route table, then `ready`. `GET /{year}/{slug} → Perma`
is listed beside `auth (google)`, and those two together are what used to
die:

```
tako dev · journal

  analyzing   1 service, 7 endpoints, 1 site
  generating  .tako/gen/journal/main.go
  starting    http://localhost:8080

  GET / → Home (app.go:61)
  GET /p/{id} → Read (app.go:62)
  GET /write → Write (app.go:63)
  POST /p → Save 🔒 (app.go:64)
  POST /p/{id}/publish → Publish 🔒 (app.go:65)
  POST /p/{id}/rename → Rename 🔒 (app.go:66)
  GET /{year}/{slug} → Perma (app.go:73)
  entity posts (indexes: Published, Created)
  entity moved_urls
  site ./web
  auth (google)

ready in 947ms — watching (backends: local, in-process)
```

Before FL5 the same declarations printed that table and then panicked:

```
panic: pattern "GET /{year}/{slug}" conflicts with pattern "/api/_session":
	GET /{year}/{slug} matches fewer methods than /api/_session,
	but has a more general path pattern
```

**Fail if** the process exits, or if the panic appears anywhere.

## 2. Read the generated main

```
sed -n '25,42p' .tako/gen/journal/main.go
```

**Observe:** two muxes, tako's first, with the reason stated in the
output rather than implied:

```go
	// tako's own endpoints, on a mux of their own. They are answered
	// before the app's router below is consulted, so nothing under
	// /_tako/ can be shadowed by a route this app declares — and no
	// framework pattern can collide with a wildcard page, which on one
	// shared mux is a panic at startup rather than a bad response.
	takoMux := http.NewServeMux()

	takoMux.Handle("/_tako/api/session", runtime.SessionHandler())
	takoMux.Handle("POST /_tako/api/session/signout", runtime.SignOutHandler())
	takoMux.Handle("GET /_tako/signin", runtime.SignInPage("google"))

	mux := http.NewServeMux()
```

and at the bottom, `runtime.Serve(runtime.WithDevBar(runtime.Reserved(takoMux, mux)))`.

**Fail if** any `/_tako/…` pattern is registered on `mux`.

## 3. The namespace answers, and answers as tako

With the server from §1 still running:

```
curl -s -i -c /tmp/j.jar -X POST localhost:8080/_tako/api/session -d "dev_user=alice" | head -3
curl -s -o /dev/null -w "signin       %{http_code}\n" localhost:8080/_tako/signin
curl -s -o /dev/null -w "PUT session  %{http_code}\n" -X PUT localhost:8080/_tako/api/session
curl -s -o /dev/null -w "z/x          %{http_code}\n" localhost:8080/_tako/z/x
curl -s -o /dev/null -w "bare _tako   %{http_code}\n" localhost:8080/_tako
curl -s -o /dev/null -w "signout      %{http_code}\n" -X DELETE -b /tmp/j.jar localhost:8080/_tako/api/session
```

**Observe:**

```
HTTP/1.1 303 See Other
Location: /
Set-Cookie: _session=…; Path=/; Expires=…; HttpOnly; SameSite=Lax
signin       200
PUT session  405
z/x          404
bare _tako   404
signout      204
```

Two of those are the point rather than paperwork:

- **`PUT` is 405, not 404.** The session handler still refuses unlisted
  methods itself. On a shared mux that could not be true — avoiding the
  panic there required method-scoping the mount, which would have sent
  `PUT` to the site's 404 instead.
- **`/_tako/z/x` and bare `/_tako` are 404 from tako's mux**, not the
  site's styled 404 page. The namespace is never the app's to serve, so
  the app's error page is the wrong answer.

**Fail if** `PUT` returns 404, or if either 404 body is journal's
`404.html`.

## 4. The app still owns everything else

```
curl -s -o /dev/null -w "app root     %{http_code}\n" localhost:8080/
curl -s -o /dev/null -w "app /write   %{http_code}\n" -b /tmp/j.jar localhost:8080/write
curl -s -o /dev/null -w "permalink    %{http_code}\n" localhost:8080/2026/nope
```

**Observe:** `200`, `200`, `404` — the last from journal's own 404 page,
because `/2026/nope` reaches `Perma`, which finds no post. A path shaped
like the permalink is the app's; a path under `/_tako/` never is.

## 5. Declaring inside the namespace is a build error

In `examples/journal/app.go`, temporarily add:

```go
var Sneaky = tako.Page("GET /_tako/sneaky", home)
```

```
go run ../../cmd/tako gen graph
```

**Observe:**

```
app.go:NN:CC: route "GET /_tako/sneaky" is inside /_tako/, which tako reserves for the endpoints it injects (session, sign-in, cron, signed URLs, push) — the framework answers that namespace before this app's routes, so Sneaky would never run; choose a path outside it (tako/reserved-prefix)
```

Remove the line. **Fail if** it analyzes clean — a route there is dead
code, and silence is what this rule exists to prevent.

## 6. The second instance: a bucket and a deep wildcard

gallery declares a bucket (so tako injects `/_tako/api/signed/`) and a
four-segment permalink. That pair panics on one mux for the same reason
journal's did, one arity deeper.

```
cd ../gallery && go test ./... -run "MountedTogether|PermalinkResolves" -v
```

**Observe:** `TestABucketAndAWildcardRouteCanBeMountedTogether` and
`TestThePermalinkResolvesAPhotoForItsOwner` both PASS. The first asserts
in both directions — that one shared mux *still* panics (the hazard is
real, not theoretical) and that `runtime.Reserved` accepts the same pair.

## 7. The suites

```
cd ../.. && go test ./...
for d in hello shortlink gallery journal shop blog siren; do (cd examples/$d && go test ./...); done
```

**Observe:** all green, including `internal/routes` (the URL lock),
`pkg/runtime` (`Reserved`, and the parameterized proof of which mounts
panic on a shared mux), and `pkg/analyzer` (the `reserved-prefix`
golden).

## What this script does NOT check

**Cloud.** Moving these paths changes three deployed targets — the Cloud
Scheduler job's `uri`, the Pub/Sub push subscription's `endpoint`, and
the Cloud Tasks queue target — and the alert queries that match on a
`route` label. The plan-level assertions are unit-tested
(`shortlink_plan_test.go`, `alerts_test.go`, `promql_test.go`), but no
deploy has run against these paths. That belongs to the next
`validate/release-cycle.md` walk, and until then the cloud half of FL5 is
verified by construction, not by observation.
