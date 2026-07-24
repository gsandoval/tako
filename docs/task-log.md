# Task log

Append-only, one entry per task (see `tako-agent-plan.md` for the protocol).

---

## S0.1 — Repo + normative SDK stubs (2026-07-19)

**What shipped**

- Repo layout per the spec Part 4 map: `cmd/tako` (usage-only stub main),
  `pkg/{sdk,analyzer,codegen,runtime,provision,devserver}`,
  `pkg/backends/{local,gcp,conformance}`, `examples/` (README only; `hello`
  arrives in S0.2). Module `github.com/gsandoval/tako`, Go 1.26.
- `pkg/sdk` (package `tako`): every v0 exported symbol from spec Parts 1–2 as
  compilable, doc-commented stubs. Constructors are side-effect-free records
  (declaring an app never panics — proven by test); all operations panic
  `tako: not implemented`.
- `sdk_lock_test.go`: snapshots the exported surface (symbols + exported
  methods, unexported struct fields pruned) to `testdata/sdk_lock.golden`;
  regenerate deliberately with `-update`.
- `compile_check_test.go`: a miniature of the spec's gallery + journal written
  as a user would, proving the surface compiles — including
  `tako.Subscribe(photos.Uploaded, fn)` / `tako.Subscribe(ready, fn)` via the
  sealed `EventSource[T]` interface, and all generic inference.
- `pkg/analyzer/appgraph.go`: AppGraph/Service/Resource/Handler/Edge types
  with JSON tags.
- Process scaffolding: this file, `docs/validation-log.md`, `.gitignore`.

**Validation transcript (tier 1 — the only tier S0.1 defines)**

```
$ go build ./... && go vet ./...        # silent, green
$ go test ./...
ok  github.com/gsandoval/tako/pkg/sdk   0.328s
```

**API-shape decisions made while stubbing (spec was ambiguous or
uncompilable as written; pkg/sdk is now normative)**

1. **Handle type names.** Constructors and types can't share a name in one
   package, so constructors return `*BucketHandle`, `*EntityHandle[T]`,
   `*CacheHandle[T]`, `*QueueHandle[T]`, `*TopicHandle[T]`, `*LockHandle`,
   `*SecretHandle`, `*AuthHandle`, `*SiteHandle`, `*JobHandle[T]`,
   `*CounterHandle`, `*HistogramHandle` (GCS `BucketHandle` precedent). Users
   never write these names in normal code.
2. **`Site` only — no `Static`/`Templates`.** §2.11 supersedes them; the
   Part 3 gallery sample still uses `tako.Static`/`tako.Templates` and needs
   a fix-up to `Site` (`site.Render` replacing `tmpl.Render`).
3. **`NotFound() error`, not a two-value return.** Part 3's
   `return tako.NotFound()` can't compile for generic API handlers (Go
   doesn't infer type args from return context). Stubbed as an error
   constructor: `return tako.HTML{}, tako.NotFound()`. Final shape is F1's
   DX proposal (error helpers are F1 scope); spec samples then get fixed up.
4. **`Redirect(target) (HTML, error)`** — page-only by nature, so the
   two-value form from the journal sample compiles as written.
5. **Cache gets `Delete`/`Exists`** per rule 4 (keyed-store verb vocabulary),
   though §2.6 doesn't list them.
6. **`Topic.Publish` takes no options** (delay/rate-limit are Queue powers,
   §2.4); adding options later is non-breaking.
7. **Sealed `EventSource[T]`** unifies `Subscribe(topic, …)` and
   `Subscribe(bucket.Uploaded, …)`; `BucketHandle.Uploaded` is a `BucketEvent`
   field implementing `EventSource[ObjectInfo]`.
8. **`NewTestApp(testing.TB)`** rather than `*testing.T`.

**Discovered work (not done)**

- Spec fix-up PR: Part 3 gallery → `Site`; §2.9/Part 3 `NotFound()` usage;
  §2.1 resource catalog still lists `Static`/`Templates` (align after F1's
  error-helper DX settles anything else).
- Import-path DX question for the human: samples import
  `github.com/gsandoval/tako` (module root) but the API lives at
  `…/tako/pkg/sdk`. Options: move package to root, or a root alias package
  (Go 1.24+ generic aliases), or bless the `pkg/sdk` path. Cheap until F10.
- `Iter[T]` shape (`Next/Err` vs Go 1.23 `iter.Seq2`) to be settled by F3's
  DX proposal (first real `List` user).

**Gate:** none — per the plan, S0.2 completes the walking skeleton and
requests Gate G0.

---

## S0.2 — DX proposal: one endpoint, `tako dev`, hello (2026-07-19)

*Status: awaiting human approval. Implementation starts only after this
surface is approved (or amended) here.*

### 1. The user's entire app — `examples/hello`

```go
// examples/hello/hello.go
package hello

import "github.com/gsandoval/tako"

var Hello = tako.API("GET /hello", hello)

func hello(ctx tako.Ctx, _ struct{}) (Greeting, error) {
	return Greeting{Message: "Hello from Tako!"}, nil
}

type Greeting struct {
	Message string `json:"message"`
}
```

Notes:
- **Import path decision needed.** The spec's samples import
  `github.com/gsandoval/tako` (module root); the S0.1 package lives at
  `pkg/sdk`. Recommendation: **move the SDK package to the module root**
  (package `tako` at repo root — the chi/bbolt layout). A named import
  (`tako "github.com/…/tako/pkg/sdk"`) in every user file is ceremony; a
  root wrapper package can't cleanly re-export generic functions. Moving it
  updates CLAUDE.md's repo map (`pkg/sdk` → root) and is mechanical now,
  expensive after F10.
- **Each example is its own Go module** (`module hello`), wired to the repo
  by a root `go.work`. That's how real user apps look, and it keeps the
  analyzer honest about module loading. No `tako.yaml` until the cloud
  phases need it.
- S0.2 supports **empty-Req endpoints only** (`_ struct{}`); Req binding is
  F1. Res is JSON-encoded.

### 2. Expected CLI transcript

```
$ cd examples/hello
$ tako dev
tako dev · hello

  analyzing   1 service, 1 endpoint
  generating  .tako/gen/hello/main.go
  starting    http://localhost:8080

  GET /hello → Hello (hello.go:6)

ready in 412ms — watching (backends: local, in-process)
```

```
$ curl localhost:8080/hello
{"message":"Hello from Tako!"}
```

Edit the message string, save — the running terminal appends:

```
reloaded in 380ms
```

Analysis failure while watching (e.g. the error below) prints the error,
keeps the last good build running, and reloads when the file is fixed:

```
✗ analysis failed — still serving last good build
  hello.go:6:22: … (tako/…)
```

`tako gen graph` (from the app dir) prints the AppGraph JSON followed by a
human summary:

```
$ tako gen graph
{ …AppGraph JSON… }

hello: 1 service
  service hello (package hello, profile default)
    GET /hello → Hello (hello.go:6)
```

- Port: 8080 (`--port` later, when something demands it).
- Request logging during dev: one plain line per request
  (`GET /hello → 200 (0.4ms)`); structured request logs are F1 scope.
- Reload target: sub-second, printed honestly (`reloaded in Nms`).

### 3. New error texts (the only analyzer rule in S0.2)

Constant-route enforcement, format `path:line:col: message (tako/RULE-ID)`:

```
hello.go:6:22: tako.API route must be a string constant — routes are read
at analysis time, not run time; got variable "route" (tako/const-route)
```

(Same text with "got call expression", etc., for other non-constant
arguments. Rule IDs are lower-kebab in the `tako/` namespace; this is the
naming precedent for every later rule.)

### 4. Generated code the user may read (`.tako/gen/hello/main.go`)

Not part of the approved surface, but the transparency story previewed:
a `// Code generated by tako. DO NOT EDIT.` main that visibly does
`mux.HandleFunc("GET /hello", runtime.JSONHandler(hello.Hello))` — route
registration on stdlib `net/http`, one readable line per endpoint, no
reflection. `.tako/` is gitignored.

### 5. Internal (non-surface) notes

`Endpoint` records gain an unexported `fn` field so generated mains can
invoke the handler through the var (lock test unaffected — unexported
fields are pruned). Golden test: hello's AppGraph JSON under
`pkg/analyzer/testdata/`; error-case golden for `const-route` asserting the
exact text above.

---

## S0.2 — implementation: one endpoint, `tako dev`, hello (2026-07-19)

DX proposal approved (both questions): SDK moved to module root
(`import "github.com/gsandoval/tako"`), proposal implemented as written.

**What shipped**

- SDK at module root; CLAUDE.md repo map updated; lock golden unchanged.
- `Endpoint` now records its handler fn; `Endpoint.Call` is a direct
  in-process invocation (dev/test transport).
- `pkg/runtime`: `NewCtx` (auth-less request Ctx), `JSONHandler` (empty-Req
  endpoints → JSON response), `Serve` (PORT env, request-log lines).
- `pkg/analyzer`: `Analyze(Config)` — go/packages load, `tako.API` var
  extraction, constant-route enforcement (`tako/const-route`), service
  partitioning (package = service), `Summary` renderer. `Error` format is
  `path:line:col: message (tako/rule-id)`, positions module-relative.
- `pkg/codegen`: `Generate` — per-service `.tako/gen/<svc>/main.go`,
  gofmt-clean, one readable line per endpoint, golden-tested byte-for-byte.
- `pkg/devserver`: watch (fsnotify, debounced) → analyze → generate →
  `go build` → run; keeps last good build serving on failure; honors PORT.
- `cmd/tako`: `dev` and `gen graph`.
- `examples/hello` (own module, `replace`-wired) + HTTP test.
- Tests: analyzer AppGraph + summary + error-text goldens, codegen golden,
  hello endpoint test, SDK lock + compile fixture — all green
  (`go test ./...` in both modules).

**Deviations from the approved proposal (small, flagged for the gate)**

1. `go.work` → per-example `replace` directive. Workspace mode broke root
   builds (module graph tried to fetch the unpublished module); `replace`
   in the example's go.mod is the standard pattern and needs no workspace.
2. `tako dev` honors `PORT` env (flag still deferred). Discovered during
   validation: this machine already has a Java service on 127.0.0.1:8080,
   so validation runs on PORT=8090. Not a new flag — the Cloud Run
   convention applied to dev.
3. Cosmetic: durations print as Go durations ("407ms" not "407 ms");
   reload after an error prints below the error block.

**Tier-2 validation — real transcript (validate/skeleton.md executed)**

```
$ cd examples/hello && PORT=8090 tako dev
tako dev · hello

  analyzing   1 service, 1 endpoint
  generating  .tako/gen/hello/main.go
  starting    http://localhost:8090

  GET /hello → Hello (hello.go:5)

ready in 407ms — watching (backends: local, in-process)

$ curl -s localhost:8090/hello
{"message":"Hello from Tako!"}

# edited the message string, saved:
GET /hello → 200 (0.0ms)
reloaded in 693ms

$ curl -s localhost:8090/hello
{"message":"Hello, reloaded Tako!"}

# replaced the route constant with `var route = ...`:
✗ analysis failed — still serving last good build
  hello.go:7:22: tako.API route must be a string constant — routes are read at analysis time, not run time; got variable "route" (tako/const-route)

$ curl -s localhost:8090/hello       # old build still answering
{"message":"Hello, reloaded Tako!"}

# reverted; Ctrl-C: port freed, clean exit.

$ tako gen graph        # (from examples/hello; output stable across runs)
{ "module": "hello", … "route": "GET /hello", "pos": "hello.go:5:5" … }

hello: 1 service
  service hello (package hello, profile default)
    GET /hello → Hello (hello.go:5)
```

`go test ./...`: tako module (sdk lock, analyzer goldens incl. exact error
text, codegen golden) and hello module — all ok.

**Discovered work (not done)**

- Reload latency is ~700ms measured from save; most is `go build`. Fine
  for G0; if it creeps toward 1s as services grow, cache generated-main
  builds or skip regeneration when the graph hash is unchanged.
- `waitReady` dials the port, which can false-positive if another process
  already listens there (how the 8080 collision surfaced). A real health
  endpoint on generated services would make readiness exact — natural once
  services get more plumbing.
- First-cycle failure then fix: the recovery prints `reloaded in …` though
  nothing was serving before; "up in …" would be more honest.

**GATE REQUEST — G0 (local): walking skeleton.** Please run
`validate/skeleton.md` end-to-end and record the verdict in
`docs/validation-log.md`. No further feature work until then.

---

## S0.2 fix — G0 validation feedback: `go run ../../cmd/tako dev` (2026-07-19)

Gate G0 dry-run feedback: from `examples/hello`,
`go run ../../cmd/tako dev` failed with missing go.sum entries (fsnotify).
Cause: the CLI was being resolved *through hello's module* via its
`replace`, and hello's go.sum lacks the framework's own dependencies. My
tier-2 transcript used a pre-built binary, which masked this — the
validation script's exact command was never run as written. Noted.

Fix: committed `go.work` (uses `.` and `./examples/hello`). In workspace
mode sums come from the union of member go.sums, so every `go` command
works from any directory in the repo. The earlier go.work attempt failed
only because it predated hello's `replace` and had a go-version mismatch;
with both in place the workspace is clean. The per-example `replace` stays
so examples also build standalone (GOWORK=off), which the analyzer's
fixture tests rely on.

Verified: root `go build ./...`/`go test ./...` green; from
`examples/hello`: `go test ./...`, `go run ../../cmd/tako gen graph`, and
`PORT=8090 go run ../../cmd/tako dev` → ready in 684ms, curl serves,
Ctrl-C frees the port. Gate G0 request stands.

**Discovered work addendum (2026-07-19, from G0 review):** `tako gen graph`
currently prints JSON with the human summary appended on the same stream, so
the output isn't jq-able. Wanted (human-requested): (a) a JSON mode whose
output is clean, pipeable `jq` input, and (b) a mermaid output mode for
visualizing the graph (services, endpoints, resources, edges). Likely shape
`tako gen graph [--json|--mermaid]` with the human summary as default —
CLI surface, so it gets a mini DX proposal when scheduled.

---

## F1 — DX proposal: API endpoints, complete (2026-07-19)

*Status: awaiting human approval. Scope per agent plan F1: Req-struct
binding (`path:`/`query:`/JSON body/`form:`), error helpers (`Errorf`,
`NotFound`, `IsNotFound`), structured request logs, hello extended with a
parameterized route, analyzer rules (route↔`path:` matching, option
validity, package-level enforcement) + compile-error fixture.*

### 1. User code — hello grows two endpoints

```go
var Hello = tako.API("GET /hello", hello)
var Greet = tako.API("GET /greet/{name}", greet)
var Echo  = tako.API("POST /echo", echo)

type GreetReq struct {
	Name string `path:"name"`
	Loud bool   `query:"loud"`
}

func greet(ctx tako.Ctx, req GreetReq) (Greeting, error) {
	if req.Name == "nobody" {
		return Greeting{}, tako.NotFound()
	}
	msg := "Hello, " + req.Name + "!"
	if req.Loud {
		msg = strings.ToUpper(msg)
	}
	return Greeting{Message: msg}, nil
}

type EchoReq struct {
	Message string `json:"message"` // untagged/JSON fields bind from the body
}

func echo(ctx tako.Ctx, req EchoReq) (Greeting, error) {
	if req.Message == "" {
		return Greeting{}, tako.Errorf(422, "message must not be empty")
	}
	return Greeting{Message: req.Message}, nil
}
```

Binding rules (as spec §2.8, now concrete):
- `path:"x"` ← route `{x}`; `query:"x"` ← query string; `form:"x"` ←
  urlencoded form posts; everything else ← JSON body (only for
  non-GET/HEAD; a GET handler whose Req has untagged fields is an
  analysis error, see §4).
- Bindable field types v0: `string`, `bool`, `int`, `int64`, `float64`.
  `tako.Blob` fields (multipart) arrive with F3.
- All binding code is generated per endpoint — no reflection at request
  time.

### 2. Error helpers — final shapes (settles the S0.1 flag)

- `tako.Errorf(status int, format string, args ...any) error` — handler
  surfaces as that HTTP status with body `{"error":"<message>"}`.
