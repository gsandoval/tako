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