- `tako.NotFound() error` — surfaces as 404 `{"error":"not found"}`.
  **Single-value error constructor**: handlers write
  `return Greeting{}, tako.NotFound()`. (The spec's two-value
  `return tako.NotFound()` cannot compile for generic API handlers; spec
  Part 3/3b get a fix-up PR after approval.)
- `tako.IsNotFound(err) bool` — true for `NotFound()` now; F2 extends it
  to store misses so handler code can pass them straight through.
- Any other handler error → 500 with body `{"error":"internal error"}`;
  the real error is logged with the request id, never leaked to clients.

### 3. Expected CLI transcript (good and bad requests)

```
$ curl -s localhost:8090/greet/memo
{"message":"Hello, memo!"}

$ curl -s "localhost:8090/greet/memo?loud=true"
{"message":"HELLO, MEMO!"}

$ curl -si localhost:8090/greet/nobody | head -1
HTTP/1.1 404 Not Found                    # body: {"error":"not found"}

$ curl -si "localhost:8090/greet/memo?loud=banana" | head -1
HTTP/1.1 400 Bad Request
# body: {"error":"query \"loud\": \"banana\" is not a bool"}

$ curl -s -X POST localhost:8090/echo -d '{"message":"hi"}'
{"message":"hi"}

$ curl -si -X POST localhost:8090/echo -d '{oops' | head -1
HTTP/1.1 400 Bad Request
# body: {"error":"body: invalid JSON"}

$ curl -si -X POST localhost:8090/echo -d '{"message":""}' | head -1
HTTP/1.1 422 Unprocessable Entity         # body: {"error":"message must not be empty"}
```

Decode-failure texts are product UX and get golden tests:
`query "loud": "banana" is not a bool` · `path "id": "abc" is not an int`
· `body: invalid JSON`.

### 4. New analyzer errors (exact texts, all golden-tested)

```
hello.go:9:13: route "GET /greet/{name}" has path parameter {name} but
GreetReq has no field tagged path:"name" (tako/route-params)

hello.go:13:2: GreetReq field Extra is tagged path:"extra" but route
"GET /greet/{name}" has no {extra} parameter (tako/route-params)

hello.go:9:38: tako.WithTTL is not a tako.API option (tako.API accepts
WithAuth, WithPrivate, WithProfile) (tako/invalid-option)

hello.go:17:9: tako.API must initialize a package-level var — handlers
declared inside functions are invisible to analysis (tako/package-level)

hello.go:9:13: GET route "GET /greet/{name}" cannot bind a JSON body, but
GreetReq field Extra has no path:/query: tag (tako/body-on-get)
```

Plus a compile-error fixture (not an analyzer test): a program whose
handler signature doesn't match its Req/Res, asserting *the compiler*
rejects it — payload shape is the type system's job, per interface rule 1.

### 5. Structured request logs

Generated services log one structured line per request via `slog`
(request id, method, path, status, duration); `ctx.Log()` carries the
request id, so handler logs correlate. In `tako dev` the terse line stays
(`GET /greet/memo → 200 (0.3ms)`) — structured JSON becomes the cloud
default in Phase E. Example handler log in dev:

```
level=INFO msg=resized photo=p42 req=8c31f2
GET /greet/memo → 200 (0.3ms)
```

### 6. Not in scope (F1 non-goals)

Auth enforcement (`WithAuth` accepted, enforced when Auth lands), `form:`
multipart/Blob (F3), Page handlers (Stage 3), `IsNotFound` on stores (F2),
`gen graph --json/--mermaid` (separate noted task).

---

## F1 — implementation: API endpoints, complete (2026-07-19)

DX proposal approved as written; shipped surface matches it.

**What shipped**

- **Error helpers live**: `Errorf` (status + `{"error":…}` body),
  `NotFound` (404), `IsNotFound` (sentinel match; stores join in F2).
  Non-status errors surface as opaque 500 `{"error":"internal error"}`
  with the real error logged under the request id. Spec fix-up applied:
  the three `return tako.NotFound()` samples in Parts 3/3b/§2.11 now read
  `return tako.HTML{}, tako.NotFound()`.
- **Static binding**: analyzer extracts each API handler's Req struct
  (fields, tags, types) into the AppGraph (`req_type`, `bindings`,
  `has_body`); codegen emits one plain decode function per endpoint
  (`decodeGreet` etc.) calling typed `pkg/runtime` helpers
  (`PathString`, `QueryBool`, `DecodeJSON`, …). No reflection anywhere.
- **Analyzer rules + goldens**: `tako/route-params` (both directions),
  `tako/invalid-option`, `tako/package-level`, `tako/body-on-get` — plus
  the compile-error fixture proving payload shape is the compiler's job.
- **Request logs**: every request gets a short id; `ctx.Log()` carries
  `req=<id>` so handler logs correlate with the terse request line.
  Structured JSON stays a Phase E (cloud) default per the proposal.
- **hello** grows `Greet` (path+query binding, NotFound) and `Echo`
  (JSON body, Errorf 422); its test suite hits all eight good/bad cases
  over HTTP with exact bodies, plus `.Call` + `IsNotFound` in-process.
- Decode error texts unit-tested exactly (`query "loud": "banana" is not
  a bool`, `path "id": "abc" is not an int`, `body: invalid JSON`;
  absent values are zero values, never errors).

**Deviations from the approved proposal (flagged for the gate)**

Two analyzer rules were *added* beyond the approved five, both to prevent
generated code failing to compile as the user-facing symptom:
1. `tako/unexported-req` — request types must be exported (or `struct{}`)
   because generated services construct them outside the package.
2. `tako/bind-type` — tagged fields must be a bindable type
   (string, bool, int, int64, float64).
Both texts are golden-tested; veto or amend at the gate.

**Tier-2 validation — real transcript (validate/api.md §§1–3, 6)**

```
$ cd examples/hello && PORT=8090 tako dev
  analyzing   1 service, 3 endpoints
  GET /hello → Hello (hello.go:9)
  GET /greet/{name} → Greet (hello.go:10)
  POST /echo → Echo (hello.go:11)
ready in 691ms — watching (backends: local, in-process)

$ curl -s localhost:8090/greet/memo
{"message":"Hello, memo!"}
$ curl -s 'localhost:8090/greet/memo?loud=true'
{"message":"HELLO, MEMO!"}
$ curl -s localhost:8090/greet/nobody            # 404
{"error":"not found"}
$ curl -s 'localhost:8090/greet/memo?loud=banana' # 400
{"error":"query \"loud\": \"banana\" is not a bool"}
$ curl -s -X POST localhost:8090/echo -d '{"message":"hi"}'
{"message":"hi"}
$ curl -s -X POST localhost:8090/echo -d '{oops'  # 400
{"error":"body: invalid JSON"}
$ curl -s -X POST localhost:8090/echo -d '{"message":""}'  # 422
{"error":"message must not be empty"}
# Ctrl-C: 0 listeners left
```

Analyzer error texts: `pkg/analyzer/testdata/errors/*/expect.golden.txt`
— texts match the proposal §4 exactly. `go test ./...` green in both
modules (sdk lock unchanged).

**Discovered work (not done)**

- Multiple tags on one field (`path:` + `query:`) resolve by silent
  precedence; an analysis error would be more honest.
- Route *format* validity (bad method token, malformed pattern) surfaces
  at serve time via net/http; an analysis rule would move it earlier.
- `Endpoint.Call` bypasses handler-level option middleware (none exists
  yet); revisit when WithAuth is enforced.

**GATE REQUEST — F1 (local): API endpoints.** Please run
`validate/api.md` end-to-end — especially §4's six error texts, which are
the product surface this gate exists to judge — and record the verdict in
`docs/validation-log.md`.

---

## F2 — DX proposal: Entity + shortlink (2026-07-19)

*Status: awaiting human approval. Scope per agent plan F2: Entity
Get/Put/Delete/Exists, Query builder, Update, Tx; bbolt local backend;
conformance suite skeleton (Entity first citizen); index derivation with
unindexed-query error; `examples/shortlink`.*

### 1. User code — `examples/shortlink`

```go
package shortlink

import (
	"time"

	"github.com/gsandoval/tako"
)

var links = tako.Entity[Link]("links")

// Link's schema comes from the struct: entity:"id" is the key,
// entity:"index" marks queryable fields.
type Link struct {
	Slug    string    `entity:"id"`
	URL     string    `json:"url"`
	Owner   string    `entity:"index" json:"owner"`
	Created time.Time `entity:"index" json:"created"`
	Hits    int       `json:"hits"`
}

var (
	Create  = tako.API("POST /api/links", create)
	Resolve = tako.API("GET /api/links/{slug}", resolve)
	List    = tako.API("GET /api/links", list)
)

type CreateReq struct {
	Slug  string `json:"slug"`
	URL   string `json:"url"`
	Owner string `json:"owner"` // no auth yet; owner is honest input until F-auth
}

func create(ctx tako.Ctx, req CreateReq) (Link, error) {
	if req.Slug == "" || req.URL == "" {
		return Link{}, tako.Errorf(422, "slug and url must not be empty")
	}
	link := Link{Slug: req.Slug, URL: req.URL, Owner: req.Owner, Created: time.Now()}
	// Claim the slug atomically: check-then-put inside one transaction.
	err := links.Tx(ctx, func(tx tako.EntityTx[Link]) error {
		_, err := tx.Get(req.Slug)
		if err == nil {
			return tako.Errorf(409, "slug %q is taken", req.Slug)
		}
		if !tako.IsNotFound(err) {
			return err
		}
		return tx.Put(link)
	})
	return link, err
}

type ResolveReq struct {
	Slug string `path:"slug"`
}

func resolve(ctx tako.Ctx, req ResolveReq) (Link, error) {
	err := links.Update(ctx, req.Slug, func(l *Link) { l.Hits++ })
	if err != nil {
		return Link{}, err // Update on a missing id is IsNotFound → 404
	}
	return links.Get(ctx, req.Slug)
}

type ListReq struct {
	Owner string `query:"owner"`
}

func list(ctx tako.Ctx, req ListReq) ([]Link, error) {
	q := links.Query(ctx)
	if req.Owner != "" {
		q = q.Where("Owner", "=", req.Owner)
	}
	return q.OrderBy("-Created").Limit(100).All()
}
```

### 2. Semantics being locked (v0 Entity contract)

- Keys: exactly one `entity:"id"` field, type `string` (analyzer-enforced).
- `Put` is an upsert; `Get`/`Update`/query `First` miss → `IsNotFound`.
- `Update` is transactional read-modify-write; `Tx` gives multi-op
  atomicity scoped to one store; an error from the fn rolls everything
  back — and the fn's error (e.g. the 409 above) is returned as-is, so
  status errors compose with transactions.
- Query ops: `=`, `<`, `<=`, `>`, `>=`; `Where`/`OrderBy` accept only
  `entity:"index"` fields (analysis-time error otherwise); `OrderBy("-X")`
  is descending; results are deterministic (order-by, then key).
- Local backend: bbolt file `.tako/data/entity.db` — survives dev-server
  restarts; deleted only by deleting `.tako/`.
- The **conformance suite** is born here: one shared behavior suite for
  the entity contract, run against `local` now (`go test
  ./pkg/backends/conformance -backend=local`), `gcp` in Phase E.

### 3. Expected CLI transcript

```
$ cd examples/shortlink && PORT=8090 go run ../../cmd/tako dev
  analyzing   1 service, 3 endpoints
  ...
  entity links (indexes: Owner, Created)      ← resources now listed

$ curl -s -X POST localhost:8090/api/links \
    -d '{"slug":"gh","url":"https://github.com","owner":"memo"}'
{"url":"https://github.com","owner":"memo","created":"…","hits":0}

$ curl -s -X POST localhost:8090/api/links -d '{"slug":"gh","url":"https://x.com"}'
{"error":"slug \"gh\" is taken"}                       # 409, from inside Tx

$ curl -s localhost:8090/api/links/gh
{"url":"https://github.com","owner":"memo","created":"…","hits":1}

$ curl -s localhost:8090/api/links/nope
{"error":"not found"}                                  # 404 via Update miss

$ curl -s "localhost:8090/api/links?owner=memo"
[{"url":"https://github.com",…,"hits":1}]

# Ctrl-C, restart tako dev, curl again → hits and links survive (bbolt).
```

### 4. New analyzer errors (exact texts, golden-tested)

```
app.go:31:24: query filters on Link.URL, but URL is not indexed — tag it
entity:"index" to make it queryable (tako/unindexed-query)

app.go:33:15: query orders by Link.Hits, but Hits is not indexed — tag it
entity:"index" to make it orderable (tako/unindexed-query)

app.go:9:6: entity type Link must have exactly one field tagged
entity:"id" (tako/entity-id)

app.go:12:2: entity id field Slug must be a string; got int (tako/entity-id)

app.go:7:30: tako.Entity name must be a string constant — names are read
at analysis time, not run time; got variable "n" (tako/const-name)

app.go:31:26: query field must be a string constant — indexes are derived
at analysis time; got variable "f" (tako/const-query-field)
```

(`tako/const-name` extends the const rule to all resource constructors;
`gen graph` now also derives and prints each store's index set from the
actual `Where`/`OrderBy` call sites.)

### 5. Internals preview (not surface, for orientation)

`pkg/backends` gains the entity contract interface; `local` implements it
on bbolt (JSON values, in-process index scans); the SDK's `EntityHandle`
methods delegate to the backend wired at boot by the generated main.
Conformance suite exercises the contract (CRUD, queries, tx atomicity,
ordering, IsNotFound) against any backend.

### 6. Not in scope (F2 non-goals)

Usage edges / derived IAM (Phase A completion, later slice), migrations,
non-string keys, `!=`/`IN` operators, cross-store Tx (never), auth-scoped
owner (arrives with Auth), Datastore backend (Phase E).

---

## F2 — implementation: Entity + shortlink (2026-07-19)

DX proposal approved as written; shipped surface matches it.

**What shipped**

- **Backend contract born** (`pkg/backends`): `Entity` interface — JSON
  documents keyed by (kind, id), filtered/ordered/limited queries,
  atomic Update and Tx with error-passthrough rollback.
- **bbolt local backend** (`pkg/backends/local`): one bucket per kind at
  `.tako/data/entity.db`; queries are in-process scans with type-aware
  comparison (RFC 3339 strings compare as instants, so time.Time fields
  order correctly across zones); persistence-across-reopen under test.
- **Conformance suite born** (`pkg/backends/conformance`): 17 entity
  contract cases (CRUD, upsert, idempotent delete, query ops/AND/order/
  limit, tx commit/rollback/reads) — run via
  `go test ./pkg/backends/conformance -backend=local`; `-backend=gcp`
  skips until Phase E.
- **SDK Entity live**: Get/Put/Delete/Exists/Query/Update/Tx on the
  backend via the internal boot seam (`internal/hooks`); schema (id +
  indexed fields) read once per handle from struct tags — init-shaped
  reflection only, request path stays on encoding/json; `Iter[T]`
  implemented. Runtime gains `Boot()` (TAKO_BACKEND, default local) and
  `BootLocal(dir)` for tests; generated mains call `runtime.Boot()` when
  the package declares resources.
- **Analyzer**: Entity resource extraction (`tako/const-name`), schema
  validation (`tako/entity-id` ×2), query-site scan on
  `EntityQuery[T]` receivers — no dataflow needed, the type parameter
  identifies the store — with `tako/unindexed-query` (filter + order
  variants) and `tako/const-query-field`; derived index set lands in the
  AppGraph and prints as `entity links (indexes: Owner, Created)` in
  `gen graph` and the dev banner. All six texts golden, matching the
  proposal exactly.
- **examples/shortlink**: the approved app (Tx slug claim → 409,
  Update-miss → 404, filtered ordered list) with a suite covering all
  semantics incl. rollback-preserves-original.

**Tier-2 validation — real transcript (validate/entity.md §§1–4, 6)**

```
$ cd examples/shortlink && PORT=8090 tako dev
  POST /api/links → Create (app.go:22)
  GET /api/links/{slug} → Resolve (app.go:23)
  GET /api/links → List (app.go:24)
  entity links (indexes: Owner, Created)
ready in 728ms — watching (backends: local, in-process)

$ curl -s -X POST …/api/links -d '{"slug":"gh","url":"https://github.com","owner":"memo"}'
{"slug":"gh","url":"https://github.com","owner":"memo","created":"…","hits":0}
$ curl -s -X POST …/api/links -d '{"slug":"gh","url":"https://x.com"}'   # 409
{"error":"slug \"gh\" is taken"}
$ curl -s …/api/links/gh   # second resolve
{"slug":"gh",…,"hits":2}
$ curl -s …/api/links/nope   # 404
{"error":"not found"}
$ curl -s '…/api/links?owner=memo'
[{"slug":"gh",…,"hits":2}]

# Ctrl-C, restart tako dev:
$ curl -s …/api/links/gh
{"slug":"gh",…,"hits":3}          ← survived restart (.tako/data/entity.db)
```

Conformance(local): 17/17 ok. `go test ./...` green in all three
modules; hello goldens unchanged.

**Discovered work (not done)**

- Composite-index derivation (Where+OrderBy combinations → Datastore
  index.yaml) — F2 derives the per-store field set; composites are
  Phase E work when the Datastore backend makes them real.
- Query-chain attribution uses the payload type parameter; two stores
  sharing one payload type would pool their derived indexes. Harmless
  (over-indexing), but worth an analyzer note when a real example hits
  it.
- `runtime.BootLocal` is test plumbing that F7's NewTestApp replaces;
  the panic message for an unwired backend names both.

**GATE REQUEST — F2 (local): Entity + shortlink.** Please run
`validate/entity.md` — the centerpiece is §3 (kill/restart persistence)
and §4 (the unindexed-query error text) — and record the verdict in
`docs/validation-log.md`.

---

## F3 — DX proposal: Bucket + Blob (2026-07-19)

*Status: awaiting human approval. Scope per agent plan F3: keyed verbs,
`List`, streaming Blob through endpoints; fs local backend; conformance
grows. This slice starts `examples/gallery` — the example that demands
buckets — as a minimal upload/download app that later slices (F4–F9)
grow into spec Part 3.*

### 1. User code — `examples/gallery`, first slice

```go
package gallery

import "github.com/gsandoval/tako"

var (
	photos     = tako.Bucket("photos")
	photoStore = tako.Entity[Photo]("photos")
)

type Photo struct {
	ID      string `entity:"id" json:"id"`
	Caption string `json:"caption"`
}

var (
	Upload  = tako.API("POST /api/photos", upload)
	Image   = tako.API("GET /api/photos/{id}/image", image)
	List    = tako.API("GET /api/photos", list)
	Objects = tako.API("GET /api/objects", objects)
)

// A tako.Blob field makes the request multipart: img streams from the
// form field, caption arrives beside it.
type UploadReq struct {
	Caption string    `form:"caption"`
	Img     tako.Blob `form:"img"`
}

func upload(ctx tako.Ctx, req UploadReq) (Photo, error) {
	p := Photo{ID: tako.NewID(), Caption: req.Caption}
	if err := photos.Put(ctx, p.ID, req.Img); err != nil {
		return Photo{}, err
	}
	if err := photoStore.Put(ctx, p); err != nil {
		return Photo{}, err
	}
	return p, nil
}

type ImageReq struct {
	ID string `path:"id"`
}

// Returning tako.Blob streams the object: correct Content-Type, no JSON.
func image(ctx tako.Ctx, req ImageReq) (tako.Blob, error) {
	return photos.Get(ctx, req.ID) // miss → IsNotFound → 404
}

func list(ctx tako.Ctx, _ struct{}) ([]Photo, error) {
	return photoStore.Query(ctx).Limit(100).All()
}

// Bucket.List iterates keys by prefix.
func objects(ctx tako.Ctx, _ struct{}) ([]tako.ObjectInfo, error) {
	var out []tako.ObjectInfo
	it := photos.List(ctx, "")
	for o, ok := it.Next(); ok; o, ok = it.Next() {
		out = append(out, o)
	}
	return out, it.Err()
}
```

### 2. Semantics being locked (v0 Bucket/Blob contract)

- `Blob{Reader, ContentType, Size}`; `Size` -1 when unknown.
- **Response streaming**: a `tako.Blob` result streams with its
  Content-Type (and Content-Length when Size ≥ 0); error → the usual
  JSON error body. Codegen picks the blob path statically — the AppGraph
  knows the Res type.
- **Request blobs**: a `tako.Blob` field switches the endpoint to
  multipart; the blob streams from its `form:"…"` field with the part's
  Content-Type; other fields must be tagged (path/query/form).
- Keyed verbs mirror Entity: `Put` overwrites, `Get` miss → IsNotFound,
  `Delete` idempotent, `Exists`; `List(ctx, prefix)` yields ObjectInfo
  (Key, Size, ContentType, Updated) in lexicographic key order.
- Local backend: raw object bytes at `.tako/data/buckets/<bucket>/<key>`
  — inspectable, file IS the object; metadata (content type) in a
  `.meta/` sidecar directory beside the keys.
- `tako.NewID()` implemented: 16-char lowercase base32, crypto/rand.
- Conformance grows a Bucket contract suite (roundtrip incl. content
  type, overwrite, ErrNotFound, idempotent delete, prefix list + order).

### 3. Expected CLI transcript

```
$ cd examples/gallery && PORT=8090 go run ../../cmd/tako dev
  analyzing   1 service, 4 endpoints
  …
  bucket photos
  entity photos

$ curl -s -F caption=cat -F img=@cat.jpg localhost:8090/api/photos
{"id":"k7m2…","caption":"cat"}

$ curl -sI localhost:8090/api/photos/k7m2…/image | grep -i content-type
Content-Type: image/jpeg

$ curl -s localhost:8090/api/photos/k7m2…/image -o out.jpg && cmp cat.jpg out.jpg && echo identical
identical

$ curl -s localhost:8090/api/objects
[{"key":"k7m2…","size":51213,"contentType":"image/jpeg","updated":"…"}]

$ ls .tako/data/buckets/photos/
k7m2…                       ← the file is the object, byte for byte
```

### 4. New analyzer errors (exact texts, golden-tested)

```
app.go:14:2: tako.Blob field Img needs a form:"…" tag — blobs bind from
multipart form fields (tako/blob-binding)

app.go:12:15: GET route "GET /api/photos" cannot receive an upload —
requests with tako.Blob fields need a non-GET route (tako/blob-binding)

app.go:15:2: UploadReq mixes a tako.Blob field with untagged field Extra
— when a blob is present every other field needs a path:/query:/form:
tag (tako/blob-binding)
```

(Also: `tako.Bucket` name joins `tako/const-name` via the same resource
extraction as Entity.)

### 5. Not in scope (F3 non-goals)

Signed URLs (F6), bucket events / `photos.Uploaded` (F5), size/type
constraints (F6 options), thumbnails and queues (F4), GCS backend
(Phase E), response blobs from anything but Bucket.Get (works, but no
example demands constructing Blobs by hand yet).

---

## F3 — implementation: Bucket + Blob (2026-07-19)

DX proposal approved as written; shipped surface matches it.

**What shipped**

- **Bucket contract** in `pkg/backends` (streams + ObjectMeta, ErrNotFound,
  idempotent delete, prefix List in key order) and the **fs local
  backend**: the file at `.tako/data/buckets/<bucket>/<key>` IS the
  object; content types in a `.meta/` sidecar; `..`/absolute keys
  rejected. Conformance grows 7 bucket cases (roundtrip incl. content
  type, overwrite, isolation across buckets, prefix list order).
- **SDK Bucket live**: Get/Put/Delete/Exists/List through the boot seam
  (`hooks.Bucket`, wired by `runtime.Boot`/`BootLocal`); `Iter[ObjectInfo]`
  materialized in key order; SignedPut/SignedGet stay stubbed for F6.
  `tako.NewID()` implemented (16-char lowercase base32, crypto/rand).
  ObjectInfo gains JSON tags (key/size/contentType/updated).
- **Blob streaming both directions**: `runtime.BlobHandler` streams
  Blob results with Content-Type + Content-Length (falls back to
  application/octet-stream), closes closers; `runtime.FormBlob` binds
  multipart file fields with the part's Content-Type and size; error
  texts `form "img": missing file upload` and `body: invalid multipart
  form` (both 400).
- **Analyzer**: Bucket resource extraction (joins `tako/const-name`),
  `res_blob` detection choosing BlobHandler in codegen, blob `Binding`
  kind, and the three `tako/blob-binding` rules with the proposal's exact
  texts (needs-form-tag, no-uploads-on-GET, no-mixing-blob-and-body).
- **examples/gallery** (first slice): Upload (multipart) / Image
  (streamed) / List / Objects; suite covers byte-for-byte roundtrip with
  headers, object listing, 404, and the missing-file 400.

**Fixed during validation:** first smoke run showed `entity photos` but
no `bucket photos` in the dev banner — Bucket resource extraction had
been missed; added, goldens regenerated, banner verified.

**Tier-2 validation — real transcript (validate/bucket.md §§1–5, 7)**

```
$ cd examples/gallery && PORT=8090 tako dev
  POST /api/photos → Upload (app.go:16)
  GET /api/photos/{id}/image → Image (app.go:17)
  GET /api/photos → List (app.go:18)
  GET /api/objects → Objects (app.go:19)
  bucket photos
  entity photos
ready in 447ms — watching (backends: local, in-process)

$ curl -s -F caption=cat -F 'img=@cat.jpg;type=image/jpeg' …/api/photos
{"id":"v54nizkly7ibluym","caption":"cat"}
$ curl -sI …/api/photos/v54nizkly7ibluym/image | grep -i content-
Content-Length: 23
Content-Type: image/jpeg
$ curl -s …/…/image -o out.jpg && cmp cat.jpg out.jpg && echo identical
identical
$ curl -s …/api/objects
[{"key":"v54nizkly7ibluym","size":23,"contentType":"image/jpeg","updated":"…"}]
$ curl -s …/api/photos/nope/image        # 404
{"error":"not found"}
$ ls .tako/data/buckets/photos/
v54nizkly7ibluym                          ← the file is the object
# Ctrl-C: 0 listeners
```

Conformance(local): entity 17 + bucket 7 cases ok. `go test ./...`
green in all four modules.

**Discovered work (not done)**

- Multipart uploads buffer up to 32MB in memory/temp before the handler
  runs; fine for v0 (signed URLs are the large-upload story), but a
  streaming multipart reader would drop the buffering.
- Bucket.List materializes; a cursor-based Iter would matter for large
  buckets — revisit when a real example lists thousands of objects.
- `file` on macOS shows the object plainly — good; Finder preview needs
  an extension, which keys don't have. Cosmetic, noted only.

**GATE REQUEST — F3 (local): Bucket + Blob.** Please run
`validate/bucket.md` — the centerpiece is §5 (the on-disk layout
explaining itself) and §3 (streaming correctness) — and record the
verdict in `docs/validation-log.md`.

---

## F4 — DX proposal: Queue + Worker (2026-07-19)

*Status: awaiting human approval. Scope per agent plan F4: `Publish`,
`tako.Worker` binding (compile-checked payload), ack/retry/`Permanent`
mapping, dead-letter surfacing in dev output; channel local backend;
conformance grows. Gallery grows its thumbnail pipeline.*

### 1. User code — gallery grows the worker

```go
var thumbs = tako.Queue[ThumbJob]("thumbnails")

type ThumbJob struct {
	PhotoID string `json:"photoID"`
}

type Photo struct {
	ID      string `entity:"id" json:"id"`
	Caption string `json:"caption"`
	Ready   bool   `json:"ready"`   // flipped by the worker
}

// upload now enqueues work after storing the original:
//	if err := thumbs.Publish(ctx, ThumbJob{PhotoID: p.ID}); err != nil { … }

// Exactly one worker per queue — a second is an analysis error.
var MakeThumb = tako.Worker(thumbs, makeThumb)

// One async shape everywhere: nil → ack, error → retry,
// tako.Permanent(err) → give up and dead-letter.
func makeThumb(ctx tako.Ctx, job ThumbJob) error {
	img, err := photos.Get(ctx, job.PhotoID)
	if err != nil {
		return err // transient (object may lag the message): retry
	}
	if img.Size == 0 {
		return tako.Permanent(errors.New("empty upload")) // never retriable
	}
	if !strings.HasPrefix(img.ContentType, "image/") {
		return fmt.Errorf("unsupported content type %q", img.ContentType) // retries, then dead-letters
	}
	thumb := tako.Blob{Reader: img.Reader, ContentType: img.ContentType, Size: img.Size}
	if err := photos.Put(ctx, job.PhotoID+"_thumb", thumb); err != nil {
		return err
	}
	return photoStore.Update(ctx, job.PhotoID, func(p *Photo) { p.Ready = true })
}

// New endpoint: GET /api/photos/{id}/thumb streams the worker's output.
```

(v0 "thumbnail" is a passthrough copy — real resizing is app code and
arrives with F9's full gallery; the pipeline is what F4 ships.)

### 2. Semantics being locked (v0 Queue/Worker contract)

- `Publish(ctx, msg)` enqueues; the worker runs asynchronously,
  in-process locally (breakpoints step from publish into the worker).
- Return mapping: `nil` acks; any other error redelivers with backoff
  until an attempt cap, then dead-letters; `tako.Permanent(err)`
  dead-letters immediately, no retries.
- Local policy, stated honestly in dev output: 5 attempts, fixed 200ms
  backoff (cloud uses the real Pub/Sub retry policy from Phase F).
- Dead letters are inspectable files:
  `.tako/data/deadletter/<queue>/<id>.json` — payload, error, attempts.
- Conformance grows a Queue suite: deliver, ack-stops-redelivery,
  error→redelivery→success, Permanent→one attempt, attempt-cap→dead-
  letter, queue isolation.
- Test plumbing until F7's DrainQueues: `runtime.WaitIdle(timeout)`
  blocks until local queues are drained.

### 3. Dev output (the gate's centerpiece)

Banner adds the worker with its binding:

```
  worker MakeThumb ← queue thumbnails (app.go:31)
  bucket photos
  entity photos
  queue thumbnails
```

Happy path (curl an image):

```
⚙ thumbnails → MakeThumb ok (2ms)
```

Failing worker — retry then dead-letter (curl a .txt as text/plain):

```
⚙ thumbnails → MakeThumb attempt 1/5 failed: unsupported content type "text/plain" — retrying in 200ms
⚙ thumbnails → MakeThumb attempt 2/5 failed: … — retrying in 200ms
…
✗ dead-letter thumbnails: MakeThumb gave up after 5 attempts: unsupported content type "text/plain"
  payload → .tako/data/deadletter/thumbnails/<id>.json
```

Permanent (upload an empty file):

```
✗ dead-letter thumbnails: MakeThumb permanent failure: empty upload
  payload → .tako/data/deadletter/thumbnails/<id>.json
```

### 4. New analyzer errors (exact texts, golden-tested)

```
app.go:40:24: queue "thumbnails" already has worker MakeThumb (app.go:31)
— a queue has exactly one worker; use a Topic for fan-out
(tako/one-worker)

app.go:31:25: tako.Worker must bind a queue declared as a package-level
var in this module (tako/worker-binding)

app.go:31:40: tako.WithTTL is not a tako.Worker option (tako.Worker
accepts WithProfile) (tako/invalid-option)
```

(Queue names join `tako/const-name`. Payload-shape mismatches stay
compile errors — the compile fixture grows a Worker case.)

### 5. Service partitioning honesty

The AppGraph now records each worker as its own service (spec §4.1) —
`gen graph` shows `service gallery-makethumb (worker)`. Locally one
process runs everything: the generated app main also wires workers into
the in-process dispatcher, and that collapse is printed in the banner
(`local: workers run in-process`). Real per-worker services arrive with
the cloud phases.

### 6. Not in scope (F4 non-goals)

Topics/Subscribe/fan-out (F5), bucket events (F5), `WithDelay`/rate
limits (F8), chaos mode (F7), Pub/Sub (Phase F), retry-policy options.

---

## F4 — implementation: Queue + Worker (2026-07-19)

DX proposal approved as written; shipped surface matches it.

**What shipped**

- **Queue contract** (`pkg/backends`): Publish/Consume with at-least-once
  delivery; `DeadLetter` record; `IsPermanent` duck-typing so backends
  never import the SDK. Conformance grows 6 queue cases (deliver+ack,
  redeliver-until-success, permanent one-attempt, exhaustion cap, queue
  isolation, publish-without-worker error).
- **Channel dispatcher** (`pkg/backends/local`): per-message goroutine
  detached from the request context, 5 attempts × 200ms fixed backoff
  (defaults stated, configurable for tests), dead letters written as
  inspectable JSON to `.tako/data/deadletter/<queue>/` and surfaced to
  observers; `WaitIdle` drains for tests.
- **SDK live**: `Publish` (JSON payload via the boot seam), `Permanent`
  (unwrappable, duck-marked), `Worker` records its queue + a typed
  decode-and-invoke closure; undecodable payloads dead-letter as
  permanent. `internal/hooks.HandlerInfo` opens Handler records to the
  runtime without exporting internals.
- **Runtime**: `RegisterWorker` binds workers on the wired backend with a
  worker-scoped logger; the dev console narrates delivery (⚙ ok /
  ⚙ attempt n/max failed … retrying / ✗ dead-letter with payload path);
  `WaitIdle(timeout)` as interim test plumbing (F7 replaces).
- **Analyzer**: Queue resources (join `tako/const-name`), Worker handlers
  partitioned into their own services (`gallery-makethumb`) with
  `tako/one-worker`, `tako/worker-binding`, and the Worker option set on
  `tako/invalid-option`; compile fixture grows the async payload-shape
  case. Banner counts endpoints and workers separately and prints the
  local-collapse honesty line; `HandlerLine` renders
  `worker MakeThumb ← queue thumbnails (app.go:35)`.
- **Codegen**: app-service mains register the module's workers
  (`runtime.RegisterWorker("MakeThumb", gallery.MakeThumb)`); worker-only
  services generate no main yet (their push-endpoint mains are Phase F).
- **Gallery**: thumbnail pipeline per the proposal — publish on upload,
  worker copies to `_thumb` + flips `Ready`, `GET …/thumb` streams the
  result; failure levers: non-image → retries → dead-letter; empty
  upload → `Permanent`. Suite covers pipeline success, both dead-letter
  shapes (attempts/permanent/lastError asserted from the JSON file), and
  ready-flag integrity.

**Fixed during implementation:** the analyzer restructure (resources
pass before handlers pass) initially failed to apply via a fuzzy patch
and Queue/Worker silently didn't extract — caught by the gallery golden
diff; re-applied with an exact edit.

**Tier-2 validation — real transcript (validate/queue.md §§1–5)**

```
$ cd examples/gallery && PORT=8090 tako dev
  analyzing   2 services, 5 endpoints, 1 worker
  …
  worker MakeThumb ← queue thumbnails (app.go:35)
  queue thumbnails
  local: workers run in-process (per-worker services arrive with cloud deploys)

$ curl -F caption=cat -F 'img=@cat.jpg;type=image/jpeg' …/api/photos
⚙ thumbnails → MakeThumb ok (9ms)
$ curl -s …/api/photos            # "ready": true
$ curl -s …/api/photos/<id>/thumb # FAKEJPEG (worker's output streams)

$ curl -F caption=doc -F 'img=@notes.txt;type=text/plain' …/api/photos
⚙ thumbnails → MakeThumb attempt 1/5 failed: unsupported content type "text/plain" — retrying in 200ms
⚙ … attempt 2/5 … 3/5 … 4/5 …
✗ dead-letter thumbnails: MakeThumb gave up after 5 attempts: unsupported content type "text/plain"
  payload → .tako/data/deadletter/thumbnails/

$ curl -F caption=void -F 'img=@empty.png;type=image/png' …/api/photos
✗ dead-letter thumbnails: MakeThumb permanent failure: empty upload

$ ls .tako/data/deadletter/thumbnails/   # two inspectable JSON files
# Ctrl-C: 0 listeners
```

Conformance(local): entity 17 + bucket 7 + queue 6 ok. `go test ./...`
green in all four modules.

**Discovered work (not done)**

- Redelivery of a Blob-consuming retry: MakeThumb re-Gets the object per
  attempt so streams re-open correctly; a doc note for users writing
  workers that consume readers would help (readers don't rewind).
- Dead letters have no replay tool yet; `tako queue replay <file>` is a
  natural CLI addition once a real workflow demands it.
- The local dispatcher delivers immediately with fixed backoff; jittered
  or exponential local backoff only matters once chaos mode (F7) starts
  exercising timing.

**GATE REQUEST — F4 (local): Queue + Worker.** Please run
`validate/queue.md` — the centerpiece is §3, watching the retry
narration end in a dead-letter — and record the verdict in
`docs/validation-log.md`.

---

## F5 — DX proposal: Topic + Subscribe + bucket events (2026-07-19)

*Status: awaiting human approval. Scope per agent plan F5: fan-out
semantics (per-handler independence), `bucket.Uploaded`,
duplicate-worker vs. multi-subscriber analysis rules. Gallery grows its
event spine and moves to the spec Part 3 pipeline shape (minus signed
URLs, which are F6).*

### 1. User code — gallery grows the event spine

```go
var (
	photos     = tako.Bucket("photos")
	photoStore = tako.Entity[Photo]("photos")
	thumbs     = tako.Queue[ThumbJob]("thumbnails")
	ready      = tako.Topic[PhotoReady]("photo-ready") // fan-out: MANY subscribers
)

// Owner joins this payload when auth lands (F9 aligns fully with spec
// Part 3); until then Caption is the human-readable bit.
type PhotoReady struct {
	PhotoID string `json:"photoID"`
	Caption string `json:"caption"`
}

var (
	// The pipeline is now event-driven end to end:
	// upload → bucket event → queue → worker → topic → 2 subscribers.
	OnUploaded  = tako.Subscribe(photos.Uploaded, onUploaded)
	MakeThumb   = tako.Worker(thumbs, makeThumb)
	NotifyOwner = tako.Subscribe(ready, notifyOwner)
	WarmFeed    = tako.Subscribe(ready, warmFeed)
)

// upload no longer publishes ThumbJob — the bucket event does. It puts
// the entity first, then the blob (the event races the entity write;
// worker retries absorb the gap, as F4 established).

// Bucket events see EVERY successful write — including the worker's own
// `_thumb` writes. Filtering your own output is the standing idiom
// (straight from spec Part 3):
func onUploaded(ctx tako.Ctx, obj tako.ObjectInfo) error {
	if strings.HasSuffix(obj.Key, "_thumb") {
		return nil
	}
	return thumbs.Publish(ctx, ThumbJob{PhotoID: obj.Key})
}

// makeThumb keeps its F4 body and gains one closing line:
//	return ready.Publish(ctx, PhotoReady{PhotoID: p.ID, Caption: p.Caption})

// Two independent subscribers to one event. notifyOwner carries the
// demo failure lever (caption containing "boom" → retries → dead-letter
// while WarmFeed acks the same event untouched):
func notifyOwner(ctx tako.Ctx, e PhotoReady) error {
	if strings.Contains(e.Caption, "boom") {
		return fmt.Errorf("notification service rejected %q", e.Caption)
	}
	ctx.Log("owner notified", "photo", e.PhotoID)
	return nil
}

func warmFeed(ctx tako.Ctx, e PhotoReady) error {
	ctx.Log("feed warmed", "photo", e.PhotoID) // real cache arrives F8
	return nil
}
```

(Handler signatures are compile-checked exactly like Worker:
`tako.Subscribe(ready, fn)` requires `func(Ctx, PhotoReady) error`;
`tako.Subscribe(photos.Uploaded, fn)` requires
`func(Ctx, tako.ObjectInfo) error`.)

### 2. Semantics being locked (v0 Topic/Subscribe/bucket-event contract)

- **Fan-out with per-handler independence**: every subscriber gets every
  event; each subscriber has its own attempt counter, its own retry
  schedule, and its own dead-letter. One subscriber failing never delays
  or poisons another.
- Same async shape and retry policy as F4: `nil` ack, error → 5
  attempts × 200ms locally, `tako.Permanent` → immediate dead-letter.
- **Per-subscriber dead letters**:
  `.tako/data/deadletter/<topic>/<subscriber>/<id>.json`. (Queues keep
  their F4 layout `deadletter/<queue>/` — a queue has exactly one
  consumer, so no subdir.)
- A topic with zero subscribers is legal; publish is a broadcast into
  silence (matches Pub/Sub topics without subscriptions).
- **`bucket.Uploaded`** fires after every successful `Put` locally
  (GCS notifications provide it in Phase F — same contract). Payload is
  `tako.ObjectInfo`. It fires for the service's own writes too; the
  `_thumb` filter above is the documented idiom. Dead letters for bucket
  events land under `deadletter/<bucket>.uploaded/<subscriber>/`.
- Delivery is at-least-once, unordered — same honesty as F4.
- **Backend contract** grows `backends.Topic`
  (`Publish(ctx, topic, payload)` /
  `Subscribe(topic, subscriber, deliver)`) and the conformance suite
  grows a Topic chapter: fan-out to N, independent retry (one fails
  while the other acks), per-subscriber dead-letter, Permanent,
  zero-subscriber publish, topic isolation — plus a bucket-event
  chapter: Put → ObjectInfo delivered, self-write events fire too.

### 3. Dev output

Banner (gallery now partitions into 5 services):

```
analyzing … 5 services, 5 endpoints, 1 worker, 3 subscribers
  subscriber OnUploaded ← bucket photos.Uploaded (app.go:38)
  worker MakeThumb ← queue thumbnails (app.go:39)
  subscriber NotifyOwner ← topic photo-ready (app.go:40)
  subscriber WarmFeed ← topic photo-ready (app.go:41)
  …
  topic photo-ready
  local: workers and subscribers run in-process (per-handler services arrive with cloud deploys)
```

One clean upload narrates the whole spine — note OnUploaded firing a
second time for the worker's `_thumb` write and acking it (the filter
at work):

```
⚙ photos.Uploaded → OnUploaded ok (0ms)
⚙ thumbnails → MakeThumb ok (2ms)
⚙ photos.Uploaded → OnUploaded ok (0ms)
⚙ photo-ready → NotifyOwner ok (0ms)
⚙ photo-ready → WarmFeed ok (0ms)
```

The gate's centerpiece — one event, two subscribers, independent fates
(upload with caption "boom"):

```
⚙ photo-ready → WarmFeed ok (0ms)
⚙ photo-ready → NotifyOwner attempt 1/5 failed: notification service rejected "boom" — retrying in 200ms
…
✗ dead-letter photo-ready: NotifyOwner gave up after 5 attempts: notification service rejected "boom"
  payload → .tako/data/deadletter/photo-ready/NotifyOwner/
```

WarmFeed's ack printing before NotifyOwner even starts failing IS the
feature.

### 4. New analyzer errors (exact texts, golden-tested)

```
app.go:40:27: tako.Subscribe must bind a topic or bucket event declared
as a package-level var in this module (tako/subscribe-binding)

app.go:40:44: tako.WithTTL is not a tako.Subscribe option
(tako.Subscribe accepts WithProfile) (tako/invalid-option)
```

Topic names join `tako/const-name`. Multi-subscriber is explicitly
legal — the gallery golden itself has two subscribers on one topic and
zero diagnostics (this is the "duplicate-worker vs. multi-subscriber"
distinction from the plan: queue + second worker errors, topic + Nth
subscriber is the point). Payload mismatches stay compile errors — the
compile fixture grows Subscribe cases (topic and bucket-event).

### 5. Service partitioning

Each subscriber is its own service in the AppGraph
(`gallery-onuploaded`, `gallery-notifyowner`, `gallery-warmfeed`), same
as workers in F4 — independent scaling and IAM per spec §4.1. The local
banner honesty line covers both kinds.

### 6. Not in scope (F5 non-goals)

Signed URLs (F6 — upload stays a multipart endpoint), Cache/`GetOrFill`
(F8 — warmFeed logs), auth/Owner in payloads (F9), `WithParallel`,
external/cross-project topics, GCS notification plumbing (Phase F),
dead-letter replay CLI (noted discovered work), ordering keys.

---

## F5 — implementation: Topic + Subscribe + bucket events (2026-07-19)

DX proposal approved as written; shipped surface matches it, with one
spelling unification: the bucket-event source is `photos.Uploaded`
everywhere — console lines, dead-letter directory, AppGraph binding —
matching the exact spelling users write in code (the proposal's §2 had
shown a lowercase `.uploaded` dir; the capitalized form won).

**What shipped**

- **Topic contract** (`pkg/backends/topic.go`): `Publish` broadcasts to
  every subscriber; `Subscribe(topic, subscriber, deliver)` registers a
  named subscriber. `DeadLetter` grew `subscriber` (omitempty — queue
  letters are byte-identical to F4's). `ObjectMeta` grew json tags: it IS
  the bucket-event wire shape, matching `tako.ObjectInfo`.
- **Local dispatcher generalized** (`pkg/backends/local/queue.go`): one
  delivery loop now serves queues and topics; each (topic, subscriber)
  pair gets its own attempts/backoff/dead-letter
  (`deadletter/<topic>/<subscriber>/<id>.json`; queues keep their flat
  F4 layout). `Topics()` adapts the dispatcher to the topic contract.
  Observer callbacks gained the consumer name.
- **Bucket events** (`pkg/backends/local/bucket.go`): `OnPut` observer on
  the fs store; `WireBucketEvents` publishes each successful write's
  ObjectMeta on `<bucket>.Uploaded`. Wired in `runtime.BootLocal` — the
  local stand-in for GCS notifications (Phase F ships the same contract).
- **SDK live** (root): `TopicHandle.Publish`, `Subscribe` (topic AND
  bucket-event bindings, one generic body), `BucketEvent.sourceName`.
  `EventSource[T]` gained the unexported `sourceName() string` — the SDK
  lock flagged it and was updated deliberately: the interface remains
  implementable only by SDK types; user-visible surface is unchanged.
  Handler's internal field renamed queue→source.
- **Runtime**: `RegisterSubscriber` beside `RegisterWorker`; console
  narration carries the subscriber name per delivery; dead-letter lines
  print the per-subscriber path.
- **Analyzer**: `topic` resources (payload type recorded, `const-name`
  enforced); `subscribe` handlers with `binding` + new `binding_kind`
  ("topic"/"bucket") in the AppGraph; each subscriber partitions into its
  own service. New rule `tako/subscribe-binding`; Subscribe joins
  `invalid-option` (accepts WithProfile). Multi-subscriber is legal by
  construction — the gallery golden has two subscribers on one topic and
  zero diagnostics. Fixtures: subscribebinding, subscribeopt; the compile
  fixture grew a Subscribe payload-mismatch case. New
  `gallery_summary.golden.txt` covers the subscriber banner lines.
- **Codegen**: `RegisterSubscriber` lines in the app main (local
  collapse); subscriber-only services generate no main yet (asyncOnly,
  formerly workerOnly).
- **Devserver**: banner counts endpoints/workers/subscribers accurately;
  honesty line adapts (`workers and subscribers run in-process`).
- **Conformance grows two chapters**: Topic (6 cases — fan-out,
  independent retry, per-subscriber dead letters, Permanent,
  zero-subscriber publish, isolation) and BucketEvents (3 cases — Put
  publishes meta, self-writes fire, per-bucket topics). Total
  local suite: entity 18, bucket 7, queue 6, topic 6, bucket-events 3.
- **Gallery** is event-driven end to end per spec Part 3's shape (minus
  signed URLs): upload writes entity+blob only; `photos.Uploaded` →
  `OnUploaded` (with the `_thumb` self-write filter) → `thumbs` →
  `MakeThumb` → `ready` → `NotifyOwner` + `WarmFeed`. Caption containing
  "boom" is the fan-out failure lever. New `TestFanOutIndependence`
  asserts the boom letter's queue/subscriber/attempts/lastError and that
  WarmFeed and the queue stayed clean.

**Bug found & fixed during validation (in scope — the dev loop could not
survive the validation run):** `tako dev` reloaded endlessly while idle.
Two causes, both fixed in `pkg/devserver`: (1) macOS kqueue surfaces
attribute-only (Chmod) events when the analyzer READS source files
(atime), so every analyze pass triggered the next reload — chmod-only
events are now ignored (real saves emit Write/Create); (2) events from
hidden paths (.tako's own generated output/data) are now filtered.
Verified: 25s idle → zero reloads; a real append to app.go still
reloads. This also explains why the first validation curl came back
empty (a reload killed the in-flight request). Also of note: killing
`go run` does not kill the dev child process — orphaned instances then
fight over port and bbolt lock; noted under discovered work as a future
`tako dev` hardening item (own-pid lockfile).

**Validation transcript (tier 2, validate/topic.md, PORT=8090)**

Banner:

```
  analyzing   5 services, 5 endpoints, 1 worker, 3 subscribers
  ...
  subscriber OnUploaded ← bucket photos.Uploaded (app.go:44)
  worker MakeThumb ← queue thumbnails (app.go:45)
  subscriber NotifyOwner ← topic photo-ready (app.go:46)
  subscriber WarmFeed ← topic photo-ready (app.go:47)
  bucket photos
  entity photos
  queue thumbnails
  topic photo-ready
  local: workers and subscribers run in-process (per-handler services arrive with cloud deploys)
ready in 689ms — watching (backends: local, in-process)
```

§2 clean upload (`caption=cat`) — the whole spine, no hand-published
messages; note OnUploaded firing twice (the worker's `_thumb` write,
acked by the filter):

```
POST /api/photos → 200 (23.7ms)
⚙ photos.Uploaded → OnUploaded ok (0s)
⚙ photos.Uploaded → OnUploaded ok (0s)
⚙ thumbnails → MakeThumb ok (9ms)
INFO owner notified subscriber=NotifyOwner photo=yxwkkk3h3pqibl4w
⚙ photo-ready → NotifyOwner ok (0s)
INFO feed warmed subscriber=WarmFeed photo=yxwkkk3h3pqibl4w
⚙ photo-ready → WarmFeed ok (0s)
```

`GET /api/photos` → `"ready":true`.

§3 fan-out, independent fates (`caption=boom`) — WarmFeed acks before
NotifyOwner's first failure:

```
⚙ thumbnails → MakeThumb ok (9ms)
INFO feed warmed subscriber=WarmFeed photo=e2f7jwanq7s3auve
⚙ photo-ready → WarmFeed ok (0s)
⚙ photo-ready → NotifyOwner attempt 1/5 failed: notification service rejected "boom" — retrying in 200ms
⚙ photo-ready → NotifyOwner attempt 2/5 failed: … — retrying in 200ms
⚙ photo-ready → NotifyOwner attempt 3/5 failed: … — retrying in 200ms
⚙ photo-ready → NotifyOwner attempt 4/5 failed: … — retrying in 200ms
✗ dead-letter photo-ready: NotifyOwner gave up after 5 attempts: notification service rejected "boom"
  payload → .tako/data/deadletter/photo-ready/NotifyOwner/
```

§4 the letter (`deadletter/photo-ready/NotifyOwner/…json`):

```
{"queue": "photo-ready", "subscriber": "NotifyOwner", "attempts": 5,
 "lastError": "notification service rejected \"boom\"", "permanent": false,
 "payload": {"photoID": "e2f7jwanq7s3auve", "caption": "boom"}, "time": …}
```

No WarmFeed directory exists; the boom photo IS `"ready": true` (only
the notification failed).

§5 inline-constructor binding → analysis error, last good build keeps
serving:

```
✗ analysis failed — still serving last good build
  app.go:46:31: tako.Subscribe must bind a topic or bucket event declared as a package-level var in this module (tako/subscribe-binding)
```

§7 `notifyOwner(ctx, e string)` → Go compile error naming the inferred
type mismatch, not a tako error. §8: conformance 40/40; `go test ./...`
green in all four modules.

**Discovered work (noted, not done)**

- `tako dev` hardening: pidfile/port-guard so an orphaned instance (e.g.
  after `go run`'s parent is killed) can't silently fight a new one over
  the port and the bbolt lock.
- Dead-letter replay CLI grows a per-subscriber dimension
  (`tako replay <topic>/<subscriber>`) — already noted in F4; shape
  updated.

**GATE REQUEST — F5 (local): Topic + Subscribe + bucket events.** Please
run `validate/topic.md` — the centerpiece is §3, one event splitting
into two independent fates — and record the verdict in
`docs/validation-log.md`.

---

## F6 — DX proposal: Signed URLs, local (2026-07-20)

*Status: awaiting human approval. Scope per agent plan F6: `SignedPut` /
`SignedGet` with size/content-type constraints via local endpoints. Gate
F6: human uploads oversized and wrong-type files, reviews rejections.*

### 1. User code — gallery grows the direct-upload flow

```go
// Client asks to upload → gets a ticket with a signed URL. The bytes
// then go straight to storage — no 32MB request cap, no proxying
// through the service (spec Part 3's startUpload, minus auth until F9).
type StartUploadReq struct {
	Caption string `json:"caption"`
}

type UploadTicket struct {
	Photo     Photo  `json:"photo"`
	UploadURL string `json:"uploadURL"`
}

var StartUpload = tako.API("POST /api/uploads", startUpload)

func startUpload(ctx tako.Ctx, req StartUploadReq) (UploadTicket, error) {
	p := Photo{ID: tako.NewID(), Caption: req.Caption}
	if err := photoStore.Put(ctx, p); err != nil {
		return UploadTicket{}, err
	}
	url, err := photos.SignedPut(ctx, p.ID, 15*time.Minute,
		tako.WithContentType("image/*"), tako.WithMaxSize(20<<20))
	return UploadTicket{Photo: p, UploadURL: url}, err
}

// And a signed download link on demand:
type DownloadReq struct {
	ID string `path:"id"`
}

var Download = tako.API("GET /api/photos/{id}/download", download)

func download(ctx tako.Ctx, req DownloadReq) (string, error) {
	return photos.SignedGet(ctx, req.ID, 15*time.Minute)
}
```

The client's side is two curls:

```
curl -s -X POST localhost:8090/api/uploads -d '{"caption":"cat"}'
  → {"photo":{...,"ready":false}, "uploadURL":"http://localhost:8090/_tako/signed/<token>"}
curl -s -X PUT --data-binary @cat.jpg -H 'Content-Type: image/jpeg' '<uploadURL>'
```

…and the moment that PUT lands, the F5 event spine takes over
unchanged: `photos.Uploaded → OnUploaded → thumbnails → MakeThumb →
photo-ready`. Nothing else in the app changes. (The F3 multipart
`Upload` endpoint stays; F9 decides which the finished gallery keeps.)

### 2. Semantics being locked (v0 signed-URL contract)

- `SignedPut(ctx, key, ttl, opts…)` returns an absolute URL accepting
  exactly one verb: `PUT` with the raw bytes as the body and the
  client's `Content-Type` header. `SignedGet(ctx, key, ttl)` returns a
  URL streaming the object (content type intact, `404` JSON if absent).
- **Constraints are enforced at upload time, not sign time**, and each
  rejection is product UX (exact texts in §4): size cap → 413,
  content-type mismatch → 415, expiry → 403, tampered token → 403.
  `WithContentType("image/*")` matches on the `/`-prefix (`image/*` ⊇
  `image/png`); a pattern without `*` must match exactly.
- **Size is enforced twice**: a declared `Content-Length` over the cap
  is rejected before reading the body, and the stream is hard-capped
  while writing, so a lying client cannot exceed the limit either.
- **A signed upload IS a Put**: it fires `bucket.Uploaded` exactly like
  `photos.Put` (locally it goes through the same store; in the cloud
  GCS notifies — same contract).
- Local URLs live under the reserved path `/_tako/signed/<token>` on
  the app's own server; the token is base64(JSON claims) + HMAC-SHA256
  with a secret persisted at `.tako/data/signing.key` — URLs survive
  `tako dev` reloads and restarts. `/_tako/` is documented as reserved.
- Options are validated at call time: `SignedPut` with e.g. `WithTTL`
  returns the error `tako.WithTTL is not a SignedPut option (SignedPut
  accepts WithMaxSize, WithContentType)` — no analyzer rule; this is a
  method call, not a declaration.
- Backend seam: `backends.URLSigner` (`SignPut(bucket, key, ttl,
  constraints)`, `SignGet(bucket, key, ttl)`); the local signer + the
  HTTP endpoint are the v0 pair, the GCS V4 signer arrives Phase F.
- **Conformance grows a SignedURL chapter** run over (signer, handler)
  pairs: put-roundtrip fires the upload event, size cap (declared and
  lying stream), content-type mismatch, expiry, tampered signature,
  get-roundtrip + absent-key 404. Phase F runs the same chapter against
  real GCS, where GCS itself is the enforcer.

### 3. Generated code stays readable

The generated main mounts the endpoint explicitly when the app declares
a bucket — no hidden routes:

```go
	// Signed upload/download URLs land here (SignedPut/SignedGet).
	mux.Handle("/_tako/signed/", runtime.SignedURLHandler())
```

The dev console keeps narrating: a signed upload logs the PUT and then
the familiar ⚙ chain.

### 4. Rejection texts (exact, under test)

```
413 {"error":"upload is 22020096 bytes; the signed URL allows at most 20971520"}
415 {"error":"content type \"text/plain\" does not match required \"image/*\""}
403 {"error":"signed URL expired"}
403 {"error":"invalid signature"}
404 {"error":"not found"}
405 {"error":"signed upload URLs accept PUT only"}
```

### 5. Not in scope (F6 non-goals)

GCS V4 signing and `iam.serviceAccountTokenCreator` (Phase F), browser
CORS on the signed endpoints (F9, when the gallery grows its web UI),
resumable/chunked uploads, one-time-use URLs, signed DELETE, per-URL
rate limits, auth on StartUpload (F9 adds `tako.WithAuth()` per spec).

---

## F6 — implementation: Signed URLs, local (2026-07-20)

DX proposal approved as written; shipped surface matches it, with one
addition to the locked texts: a lying stream (no/false Content-Length)
gets its own 413 body — `upload exceeds the signed URL's <n>-byte
limit` — since the actual size is unknowable mid-stream; the declared-
length text from §4 is used when Content-Length is present. Both are
under test.

**What shipped**

- **Signer seam** (`pkg/backends/signed.go`): `SignConstraints{MaxSize,
  ContentType}` + `URLSigner{SignPut, SignGet}`; wired via
  `hooks.Signer`. The GCS V4 signer arrives Phase F behind the same
  interface.
- **Local signer** (`pkg/backends/local/signer.go`): URLs are
  `<base>/_tako/signed/<base64url claims>.<HMAC-SHA256>`; the secret
  persists at `.tako/data/signing.key` (0600), so URLs survive reloads
  and restarts. Claims carry bucket/key/method/expiry/constraints.
- **Local enforcer** (`pkg/backends/local/signedhttp.go`):
  `SignedURLEndpoint(signer, store)` — verb check (405 texts per
  method), content-type pattern match (415), size enforced twice (413
  on declared Content-Length; a capped reader catches lying streams and
  the partial object is deleted), 403 for expired vs. tampered as
  distinct texts, 404 JSON on absent downloads. A signed upload goes
  through `BucketStore.Put`, so `photos.Uploaded` fires exactly like
  any write — the F5 spine needs zero changes.
- **SDK live**: `SignedPut` (parses WithMaxSize/WithContentType;
  any other option errors at call time with the exact proposal text —
  `optionDisplayName` maps internal names back to `WithX`),
  `SignedGet`. No surface change; the two methods existed as stubs
  since F3, so the SDK lock is untouched.
- **Boot**: `BootLocal` mints the signer with base
  `http://localhost:$PORT`; `runtime.SignedURLHandler()` adapts the
  booted pair for mains.
- **Codegen**: the mount is explicit and commented in the generated
  main whenever the app declares a bucket — no hidden routes.
- **Conformance grows a SignedURL chapter** (7 cases): put-roundtrip +
  upload event, declared-oversize, lying-stream, content-type, expired,
  tampered, get-roundtrip/absent. It asserts statuses and effects (no
  object survives a rejection), not texts — in the cloud GCS is the
  enforcer and words its own refusals; the exact local texts are
  asserted in the gallery suite instead.
- **Gallery**: `StartUpload` (ticket with signed PUT URL, image/*,
  20MB) and `Download` (signed GET URL) per spec Part 3 minus auth;
  the F3 multipart endpoint stays until F9 decides. Tests:
  `TestSignedUploadPipeline` (ticket → PUT → spine → ready → signed
  download roundtrip) and `TestSignedUploadRejections` (all six
  rejection texts byte-for-byte, partial-object cleanup, negative-ttl
  expiry, call-time option error).

**Validation transcript (tier 2, validate/signed.md, PORT=8090)**

Banner: `analyzing 5 services, 7 endpoints, 1 worker, 3 subscribers`;
generated main shows the explicit
`mux.Handle("/_tako/signed/", runtime.SignedURLHandler())`.

§2 ticket → direct PUT → spine:

```
POST /api/uploads → {"photo":{"id":"d26uib6z…","ready":false},
  "uploadURL":"http://localhost:8090/_tako/signed/eyJiIjoi…"}
PUT <uploadURL> → 200
⚙ photos.Uploaded → OnUploaded ok (0s)   ×2 (original + _thumb, filter acks)
⚙ thumbnails → MakeThumb ok (8ms)
⚙ photo-ready → NotifyOwner ok / WarmFeed ok
GET /api/photos → "ready":true
```

§3 rejections, exact texts live:

```
415 {"error":"content type \"text/plain\" does not match required \"image/*\""}
413 {"error":"upload is 22020096 bytes; the signed URL allows at most 20971520"}
403 {"error":"invalid signature"}
```

`/api/objects` afterward: only the good photo + its `_thumb` — no
partial objects, no pipeline noise from rejected uploads.

§4 signed download: 200 with `Content-Type: image/jpeg` +
`Content-Length`; PUT on a download URL →
`405 {"error":"signed download URLs accept GET only"}`.

§5 minted a URL, forced a reload (2 reloads logged), PUT to the
pre-reload URL → 200 and the spine ran — signing.key persistence works.

§6: conformance 47 cases green; `go test ./...` green in all four
modules.

**Discovered work (noted, not done)**

- Request log prints the full signed token path — consider truncating
  `/_tako/signed/<token>` in the dev request log for readability.
- The orphaned-dev-instance hazard (noted at F5) bit again while
  re-verifying validate/signed.md §4: a stale instance held :8090, the
  new app died with "address already in use", but `tako dev` still
  printed "ready" because waitReady only dials the port — which the
  orphan answered. The hardening should make dev fail loudly when its
  OWN app didn't bind (own-pid lockfile / health token), not just when
  nothing listens.
- CORS headers on /_tako/signed for browser-direct uploads (F9's web
  UI will need them).

**GATE REQUEST — F6 (local): Signed URLs.** Please run
`validate/signed.md` — the centerpiece is §3, judging whether each
rejection body alone tells a client-side engineer what to fix — and
record the verdict in `docs/validation-log.md`.

---

## F7 — DX proposal: Testing as a feature (2026-07-21)

*Status: awaiting human approval. Scope per agent plan F7: `NewTestApp`,
`SignIn`, `DrainQueues`, chaos mode (`app.Chaos()`, `tako dev --chaos`,
seeded/reproducible); `tako test` with generation-freshness check;
shortlink suite rewritten user-style. Gate F7: human seeds a
deliberately non-idempotent worker, watches chaos catch it with a
printed reproduction seed.*

### 1. User code — the test story (shortlink, rewritten user-style)

Shortlink first becomes honestly asynchronous: `resolve` stops
incrementing inline and publishes a click event; a worker tallies it
**exactly once** even under duplicate delivery:

```go
var (
	links  = tako.Entity[Link]("links")
	clicks = tako.Queue[Click]("clicks")
	seen   = tako.Entity[Click]("clicks-seen") // processed-event log: the idempotency ledger
)

// Click's ID is minted at publish time — it IS the idempotency key.
type Click struct {
	ID   string `entity:"id" json:"id"`
	Slug string `json:"slug"`
}

var CountClick = tako.Worker(clicks, countClick)

// resolve returns the link and records the click asynchronously; Hits
// is eventually consistent now (semantic change from F2, stated below).
func resolve(ctx tako.Ctx, req ResolveReq) (Link, error) {
	l, err := links.Get(ctx, req.Slug)
	if err != nil {
		return Link{}, err
	}
	return l, clicks.Publish(ctx, Click{ID: tako.NewID(), Slug: req.Slug})
}

// Delivery is at-least-once — chaos mode WILL deliver duplicates — so
// the event ID is claimed atomically before counting. Remove this claim
// and the chaos test below fails with a reproduction seed.
func countClick(ctx tako.Ctx, c Click) error {
	err := seen.Tx(ctx, func(tx tako.EntityTx[Click]) error {
		if _, err := tx.Get(c.ID); err == nil {
			return errAlreadyCounted
		} else if !tako.IsNotFound(err) {
			return err
		}
		return tx.Put(c)
	})
	if errors.Is(err, errAlreadyCounted) {
		return nil // duplicate delivery: ack, count nothing
	}
	if err != nil {
		return err
	}
	return links.Update(ctx, c.Slug, func(l *Link) { l.Hits++ })
}
```

And the suite reads like the spec §2.10 promised — no runtime plumbing,
no httptest, no `GOWORK` incantations:

```go
func TestClicksCountExactlyOnce(t *testing.T) {
	app := tako.NewTestApp(t) // fresh in-process backends, all handlers wired
	u := app.SignIn("alice")
	ctx := app.Ctx(u)

	if _, err := Create.Call(ctx, CreateReq{Slug: "go", URL: "https://go.dev"}); err != nil {
		t.Fatal(err)
	}
	for range 20 {
		if _, err := Resolve.Call(ctx, ResolveReq{Slug: "go"}); err != nil {
			t.Fatal(err)
		}
	}
	app.DrainQueues()

	l, _ := links.Get(app.Ctx(), "go")
	if l.Hits != 20 {
		t.Fatalf("hits = %d, want exactly 20", l.Hits)
	}
}

// The same assertion under chaos: duplicates, redelivery-after-ack,
// reordering. Exactly-once tallying must hold anyway.
func TestClicksCountExactlyOnceUnderChaos(t *testing.T) {
	app := tako.NewTestApp(t)
	app.Chaos() // seeded; the seed prints so any failure reproduces
	...same body...
}
```

`create` also starts honoring identity: `if u, ok := ctx.MaybeUser();
ok { owner = u.ID }` — `SignIn` makes owners real in tests; the
`CreateReq.Owner` field stays as the no-auth fallback until auth lands.

### 2. Semantics being locked (v0 testing contract)

- **`NewTestApp(t)`** boots fresh local backends in `t.TempDir()` — no
  cloud, no network, no PORT — auto-wires every declared worker and
  subscriber, and cleans up via `t.Cleanup`. Parallel tests get
  isolated apps.
- **`SignIn(name)`** returns a stub identity (`User{ID: name, …}`) —
  identity injection for tests, NOT auth enforcement (WithAuth
  middleware arrives with the auth feature).
- **`Ctx(u ...User)`** mints a request context, authenticated when a
  user is passed.
- **`DrainQueues()`** blocks until every published message (including
  chaos duplicates) acked or dead-lettered; fails the test on timeout.
- **`Chaos()`** enables, on that app's queues AND topics: duplicate
  delivery, redelivery-after-ack, and delivery reordering — the
  at-least-once bug class. Deterministic per seed: the seed prints at
  enable time (`t.Logf`), and `TAKO_CHAOS_SEED=<n>` reproduces a
  failure exactly. Failure-free chaos runs stay quiet.
- **`tako dev --chaos`** turns the same behaviors on in the dev loop
  (banner prints the seed; ☢ console lines narrate each injection), so
  a human can SEE duplicate deliveries happen.
- **`tako test`**: analyze (analysis errors block, printed in the
  standard format) → regenerate (stale `.tako/gen` output is refreshed
  and reported) → `go test ./...` with output passed through.
- **Shortlink's Hits are eventually consistent** from now on (read
  after DrainQueues/moments later) — the honest price of the async
  counter, stated in the example's comments.

### 3. One internals decision that needs your eyes (interface rule 1)

`NewTestApp(t)` takes no handler list, yet must wire `CountClick` to
its queue. The generated mains do this with explicit `RegisterWorker`
lines derived from analysis — unavailable inside `go test`. Proposal:
`tako.Worker` and `tako.Subscribe` additionally append their (already
constructed) record to an internal registry the SDK owns;
`NewTestApp` consumes it. This is technically a constructor side
effect — rule 1 says "side-effect-free records" — but it preserves the
rule's actual guarantees: order-independence (append-only, no
cross-var reads), no behavior in production (generated mains keep
their explicit registration lines), no comment directives. Test-mode
worker identity derives from the handler function's name
(`countClick`), which can differ from the var name the dev console
shows (`CountClick`) — visible only in test narration. Alternative
rejected: `NewTestApp(t, CountClick, OnUploaded, …)` — explicit but
exactly the boilerplate rule 1 exists to kill, and silently dropping
one handler makes tests pass vacuously.

### 4. Expected CLI transcripts

`tako test` (from the module dir):

```
$ tako test
tako test · shortlink
  analyzing   2 services, 3 endpoints, 1 worker
  generating  .tako/gen/shortlink/main.go (regenerated: stale)
  go test ./...
ok  	shortlink	0.41s
```

The gate's centerpiece — dedup claim removed, chaos catches it:

```
$ TAKO_CHAOS_SEED= go test -run Chaos ./...
--- FAIL: TestClicksCountExactlyOnceUnderChaos (0.31s)
    testapp.go:NN: tako: chaos enabled (seed 1721594882414）— rerun with TAKO_CHAOS_SEED=1721594882414 to reproduce
    app_test.go:NN: hits = 27, want exactly 20
FAIL
```

Rerunning with that env var fails identically; restoring the dedup
claim turns both green.

`tako dev --chaos` (gallery or shortlink):

```
  local: workers and subscribers run in-process (…)
  chaos: enabled (seed 1721594882414) — deliveries may duplicate, reorder, redeliver
⚙ clicks → CountClick ok (1ms)
☢ chaos clicks: delivering again after ack
⚙ clicks → CountClick ok (0s)
```

### 5. Not in scope (F7 non-goals)

`AdvanceCron` stays a stub (no Cron runtime exists; it lands with F9's
gallery `Cleanup` cron — the stub's panic message now says so), auth
enforcement/`WithAuth` middleware (SignIn is identity injection only),
migrating gallery's HTTP-level tests to NewTestApp (F9; they
deliberately exercise the generated-mux plumbing), chaos on
entity/bucket operations or injected handler errors (only delivery
chaos in v0), network fault injection, `tako test` flags passthrough
beyond the target package list.

---

## F7 — implementation: Testing as a feature (2026-07-21)

DX proposal approved as written (registry variant); shipped surface
matches it.

**What shipped**

- **`tako.NewTestApp(t)`** (testapp.go, live): boots fresh local
  backends in `t.TempDir()` via the new `internal/boot` package (the
  one place that knows how a local app comes up — `runtime.BootLocal`
  now delegates to it too), auto-wires every registered worker and
  subscriber, cleans up on test end. Test retry pacing is 5×5ms — the
  contract, not production timing.
- **True isolation via contexts**: resource operations now resolve
  backends from the context first (`appFrom(ctx)`), falling through to
  the process globals — `TestAppsAreIsolated` proves two apps share
  nothing. This touched every `xxxBackend()` resolver (now
  `xxxBackend(ctx)`), no exported surface change.
- **The approved rule-1 carve-out**: `Worker`/`Subscribe` append their
  record to an internal registry (order-independent, test-read-only);
  test wiring identity is the handler function's name via reflection
  (`countClick`), while generated mains keep explicit var-name lines.
- **`SignIn`/`Ctx`/`DrainQueues`** per spec §2.10; `AdvanceCron` stays
  a stub whose panic says cron arrives with F9.
- **Chaos mode**, seeded and REPRODUCIBLE: duplicate delivery (25%),
  redelivery after ack (15%), reordering delays (0–20ms) on queues and
  topics. First implementation drew from one shared `rand` — my own
  validation caught that goroutine interleaving made the same seed give
  different runs (29/28/27 hits). Redesigned: each decision is a pure
  FNV hash of (seed, destination, per-destination delivery ordinal) —
  no draw-order dependence. Verified: fixed seed → identical failing
  count across 4 runs; different seeds → different patterns; docs state
  the honest boundary (reproducible when publish order is stable, which
  sequential test bodies guarantee).
- **`tako dev --chaos`**: passes `TAKO_CHAOS=<seed>` to the app;
  `BootLocal` enables chaos and the console narrates — `☢ chaos:
  enabled (seed …)` banner, `☢ chaos clicks: delivering to CountClick
  twice` / `again after ack` lines.
- **`tako test`**: analyze (analysis errors block) → regenerate with a
  freshness note (`regenerated: shortlink was stale`) only when output
  actually changed → `go test ./...` (or passed-through args) with
  output streaming.
- **Shortlink rewritten as the teaching example**: `resolve` publishes
  `Click{ID: tako.NewID(), Slug}` (Hits now eventually consistent, per
  proposal); `countClick` claims the event ID in `seen` before
  counting — the idempotency-ledger idiom; `create` honors
  `ctx.MaybeUser()` so `SignIn` owners are real. Suite is user-style
  end to end: `TestClicksCountExactlyOnceUnderChaos` is the centerpiece,
  plus isolation, 409/404/422 coverage via `Call`.
- Analyzer/codegen goldens regenerated (shortlink gained queue
  `clicks`, ledger entity `clicks-seen`, worker service
  `shortlink-countclick`); fixture `go.sum`s refreshed (the SDK now
  transitively carries bbolt because it contains the test harness).

**Validation transcript (tier 2, validate/testing.md)**

Seeded the bug (idempotency claim removed):

```
--- FAIL: TestClicksCountExactlyOnceUnderChaos (0.25s)
    app_test.go:60: tako: chaos enabled (seed 1784686156364961000) — rerun with TAKO_CHAOS_SEED=1784686156364961000 to reproduce
    app_test.go:78: hits = 29, want exactly 20
```

Reproducibility (after the determinism redesign), fixed seed 424242,
four runs: `hits = 26` every time. Seeds 1/2/3 → 29/28/32 (patterns
vary by seed, each reproducible). Claim restored → whole suite green.

`tako test`:

```
tako test · shortlink
  analyzing   2 services, 3 endpoints, 1 worker
  generating  .tako/gen/shortlink/main.go (regenerated: shortlink was stale)
  go test ./...
ok  	shortlink	0.705s
```

(second run: no stale note — freshness check is content-based.)

`tako dev --chaos` (PORT=8090): banner `☢ chaos: enabled (seed …)`;
1 create + 8 resolves produced interleaved `☢ chaos clicks: delivering
to CountClick twice` / `again after ack` lines — and
`/api/links?owner=memo → hits: 8`, exact despite the chaos, because the
ledger holds in dev too.

Full sweep: conformance 47 green (chaos is off by default — the suite
is unchanged); `go test ./...` green in all four modules.

**Discovered work (noted, not done)**

- Chaos probabilities/delays are fixed constants; a `ChaosOptions`
  surface (rates, max delay) can wait until an example demands it.
- `runtime.WaitIdle`/`runtime.NewCtx`/`runtime.BootLocal` remain only
  for gallery's generated-mux-mirror tests; F9 decides whether those
  migrate to NewTestApp and the plumbing shrinks.

**GATE REQUEST — F7 (local): Testing as a feature.** Please run
`validate/testing.md` — the centerpiece is §2–§3: gut the idempotency
claim, watch chaos catch it, and verify the printed seed replays the
failure exactly — and record the verdict in `docs/validation-log.md`.

## F8 — DX proposal: Secret, Cache, metrics, WithDelay — local (2026-07-21)

Agent-plan scope: env-file Secret; TTL Cache + `GetOrFill`; Counter/Histogram
logged; timer delays with fake-clock tests. Gate F8: quick script pass.

The entire SDK surface for this feature already exists as stubs (`secret.go`,
`cache.go`, `metrics.go`, `WithTTL`, `WithDelay`, and `Publish` already takes
`opts ...Option`) — F8 makes the stubs real. **One new exported symbol** is
proposed: `TestApp.Advance` (§4). Nothing else is added, renamed, or
re-shaped.

### 1. User code — gallery grows the spec-Part-3 observability spine

Gallery gains exactly the pieces spec Part 3 already shows (pulled forward
from F9 because F8 needs a demanding example):

```go
var (
    // ...existing gallery resources...
    feed     = tako.Cache[[]Photo]("feed", tako.WithTTL(time.Minute))
    notifier = tako.Secret("notifier-key")
    ups      = tako.Counter("uploads")
    thumbMs  = tako.Histogram("thumb_ms")
)

// onUploaded counts every original upload.
func onUploaded(ctx tako.Ctx, obj tako.ObjectInfo) error {
    ups.Add(ctx, 1)
    // ...enqueue thumb work as today...
}

// list serves the ready feed through the cache; the fill runs on miss.
func list(ctx tako.Ctx, req ListReq) ([]Photo, error) {
    return feed.GetOrFill(ctx, "ready", func(ctx tako.Ctx) ([]Photo, error) {
        return photoStore.Query(ctx).Where("Ready", "=", true).Limit(100).All()
    })
}

// warmFeedCache repopulates the feed when a photo becomes ready.
func warmFeedCache(ctx tako.Ctx, e PhotoReady) error {
    photos, err := photoStore.Query(ctx).Where("Ready", "=", true).Limit(100).All()
    if err != nil { return err }
    return feed.Put(ctx, "ready", photos)
}

// notifyOwner authenticates to the (stub) notification service with a
// secret resolved once at startup — Value() takes no ctx, cannot fail here.
func notifyOwner(ctx tako.Ctx, e PhotoReady) error {
    key := notifier.Value()
    // ...
}
```

`WithDelay` has no gallery/shortlink call site yet (shop's delayed order
email is the demanding example, later). It ships now because the agent plan
schedules it with the queue machinery, exercised by a user-style fake-clock
test (§4) and conformance. Flagging this explicitly against the
"no feature without an example" rule — your call at approval.

```go
digest.Publish(ctx, job, tako.WithDelay(4*time.Hour))  // delivers no sooner than 4h
```

### 2. Semantics being locked (v0 contract)

**Secret — resolved once at startup, env-file locally.**
- Local source: a `.env` file in the app's module dir (gitignored), literal
  names: `notifier-key=nk_dev_123`. Format: `name=value` lines, `#` comments,
  blank lines; no quoting rules, no interpolation.
- Boot (dev, generated main) resolves every *declared* secret up front;
  any missing → the service refuses to start:
  `secret "notifier-key" is not set — add it to .env (local) or Secret Manager (cloud)`
- `Value()` never fails at request time. Outside a booted app it panics with
  the standard "run via tako dev, a generated service, or tako.NewTestApp".
- Tests: `NewTestApp` starts with no secrets; `app.SetSecret(name, value)`
  provides them per-app (ctx-carried, isolated like every other backend).
  Reading an unset secret in a test panics with the name and a pointer to
  SetSecret.

**Cache — per-instance, in-memory, TTL from the declaration.**
- `Get`/`Put`/`Delete`/`Exists` — the keyed-store verb vocabulary. Absent or
  expired ⇒ `tako.IsNotFound`. No TTL declared ⇒ entries never expire
  (documented; WithTTL is the normal case).
- `GetOrFill(ctx, key, fill)`: hit returns cached; miss calls fill, Puts,
  returns. Concurrent misses may fill more than once (it's a cache; a
  singleflight upgrade is a later, call-site-compatible change). A fill
  error is returned and nothing is cached.
- Honest per-instance caveat stays in the docs: no cross-instance
  invalidation; `WithShared()` is a later opt-in.

**Metrics — logged, structured, visible in dev.**
- `ups.Add(ctx, n)` / `lat.Observe(ctx, v)` emit one structured log line
  through the app's logger (`metric` records: name, kind, value, running
  total/count) — fire-and-forget, never an error, never blocks.
- The dev console renders them: `📈 uploads +1 (total 3)` and
  `📈 thumb_ms 12.4ms (n=7)`. Cloud Monitoring export is Phase E's problem;
  the call sites don't change.

**WithDelay — queue-publish option, fake clock in tests.**
- `q.Publish(ctx, msg, tako.WithDelay(d))`: the local dispatcher holds the
  message and first attempts delivery no sooner than d. Retry/backoff/chaos
  semantics after first delivery are unchanged. Topics reject it:
  `tako.WithDelay is not a Topic publish option (delay is a queue power)`.
- Dev: real timers; console notes `⚙ clicks: 1 message delayed 5s`.
- Non-queue/non-publish misuse of WithTTL/WithDelay keeps the existing
  call-time option-error style (`tako.X is not a ... option (...)`).

### 3. Internals (no user-visible surface)

- `backends.Cache` interface + local in-memory impl; conformance chapter
  (roundtrip, miss, delete, exists, expiry, GetOrFill fill-once-on-hit).
- `backends.Queue.Publish` grows a delay (internal signature; local
  dispatcher gets an injectable clock so conformance and TestApp never
  sleep real hours).
- `backends.Secrets` resolver (env-file impl) wired in `internal/boot`;
  `boot.App` carries Secrets/Cache/Metrics like the rest.
- Analyzer: Secret/Cache/Counter/Histogram become AppGraph resources
  (const-name rule reused → same error text family), shown in `gen graph`
  and the dev banner. Cloud-Tasks-vs-PubSub selection on WithDelay is
  Phase E, not analyzed now.

### 4. The one new exported symbol — TestApp.Advance

```go
func TestDigestIsDelayed(t *testing.T) {
    app := tako.NewTestApp(t)
    ctx := app.Ctx()
    digest.Publish(ctx, Digest{...}, tako.WithDelay(4*time.Hour))

    app.DrainQueues()                  // idle: nothing due yet — worker has NOT run
    app.Advance(3 * time.Hour)         // fake clock; still not due
    app.Advance(2 * time.Hour)         // due → delivery fires
    app.DrainQueues()                  // worker ran exactly once
}
```

- `Advance(d)` moves the app's fake clock forward and releases every
  delivery that has come due; it is TestApp-only (dev uses real time).
- `DrainQueues` treats not-yet-due deliveries as idle (it drains what can
  run, it doesn't wait out a 4-hour timer). This is the same clock
  `AdvanceCron` will use in F9.
- Retry backoff in tests keeps its real (5ms) timer — Advance is for
  *scheduled* time (delays, cron), not a general time machine. Chaos
  reorder delays are likewise real.

### 5. Not in scope (F8 non-goals)

- No `WithRateLimit` (needs Cloud Tasks; Phase E).
- No `WithShared()` cache, no Redis.
- No Cloud Monitoring export; metrics are logs.
- No Secret Manager backend (Phase E); no secret rotation/refresh.
- No cron (`AdvanceCron` still panics until F9); Advance only releases
  queue delays now.
- No singleflight in GetOrFill (noted as later, call-site compatible).
- Gallery changes limited to the §1 snippet; the full Part-3 gallery
  (cron, auth, SSR pages) stays F9.

**STOP — awaiting DX approval before implementation.**

## F8 — implementation: Secret, Cache, metrics, WithDelay — local (2026-07-21)

Shipped as approved. All four stubs are now real; the only exported-surface
change is the proposed pair `TestApp.Advance` / `TestApp.SetSecret`
(`testdata/sdk_lock.golden` regenerated once, deliberately — the lock diff
listed exactly those two symbols).

**What landed**
- `backends.Cache` + `local.MemoryCache`; `backends.Metrics` +
  `local.MetricsLog` (structured `metric` log lines + `OnMetric` observer);
  `backends.Queue.Publish` grew `PublishOptions{Delay}` (internal
  signature). The local dispatcher parks delayed messages: real
  `time.AfterFunc` in dev/generated services, a virtual clock in test apps
  (`UseManualClock`/`Advance`) — parked messages are idle to `WaitIdle`,
  so `DrainQueues` never waits out a delay.
- Secrets: process-global table (`internal/secrets` — `Value()` takes no
  Ctx, so per-app isolation is impossible by design and documented);
  `.env` loader (`local.LoadEnvFile`: name=value, `#` comments, missing
  file fine); `Secret()` registers its name (same rule-1 carve-out as
  handlers) and `runtime.BootLocal` refuses to start with any declared
  secret unset. TestApp gets `SetSecret` (cleanup-scoped) and unset reads
  panic pointing at it.
- SDK: Cache verbs + `GetOrFill` (fill-once-on-hit; call-time option
  errors in the standard text family), `Publish(..., WithDelay(d))`
  (`tako.WithTTL is not a Publish option (Publish accepts WithDelay)`),
  Counter/Histogram via ctx-resolved sink. Topic.Publish takes no opts —
  the proposal's "delay is a queue power" rejection turned out to be
  compile-enforced, better than a runtime text.
- Analyzer: Cache/Secret/Counter/Histogram are AppGraph resources
  (shared `extractCarrier`; const-name error family; new
  `errors/secretname` fixture). Dev banner + `gen graph` render them.
- Dev console: `📈 uploads +1 (total 3)` / `📈 thumb_ms 8 (n=1)` and
  `⚙ <queue>: message delayed <d>`.
- Gallery grew its spec-Part-3 spine per the proposal: `feed` cache
  (list via `GetOrFill`, `warmFeed` re-puts on PhotoReady), `notifier`
  secret (used in notifyOwner), `ups`, `thumbMs`; `Ready` is now
  indexed; committed demo `examples/gallery/.env` (with a comment that
  real apps gitignore it) so tests/dev/validation boot out of the box.
  New `TestFeedCacheServesReadyPhotos`.
- Conformance: Cache chapter (7 cases) + QueueDelay chapter (3 cases,
  fake clock) → 57 local cases total.

**Bug found by this feature (pre-existing, fixed)**: the analyzer stored
derived-index results through a `*Resource` pointing into
`graph.Resources`; appending the four new resource kinds reallocated the
slice, so indexes vanished from the goldens. Fixed by storing the slice
index. The fix also restored shortlink's `entity links (indexes: Owner,
Created)`, which the bug had been silently eating since F7 added the
second entity — goldens now show the F2-era output again.

**Tier-2 validation — validate/extras.md executed, real transcripts**

§1 (gallery, product way):
```
$ tako test
tako test · gallery
  analyzing   5 services, 7 endpoints, 1 worker, 3 subscribers
  generating  .tako/gen/gallery/main.go (regenerated: gallery was stale)
  go test ./...
ok  	gallery	2.134s
```

§2 (dev banner + metrics + warm feed):
```
  entity photos (indexes: Ready)
  ...
  cache feed
  secret notifier-key
  counter uploads
  histogram thumb_ms
...
📈 uploads +1 (total 1)
⚙ photos.Uploaded → OnUploaded ok (0s)
📈 thumb_ms 8 (n=1)
⚙ thumbnails → MakeThumb ok (9ms)
⚙ photo-ready → WarmFeed ok (0s)
$ curl -s localhost:8090/api/photos | jq -c '[.[] | {caption, ready}]'
[{"caption":"cat","ready":true}]
```

§3 (missing secret, exact text):
```
$ mv .env .env.bak && rm -rf .tako && PORT=8090 tako dev
tako: boot: secret "notifier-key" is not set — add it to .env (local) or Secret Manager (cloud)
```

§4 (fake clock + secrets, milliseconds not hours):
```
--- PASS: TestWithDelayRidesTheFakeClock (0.01s)
--- PASS: TestCacheGetOrFillFillsOnceThenHits (0.01s)
--- PASS: TestSecretResolvesViaSetSecret (0.01s)
--- PASS: TestUnsetSecretPanicsWithGuidance (0.00s)
```

§5: conformance 57/57 local; full tree + hello/shortlink/gallery green.

**Discovered work (not done)**: devserver wraps a boot failure as
"✗ analysis failed — still serving last good build" (it is neither);
wants a distinct boot-failure message. GetOrFill singleflight (later,
call-site compatible). WithDelay still has no example call site — shop's
delayed order email is the demanding example.

**GATE REQUEST — F8 (local): Secret, Cache, metrics, WithDelay.** Please
run `validate/extras.md` — the quick script pass the agent plan calls
for; §3 (boot refusal) and §4 (a 4-hour delay tested in 10ms) are the
judgment calls — and record the verdict in `docs/validation-log.md`.

## F9 — DX proposal: Gallery, complete (2026-07-22)

Agent-plan scope: `examples/gallery` per spec Part 3, full test suite,
chaos coverage on `makeThumb`. Gate F9: the full demo script — signed
upload, event → queue → worker, fan-out, queries, cron — doubling as the
first whole-DX review.

**The Part-3 boundary, drawn explicitly.** Spec Part 3's gallery also
declares `auth`/`site`/`tmpl` and a `Page` — the implementation plan puts
all of that web/auth surface in Phase G (journal), and the lease behind
`WithSingleflight` in Phase H. F9 ships everything else: the async +
storage + observability gallery, plus **cron** (the one Part-3 primitive
still stubbed, whose own stub says "arrives with the full gallery").
Deviations from Part 3 are §5 — each one is a Phase G/H arrival, not a
redesign.

### 1. User code — gallery, final local form

```go
var (
    photos     = tako.Bucket("photos")
    photoStore = tako.Entity[Photo]("photos")
    thumbs     = tako.Queue[ThumbJob]("thumbnails")
    ready      = tako.Topic[PhotoReady]("photo-ready")
    feed       = tako.Cache[[]Photo]("feed", tako.WithTTL(time.Minute))
    notifier   = tako.Secret("notifier-key")
    ups        = tako.Counter("uploads")
    thumbMs    = tako.Histogram("thumb_ms")
)

type Photo struct {
    ID      string `entity:"id" json:"id"`
    Owner   string `entity:"index" json:"owner"`
    Caption string `json:"caption"`
    Ready   bool   `entity:"index" json:"ready"`
}
type PhotoReady struct{ PhotoID, Owner, Caption string }

var (
    Upload      = tako.API("POST /api/photos", upload)
    StartUpload = tako.API("POST /api/uploads", startUpload)
    Download    = tako.API("GET /api/photos/{id}/download", download)
    // ...Image, Thumb, List, Objects as today...

    OnUploaded  = tako.Subscribe(photos.Uploaded, onUploaded)
    MakeThumb   = tako.Worker(thumbs, makeThumb, tako.WithProfile(tako.Batch))
    NotifyOwner = tako.Subscribe(ready, notifyOwner)
    WarmFeed    = tako.Subscribe(ready, warmFeed)
    Cleanup     = tako.Cron("every 24h", cleanupOrphans, tako.WithSingleflight())
)

// makeThumb now really resizes (x/image/draw in the EXAMPLE's go.mod —
// app code, not framework): 256px, aspect preserved, re-encoded.
// Undecodable image → tako.Permanent. Non-image content type keeps
// today's retry-then-dead-letter behavior.

// download returns a shaped response (was a bare JSON string — F6's
// noted DX smell):
type DownloadRes struct{ URL string `json:"url"` }

// cleanupOrphans deletes never-completed uploads: photos still not
// Ready when the daily run fires (a ticket was issued, bytes never
// arrived — or the pipeline dead-lettered), entity + any object.
func cleanupOrphans(ctx tako.Ctx) error { ... }
```

Owner flows via `ctx.MaybeUser()` (shortlink's pattern): `SignIn` gives
tests real owners; anonymous HTTP uploads get `""` until Phase G's
`WithAuth` makes `ctx.User()` enforceable. `list` filters Ready + (when
signed in) Owner; the feed cache keys per owner (`"ready"` /
`"ready:"+uid`); `warmFeed` warms the anonymous feed and the owner's.

### 2. Cron — the v0 contract

- Schedule syntax: **`"every <Go duration>"`** ("every 24h", "every 90s"),
  const (analyzer-enforced). Anything else:
  `tako.Cron schedule must be "every <duration>" for now (cron expressions arrive with cloud scheduling); got "0 3 * * *"` (tako/cron-schedule).
- Handler shape `func(Ctx) error` — validated like every handler sig.
  Cron handlers belong to the **app service** (spec §4.1 partitioning),
  not their own service.
- Local runner: one timer per cron; runs are serialized per cron — a
  tick that lands while the previous run is still going is **skipped
  with a narration**, which is also the honest local face of
  `WithSingleflight` (the option parses; the cross-instance lease is
  Phase H). Dev console: `⏰ cron cleanupOrphans: every 24h` at boot,
  `⏰ cron cleanupOrphans ok (3ms)` per run.
- Options: `WithSingleflight` only. Others:
  `tako.X is not a Cron option (Cron accepts WithSingleflight)`.
- **`AdvanceCron(name)` goes live** (same registry carve-out): runs the
  named cron once, synchronously, on the test app's backends; unknown
  name Fatals listing the declared crons. Name = handler function name
  (`"cleanupOrphans"`), consistent with F7's wiring identity.

### 3. The test suite — migrated user-style, honestly split

`app_test.go` becomes the spec §2.10 shape: `NewTestApp` / `SignIn` /
`Call` / `DrainQueues` for everything whose contract is app logic —
upload pipeline, feed cache, owner filtering, fan-out, and NEW:

```go
func TestThumbPipelineSurvivesChaos(t *testing.T) {   // the Phase D exit criterion
    app := tako.NewTestApp(t); app.Chaos()
    u := app.SignIn("memo"); ctx := app.Ctx(u)
    for i := range 8 { Upload.Call(ctx, UploadReq{Caption: ..., Img: pngBlob(t)}) }
    app.DrainQueues()
    // every photo Ready, every thumb present and well-formed, feed exact
}

func TestCronCleansNeverCompletedUploads(t *testing.T) {
    app := tako.NewTestApp(t); ctx := app.Ctx(app.SignIn("memo"))
    StartUpload.Call(ctx, StartUploadReq{Caption: "ghost"}) // ticket, no bytes
    Upload.Call(ctx, UploadReq{Caption: "real", Img: pngBlob(t)})
    app.DrainQueues()
    app.AdvanceCron("cleanupOrphans")
    // ghost entity gone; real photo untouched
}
```

Two suites deliberately stay on the HTTP/disk harness (`serve()` +
`BootLocal`): signed-URL semantics (the contract IS HTTP: statuses,
rejection texts, CORS) and dead-letter-on-disk assertions (the contract
IS the `.tako/data/deadletter` layout). Splitting by contract beats
forcing one harness everywhere.

### 4. Signed-URL endpoint: CORS (noted F6 follow-up)

Browsers PUT to signed URLs cross-origin. `/_tako/signed/` gains:
OPTIONS preflight (204), `Access-Control-Allow-Origin: *`,
`-Methods: GET, PUT`, `-Headers: Content-Type` — on the endpoint, not
per-app config (GCS behaves equivalently once bucket CORS is
provisioned in Phase F). Conformance grows a CORS case.
`validate/signed.md` §4 gets a fix-up for the new shaped `Download`
response (`jq -r .url`).

### 5. Deviations from spec Part 3 (all deferred arrivals, flagged)

- No `auth`/`WithAuth`/`ctx.User()` enforcement, no `site`/`tmpl`, no
  `Page` (`photoPage`) — Phase G (journal). Owner rides `MaybeUser`.
- `WithSingleflight` parses; the real lease is Phase H. Locally:
  serialized runs + skip narration.
- `resize` uses `golang.org/x/image/draw` in the example's module —
  the framework gains no dependency.
- Spec Part 3's `Upload`-only flow also keeps our multipart `Upload`
  AND the ticket `StartUpload` (both were validated features; Part 3
  shows the ticket flow).

### 6. Not in scope (F9 non-goals)

- No `tako.Singleton`, no `Lock` (Phase H). No `Job` (Phase H).
- No cron expressions, no timezone handling — `every <duration>` only.
- No auth middleware — even though SignIn exists (test identity only).
- No dev-console `AdvanceCron` analog (crons fire on their real
  schedule in dev; tests own time).
- No spec fix-up beyond validate/signed.md; the Part 3
  Static/Templates→Site drift stays a Phase G task.

**STOP — awaiting DX approval before implementation.**

## F9 — implementation: Gallery, complete (2026-07-22)

Shipped as approved: Part 3 minus the Phase-G web/auth layer, cron live,
user-style test migration with the HTTP/disk split, CORS, shaped
DownloadRes. No exported-SDK surface changed beyond what F7 already
locked (`Cron` and `AdvanceCron` had their signatures in the lock;
both stubs are now real — sdk_lock.golden unchanged this time).

**What landed**
- `tako.Cron` live: registers via the F7 carve-out (kind "cron",
  schedule as source, fn-name identity). `internal/schedule` parses
  `"every <duration>"` — one parser shared by analyzer and runtime, so a
  schedule that analyzes always runs. Runtime `RegisterCron`: boot line
  `⏰ cron Cleanup: every 24h`, per-run `⏰ … ok (3ms)` / failure
  narration, serialized runs with tick-skip narration (the honest local
  face of WithSingleflight; the lease is Phase H). Analyzer: cron
  handlers join the app service; `cron Cleanup — every 24h` in
  banner/summary; `1 cron` in the analyzing count; error fixtures
  `cronschedule` (tako/cron-schedule) + `cronopt` (invalid-option).
- `AdvanceCron(name)` implemented: runs one cron synchronously on the
  test app's backends; unknown name Fatals listing declared crons.
- Gallery final local form: `Owner` (indexed, via MaybeUser — WithAuth
  is Phase G), `PhotoReady.Owner`, per-owner feed keys with warmFeed
  warming anonymous + owner feeds, `WithProfile(tako.Batch)` on
  MakeThumb, `Cleanup` cron deleting never-Ready photos (entity +
  object), shaped `DownloadRes{URL}`, and REAL image resizing
  (x/image/draw in the example module only; PNG stays PNG, others
  re-encode JPEG; undecodable bytes → `tako.Permanent`). The `image`
  handler fn was renamed `serveImage` (collides with the std `image`
  package the example now imports) — the route and `Image` var are
  unchanged.
- Tests split by contract: `app_test.go` is pure user code
  (NewTestApp/SignIn/SetSecret/Call/DrainQueues/AdvanceCron) with the
  Phase-D exit criterion `TestThumbPipelineSurvivesChaos` (8 uploads
  under chaos → every photo ready, every thumb decodes, feed exact) and
  `TestCronCleansNeverCompletedUploads`; `http_test.go` keeps the
  signed-URL texts (all 8 subtests preserved + new CORS preflight),
  dead-letter-on-disk suites (+ new `TestBadImageBytesArePermanent`),
  and the multipart HTTP contract. Nothing dropped: two old tests were
  superseded by stronger user-style ones, the rest moved verbatim.
- `/_tako/signed/` speaks CORS (permissive — the token is the
  authorization); conformance grew a cross-origin case → 58 local.
- validate/signed.md §4 fixed for the shaped response (`jq -r .url`).

**Bugs found while implementing (all fixed, all now under test)**
1. Codegen emitted crons into the route loop (`mux.Handle("", …)`) —
   sync-handler filter now excludes kind "cron"; gallery_main.golden
   shows `RegisterCron` only.
2. `NewTestApp` cleanup raced in-flight workers writing to the TempDir
   ("directory not empty") — teardown now WaitIdles before Close.
3. `tb.Cleanup` LIFO meant `SetSecret`'s unset ran BEFORE app teardown,
   so an undrained subscriber could read a removed secret (panic) —
   SetSecret's cleanup now WaitIdles before unsetting. Gallery suite
   passes under `-race`.

**Tier-2 validation — validate/gallery.md executed, real transcripts**

§1: `tako test` → `analyzing 5 services, 7 endpoints, 1 worker,
3 subscribers, 1 cron` … `ok gallery 2.392s` (chaos + cron tests in).

§2 (banner + full signed flow):
```
  cron Cleanup — every 24h (app.go:68)
  entity photos (indexes: Owner, Ready)
⏰ cron Cleanup: every 24h
{"id":"cqfsbgsf5wfko7qa","owner":"","caption":"sunset","ready":false}
signed PUT: 200
[{"caption":"sunset","ready":true}]
signed GET: 200 70 bytes
thumb: 200 image/png
```

§3 (fan-out): WarmFeed `ok (0s)` while
`NotifyOwner attempt 1/5 … — retrying in 200ms` ×4 →
`✗ dead-letter photo-ready: NotifyOwner gave up after 5 attempts:
notification service rejected "boom goes"`; JSON present under
`.tako/data/deadletter/photo-ready/NotifyOwner/`.

§4 (real decoding): `✗ dead-letter thumbnails: MakeThumb permanent
failure: decode image: image: unknown format` — one attempt, no retry.

§5: `--- PASS: TestCronCleansNeverCompletedUploads (0.05s)`.

§6: conformance 58/58 local; full tree + hello/shortlink/gallery green.

**Discovered work (not done)**: spec Part 3 fix-ups now pending F10/
Phase G review — `image`→`serveImage` naming, `Upload`+`StartUpload`
dual flow, Static/Templates→Site drift. Dev has no way to fire a cron
by hand (`tako cron run <name>`?) — worth considering when cloud
scheduling lands. Dead-letter replay CLI still open.

**GATE REQUEST — F9 (local): Gallery, complete.** Please run
`validate/gallery.md` — the agent plan's full demo script; this gate
doubles as the first whole-DX review, so notes beyond pass/fail are
welcome — and record the verdict in `docs/validation-log.md`. F10 (API
freeze review) follows.

## F10 — API freeze review: materials prepared (2026-07-22)

F10 is human-led, so there is no DX proposal cycle: the deliverable is
`docs/api-review-1.md`, now written. It walks all 132 locked symbols
(testdata/sdk_lock.golden) in 8 groups, each row assessed against the
six rules with a recommendation and an empty Verdict column; §9 is the
complete enumerated list of the seven deliberate rule deviations (each
load-bearing, none accidental); §10 lists the non-SDK follow-ups that
came up during gates.

Items explicitly flagged for the reviewer's judgment rather than
rubber-stamping:
- `Ctx.IsHTMX()` on the universal Ctx (decision 13 argues keep; workers
  answering false argues page-scoped ctx in Phase G).
- `TestApp.Chaos()` without a seed parameter — signature is frozen
  after this gate; env-var replay has sufficed so far.
- `Advance` vs `AdvanceCron` — recommended keeping both (reasoning in
  §8).
- The Phase G/H stubs (Auth, Site, Page, Job, Lock, Singleton + their
  options) — recommended `keep (provisional)`: freeze the intent, let
  the implementing phase reopen with a documented reason.

Nothing was renamed, added, or re-shaped — per the working rules, API
changes only happen from the verdicts of this review.

**GATE REQUEST — F10 (with F9).** Per your instruction the F9 gallery
review and F10 run together: run `validate/gallery.md` (the whole-DX
demo — its §2–§4 double as the API's feel test), then fill the Verdict
columns and the approval block in `docs/api-review-1.md`. Record both
verdicts in `docs/validation-log.md`. Any renames you request become
the last F10 implementation task before the lock freezes.

## C1 — DX proposal: Build + push (2026-07-23)

Stage 2 begins. Agent-plan scope: ko-style static binary → distroless →
Artifact Registry by digest. Gate C1 (explicitly a partial cycle — no
deploy): human verifies the pushed image runs locally via docker and
inspects the digest pinning. Spec §4.5 is normative: `CGO_ENABLED=0 go
build` per service → append binary as a layer on distroless via
go-containerregistry → push. No Docker daemon for BUILDING (docker is
only used by the human to verify the result).

No SDK surface changes (the API is frozen). C1 is CLI + a new
`pkg/build` package.

### 1. CLI surface

```
$ cd examples/hello
$ tako build
tako build · hello
  analyzing   1 service, 1 endpoint
  generating  .tako/gen/hello/main.go
  building    hello  linux/amd64 static (6.1 MB in 1.8s)
  base        gcr.io/distroless/static-debian12:nonroot@sha256:6d31f9…
  image       .tako/images/hello (OCI layout)
✓ hello  sha256:e4b1c0…   (not pushed — use --push <repo>)

$ tako build --push us-central1-docker.pkg.dev/my-proj/tako
tako build · hello
  analyzing   1 service, 1 endpoint
  generating  .tako/gen/hello/main.go
  building    hello  linux/amd64 static (6.1 MB in 1.8s)
  base        gcr.io/distroless/static-debian12:nonroot@sha256:6d31f9…
  pushing     us-central1-docker.pkg.dev/my-proj/tako/hello
✓ hello  us-central1-docker.pkg.dev/my-proj/tako/hello@sha256:e4b1c0…
```

- Builds EVERY service in the graph (shortlink: app + worker service),
  one line + digest each. C2's reconciler consumes exactly these
  digest-pinned references — a deploy never says ":latest".
- Without `--push`: image written to `.tako/images/<service>` as an OCI
  layout (inspectable with crane/skopeo), digest printed. With
  `--push <repo>`: pushed as `<repo>/<service>`, pinned line printed.
- Registry auth: the gcloud/ADC keychain (whatever `gcloud auth` or
  workload identity provides). No tako-specific credential config.

Error texts (exact, under test where reachable):
- no credentials → `cannot authenticate to <repo>: no Google credentials — run "gcloud auth login" (or set GOOGLE_APPLICATION_CREDENTIALS) and retry`
- unreachable/denied repo → the registry's error, prefixed `push <service>: `

### 2. Semantics being locked

- **Static by construction**: `CGO_ENABLED=0 GOOS=linux GOARCH=amd64
  go build -trimpath -ldflags "-s -w"` of the generated service main.
  amd64 only in v0 (Cloud Run's default); multi-arch is a later flag.
- **Base**: `gcr.io/distroless/static-debian12:nonroot`, tag resolved
  to a digest at build time and PRINTED — the image config pins the
  resolved digest, never the floating tag.
- **Deterministic images**: fixed timestamps in the appended layer and
  image config (ko-style zero epoch), so same source → same image
  digest. This is what makes C2's spec-hash reconcile ("did anything
  change?") cheap and honest.
- **Image shape**: exactly base + one layer containing `/app/<service>`;
  entrypoint `/app/<service>`; runs as distroless `nonroot`; OCI labels
  `tako.app` / `tako.service` (the reconciler's label vocabulary starts
  here). No secrets, no .env, no source in the image.

### 3. Internals

- `pkg/build`: `Build(graph, dir) []Image` (compile + layer into OCI
  layout) and `Push(image, repo)` via go-containerregistry; the google
  keychain for auth. go-containerregistry becomes a framework
  dependency (that's the point of C1).
- Tests, no cloud needed: go-containerregistry ships an in-memory
  registry (`pkg/registry`) — the suite builds hello, pushes to an
  httptest registry, pulls back, and asserts: digest round-trips, one
  appended layer, entrypoint/user/labels correct, rebuild reproduces
  the same digest, binary in the layer is statically linked.
- `cmd/tako`: new `build` verb wired like `test` (analyze → generate →
  build), sharing the banner style.

### 4. Gate C1 (partial cycle — needs your GCP test project + docker)

`validate/build.md`: `tako build` (local, inspect OCI layout + digest
determinism: build twice, same digest), then `tako build --push` to the
dedicated test project's Artifact Registry, then THE point of the gate:

```
docker run --rm -e PORT=8080 -p 8080:8080 <repo>/hello@sha256:…   # by DIGEST
curl localhost:8080/api/hello
```

— the pushed artifact, pulled by pin, serving the same app `tako dev`
serves. Plus `docker inspect` (nonroot user, entrypoint) and a look at
the two-line layer stack.

### 5. Not in scope (C1 non-goals)

- No deploy, no Cloud Run, no provisioning, no IAM (C2).
- No `TAKO_BACKEND=gcp` wiring in the image config yet — the image
  runs with local backends when docker-run (that IS the gate demo);
  C2's reconciler sets the env at service-creation time.
- No multi-arch, no build cache, no SBOM/signing (noted for later).
- No new SDK symbols — the freeze holds.

**STOP — awaiting DX approval before implementation.**

## C1 — implementation: Build + push (2026-07-23)

Shipped per the approved proposal, with ONE deviation flagged below. No
SDK surface changed (freeze holds; `go test .` incl. sdk_lock green).

**What landed**
- `pkg/build`: ko-style pipeline — `CGO_ENABLED=0 GOOS=linux GOARCH=amd64
  go build -trimpath -ldflags "-s -w -buildid="` per generated service →
  one tar layer at `/app/<service>` (fixed epoch timestamps) appended to
  distroless via go-containerregistry → OCI layout at
  `.tako/images/<service>` or digest-pinned push to `<repo>/<service>`.
  Base tag resolved to a digest at build time and printed; image config:
  entrypoint `/app/<service>`, inherits distroless `nonroot`, labels
  `tako.app`/`tako.service` (the reconciler vocabulary's first words),
  created = epoch. Auth = google keychain + docker fallback; 401/403 on
  push maps to the proposal's exact no-credentials text.
- `cmd/tako`: `tako build [--push <repo>]`, banner-style output with
  per-service size/time, resolved base line, and `✓ <service> <ref>`
  pinned lines. go-containerregistry v0.21.7 is now a framework dep.
- Tests (`pkg/build`, no cloud): in-memory registry (pkg/registry) —
  push/pull-back digest equality, exactly one appended layer, entrypoint/
  labels/nonroot inheritance/epoch config, digest determinism across
  rebuilds, and the layer's binary parsed as ELF x86-64 with no
  PT_INTERP (statically linked, the property distroless/static demands).

**Deviation from the approved proposal (flagged for the gate):** the
proposal said "builds EVERY service in the graph (shortlink: app +
worker service)". Worker/subscriber services have no generated mains yet
— per-service async mains arrive with cloud async (Phase F), and
generating placeholder mains now would be fake. `tako build` builds
every service WITH a generated main and prints
`skipping shortlink-countclick (async-only — its per-service main arrives with cloud async)`
— never silent. If you'd rather have real async-service mains now, say
so at the gate and it becomes the next task.

**Tier-2 validation — local sections of validate/build.md, real
transcripts** (the push + docker-run sections are yours: no docker on
this machine and no test-project credentials I should be using):

§1 determinism (real distroless base):
```
$ tako build
  building    hello  linux/amd64 static (5.9 MB in 2.3s)
  base        gcr.io/distroless/static-debian12@sha256:7a2bd171a18bd…
  image       .tako/images/hello (OCI layout)
✓ hello  sha256:05fa0495c708b2685616d9898cb1832e6e25b056f59fdc716df8ba6848f993a9
$ tako build   # again
✓ hello  sha256:05fa0495c708b2685616d9898cb1832e6e25b056f59fdc716df8ba6848f993a9   # identical
```
OCI layout index digest matches the printed digest (jq on index.json).

§1 shortlink:
```
  building    shortlink  linux/amd64 static (6.4 MB in 2s)
  skipping    shortlink-countclick (async-only — its per-service main arrives with cloud async)
✓ shortlink  sha256:c53c64df510a…
```

§5: `go test ./pkg/build/` ok (4.7s); full tree green.

**Discovered work (not done)**: per-async-service generated mains
(Phase F, or earlier if the gate asks); `--platform` multi-arch flag;
build cache for the base image (currently re-fetched per run; fast but
network-dependent); SBOM/signing.

**GATE REQUEST — C1: Build + push (partial cycle).** Please run
`validate/build.md` §2–§4 with your test project: push, then docker-run
the image BY DIGEST and curl it, and inspect nonroot/entrypoint/layers.
Record the verdict in `docs/validation-log.md`. C2 (minimal reconciler +
hello deploys) follows.
