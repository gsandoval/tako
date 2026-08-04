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

## C1.5 — implementation: Stage-1 gap closure + cloud groundwork (2026-07-24)

Scope approved interactively (four items, all selected) after the
pre-C2 audit. No exported SDK surface changed — the F10 freeze holds.

**1. Async service mains — the C1 deviation is closed.** Codegen now
generates a main for every service: async services get `runtime.Boot()`,
their `Register*` line, and `mux.Handle("POST /_tako/push",
runtime.PushHandler(...))`. The push status contract IS Pub/Sub's ack
contract — nil→204, error→500 (redeliver), Permanent→400 (dead-letter
policy's job, wired in Phase F along with envelope + OIDC) — pinned by
`TestPushHandlerContract`, including undecodable-payload→400. Live
transcript: generated shortlink-countclick served 204 (real click
counted against the existing ledger), 500 (unknown slug — correctly
retriable), 400 (junk payload). `tako build` now builds shortlink's two
and gallery's five services; the "skipping" line is gone. New golden:
`shortlink_worker_main.golden`.

**2. `tako gen graph --json | --mermaid`** (S0 follow-up, finally):
default output is now the human summary; `--json` is clean pipeable
JSON (`jq '.services | length'` → 5 on gallery); `--mermaid` renders
service subgraphs, family-shaped resource nodes, and binding edges
(dotted for bucket events). Goldened (`gallery_mermaid.golden.txt`).
Publish/read edges join when the analyzer derives usage edges.

**3. No silent ignores.** Declaring Page/Site/Auth (Phase G) or
Job/JobHandler/Lock/Singleton (Phase H) is now an analysis error with a
phase pointer (tako/not-yet) instead of silent nothing. Fixture:
errors/notyet.

**4. Permission model + error taxonomy.**
- `docs/tako-cloud-permissions.md`: two-layer model — deployer gets a
  simple one-time bundle on a dedicated project (friction minimized
  where it's felt); runtime gets per-service SAs with grants derived
  from AppGraph edges only (minimality where it's permanent);
  preflight-as-UX; the 401/403/403-API-disabled/404 taxonomy; tako
  never self-escalates (prints grant commands, never runs them).
- C1 retrofit: push errors now distinguish unauthenticated (401 →
  gcloud auth login) / unauthorized (403 → exact add-iam-policy-binding
  with repo/project/location parsed from the repo path) / repo missing
  (404 → exact repositories create). `TestPushErrorTaxonomy` pins all
  three texts.
- CLAUDE.md working rule added: every C-phase proposal carries a
  "Cloud errors & permissions" section designed against the doc.

**Validation:** full tree + all examples green; conformance 58 local;
`validate/stage2-prep.md` written, §1–§3 executed with transcripts
above (§4 is a design read — your gate judgment).

**GATE REQUEST — C1.5.** Please run `validate/stage2-prep.md` — §4 (the
permission model design review) is the real judgment call; §1's push
contract is the fun part. Record the verdict in `docs/validation-log.md`.
Note C1's own gate (validate/build.md §2–§4: push to your registry +
docker run by digest) is still open too — both can close in one sitting,
and C2's proposal (which now must include its Cloud errors & permissions
section) follows.

## C2 — DX proposal: Minimal reconciler + hello deploys (2026-07-24)

Agent-plan scope: service accounts, Cloud Run service, spec-hash
annotations, deploy lease, `tako deploy --plan`, `logs`, `destroy`.
Gate C2 — the first full cycle: clean project → live URL (target <~2min)
→ destroyed project, reviewing `--plan` line by line. Spec §4.6/§4.6a
are normative; hello (no resources) keeps the reconcile set minimal:
service account + Artifact Registry repo + image + Cloud Run service +
the lease/label/spec-hash machinery everything later builds on.

### 1. CLI surface

```
$ cd examples/hello
$ tako deploy --project my-tako-proj
tako deploy · hello → my-tako-proj (us-central1)

  analyzing    1 service, 3 endpoints
  generating   .tako/gen/hello/main.go
  preflight    ok (run, iam, artifactregistry, storage)
  plan         create  service account hello@my-tako-proj.iam
               create  artifact repo tako (us-central1)
               create  run service hello
               3 creates, 0 updates, 0 skips, 0 orphans
  lease        acquired (memo@… on my-tako-proj/hello/prod)
  ⛅ sa         hello@my-tako-proj.iam.gserviceaccount.com
  ⛅ image      us-central1-docker.pkg.dev/my-tako-proj/tako/hello@sha256:e4b1…
  ⛅ run        hello · revision hello-00001 · 100% traffic

✓ hello → https://hello-xxxxx-uc.a.run.app  (1m24s)

Security summary:
  hello runs as hello@my-tako-proj.iam — no resource grants (hello
  declares no resources); public ingress (no WithPrivate routes)
```

- **Re-deploy with nothing changed** (the spec-hash payoff):
  `plan: 0 creates, 0 updates, 3 skips` → `✓ no changes (4s)`.
- **`tako deploy --plan`**: prints the classified diff (create /
  update / skip / orphaned-kept / DRIFT), applies nothing, releases no
  lease (never takes one).
- **Drift**: hand-edited service in the console → next deploy prints
  `drift: run service hello was modified outside tako (spec-hash
  mismatch) — tako-owned fields will be overwritten` before applying.
- **`tako logs [service] [--since 15m]`**: recent structured logs for
  the service (all services when omitted), newest last:
  `12:01:03 hello INFO GET /api/hello → 200 (1.2ms)`.
- **`tako destroy --project my-tako-proj`**: lists everything holding
  the app's labels, asks `type the app name to confirm:`, deletes in
  reverse reconcile order. Hello has no data-bearing resources; when
  those arrive (C3), destroy inherits the §4.6a data confirmation.
- Project resolution (friction rule): `--project` flag, else
  `GOOGLE_CLOUD_PROJECT`, else gcloud's configured default, else:
  `no project set — pass --project, set GOOGLE_CLOUD_PROJECT, or run "gcloud config set project <id>"`.
  Region: `--region`, else `TAKO_REGION`, else `us-central1`.

### 2. Semantics being locked

- **Names & labels.** Cloud Run service/SA named after the graph
  service (`hello`, later `gallery-makethumb`); every managed object
  carries labels `tako-app=<app>`, `tako-env=<env>` (default env
  `prod`; `--env pr-42` prefixes names and re-labels — disjoint
  resource sets from the same code). Observed state = list-by-label;
  the cloud is the state.
- **Spec-hash diffing** (§4.6a): every object annotated
  `tako-spec-hash: sha256(<intended spec>)`; absent→create, hash
  differs→update, matches→skip; hand-edits detected as drift and
  announced before overwrite.
- **Deploy lease**: object `leases/deploy-<app>-<env>` in bucket
  `<project>-tako` (created on first deploy, labeled), taken with a
  generation-0 precondition, holder + timestamp in metadata; concurrent
  deploy → `deploy in progress by <who> since <when> — waiting…`;
  stale (>10min) leases are broken with a warning. Released on exit,
  even on failure.
- **The service spec**: image by digest (built + pushed as part of
  deploy — `tako build` remains the standalone piece), env
  `TAKO_BACKEND=gcp`, `default` profile knobs (min 0 / max 20 /
  conc 80 / 1 CPU / 512Mi, §4.6), runs as its per-service SA, public
  ingress in C2 (auth/private arrive with their features), revision
  annotated with the git SHA + AppGraph hash.
- **Deploys are additive-first** and idempotent; interrupting one and
  re-running converges (stateless reconcile — nothing half-remembered).

### 3. Internals

- `pkg/provision`: the reconciler skeleton — `Observe` (list-by-label)
  → `Plan` (classified diff vs. desired-from-AppGraph) → `Apply`
  (ordered) — behind narrow client interfaces (serviceAccounts, repos,
  runServices, leaseBucket) with in-memory fakes; PLAN OUTPUT IS
  GOLDEN-TESTED against the fakes (create-all, no-op, drift, orphan
  cases). Real GCP clients (cloud.google.com/go/{run,storage,
  artifactregistry}, iam REST, logadmin) are thin adapters — the gate
  exercises them.
- `cmd/tako`: `deploy`, `logs`, `destroy` verbs.
- No SDK surface changes (freeze holds).

### 4. Cloud errors & permissions (per docs/tako-cloud-permissions.md)

- **Preflight before any mutation**: `testIamPermissions` for
  run.services.{create,update}, iam.serviceAccounts.{create,actAs},
  artifactregistry.repositories.uploadArtifacts, storage buckets/objects
  (lease); plus API-enabled probes. Failure = one line per gap, exact
  taxonomy texts:
  - not authenticated → `cannot authenticate to Google Cloud — run "gcloud auth login" (or set GOOGLE_APPLICATION_CREDENTIALS)`
  - API disabled → `the Cloud Run API is disabled on my-tako-proj — enable it: gcloud services enable run.googleapis.com --project my-tako-proj`
  - missing role → `you lack run.services.create on my-tako-proj — grant: gcloud projects add-iam-policy-binding my-tako-proj --member user:$(gcloud config get account) --role roles/run.admin`
  - wrong project id → `project "my-tako-prj" not found (or you have no access) — check the id`
- **Org-policy denials** (e.g. domain-restricted sharing blocking
  public ingress): surfaced verbatim with
  `your organization's policy forbids this — tako will not try to work around it; see docs/tako-cloud-permissions.md`.
- **Runtime grants for hello**: none (no resources) — and the security
  summary SAYS so explicitly; the derived-grant machinery ships with
  C3's first real edges. Tako never grants the deployer anything
  (prints commands only).
- **Taxonomy tests**: classification unit-tested against constructed
  API errors (like C1's `TestPushErrorTaxonomy`); live texts verified
  at the gate.

### 5. Not in scope (C2 non-goals)

- No resource backends (buckets/entities/queues in GCP) — C3+; hello
  needs none.
- No `--canary` / `promote` / `rollback` (arrive with a later slice;
  revisions + digest pinning already make them cheap).
- No `tako prune` (nothing can be orphaned before C3), no `overrides:`
  escape hatch, no custom domains.
- No `--env` CI wiring (the flag works; PR automation is the user's).
- No log following (`-f`) — recent-window fetch only in v0.

**STOP — awaiting DX approval before implementation.**

## C2 — implementation: Minimal reconciler + hello deploys (2026-07-24)

Shipped per the approved proposal. No SDK surface changed (freeze holds).

**What landed**
- `pkg/provision`: the reconciler core, cloud-free and scenario-tested —
  `Desired` (AppGraph → managed objects: repo, per-service SA, run
  service with §4.6 profile knobs, env-prefixed names, tako labels),
  `Compute` (spec-hash diff → classified plan: create/update/skip/
  orphaned-kept, DRIFT flag, deterministic order), `Apply` (ordered,
  routed per kind), `SpecHash` (canonical, label-safe hex — key order
  can never flap a deploy). Scenarios pinned: fresh project = 3 creates;
  second deploy = 3 skips; new image digest = 1 update; console edit =
  DRIFT warning; removed service = orphaned (kept), never deleted,
  Changed()==false; env prefixes.
- `pkg/provision/gcp`: thin REST adapters (plain HTTP + ADC — no
  product SDKs, so every failure is a status code the taxonomy owns):
  Artifact Registry repos (hash in labels), service accounts (no labels
  on SAs — markers ride the description field), Cloud Run v2 services
  (create/patch + LRO wait + public run.invoker; LiveHash re-derived
  from live fields with normalization for drift detection), the GCS
  deploy lease (generation-0 precondition, holder metadata, stale>10min
  broken with warning, waits with "deploy in progress by <who>"),
  Cloud Logging reader.
- `cmd/tako`: `deploy [--plan|--project|--region|--env]` (analyze →
  generate → preflight → local digest builds → observe-by-label → plan →
  lease → apply repos/SAs → push → apply services → URLs + security
  summary), `logs [service] [--since]`, `destroy` (typed confirmation,
  reverse reconcile order, framework bucket last). Project resolution:
  flag → GOOGLE_CLOUD_PROJECT → gcloud config; region default
  us-central1.

**Cloud errors & permissions (as proposed)**
- Preflight before any mutation: serviceusage probes for the four APIs +
  `testIamPermissions` for seven permissions, each gap one line with its
  exact enable/grant command (narrowest role, per the model).
- `gcp.Classify` implements the taxonomy; `TestClassifyTaxonomy` pins
  all five texts (unauthenticated / API-disabled / org-policy — surfaced
  verbatim, never worked around / missing-role / bad-project) against
  real-shaped API error bodies.
- Live check from this credential-less machine: `tako deploy --plan
  --project fake-proj` → `cannot authenticate to Google Cloud — run
  "gcloud auth application-default login" …` before anything else.

**Tier-2 validation** — everything cloud-free ran here: provision
scenarios green, gcp taxonomy green, full tree + all examples green, and
the unauthenticated CLI path above. §§1–6 of `validate/deploy.md` (the
first full cycle: clean project → URL → destroy, drift, lease contention,
logs) need your test project and are the gate itself, by design.

**Discovered work (not done)**: `tako prune` (orphan cleanup);
canary/promote/rollback (revisions + digests already in place);
deploy-time build parallelism (services build serially); logs -f.

**GATE REQUEST — C2: the first full cycle.** Please run
`validate/deploy.md` end to end with your test project — time §3
against the <~2min target, review the §2 plan line by line, and try §4's
drift heal and §5's lease contention. Record the verdict in
`docs/validation-log.md`. C3 (gcp Bucket + Entity; shortlink graduates)
follows.

## C3 — DX proposal: gcp Bucket + Entity; shortlink graduates (2026-07-24)

Agent-plan scope: conformance(gcp) green as the internal bar; reconciler
grows buckets, Datastore indexes, derived IAM. Gate C3: full cycle on
shortlink + human reviews the printed IAM summary against expectations.
Spec §4.6 (reconcile order, IAM table) and
docs/tako-cloud-permissions.md are normative.

### 0. The headline

`examples/shortlink` does not change by a single line. The same code
that passed gate F2 deploys to GCP: `links`/`clicks-seen` become
Datastore kinds, derived composite indexes are created from the Query
call sites the analyzer already extracts, and every grant in the
security summary traces to a graph edge.

### 1. Scope call needing your decision (before anything else)

shortlink is Entity + **Queue + Worker**. Pub/Sub push is C4's scope,
so C3 must either:

- **(a) RECOMMENDED — minimal Pub/Sub in C3**: plain Pub/Sub topic + one
  OIDC push subscription per worker, default retry policy. C4 keeps
  dead-letter topics, backoff/retry config, per-handler topic fan-out,
  and the kill-mid-job cloud validation. Payoff: the C3 gate demo is
  the real app — click a link, watch Hits increment in the cloud.
- **(b) shortlink graduates degraded**: countclick deploys but no
  subscription exists until C4; Hits stays 0 in the cloud. The gate
  demo would validate Entity only — weaker, and the security summary
  would show a publisher grant into a topic nobody consumes.

The proposal below assumes (a); strike the queue lines for (b).

### 2. CLI surface

```
$ cd examples/shortlink
$ tako deploy --project tako-test-01
tako deploy · shortlink → tako-test-01 (us-central1)

  analyzing    2 services, 3 endpoints, 1 worker, 2 entities, 1 queue
  generating   .tako/gen
  preflight    ok (run, iam, artifactregistry, storage, datastore, pubsub)
  building     2 service image(s), digest-pinned
  plan
    create  service account shortlink-sa
    create  service account shortlink-countclick-sa
    create  datastore index links (owner asc), links (created asc)
    create  topic clicks
    create  bucket tako-test-01-tako (lease, exists → skip)
    create  run service shortlink
    create  run service shortlink-countclick
    create  push subscription clicks → shortlink-countclick
    create  iam grant datastore.user → shortlink-sa
    create  iam grant datastore.user → shortlink-countclick-sa
    create  iam grant pubsub.publisher(clicks) → shortlink-sa
    create  iam grant run.invoker(shortlink-countclick) → pubsub push SA
    12 creates, 0 updates, 0 skips, 0 orphans
  lease        acquired (memo@… on tako-test-01/shortlink/prod)
  ⛅ …          (index build waits with narration; empty DB ≈ seconds)

✓ shortlink → https://shortlink-xxxxx-uc.a.run.app  (1m58s)

Security summary:
  shortlink runs as shortlink-sa@… —
    datastore [user — project-wide: GCP has no per-kind Datastore IAM],
    clicks [publisher]
  shortlink-countclick runs as shortlink-countclick-sa@… —
    datastore [user]; ingress: OIDC push from Pub/Sub SA only (run.invoker)
  public ingress: shortlink
```

- **Demo (the gate)**: `curl -X POST …/api/links -d '{"slug":"gh",…}'`
  → 200; duplicate slug → 409 (Datastore transaction, same text as
  local); `curl …/api/links/gh` → 200, and a few seconds later
  `…/api/links?owner=…` shows `hits: 1` (real push round-trip);
  unknown slug → 404.
- **Conformance(gcp)** — the internal bar, run against the test
  project: `go test ./pkg/backends/conformance -backend=gcp
  -project tako-test-01` — Entity + Bucket chapters (topics/signed/
  events chapters remain local-only until C4/C5).
- **`tako destroy`** now meets §4.6a for real: it lists data-bearing
  resources separately and requires the app name typed:
  `DATA: datastore kinds links, clicks-seen (rows are not counted —
  deletion is total) · type the app name to confirm:`.
- **Re-deploy unchanged** → all skips, `✓ no changes`.

### 3. Semantics being locked

- **Entity → Datastore** (Firestore in Datastore mode, database
  `(default)`). Kind = declared store name. **Env isolation =
  Datastore namespace = env** (`prod` included, no default-namespace
  special case): disjoint envs per §4.6a, no kind-name mangling.
- **Indexes**: derived from Query call sites (already in the
  AppGraph), created before services (reconcile step 2), deploy waits
  on index builds with narration. Datastore indexes carry no labels:
  ownership = exact match against the derived set; foreign indexes are
  warned as unmanaged, never touched.
- **Bucket → GCS**: name `<project>-<app>-<bucket>` (global
  uniqueness), uniform bucket-level access, no public access, labeled.
  shortlink declares none — the Bucket backend's bar is conformance.
- **Minimal Pub/Sub (if (a))**: topic named after the queue, one push
  subscription per worker with OIDC auth to the worker's URL, GCP
  default retry. The generated /_tako/push contract from C1.5 is
  unchanged.
- **Grants are objects**: IAM bindings appear in the plan, are diffed
  like everything else, and removing a code edge removes the grant on
  the next deploy (grants are not data).
- **Runtime env**: services get TAKO_BACKEND=gcp, TAKO_PROJECT,
  TAKO_ENV; resource names resolve from declarations exactly as local.

### 4. Internals

- `pkg/backends/gcp`: `entity.go` (cloud.google.com/go/datastore —
  Tx/Get/Put/Query/IsNotFound mapped to the contract), `bucket.go`
  (GCS). Conformance runner grows `-backend=gcp -project` wiring;
  refuses to run without explicit project (never accidental).
- `pkg/provision`: new kinds — bucket, datastore index, topic, push
  subscription, IAM grant — same Observe/Plan/Apply shape, fakes +
  golden plans first, thin GCP adapters after.
- No SDK surface changes (freeze holds).

### 5. Cloud errors & permissions (per docs/tako-cloud-permissions.md)

- **Preflight adds**: datastore + pubsub API probes; database
  existence — missing →
  `no Datastore database in tako-test-01 — create it: gcloud firestore databases create --location=us-central1 --type=datastore-mode --project tako-test-01`;
  deployer permission probes for datastore.indexes.create,
  pubsub.topics.create, pubsub.subscriptions.create,
  storage.buckets.create, resourcemanager.projects.setIamPolicy — each
  gap one line with the exact grant command, taxonomy texts pinned by
  unit test as in C1/C2.
- **setIamPolicy honesty**: granting runtime roles needs project
  setIamPolicy; tako still never self-escalates — missing it prints
  the grant command and stops before any mutation.
- **Runtime 403 = reconciler bug**: backend code never retries or
  escalates; the error text says
  `shortlink lacks datastore.user — this is a tako deploy bug, re-run tako deploy; if it persists, file it`.
- **Org-policy denials** (e.g. public ingress forbidden, uniform
  bucket enforcement conflicts): surfaced verbatim, never worked
  around.

### 6. Not in scope (C3 non-goals)

- Dead-letter topics, retry/backoff config, kill-mid-job validation,
  per-handler topic fan-out → C4 (even under (a)).
- GCS notifications, signBlob signed URLs, Cloud Tasks → C5.
- Cache and metrics cloud backends (cache stays in-process per
  instance in v0; metrics land with the observability slice).
- `tako prune`, `--env` cloud previews beyond what C2 shipped.

**Discovered work (not done)**: SAName clamping means very long worker
names shorten — the security summary should print the actual SA name
verbatim rather than re-deriving it.

**STOP — awaiting DX approval (and the §1 scope call) before
implementation.**

### Amendments (2026-07-24, per review)

- **§1 scope call decided: (a)** — minimal Pub/Sub ships in C3 (topic +
  OIDC push subscription per worker, GCP default retry); the
  "queue-lite" label is retired — it is plain Cloud Pub/Sub with C4's
  tako features (dead-letter, retry config, fan-out, kill-mid-job
  validation) deferred.
- **SA naming settled: `<service>-sa`** (env-prefixed, 6–30 clamp with
  hash pad/truncation) — replaces both the interim `tako-` prefix and
  the permissions doc's `<app>-<service>` pattern (doc updated; app
  prefix was redundant under one-app-per-project and stuttered when
  package == module). Shipped immediately in pkg/provision since it
  amends C2 behavior: hello → `hello-sa`. The tako-test-01 project may
  hold an orphaned `tako-hello` SA from the C2 gate — next deploy warns
  orphaned-kept; delete it manually or via tako destroy.
- Full DX approval for C3 remains open.

## C3 — implementation: gcp Bucket + Entity + minimal Pub/Sub; shortlink graduates (2026-07-24)

Shipped per the amended proposal (scope call (a)); shortlink's code
unchanged. Full cycle executed live against tako-test-01, including
destroy — transcript excerpts below.

**Analyzer (prerequisite discovered during mapping):** AppGraph.Edges
was a declared-but-unpopulated contract, and Resource.Indexes discarded
query grouping/direction — derived IAM and composite indexes had
nothing to read. Shipped: `edges.go` (per-handler AST reference scan,
transitive through same-package calls, verbs from SDK method names;
Edge gains ToKind since resource names are only unique per kind —
gallery has bucket AND entity "photos"), and per-entity aggregate
`Resource.Composite` (filter fields then OrderBy fields with
direction). Composite property names are the MARSHALED (json tag)
names, not Go field names — caught live when Datastore built an index
on Owner/Created while the wire writes owner/created; the analyzer now
mirrors the SDK's jsonName rule. Goldens + TestShortlinkEdges/
TestShortlinkComposite pin all of it.

**Backends (pkg/backends/gcp):** Entity on Firestore-in-Datastore-mode
(raw doc as unindexed _doc blob + typed indexed properties per
top-level field; namespace = env; native filters/orders with local's
semantics — RFC3339 strings become timestamps, default id-order and
its limit apply client-side when they can't push down); Bucket on GCS
(physical names via shared naming.go, also used by the provisioner);
Queue publishes to Pub/Sub (delivery is push; Consume is a documented
no-op); Topic/Signer stubs error with C4/C5 phase pointers.
runtime.BootGCP wires hooks from TAKO_PROJECT/APP/ENV (injected into
every run service); PushHandler shape-detects and unwraps real Pub/Sub
envelopes (base64 message.data) while local raw posts pass through —
unit-tested.

**Conformance(gcp)** — the internal bar, run live:
```
go test ./pkg/backends/conformance -backend=gcp -project tako-test-01 -run 'TestEntity|TestBucket'
Entity 18/18, Bucket 7/7 (after index serving lag; suite refuses to run without -project)
```

**Reconciler:** five kinds — bucket, datastore index (content-addressed
ownership, immutable, 10m build wait with narration), topic, push
subscription (OIDC as the worker's own SA, deterministic
https://<name>-<projectNumber>.<region>.run.app endpoint so plans
precede the service), iam grants (one object per target owning the
tako-managed policy slice; ManagedMember keys on the -sa suffix, which
now survives SAName clamping; removal converges by policy rewrite —
grants are not data). Async-only services deploy private; grants apply
before subscriptions. Preflight grows datastore/pubsub probes gated on
the graph (Needs), a Datastore-database existence check with the exact
create command, and setIamPolicy perms. Destroy: §4.6a DATA section
(buckets + datastore kinds), typed confirmation, reverse-order deletes,
grant revocation, and a namespace purge (entity count printed).

**Live cycle (tako-test-01):** plan reviewed → deploy (index build
dominates first deploy; converged re-deploy 42s) → curl create/409/404
→ composite-index list query → hits 1→3 via real publish → OIDC push →
private worker → idempotency ledger → destroy purged 6 entities and
left the project clean. IAM propagation 403'd entity ops for ~90s
post-first-deploy — the runtime taxonomy text fired (calling it a
deploy bug, which in that window it is not; noted below).

**Discovered work (not done):** security summary should soften the
runtime-403 "deploy bug" text for the first minutes after a grant write
(IAM propagation window); destroy re-runs list async-deleting indexes
and always-readable grant targets until convergence (cosmetic);
conformance leaves conf-<hex> Datastore namespaces; per-query composite
tuples (v0 aggregates per entity, may over-index); Edges are
same-package transitive only; stale datastore.user lingers if an app
drops its LAST entity (no project grants object emitted then).

**GATE REQUEST — C3: full cycle on shortlink.** Run
`validate/shortlink-cloud.md` end to end on tako-test-01 (§1
conformance, §2 plan review + security summary judgment, §3 the app +
async spine, §5 destroy with the DATA confirmation) and record the
verdict in docs/validation-log.md. C4 (Pub/Sub dead-letter + retry;
gallery async graduates) follows.

## C4 — DX proposal: Pub/Sub push + dead-letter; gallery async graduates (2026-07-25)

Agent-plan scope: OIDC push verification, per-handler subscriptions,
retry/backoff config. Gate C4: full cycle; human kills the worker
mid-job in the cloud and observes redelivery then dead-letter. Spec
§4.6 and docs/tako-cloud-permissions.md are normative. C3's verdict is
recorded; the tako-test-01 Datastore database now exists.

### 0. The headline

`examples/gallery` does not change by a single line — and neither does
the push model. The analyzer already gives every worker AND every
subscriber its own service (partition rule: API/Page/Cron share the
package's service; each async handler is alone in its own), so gallery
deploys as five services: `gallery` (7 APIs + the cron, public) and
four private single-handler async services. "Per-handler
subscriptions" is therefore per-*service* subscriptions — one push
subscription per async handler onto its source topic — and "OIDC push
verification" stays what C3 validated: private Run services admitting
only their own SA (plain curl → IAM 403). No in-runtime JWT parsing;
sync and async handlers never share a service, so a push endpoint on a
public service cannot exist. C4's real substance is what happens when
a delivery FAILS: retry with backoff, then a dead-letter topic you can
inspect — plus the two things gallery needs to run at all in the
cloud (a pipeline trigger and Secret Manager).

### 1. Scope calls needing your decision (before anything else)

1. **Pipeline trigger — bucket events lite (RECOMMENDED: include).**
   Gallery's only entry to the async spine is `photos.Uploaded` →
   `onUploaded`. GCS-native notifications are C5's scope (they exist
   to cover *signed direct* uploads, which are also C5 — the Signer is
   still a stub, so no write can bypass the service today). Without a
   trigger, C4's "gallery async graduates" is undemonstrable. Proposal:
   the gcp Bucket backend *emits* the uploaded event itself — after a
   successful through-service `Put`, the runtime publishes ObjectInfo
   to topic `<bucket>-uploaded`; subscribers bind to that topic. C5
   replaces emission with GCS-native notifications on the SAME topic
   (and turns emission off) when signed uploads land. Alternative:
   defer any trigger and gate C4 on synthetic publishes — weaker demo,
   the pipeline never runs whole.
2. **Secrets — package-wide resolution (RECOMMENDED: (a)).** Gallery
   declares `tako.Secret("notifier-key")` and F8's contract is that
   boot REFUSES to start with a declared secret unset — which today
   only local enforces; in the cloud every gallery service would crash
   on `Value()`. C4 wires BootGCP to resolve declared secrets from
   Secret Manager (`<env-prefixed name>/versions/latest`) before
   serving. The call: **(a)** every service of the package resolves
   every declared secret (upholds "Value never fails" verbatim; every
   SA gets accessor on the app's secrets), or **(b)** edge-derived
   per-service resolution (least-privilege: only `gallery-notifyowner`
   can read `notifier-key`; a non-edge `Value()` would panic — a
   contract carve-out). (a) is simpler and the secret is app-scoped
   data anyway; (b) is purer per the permissions doc. Proposal assumes
   (a).
3. **`tako dlq` (RECOMMENDED: include, peek + `--ack`).** The gate
   observes dead-letters; local parity is the on-disk dead-letter JSON
   from F4. Minimal CLI (no SDK surface — freeze holds): `tako dlq`
   lists dead-letter subscriptions with message counts; `tako dlq
   <service>` peeks messages (payload, original attempt count, time)
   without acking; `--ack` drains what it printed. Replay/redrive is a
   non-goal. Alternative: document `gcloud pubsub subscriptions pull`
   — honest but hostile.

### 2. CLI surface

```
$ cd examples/gallery
$ tako deploy --project tako-test-01
tako deploy · gallery → tako-test-01 (us-central1)

  analyzing    5 services, 7 endpoints, 1 worker, 3 subscribers, 1 cron,
               1 bucket, 1 entity, 1 queue, 1 topic, 1 secret
  preflight    ok (run, iam, artifactregistry, storage, datastore,
               pubsub, secretmanager · secret notifier-key: exists)
  building     5 service image(s), digest-pinned
  plan
    create  service account gallery-sa            (…4 more SAs)
    create  bucket tako-test-01-gallery-photos
    create  datastore index photos (owner, ready)
    create  topic thumbnails                      (queue)
    create  topic photo-ready                     (topic)
    create  topic photos-uploaded                 (bucket events)
    create  topic gallery-makethumb-dead          (…3 more dead topics)
    create  run service gallery                   (…4 more, private)
    create  iam grants …                          (per-target, see summary)
    create  push subscription photos-uploaded → gallery-onuploaded
    create  push subscription thumbnails → gallery-makethumb
    create  push subscription photo-ready → gallery-notifyowner
    create  push subscription photo-ready → gallery-warmfeed
    create  dead-letter keep subscription gallery-makethumb-dead (…3 more)
  ⚠ cron cleanupOrphans is not scheduled in the cloud yet (scheduler is
    a later feature); it still runs under tako dev.

✓ gallery → https://gallery-…-uc.a.run.app

Security summary:
  gallery runs as gallery-sa@… — datastore [user — project-wide: GCP
    has no per-kind Datastore IAM], photos [objectAdmin],
    photos-uploaded [publisher — emits bucket events],
    notifier-key [secretAccessor]
  gallery-makethumb runs as gallery-makethumb-sa@… — datastore [user],
    photos [objectAdmin], photo-ready [publisher],
    gallery-makethumb-dead [publisher — Permanent dead-letters],
    notifier-key [secretAccessor]; ingress: OIDC push from its own SA
    only · redelivery: 5 attempts, exponential backoff, then dead-letter
  (… gallery-onuploaded, gallery-notifyowner, gallery-warmfeed alike)
  public ingress: gallery
```

- **Demo (the gate)**: multipart `POST /api/photos` → bucket event →
  queue → worker (thumb appears at `/api/photos/{id}/thumb`) → topic →
  BOTH subscribers; `/api/photos` lists the ready photo. Caption
  containing `boom` → NotifyOwner retries with visible backoff
  (`attempt 3/5` in `tako logs`, from the push envelope's
  deliveryAttempt) and dead-letters on the 5th, while WarmFeed acks
  every attempt — independent fates, now in the cloud. A text-file
  upload → makeThumb returns `tako.Permanent` → dead-lettered after
  attempt 1, no retries. Kill-mid-job: while a thumb job is in flight,
  `gcloud run services update gallery-makethumb --no-traffic`-style
  interference (script will pin the exact method) → Pub/Sub redelivers
  on the ack deadline; the idempotent pipeline converges.
- **`tako dlq`**: `tako dlq` → `gallery-notifyowner  1 message`;
  `tako dlq gallery-notifyowner` prints payload JSON + attempts +
  published time; `--ack` drains. Empty: `no dead-lettered messages`.
- **Conformance(gcp)** grows the Topic chapter: publish + two-pull-
  subscription fan-out verified against real Pub/Sub via harness pull
  subs (push delivery is infrastructure, validated by the gate).
  BucketEvents chapter stays local-only until C5 unifies the native
  path.
- **Destroy** grows nothing conceptually: dead topics/keep subs delete
  with the rest (messages in them are data → they appear in the DATA
  section), secrets are NEVER deleted (user-created, values are data).

### 3. Semantics being locked

- **Retry policy is fixed, not configurable**: maxDeliveryAttempts=5
  matching local's dispatcher, Pub/Sub exponential backoff (minimum
  10s, maximum 300s), ackDeadline 600s as in C3. Per-queue tuning is
  API surface → its own post-freeze task. "Retry/backoff config" from
  the agent plan = tako CONFIGURES the subscription; the user does not.
- **`tako.Permanent` = 1 attempt, then dead-letter — exactly local**:
  Pub/Sub cannot express "nack without retry" (any non-2xx redelivers),
  so on gcp the runtime publishes the payload to the service's own
  dead topic itself and ACKS (204). Local semantics preserved; the SA
  needs publisher on its own dead topic (in the summary).
- **Dead-letter plumbing per async service**: dead topic
  `<service>-dead` + a pull "keep" subscription (Pub/Sub drops
  dead-letters with no subscription — the keep sub is retention,
  7-day default). Pub/Sub's service agent gets publisher on each dead
  topic and subscriber on each source subscription (required by
  deadLetterPolicy; grants objects like everything else).
- **Bucket events (lite)**: emission-on-Put from the runtime for
  through-service writes; topic per bucket `<bucket>-uploaded`;
  workers' own-write filtering stays app-side (`_thumb` suffix idiom
  unchanged). Signed direct uploads don't exist until C5; C5 swaps in
  GCS-native notifications on the same topic.
- **Secrets**: physical name `Prefixed(env, name)` in Secret Manager,
  `latest` version, resolved once at boot; tako never creates,
  updates, or deletes secrets — preflight verifies existence and
  prints the exact create command; grants are reconciled objects.
- **Cache stays per-instance** (C3 non-goal, unchanged) — honest
  consequence now visible: cloud `warmFeed` warms only its own
  process; `list` still serves correctly via GetOrFill + TTL. The
  fan-out demo therefore judges NotifyOwner/WarmFeed by acks and logs,
  not by cache effects. Shared cache is a later feature.
- **Cron is inert in the cloud** (deploy warns once, banner still
  shows it locally). Cloud Scheduler is a later feature; noted in the
  plan output so nobody believes cleanup runs.
- **TAKO_SERVICE joins the injected env** (dead-topic derivation +
  log labeling), alongside TAKO_PROJECT/APP/ENV.
- **Shortlink converges, doesn't break**: next shortlink deploy shows
  its countclick subscription as `update` (spec gains
  retry/dead-letter policy) plus creates for the dead topic, keep sub,
  and service-agent grants.

### 4. Internals

- `pkg/provision`: subscriptions for subscribers (topic + bucket-event
  sources), dead topics + keep subs as desired objects, retry/DLQ
  fields in the subscription spec, service-agent + secret grant
  derivation, secret preflight; golden plan test grows a gallery
  fixture (the hand-built shortlink one stays).
- `pkg/backends/gcp`: TopicStore.Publish becomes real (C4 stub dies),
  bucket event emission in BucketStore.Put, dead-letter publish path
  in the push runtime, Secret Manager resolution in BootGCP.
- `pkg/runtime`: PushHandler on gcp routes Permanent to the dead
  topic + 204 (local keeps 400), logs deliveryAttempt as `attempt n/5`.
- `cmd/tako`: `dlq` command; deploy banner cron warning.
- No SDK surface changes (freeze holds).

### 5. Cloud errors & permissions (per docs/tako-cloud-permissions.md)

- **Preflight adds**: secretmanager API probe (only when the graph
  declares secrets); per-secret existence —
  `✗ secret notifier-key: not found in tako-test-01 — create it: printf '<value>' | gcloud secrets create notifier-key --data-file=- --project tako-test-01`
  (env-prefixed name for non-prod envs); deployer perms
  secretmanager.secrets.get + secrets.setIamPolicy,
  pubsub.subscriptions.setIamPolicy (dead-letter service-agent
  grants) — each gap one line with the exact grant command, texts
  pinned by unit test.
- **Boot missing secret (cloud)**: same text local already ships —
  `secret "notifier-key" is not set — add it to .env (local) or Secret Manager (cloud)`
  — the service refuses to serve; preflight makes this unreachable in
  practice.
- **`WithDelay` on gcp**: Publish errors
  `tako: Publish(WithDelay): delayed delivery in the cloud arrives with Cloud Tasks (C5) (tako/not-yet)`
  — Pub/Sub cannot delay; erroring beats silently delivering early.
- **Runtime 403 taxonomy unchanged** — including the known C3
  discovered-work caveat (IAM-propagation window mislabels itself a
  deploy bug; softening that text stays discovered work, not C4).
- **Org-policy denials** surfaced verbatim, never worked around.

### 6. Not in scope (C4 non-goals)

- GCS-native notifications, signBlob signed URLs, Cloud Tasks
  (WithDelay), gallery FULLY graduating (the F9 demo needs signed
  URLs) → C5.
- Cloud Scheduler for cron; shared/cloud cache; metrics backend.
- DLQ replay/redrive; per-queue retry configuration (API surface).
- In-runtime OIDC JWT verification (unreachable by construction —
  revisit only if a feature ever co-locates push with public ingress).

**Validation**: `validate/gallery-cloud.md` (written at implementation
time): conformance(gcp) incl. Topic chapter → plan + security-summary
review → the pipeline demo with independent fates, Permanent, and
kill-mid-job redelivery → `tako dlq` → idempotence → destroy.

**STOP — awaiting DX approval (and the three §1 scope calls) before
implementation.**

## C4 — implementation: Pub/Sub push + dead-letter; gallery async graduates (2026-07-25)

Proposal approved with all three §1 recommendations: bucket events
lite, package-wide secret resolution (a), and tako dlq (peek + --ack).
Shipped as proposed; amendments below are what the live cycle forced.

**Provision:** subscriptions now cover subscribers (topic and
bucket-event sources; sourceTopic maps `photos.Uploaded` → the
`photos-uploaded` events topic, created only for buckets that have
subscribers); every async service gets a dead topic + a pull "keep"
subscription (new KindPullSubscription, reconciled right after topics
so no dead-letter ever lands in a subscriber-less topic); push
subscription specs carry retryPolicy (10s–300s exponential) and
deadLetterPolicy (maxDeliveryAttempts 5, matching local's dispatcher).
Grants grow subscription + secret target types: SA publisher on its
own dead topic (the Permanent path), the Pub/Sub service agent's
dead-letter pair (publisher on dead topics, subscriber on source
subscriptions — agent counts as managed on topic/subscription/SA
targets), secretAccessor for every app SA on every declared secret,
and events-topic publisher for exactly the bucket-writing services.
TAKO_SERVICE + TAKO_BUCKET_EVENTS join the injected env.
TestGalleryPlanRender pins the whole 52-object plan;
TestGalleryFanoutAndTriggers pins routing, emission grants, and
public/private; the shortlink goldens grew the convergence
(dead topic + keep sub + two grant targets).

**Backends/runtime:** TopicStore.Publish is real (C4 stub dies);
BucketStore.Put decodes the upload response and announces ObjectMeta
on the events topic (same payload as local's WireBucketEvents; a
publish failure reports "object stored but its Uploaded event did not
publish"); BootGCP resolves declared secrets from Secret Manager
versions/latest before serving (missing → boot refuses, same contract
as .env) and wires the dead-letter hook; PushHandler unwraps
deliveryAttempt, narrates `attempt n/5` and "delivery failed — nacked
for retry" (the cloud face of local's retry lines), and on
tako.Permanent publishes to the service's own dead topic and ACKS —
one attempt then dead-letter, because Pub/Sub cannot express "nack
without retry" (hook failure nacks so nothing is lost; all pinned in
push_test.go). Crons no longer tick in the cloud (a per-instance
ticker under autoscaling is not a schedule): boot marks them inert,
deploy prints the ⚠, dev unchanged.

**CLI:** grant application is now three-phase — non-run/non-
subscription targets BEFORE run services (services resolve secrets at
boot), run targets after services, subscription targets after their
subscriptions. tako dlq lists per-service dead-letter state, peeks
messages (payload + reason + time), --ack drains. Destroy's DATA
section adds the keep subscriptions.

**Conformance(gcp)** grows TestTopicGCP (publish fan-out through two
pull subscriptions, real Pub/Sub) and TestBucketEventEmitGCP (Put →
ObjectMeta on the events topic); stale "Phase F" skip texts replaced
with the honest push-is-infrastructure story.

**Live cycle (tako-test-01, validate/gallery-cloud.md):** conformance
Entity 18/18 + Bucket 7/7 + both new chapters; preflight caught the
disabled Secret Manager API, then the missing secret with the exact
create command (via direct probe — see discovered work); 52-object
plan matched the golden; deploy 1m45s once converged; the whole spine
ran with zero gallery changes — upload → emitted bucket event →
onUploaded → queue → makeThumb (real 564B resized thumb in 21s) →
photo-ready → both subscribers; `boom` caption: NotifyOwner narrated
attempt=1/5…5/5 with visible backoff then dead-lettered ("delivery
attempts exhausted") while WarmFeed acked every delivery of the same
events and the feed stayed correct — independent fates in the cloud;
empty upload: Permanent dead-lettered in seconds with reason "empty
upload", no retries; plain curl on a worker push endpoint → 403 (IAM);
re-deploy → all skips 34s; dlq --ack drained both queues to empty;
destroy listed bucket + datastore kind + keep subs under DATA, deleted
in reverse order, purged 4 entities, left the project clean.

**Three bugs the live cycle caught (all fixed and pinned):**
1. Boot-time secret resolution vs grant order: services booted before
   their secretAccessor existed and Run marked the revision dead —
   grant application became three-phase, and boot retries secret reads
   for 90s (IAM propagation) before failing with the readable text.
2. The generated main panicked on gcp ("SignedURLHandler before
   Boot"): any app with a bucket mounts /_tako/signed, which only had
   a local implementation — it now serves the C5 phase pointer (501)
   when no local boot exists.
3. Permanent false DRIFT on private services: liveHash hardcoded
   `public: true` (a C2 constant), so every async service hashed as
   drifted forever — the live ingress is now read from the service's
   own IAM policy (allUsers invoker present = public).

**Discovered work (not done):** preflight's secret probe treats only
404 as missing, so a just-enabled Secret Manager API (403 during
propagation) slips through to a boot-time failure; tako dlq's peek
holds the pull lease ~10s, so back-to-back peeks alternate messages
(should modifyAckDeadline to 0 on peek); C3 leftovers stand (runtime
403 text during propagation, conformance conf-* namespaces, shared
composite indexes surviving destroy).

**GATE REQUEST — C4: full cycle on gallery.** Run
`validate/gallery-cloud.md` end to end on tako-test-01 (§1
conformance incl. the two Pub/Sub chapters, §2 plan + security
summary judged grant by grant, §3 the pipeline + boom retries +
Permanent + the IAM-gated push endpoint, §4 idempotence + dlq drain,
§5 destroy with keep subs under DATA) and record the verdict in
docs/validation-log.md. The notifier-key secret exists in the project
(create once, survives destroy — by design). C5 (GCS notifications +
signBlob signed URLs + Cloud Tasks; gallery FULLY graduates on the F9
demo script) follows.

## C5 — DX proposal: GCS notifications + signBlob signed URLs; gallery fully graduates (2026-07-25)

Agent-plan scope: GCS notifications + signBlob signed URLs + Cloud
Tasks selection; gallery fully graduates. Gate C5: full cycle running
the same demo script as F9 against the cloud deployment — local and
cloud validated with one script from here on. C4's verdict is
recorded; notifier-key exists in tako-test-01.

### 0. The headline

The F9 demo script — ticket → direct PUT to storage → pipeline →
warmed feed → shaped download — runs against a cloud URL exactly as it
ran against localhost:8090. The two missing pieces are the direct
transfer itself (SignedPut/SignedGet mint real V4 GCS URLs via
signBlob — no key files, the SA signs as itself) and the event that
follows it (a signed upload bypasses the service, so the runtime can't
emit — GCS's own notifications take over the `photos-uploaded` topic
and C4's emission path is deleted, closing the double-fire risk).
Gallery still changes by zero lines.

### 1. Scope calls needing your decision (before anything else)

1. **Cloud Tasks — DEFER (RECOMMENDED).** No example uses WithDelay
   outside tests (gallery's pipeline is delay-free; journal and shop
   are Stage 3), and the F9 gate script never exercises it. "No
   feature without an example demanding it" says Cloud Tasks waits for
   the Stage 3 app that actually publishes with a delay; the C4 error
   (`WithDelay … needs Cloud Tasks — that arrives with C5`) gets its
   phase pointer reworded to name the future feature instead of C5.
   Alternative: build it now against a synthetic validation — plumbing
   without a consumer, and a second delivery path (Tasks headers, no
   dead-letter topics — semantics would need runtime shims) to keep
   converged for nobody.
2. **Wildcard content-type on signed PUT — platform-honest gap
   (RECOMMENDED: accept).** GCS V4 signed PUT enforces exactly what is
   signed: an exact Content-Type and a size cap
   (x-goog-content-length-range) are HARD-enforced by GCS; a wildcard
   (`image/*`, what gallery uses) cannot be — only POST policy
   documents can express starts-with, and they'd change the client
   contract from `PUT bytes` (pinned by gate F9) to a multipart form.
   Proposal: keep PUT; exact types + MaxSize enforced by GCS at upload
   (415/400 equivalents); wildcard constraints are enforced locally
   but in the cloud fall through to the pipeline's real safety net —
   undecodable bytes already dead-letter permanently (F9 §4) and the
   cron reaps the orphan. The conformance SignedURL chapter grows
   platform params (like QueueParams) making the divergence explicit
   and pinned, not silent. Alternative: POST policy for full fidelity,
   breaking the F9 client contract and both local endpoint + script.

### 2. CLI surface

```
$ cd examples/gallery
$ tako deploy --project tako-test-01
  plan
    …everything C4 built, plus:
    create  bucket notification tako-test-01-gallery-photos → photos-uploaded
    update  run service ×5            (emission env retired)
    update  iam grants topic photos-uploaded
            (publisher: GCS service agent replaces the writers)
    update  iam grants serviceAccount gallery-sa@…
            (self signBlob for SignedPut/SignedGet — from the sign edges)

Security summary additions:
  gallery … roles/iam.serviceAccountTokenCreator on its OWN SA
    [signBlob — mints signed URLs as itself]
  photos-uploaded [publisher: GCS service agent — bucket notifications]
```

- **Demo (the gate)**: `validate/gallery.md` §§2–4 run verbatim with
  `BASE=<cloud url>` instead of localhost — ticket, direct PUT 200
  (straight to storage.googleapis.com), feed `ready: true`, shaped
  download URL 200, real thumb; boom fan-out and junk-bytes Permanent
  land in `tako dlq` instead of `.tako/data/deadletter`. §1 (tako
  test) and §5 (AdvanceCron) stay local by nature; a §6 adds
  deploy/destroy brackets. The script gains the BASE parameterization
  and cloud annotations — one script, both worlds, per the agent plan.
- **Conformance(gcp)**: SignedURL chapter joins with platform params
  (exact-type 415-equivalent, size-cap rejection, expiry, GET
  round-trip; wildcard-at-upload local-only). TestBucketEventEmitGCP
  is REPLACED by a notification-config chapter: Put → GCS notification
  → pull shows the raw GCS payload (the runtime translation is
  unit-pinned).
- **Destroy**: notification configs delete before their bucket
  (reverse order); nothing else changes.

### 3. Semantics being locked

- **Signed URLs = V4 + signBlob, no keys anywhere**: the service's SA
  calls iam signBlob on itself (self tokenCreator grant derived from
  the existing `sign` edges — the C3 placeholder becomes real). PUT
  URLs sign the exact Content-Type when the constraint is exact, and
  x-goog-content-length-range when MaxSize is set; GET URLs are plain
  V4. TTL is the user's ttl argument, capped at GCS's 7 days.
- **Bucket events = GCS notifications, everywhere**: one notification
  config per events bucket (OBJECT_FINALIZE only, JSON_API_V1,
  tako-spec-hash in custom_attributes — content-addressed like
  indexes), publishing to the SAME `<bucket>-uploaded` topic C4
  created. C4's runtime emission and TAKO_BUCKET_EVENTS are DELETED —
  one event source, no double-fire; through-service Puts now also
  arrive via GCS (identical topic, subscribers can't tell). The push
  runtime translates the GCS payload (name/size-string/updated) into
  tako.ObjectInfo so subscriber code sees the exact local shape —
  shape-detected in the envelope unwrap, unit-pinned.
- **Notification configs are created AFTER the GCS service agent's
  publisher grant** (GCS validates publishing at config-creation
  time): the agent (service-<projectNumber>@gs-project-accounts…)
  joins the managed-member set on topic targets, and the deploy's
  grant phases gain a notifications step after pre-run grants.
- **Own-write filtering stays app-side**: makeThumb's `_thumb` write
  now fires a real notification too — the standing `_thumb` suffix
  idiom (F5) already handles it, demonstrated live.
- **Cloud Tasks deferred** (per §1 call 1): WithDelay on gcp keeps
  erroring, with the phase pointer reworded to the delayed-delivery
  feature rather than C5.

### 4. Internals

- `pkg/backends/gcp`: signed.go becomes real (V4 canonical request +
  signBlob REST, no SDK dependency); bucket.go loses the emission
  block; queue.go error text reworded.
- `pkg/provision`: KindBucketNotification (desired from events
  buckets, ordered after topics), GCS-agent grants on events topics,
  self-tokenCreator from sign edges; goldens grow the lines
  (gallery: +1 notification, grants deltas; shortlink unchanged — no
  buckets).
- `pkg/provision/gcp`: notifications applier (list by bucket,
  custom_attributes hash), preflight unchanged (storage API already
  probed; the GCS service agent email derives from ProjectNumber).
- `pkg/runtime`: envelope translation for JSON_API_V1 notification
  payloads; no other changes — PushHandler's contract holds.
- `validate/gallery.md`: BASE parameterization + cloud annotations +
  §6 (deploy/destroy brackets); validate/gallery-cloud.md §3 note
  pointing at the unified script for C5+.
- No SDK surface changes (freeze holds).

### 5. Cloud errors & permissions (per docs/tako-cloud-permissions.md)

- **signBlob 403 at runtime** (grant missing/propagating): surfaces
  through the existing runtime-403 taxonomy; the boot does NOT probe
  signBlob (first SignedPut would) — the known propagation-window
  caveat applies and stays discovered work.
- **Notification config creation 400/403** ("service account does not
  have pubsub.publisher"): grant ordering above makes it unreachable;
  if GCS lags the grant, the applier retries briefly (bounded, like
  index waits) before failing verbatim.
- **Deployer perms added**: storage.buckets.update (notification
  configs ride the storage API; covered by roles/storage.admin already
  probed) — no new preflight lines expected; verified live.
- **Org-policy denials** (e.g. domain-restricted sharing blocking
  allUsers, uniform-access edge cases): surfaced verbatim.

### 6. Not in scope (C5 non-goals)

- Cloud Tasks / WithDelay in the cloud (per §1 call 1 — waits for the
  Stage 3 example that demands it).
- Cloud Scheduler for cron (unchanged from C4); shared cache; metrics
  backend; C6 lifecycle (prune, canary, rollback, --env previews).
- POST policy uploads, resumable uploads, per-object ACLs.
- Signed-URL revocation (TTL is the only expiry).

**Validation**: the unified `validate/gallery.md` against
tako-test-01 (§§2–4 + dlq + destroy), conformance(gcp) incl. the
SignedURL chapter, and the full local suite unchanged.

**STOP — awaiting DX approval (and the two §1 scope calls) before
implementation.**

## C5 — implementation: GCS notifications + signBlob signed URLs; gallery fully graduates (2026-07-25)

Proposal approved with both §1 recommendations: Cloud Tasks deferred
(the WithDelay error now points at the delayed-delivery feature, not a
C-phase), and the wildcard platform gap accepted. One amendment the
implementation forced — recorded first because it widens that gap:

**AMENDMENT — MaxSize is also upload-time-unenforceable on gcp.** The
proposal claimed GCS would hard-enforce MaxSize via
x-goog-content-length-range; that header only binds if the CLIENT
sends it, and a bare `PUT <url>` client (the pinned F9 contract) never
does — signing it would 403 every legitimate upload. So on gcp the
upload-time enforcement set is: identity, expiry, EXACT content type
(signed into the URL; clients send Content-Type naturally). MaxSize
and wildcards both fall through to the pipeline safety net.
SignedURLParams pins all three axes (UploadEvents, EnforcesMaxSize,
WildcardAtUpload — local true/true/true, gcp false/false/false with
exact-type enforcement proven by its own subtest), and the unified
gate script demonstrates the net live: junk bytes arrived VIA a signed
URL, decode failed, one attempt, dead-lettered, cron reaps the orphan.

**Signer (pkg/backends/gcp/signed.go):** V4 canonical
request/string-to-sign built by hand (no SDK), signature minted by
iamcredentials signBlob as the service's own identity (resolved once
from the metadata server; SignerEmail override for harnesses), 7-day
TTL clamp, path-safe key encoding. The C3 grants placeholder became
real: sign edges derive self-tokenCreator on the service's own SA plus
bucket objectAdmin (signed URLs authorize AS the signer at use time).
App buckets now carry a CORS config (browsers execute these URLs — the
point), spec-hashed so existing buckets update.

**Bucket events = GCS notifications everywhere:** C4's runtime
emission and TAKO_BUCKET_EVENTS are deleted; a KindBucketNotification
object per events bucket (OBJECT_FINALIZE, JSON_API_V1, spec-hash in
custom_attributes, immutable → update = delete+recreate) publishes to
the SAME topic. The GCS service agent (gs-project-accounts, derived
from ProjectNumber) replaces the writer SAs as the topic's publisher
and joins the managed-member set; deploy sequences notifications after
the pre-run grants because GCS validates the agent's publish
permission AT CONFIG CREATION (bounded propagation retry in the
applier). The push runtime translates the JSON_API_V1 payload
(name/size-string/updated) into the exact ObjectInfo wire shape local
publishes — unit-pinned; subscriber code cannot tell the backends
apart. Through-service Puts now also arrive as real notifications.

**Conformance:** TestSignedURL runs on gcp with the platform params
(9 subtests: transfer, exact-type enforcement both ways, expiry,
tamper, CORS preflight + PUT, GET round-trip + absent-404; MaxSize ×2
and wildcard skip WITH the reason in the skip text);
TestBucketNotificationGCP replaces the emission chapter (config on a
real bucket → Put → raw GCS payload pulled). The tamper case was
WRONG for every verifier that tolerates trailing garbage — GCS parses
hex up to the first invalid byte, so `url+"x"` verified fine; the
chapter now flips a signature character, which must fail everywhere
(local included, verified). The stale "Phase F" skip texts (and one
C4 mispaste that put TestTopic's text on TestSignedURL) are fixed.

**Validate script unified:** validate/gallery.md is now the F9 AND
C5+ script — BASE parameterization, cloud annotations per section
(dlq/logs instead of on-disk dead letters), §7 cloud brackets. No
cloud-specific surgery beyond BASE, as the agent plan demanded.

**Live cycle (tako-test-01):** conformance(gcp) SignedURL 7 pass/2
declared-skip + notification chapter + Entity/Bucket/TopicGCP all
green (one setup step: signBlob is NOT in roles/owner — the harness's
fatal prints the exact tokenCreator grant for the conf-signer SA, run
once); deploy (index + SA-quota backoff dominated: 8m5s; converged
re-deploy 24s); the F9 §2 flow verbatim against the cloud URL —
ticket with a real V4 URL signed as gallery-sa → direct PUT 200
straight to storage.googleapis.com → GCS notification → pipeline →
564B thumb in 20s → warmed feed → signed GET download 200 from GCS;
junk via signed URL dead-lettered with `decode image: unknown format`
after ONE attempt (the safety net, live); through-service multipart
upload also flowed via notifications (thumb in 5s); tako dlq --ack
drained; destroy listed the notification config, deleted it before
its bucket, purged 3 entities, left the project clean (0 services, 0
topics).

**Two more fixes the live cycle forced:** SA creation tripped IAM's
per-minute quota on destroy→redeploy cycles — the SA applier now
treats 429 as pacing (bounded 20s-step wait, 3m ceiling) instead of
aborting a half-applied deploy; and the notification chapter's first
run hit agent-grant propagation (its retry loop absorbed it on
re-run).

**Discovered work (not done):** the security summary lists only
tako-SA members, so agent grants (GCS publisher, Pub/Sub dead-letter
pair) are invisible there — they are in the plan and grant objects,
but the summary should show them; conf-signer SA + its user
tokenCreator grant persist in the test project (harness setup, like
the conformance index); C4 leftovers stand (secret probe vs API
propagation, dlq peek lease, conf-* namespaces).

**GATE REQUEST — C5: the F9 demo script against the cloud.** Run
`validate/gallery.md` with `BASE=<deployed URL>` end to end on
tako-test-01 (§0 image, deploy + plan review, §§2–4 verbatim, §7
brackets; conformance bar incl. TestSignedURL's declared skips — judge
the MaxSize amendment §above as part of the verdict) and record it in
docs/validation-log.md. C6 (lifecycle: change-class plan, prune,
rename, rollback, canary/promote, --env previews, drift warnings)
follows as Stage 2's closer.

## C6 — DX proposal: lifecycle — change classes, canary/promote/rollback, prune, rename, --env (2026-07-25)

Agent-plan scope: change-class plan, prune/--data, rename, rollback,
canary/promote, --env previews, drift warnings. Gate C6: human walks
the scripted lifecycle (deploy → drift-inject → warn → canary →
promote → rollback → prune → destroy). Spec §4.6a is normative — with
one factual correction it needs (call 1). C5's verdict is recorded.
Stage 2 closes with this slice.

### 0. The headline

The deploy you already trust learns the rest of its life story. Plans
classify every entry (additive / in-place / replace / destructive-
suspect) and the default deploy PROMPTS before anything beyond the
first two (--yes for CI). Orphans stop being forever: `tako prune`
deletes the stateless ones, `tako prune --data` walks the §4.6a
confirmation for the rest. Bad rollouts get undo: `--canary=10` holds
the new code at 10% on public services (async services wait at 0%),
`tako promote` completes, `tako rollback` returns the whole revision
set to the previous pinned digests — instant, no rebuild, git SHAs
printed. And `--env=pr-42` is a whole preview app for the cost of a
prefix.

### 1. Scope calls needing your decision (before anything else)

1. **Rename is a data MIGRATION, not a relabel — spec fix-up + how
   much to ship (RECOMMENDED: detection now, migration deferred).**
   §4.6a says `tako rename photos pics` "relabels the existing
   bucket"; that cannot be true — GCS bucket names are immutable
   identities and tako's physical names embed the logical name
   (`<project>-<app>-<env>-photos`), and a Datastore kind rename means
   copying every entity. A real `tako rename` is therefore
   object-rewrite + entity-copy + delete-old, with confirmations — a
   substantial slice of its own, and the C6 gate walk never exercises
   it. Proposal: C6 ships the DETECTION (deploy plan spots a
   disappeared+appeared same-type pair and says: "resource `photos`
   disappeared and `pics` appeared with the same type — if this is a
   rename, your data does not follow automatically; a migrating tako
   rename is a planned feature, and until then: keep both declared,
   copy, then drop the old"), the spec paragraph gets the honesty
   fix-up PR, and the migrating command becomes its own task when an
   example forces a rename. Alternative: build the full migrating
   rename now (roughly doubles C6).
2. **Canary shape for async services (RECOMMENDED: wait at 0%).**
   Spec: workers/subscribers don't canary (push targets one URL). Two
   coherent shapes: (a) RECOMMENDED — during `--canary=10`, async
   services get their new revision created WITH NO TRAFFIC and keep
   serving the old one; `tako promote` shifts everything (public
   100%, async to latest) — the whole app is either "old + 10% public
   taste" or "new", never a mix of new workers with old APIs; or (b)
   async services jump straight to 100% new during canary — simpler,
   but the async spine runs code the API canary is still auditioning.
3. **Prompt default (RECOMMENDED: prompt, --yes to skip).** The spec
   wants the default deploy interactive when the plan holds anything
   beyond additive/in-place. That changes CI behavior: CI must pass
   `--yes` (or keep using --plan + a second call). Alternative: never
   prompt, print a loud banner — weaker than the spec's word.

### 2. CLI surface

```
$ tako deploy --project tako-test-01
  plan
    ~ update  run service gallery            [in-place: image]
    + create  topic photo-ready              [additive]
    ! update  push subscription gallery-makethumb
              [REPLACE: topic is immutable — delete+create, in-flight
               messages on the old subscription are lost]
    ? orphaned (kept) bucket …-gallery-photos
              [rename suspect: `pics` appeared with the same type —
               data does not follow automatically]
  plan contains 1 replace + 1 rename suspect — continue? [y/N]

$ tako deploy --canary=10 --project …
✓ gallery → 10% new (revision gallery-00007, sha 40b2233) / 90% prior
  gallery-makethumb → new revision staged at 0% (async: no canary)
$ tako promote --project …          # everything to latest, prints SHAs
$ tako rollback --project …         # traffic to the prior revision set
  gallery ← gallery-00006 (sha 7971f30) · gallery-makethumb ← …
  (instant: images and revisions already exist; infra-shape rollback
   stays `git revert` + `tako deploy`)

$ tako prune --project …            # stateless orphans only
  ✗ run service old-api · ✗ topic old-clicks · ✗ service account …
  kept (data): bucket …-photos — run `tako prune --data`
$ tako prune --data --project …     # §4.6a listing with object counts,
                                    # typed app name, then deletion
```

- **--env previews**: `tako deploy --env=pr-42` — every name
  prefixed, Datastore namespace pr-42, disjoint secrets (preflight
  demands `pr-42-notifier-key` with the exact create command — envs
  share NOTHING); `tako destroy --env=pr-42` removes it wholesale.
  Nothing new is built here; C6 validates the claim end to end.
- **Drift**: unchanged mechanics, now part of the gate (console-edit
  a service → next plan says DRIFT and the update overwrites).

### 3. Semantics being locked

- **Change classes** are derived, not annotated: create=additive;
  update=in-place except entries whose diff touches an immutable
  field (subscription topic; index properties — which already
  delete+create as content-addressed) → replace, called out with
  consequences; orphans stay informational, plus the rename-suspect
  line when a same-kind create pairs with an orphan (call 1).
- **Canary/promote/rollback are traffic operations, never rebuilds**:
  digests pin revisions; annotations already carry git SHA + graph
  hash, and the commands print them. Rollback moves traffic to the
  previous revision per service (async included — their push traffic
  follows the split); it does NOT undo infra shape (the plan says so
  when shape has changed since).
- **Prune deletes only labeled orphans, stateless kinds first-class**:
  run services, SAs, topics, subscriptions, notification configs,
  indexes. `--data` extends to buckets (object counts printed) and
  entity kinds (entity counts) behind the typed-name confirmation.
  Grants never need pruning (convergent rewrite, C3).
- **A canary deploy takes the same lease** — no concurrent promote/
  rollback/deploy interleavings.

### 4. Internals

- `pkg/provision`: Entry gains Class (derived in Compute from
  desired-vs-observed spec fields); Render shows it; rename-suspect
  pairing in plan assembly. Goldens grow the column.
- `pkg/provision/gcp`: run.go traffic split support (percent to
  revision, list revisions with annotations), subscriptions replace
  path (delete+create when topic differs, announced); prune reuses
  the destroy deleters behind orphan filtering.
- `cmd/tako`: `promote`, `rollback`, `prune [--data]`, `--canary`,
  `--yes`, the prompt; usage text.
- Spec §4.6a rename paragraph fix-up (call 1).
- No SDK surface changes (freeze holds).

### 5. Cloud errors & permissions (per docs/tako-cloud-permissions.md)

- No new APIs. Traffic operations ride run.services.update (already
  probed); prune uses the existing per-kind delete permissions —
  preflight unchanged.
- `tako promote`/`rollback` with no prior revision: "nothing to
  promote/roll back to — this service has a single revision" (exact
  text pinned).
- `--canary` on an app with no public service: "no public service to
  canary (async services stage at 0% and promote atomically)".
- Concurrent lifecycle ops: the lease text, unchanged.

### 6. Not in scope (C6 non-goals)

- The migrating `tako rename` (per call 1 — its own future task).
- Traffic-split canary for async services (spec v0 stance).
- `overrides:` escape hatch (no example demands it; drift handling
  treats every tako-owned field as tako's).
- Autoscaling-based progressive rollouts, error-rate-gated promotion.
- Cloud Tasks, Scheduler, shared cache, metrics (standing deferrals).

**Validation**: `validate/lifecycle.md` (new): full walk on
tako-test-01 — deploy gallery → console drift-inject → plan warns →
`--canary=10` (public split verified by curl loop hitting both
revisions, async staged at 0%) → promote → rollback (SHAs verified) →
comment out a resource → deploy shows orphan + rename-suspect text →
prune (stateless) and prune --data (counts + typed name) → `--env
pr-42` preview cycle (disjoint secret demanded, deploy, curl, destroy
--env) → destroy. Conformance untouched (no backend changes).

**STOP — awaiting DX approval (and the three §1 scope calls) before
implementation.**

## C6 — implementation: lifecycle — change classes, canary/promote/rollback, prune, --env (2026-07-25)

Proposal approved with all three §1 recommendations (rename =
detection now + spec fix-up, async canary staged at 0%, prompt with
--yes). Stage 2 closes with this slice.

**Plan intelligence (pkg/provision):** entries carry a §4.6a class —
creates `+` additive, updates `~` in-place, `!` REPLACE when the diff
touches an immutable field (push-subscription topic, detected via the
new Observed.Detail live-field channel) with the consequence spelled
out; orphans `?` pair with same-kind creates as rename suspects (`↳
data does not follow automatically`, buckets adding the
copy-then-drop guidance). NeedsConfirm() drives the default deploy's
prompt (replaces/suspects only; --yes for CI; --plan never prompts).
Drift semantics fixed while walking the gate: a drifted hash-matching
object used to be flagged on a SKIP, which never applies — the
warning promised an overwrite it could not deliver; drifted objects
now plan as in-place updates, so drift-inject → warn → overwrite →
clean converges (pinned in TestReplaceClassAndRenameSuspects and the
existing drift test).

**Traffic operations (pkg/provision/gcp/traffic.go):** revisions
(newest-first, ready-state, git SHA from template annotations — the
version stamps now ride the template so every revision permanently
names its commit), traffic read/set, and the three verbs: --canary=N
splits public services N/(100-N) between latest and the revision
serving now while async services stage their new revision at 0%
(whole-app "old + a taste" per the approved call); tako promote sends
everything to latest; tako rollback returns each service to its prior
ready revision — instant, per-service narration with SHAs,
single-revision services say so, infra shape stays git revert +
deploy. v2 quirk found live: trafficStatuses carries NO revision name
for LATEST-type entries, so the first canary silently deployed at
100% — serving() now falls back to the newest ready revision.

**Prune (cmd/tako/lifecycle.go):** stateless orphans (subscriptions,
run services, pull subs, topics, indexes, SAs) delete in reverse
reconcile order; converges to "no orphans — nothing to prune".
--data walks orphaned buckets (object counts, capped 1000+) and
undeclared datastore kinds (aggregation counts via the new
KindsInNamespace/CountKind) behind the typed-name confirmation.
Subscription topic changes now APPLY as announced delete+create
(replace class) instead of failing the patch.

**Two env-isolation bugs the pr-42 walk caught — the gate's whole
point:**
1. The shared artifact repo was env-labeled, so a preview env planned
   a create that 409'd; making its observation env-agnostic then let
   `destroy --env=pr-42` DELETE prod's repo — and the framework lease
   bucket went with it. Env-scoped destroys now keep project-shared
   machinery (repo, lease bucket) and say so.
2. Far worse: grant revocation keyed managed members on the `-sa`
   name pattern, which matches EVERY env's identities on the shared
   project policy — pr-42's destroy stripped prod's datastore.user
   and prod served 500s until re-deployed. Managed members are now
   the exact marker-derived email set of THIS app+env
   (ServiceAccounts.ManagedEmails; refreshed post-SA-creation), so no
   env can revoke another's grants. ManagedMember (the naming
   invariant) remains pinned by tests but no longer gates rewriting.

**Also:** --env=/--project=/--region= equals-forms parse (the spec
writes --env=pr-42; only the space form worked); the §4.6a rename
paragraph now tells the truth (physical names embed logical names —
GCS cannot relabel; a migrating tako rename is a planned feature);
usage text covers promote/rollback/prune. An amusing determinism
catch: the walk's scratch topic (declared, never used) was dead-code-
eliminated by the linker — byte-identical binaries, unchanged
digests, zero run-service updates, exactly as a content-addressed
system should behave.

**Live walk (tako-test-01, validate/lifecycle.md):** deploy (classified
plan, no ceremony) → console memory edit → `~ update … ← DRIFT` →
deploy overwrote → clean plan → scratch rename → `? orphaned … ↳
rename suspect` + interactive prompt answered y → --canary=10
(gallery 90% pinned / 10% latest; all four async services pinned 100%
old with new staged) → promote (100% latest, sha c324fb8 printed) →
rollback (five services pinned back instantly, SHAs shown) → prune
(✗ topic scratch, ✗ topic scratch2 → "no orphans") → --env=pr-42
(preflight demanded pr-42-notifier-key with the exact command; 45
creates all prefixed; disjoint bucket/namespace/secret; feed empty
while prod's served) → destroy --env=pr-42 (env-scoped, after the
fixes) → prod healed post-grant-restore (~50s propagation) → destroy
→ clean project (0 services, 0 topics). Gallery's code restored to
byte-identical; full local suite + all examples green.

**Discovered work (not done):** the migrating tako rename (per the
approved call); canary percent surviving a same-digest re-deploy is
undefined-but-harmless (skips don't touch traffic); C5 leftovers
stand (security summary hides agent members, conf-signer persists).

**GATE REQUEST — C6: the scripted lifecycle walk.** Run
`validate/lifecycle.md` end to end on tako-test-01 (§§1–9: classified
plans, drift repair, canary through the prompt, promote, rollback,
rename suspects, both prunes, the pr-42 preview cycle — judge
especially that pr-42's create/destroy leaves prod untouched, the two
env-isolation fixes above being this slice's hardest-won lesson) and
record the verdict in docs/validation-log.md. Stage 2 closes on a
pass; Stage 3 (web, auth, services) begins with its decomposition
per the agent plan.

## W1 — DX proposal: Site — embedded static, caching headers, WithBuild/WithDevProxy (2026-07-25)

Stage 3 opens (decomposition W1–W11 written into tako-agent-plan.md —
review it with this proposal; C6's verdict closed Stage 2). Spec §2.11
is normative. The API surface is FROZEN: `tako.Site`, `SPA()`,
`WithBuild`, `WithDevProxy`, `WithRedirect` all exist as F10-locked
symbols with phase-pointer behavior — W1 makes them true, adds none.

### 0. The headline

A website is one declaration:

```go
var site = tako.Site("./web")
```

`tako dev` serves it with correct headers; `tako deploy` ships it INSIDE
the service binary — no bucket, no LB, no CDN bill, one URL. Generators
plug in without tako knowing them: `tako.Site("./web",
tako.WithBuild("hugo --minify", "public"))` runs the build on the
deploying machine at deploy/dev time and serves the output;
`tako.WithDevProxy("http://localhost:1313")` swaps in the generator's
own dev server for HMR during tako dev. And `examples/journal` is born
as W1's demand: a static shell that W2–W5 grow into the htmx +
markdown + auth showcase gated at W6.

### 1. Scope calls needing your decision (before anything else)

1. **journal grows feature-by-feature from W1 (RECOMMENDED — as
   decomposed).** The alternative (a scratch example per W-slice,
   journal assembled only at W6) makes each gate cheaper but loses the
   compounding story that made gallery's gates convincing.
2. **WithDomain/WithCDN stay phase-pointer stubs (RECOMMENDED).**
   Domain mapping needs DNS the test project doesn't own, and §2.11
   frames CDN as the when-scale-justifies upgrade; neither is needed
   for journal or shop. They become their own slice when a deployment
   demands a custom domain. (Both symbols already exist frozen; only
   their not-yet text changes to name the future slice.)

### 2. CLI/user surface

```
$ cd examples/journal && tako dev
tako dev · journal
  site      ./web (12 files, 2 fingerprinted) → /
  …
$ curl -sI localhost:8090/            | grep -iE 'cache-control|etag'
Cache-Control: no-cache               # HTML revalidates
ETag: "…"
$ curl -sI localhost:8090/assets/app.3f2a1c.css | grep -i cache-control
Cache-Control: public, max-age=31536000, immutable   # fingerprinted
```

- **Deploy**: no new plan objects — the site rides the service image
  (`analyzing` line gains `1 site`). Build narration:
  `⛅ site      hugo --minify → public (37 files)` before images.
- **WithBuild failures are build errors**: the command's stderr
  verbatim, exit before any plan
  (`site build failed: hugo --minify: exit 1 — <stderr tail>`).
- **WithDevProxy**: tako dev proxies non-API paths to the URL and says
  so (`site      proxied → http://localhost:1313 (WithDevProxy)`);
  deploy IGNORES the proxy and requires WithBuild or a plain dir
  (erroring with exact text if the dir is missing/empty).
- **SPA()**: unknown non-API paths serve index.html 200. Without it,
  404. `WithRedirect(from, to)`: 308.
- **Routing precedence, locked**: declared API/Page routes win; the
  site serves everything else. `/_tako/*` stays the runtime's.

### 3. Semantics being locked

- **Headers**: fingerprinted assets (name matches `-[0-9a-f]{6,}.` or
  `.[0-9a-f]{8,}.`) → `public, max-age=31536000, immutable`;
  everything else `no-cache`; strong ETags (content hash) on all;
  304 on If-None-Match. Security headers on document responses:
  `Content-Security-Policy-Report-Only: default-src 'self'` (report-
  only per spec), `X-Content-Type-Options: nosniff`,
  `Referrer-Policy: strict-origin-when-cross-origin`. HSTS waits for
  custom domains (their slice).
- **Embedding is the v0 deployment**: at build time the site (built
  output when WithBuild) is COPIED into the generated service package
  and served via go:embed — the binary is the site; digest changes
  when content changes, byte-identical local/cloud, rollback rolls
  the site back too (revisions pin it). Cloud Run's 32MB response cap
  noted in docs; huge assets are a WithCDN-slice problem.
- **dev = deploy semantics minus proxy**: same header code path (the
  conformance-style bar: one handler, both modes); WithBuild re-runs
  on watched changes in dev with narration.
- **Analyzer**: `site` resource in the graph (dir, build cmd, proxy,
  spa, redirects — summary line + gen graph); one Site per service
  (two = analysis error with exact text); dir must exist at analyze
  time unless WithBuild creates it (then checked post-build).
- **No new plan objects, no IAM, no APIs** — the deliberate v0 shape.

### 4. Internals

- `pkg/analyzer`: site constructor extraction (options → Resource
  fields), error texts under test.
- `pkg/build` + `pkg/codegen`: run WithBuild, copy output into
  `.tako/gen/<svc>/site/`, generated main mounts
  `runtime.SiteHandler(embedFS, options…)`; dev server bypasses embed
  and serves the dir/proxy directly (same handler code, fs.FS
  parameter).
- `pkg/runtime`: SiteHandler (fs.FS + options): ETag/immutable/
  security headers, SPA fallback, redirects; `hooks.Site` wiring for
  `site.Render`/`Fragment`/`Markdown` STAYS phase-pointered (W2/W4).
- `examples/journal`: `web/` static shell + go.mod + first tests
  (headers, SPA, redirect — httptest against the runtime handler).
- No SDK surface changes (freeze holds; not-yet texts for
  Render/Fragment/Markdown/domain/CDN updated to name their slices).

### 5. Cloud errors & permissions

None new: no APIs, no grants, no plan objects. The failure surface is
build-time (WithBuild exit/status, missing dir, output dir empty —
exact texts pinned) and size (image growth is visible in the ⛅ site
line; the 32MB response cap documented).

### 6. Not in scope (W1 non-goals)

- Render/Fragment/Markdown (W2–W4), Auth (W5), WithDomain/WithCDN
  (their own future slice), Brotli/gzip pre-compression (later,
  additive), bring-your-own-runtime SSR (spec Part 5 boundary).

**Validation**: `validate/site.md` — dev headers judged by curl, Hugo
variant (`WithBuild("hugo --minify", "public")` — requires hugo on the
validating machine, stated), WithDevProxy HMR demo, then the full
cycle: deploy journal, identical curl judgments against the cloud URL,
SPA + redirect + 304 behavior, destroy.

**STOP — awaiting DX approval (the two §1 scope calls, and the
Stage 3 decomposition in tako-agent-plan.md) before implementation.**

## W1 — implementation: Site — embedded static, caching headers, WithBuild/WithDevProxy (2026-07-25)

Proposal approved with both §1 recommendations (journal grows from W1;
WithDomain/WithCDN deferred with named-slice not-yet texts) and the
Stage 3 decomposition. Stage 3's first slice.

**Analyzer:** tako.Site extracts for real (the Phase-G not-yet gate
falls; Page/Auth keep theirs, retexted to W2/W5). Resource kind
"site" carries Dir/Build/BuildOut/DevProxy/SPA/Redirects — paths
normalized module-relative so codegen and the build runner share one
coordinate system. Errors under golden test: non-constant args,
invalid options, not-yet options naming their slice
(WithDomain/WithCDN/WithTrustedHTML), missing dir (unless WithBuild
owns creating it), one-site-per-package. A package with a Site and NO
handlers still yields its app service — the pure-static site is the
minimal website (tako dev's app-service picker learned the same).
journal's graph + summary are goldened; the analyzing line counts
sites.

**Runtime (SiteHandler):** fs.FS + options. Strong content-hash ETags
everywhere with 304 on If-None-Match; fingerprinted names
(hex run ≥6 before the extension) → public, max-age=31536000,
immutable; everything else no-cache; documents (and only documents)
get CSP-Report-Only default-src 'self', nosniff, and
strict-origin-when-cross-origin; pretty URLs (about →
about/index.html); SPA fallback when declared, else 404; 308
redirects; WithDevProxy reverse-proxies in local mode only. Explicit
content types (nosniff means they must be right). Unit-tested over
fstest.MapFS; journal's example tests hit the same handler over
os.DirFS — the parity bar is one code path.

**Codegen/build:** the site is staged into .tako/gen/<svc>/site and
embedded via go:embed all: — the binary IS the deployment, so digests
move when content moves and rollback rolls the site back. Site-only
services generate a main with no app import (nothing to reference —
constructors are side-effect-free records). WithBuild runs BEFORE
Generate in deploy, build, and every dev cycle (⛅ site narration;
failure = the command's stderr tail; success with a missing/empty
output dir = its own text); an unbuilt output at generate time stages
a placeholder so go:embed still compiles and says why. tako test note:
WithBuild does not run there (journal W1 has none; revisit when a
generator-using example gates).

**Live validation (validate/site.md):** dev serving judged by curl
(all §2.11 headers, 308, 304, pretty URLs, 404-without-SPA); the REAL
hugo variant ran (hugo installed here): `⛅ site hugo --minify -s
hugosrc -d ../dist → dist (6 files)` staged and embedded in a
scratch module, and both failure texts fired live (broken flag → the
site-build-failed wrapper with hugo's stderr; `true` with an unwritten
output dir → the empty-output text). Full cycle: journal deployed to
tako-test-01 in 36s with the SMALLEST plan since hello (repo, SA, run
service, grants — zero new object kinds, no IAM, no APIs; security
summary: "no resource grants; public ingress"); the cloud answers were
byte-identical to local — the SAME ETag ("57fe14afb42ff075") for the
same content, identical cache-control split, 308/304/404 all matching;
destroy left the project clean.

**Discovered work (not done):** tako test should run WithBuild when a
generator-using example needs tested builds; Boot() is emitted for
site-only services because the site counts as a resource (harmless —
wires backends nobody calls; drop when codegen learns resource kinds
that need no boot); pre-compressed (brotli/gzip) asset serving is a
later additive.

**GATE REQUEST — W1: the site story.** Run `validate/site.md` on
tako-test-01 (§1 local curl judgments, §2 WithBuild with hugo — it is
installed — including both failure texts, §3 WithDevProxy against
hugo server, §4 the full cycle judging local/cloud byte-parity by
ETag) and record the verdict in docs/validation-log.md. W2
(Pages, forms, CSRF; journal gains authored-post pages) follows.

## W2 — DX proposal: Pages, forms, CSRF — journal gains authored posts (2026-07-25)

Gate batching is now in force (CLAUDE.md amendment): W2 is the first
milestone gate, absorbing W1 — `validate/journal-web.md` will cover
the whole server-rendered story. Spec §2.11 + Part 3b are normative.
Frozen surface made true this slice: `tako.Page`, `tako.HTML`,
`tako.Redirect`, `site.Render`. Nothing added.

### 0. The headline

The website gets its dynamic half, in the same declarative shape as
everything else:

```go
var (
    posts = tako.Entity[Post]("posts")
    site  = tako.Site("./web", …)

    Home  = tako.Page("GET /", home)
    Read  = tako.Page("GET /p/{id}", read)
    Write = tako.Page("GET /write", writeForm)
    Save  = tako.Page("POST /p", save)
)

func read(ctx tako.Ctx, req ReadReq) (tako.HTML, error) {
    p, err := posts.Get(ctx, req.ID)
    if tako.IsNotFound(err) { return tako.HTML{}, tako.NotFound() }
    return site.Render(ctx, "post.html", p)
}

func save(ctx tako.Ctx, req SaveReq) (tako.HTML, error) {   // Title/Body via form: tags
    p := Post{ID: tako.NewID(), Title: req.Title, Body: req.Body}
    if err := posts.Put(ctx, p); err != nil { return tako.HTML{}, err }
    return tako.Redirect("/p/" + p.ID)                       // POST-redirect-GET
}
```

Templates live in `web/templates/` (never served as static files),
render inside `layout.html`, and the sharp edge is pre-dulled: every
unsafe-method Page route REQUIRES a CSRF token the shim minted — the
template just says `{{ csrf }}`. journal's home becomes a rendered
post list; /write is a browser form; about stays static. No htmx yet
(W3), body renders escaped (markdown is W4), writes are open (auth is
W5).

### 1. Scope calls needing your decision (before anything else)

1. **Redirect status = 303 See Other (RECOMMENDED).** The root doc
   comment says 302; 303 is the status MADE for POST-redirect-GET
   (a 302 answer to POST is only get-converted by browser
   convention). Behavior, not signature — the doc comment gets the
   fix. Alternative: keep 302 verbatim.
2. **Page errors render plain in W2 (RECOMMENDED).** NotFound → 404
   text page, decode failures → 400, handler errors → 500 with the
   request id — correct, unstyled. Styled error templates
   (error.html) become their own small slice when journal wants
   branded 404s. Alternative: error templates now.

### 2. User surface being locked

- **Routing**: Page routes register beside API routes on the same
  mux; declared routes beat the site (more specific patterns); the
  site keeps everything else. `GET /` as a Page shadows the static
  index.html (journal deletes it in favor of `home.html`).
- **Binding**: identical to API — `path:`/`query:` tags, plus `form:`
  from urlencoded/multipart bodies on unsafe methods. Same decode
  error UX (400 with field naming).
- **Templates**: `site.Render(ctx, "post.html", data)` parses
  `web/templates/layout.html` + the named file; the page fills
  layout's `{{ block "content" . }}`. Template funcs in W2:
  `{{ csrf }}` (hidden input; also exposed as a meta tag by layout
  convention for fetch callers) — `{{ markdown }}` exists but errors
  with the W4 pointer. Missing template/misuse → 500 with the
  template error verbatim (dev iterates on these).
- **CSRF, automatic**: the shim mints a per-browser token
  (double-submit cookie, `__tako_csrf`, SameSite=Lax; binds to the
  session when W5 lands) and REJECTS unsafe-method Page requests
  lacking the matching `_csrf` field or `X-CSRF-Token` header:
  403 `CSRF token missing or invalid — forms need {{ csrf }}`.
  API routes are untouched (no cookies, no ambient authority).
- **`tako.Redirect(target)`**: 303 + Location (htmx dual-mode W3).
- **Headers**: rendered pages carry the same document headers the
  static site does (no-cache, CSP-Report-Only, nosniff) — one
  policy, both halves.

### 3. Internals

- `pkg/analyzer`: "page" handler kind extracts like API (route,
  Req bindings, HTML result); pages join the app service; edges scan
  covers them (posts entity → datastore grants derive unchanged).
  Goldens: journal graph grows; error fixtures for Page misuse.
- `pkg/codegen`: Page routes wrap in runtime.PageHandler (decode →
  call → render HTML/redirect/error), CSRF middleware on the page
  mux; `{{ csrf }}`/template wiring registered from the embedded
  site FS (templates/ subtree), which SiteHandler now refuses to
  serve statically.
- `pkg/runtime`: PageHandler, CSRF mint/verify, template cache
  (parse once per process; dev restarts per cycle), hooks.Site
  implementation behind site.Render.
- `examples/journal`: Post entity, four pages, layout + home/post/
  write templates; tests: form round-trip, CSRF rejection (exact
  403 text), PRG Location, 404 text, template escaping of a
  script-tag body (the pre-W4 safety judgment).
- No SDK surface changes (freeze holds; Render/Redirect doc
  comments updated to shipped behavior).

### 4. Cloud errors & permissions

None new: pages are handlers in the existing service; grants derive
from their entity edges exactly as API handlers' do (journal gains
project datastore.user — its first grant). Datastore joins journal's
preflight via the existing Needs derivation.

### 5. Not in scope (W2 non-goals)

- htmx/fragments/IsHTMX/HX-Redirect (W3); markdown rendering (W4);
  auth + WithAuth enforcement on Save (W5 — writes are open until
  then, stated in journal's README-comment); styled error pages
  (per call 2); file-upload form fields on pages (Blob binding
  stays API-side until an example needs a page upload).

**Validation**: `validate/journal-web.md` (NEW — the W2 MILESTONE
gate script, absorbing validate/site.md): §1 the static story
condensed (headers, ETag parity local/cloud), §2 pages + forms
(write → PRG → read round-trip, binding errors), §3 CSRF (curl
without token → the 403 text; with → 303), §4 escaping (script-tag
body inert), §5 full cycle on tako-test-01 with the same judgments
against the cloud URL, §6 destroy.

**STOP — awaiting DX approval (and the two §1 scope calls) before
implementation.**

## W2 — implementation: Pages, forms, CSRF — journal has authored posts (2026-07-25)

Proposal approved with both §1 recommendations (Redirect = 303 See
Other with the doc-comment fix; plain error pages). This completes the
W2 MILESTONE scope — the gate request below covers W1 + W2.

**SDK made true (frozen surface, no additions):** `tako.Page` extracts
and serves; `tako.HTML` carries markup-or-redirect (opened for the
runtime via a hooks bridge, nothing exported); `tako.Redirect` returns
303 intent; `site.Render` renders through hooks. Fragment/Markdown
now return their W3/W4 pointers as errors instead of panicking.

**Analyzer:** extractAPI generalized into extractRoute — Page shares
API's whole route/tagged-Req machinery plus one page rule: untagged
non-form fields error ("pages receive browser forms, not JSON
bodies", tako/page-binding). Pages join the app service and the edge
scan (journal's posts entity derives its grant like any API's). The
Page/Auth not-yet gate is now Auth-only.

**Runtime:** RegisterSiteTemplates parses web/templates once per
process (layout.html the shell, pages fill its "content" block; parse
errors surface at render naming the template); render clones per
request (Funcs mutate — requests race otherwise); funcs: {{ csrf }}
(hidden input), {{ csrfToken }} (the layout's meta tag),
{{ markdown }} erroring with the W4 pointer. PageHandler: decode →
CSRF (double-submit __tako_csrf cookie, SameSite=Lax; unsafe methods
require the _csrf field or X-CSRF-Token header; constant-time
compare; exact 403 text pinned) → invoke → 303 on redirect, else the
document with the SAME header policy as the static half. Errors
plain: explicit statuses as-is, else opaque 500 with the request id.
SiteHandler refuses templates/ (render input, never an asset).

**Two routing/harness bugs caught by the smoke, fixed:** a declared
"GET /" is THE home page, but to ServeMux it is the GET catch-all —
it swallowed every asset and template request away from the site;
codegen now emits the exact-root pattern "GET /{$}" for
root-path routes. (And a zombie dev process from the W1 smoke had
been answering the port — the 405s that first looked like a routing
bug were a stale binary; killed by port, everything held.)

**journal:** Post entity (Created index), Home/Read/Write/Save pages,
layout + home/post/write templates, static index retired (home is
rendered; about stays static). Example tests pin: the write → 303 →
read → home-lists round-trip, the exact CSRF 403 (tokenless AND
cookie-less first contact), script-tag bodies rendering as text
(&lt;script&gt; — the pre-W4 floor), unknown post 404, and the W1
static-half headers on /about.

**Live cycle (tako-test-01):** deploy 32s — journal's FIRST grant
appeared exactly as derived (`~ update iam grants project`:
datastore.user for journal-sa from the posts edges; preflight grew
datastore; security summary one honest line). After the known ~70s
IAM window: CSRF-less POST 403'd, the tokened write PRG'd 303 to
/p/yco2…, the post rendered inside the layout, home listed it, the
XSS body arrived escaped, the static half served with strong ETags.
Destroy listed `datastore kinds posts` under DATA and purged 2
entities; clean project; full local sweep green.

**Discovered work (not done):** W1 leftovers stand (tako test skips
WithBuild; Boot for site-only services); {{ csrf }} token pair should
bind to the session at W5; TestApp.Call has no Page ergonomics yet
(the example harness mirrors the generated main instead — fine until
journal's app_test wants user-style page tests).

**GATE REQUEST — W2 MILESTONE: the server-rendered website (closes
W1 + W2).** Run `validate/journal-web.md` end to end on tako-test-01:
§1 the static half (headers/ETag/304/308, templates/ blocked), §2
pages + forms in the browser (PRG — refresh must not resubmit), §3
both CSRF rejections with the exact text, §4 the script-tag body
inert, §5 the full cycle with the first-grant judgment and ETag
parity, §6 destroy naming the posts kind. Record ONE verdict in
docs/validation-log.md for the milestone (W1's deferred row points at
it). W3 (fragments + htmx — journal goes interactive) follows.

## W2 milestone follow-ups: conformance namespace hygiene (2026-07-26)

The user's gate run surfaced the standing C3 discovered-work item for
real: ~100 conf-<hex> namespaces buried journal's prod namespace in
the Datastore console dropdown. Done, per instruction: (1) one-time
purge of tako-test-01 — 306 fixture entities across the conf-*
namespaces (empty namespaces age out of the console's metadata
listing); provision/gcp gained Namespaces() (the __namespace__
pseudo-kind — destroy/prune can use it later). (2) conformance's
gcpClient now registers a t.Cleanup that purges the chapter's
namespace (kindless __key__ query → batched deletes, best-effort);
verified live — the full 18-chapter Entity run now leaves ZERO
entities behind. The conf-* discovered-work item is closed. Also
recorded in the W2 verdict: the security summary should flag mutating
routes without WithAuth on public services (lands with W5, where
WithAuth becomes enforceable).

## W3 — DX proposal: Fragments + htmx — journal goes interactive (2026-07-26)

The W2 milestone is recorded; W3–W5 validate agent-side per slice and
gate together at W6 (journal complete). Spec §2.11's hypermedia
paragraph + Part 3b are normative. Frozen surface made true:
`site.Fragment`, `ctx.IsHTMX` (F10 fence: request-shape probe only),
`tako.Redirect`'s dual mode. Nothing added.

### 0. The headline

One handler serves both worlds — the spec's centerpiece pattern:

```go
func save(ctx tako.Ctx, req SaveReq) (tako.HTML, error) {
    p := Post{…}
    if err := posts.Put(ctx, p); err != nil { return tako.HTML{}, err }
    if ctx.IsHTMX() {
        return site.Fragment(ctx, "post-card.html", p)  // swapped into the list
    }
    return tako.Redirect("/p/" + p.ID)                  // no-JS fallback
}
```

journal's home grows an inline write form (`hx-post="/p"
hx-target="#posts" hx-swap="afterbegin"`): with JS, a new post-card
appears without navigation; without JS, the same form posts plainly
and the same handler answers 303 — progressive enhancement with zero
branching beyond the IsHTMX line. htmx POSTs pass CSRF with no
configuration: layout gains `hx-headers` reading the existing meta
tag. And Redirect becomes dual-mode: under htmx it answers 200 +
`HX-Redirect` (htmx cannot follow a 303 into a swap), plain requests
keep the 303.

### 1. Scope calls needing your decision (before anything else)

1. **htmx is VENDORED into journal (RECOMMENDED).** A pinned
   htmx.min.js in web/assets, fingerprinted, served like any asset —
   the spec's "no client framework beyond htmx as a static asset",
   and no CDN (the site stays self-contained; CSP default-src 'self'
   keeps holding). Alternative: CDN script tag — breaks CSP and
   self-containment.
2. **The Published toggle joins NOW (RECOMMENDED).** Part 3b's
   `publish` handler (fragment-swaps just the button) is the second
   fragment pattern the W6 demo runs; Post gains `Published bool`
   (indexed), home lists published posts only, /write's flow
   publishes immediately (drafts are a UX nicety journal doesn't
   need), the toggle sits on the post page. Open until W5's auth,
   like every write — same documented gap. Alternative: defer to W6
   assembly and demo only the create fragment here.

### 2. Semantics being locked

- **IsHTMX** = the `HX-Request: true` header, nothing else (the F10
  fence: a request-shape probe, never a rendering-mode switch the
  framework acts on by itself).
- **site.Fragment** renders the named template WITHOUT the layout —
  same template set, same funcs ({{ csrf }} works inside fragments),
  same escaping. Fragment responses carry no-cache + nosniff but NOT
  the document CSP (they are partials injected into a page that has
  one).
- **Redirect, dual-mode**: plain → 303 + Location; htmx (HX-Request)
  → 200 + HX-Redirect (full-page client navigation). One return, both
  worlds, per the frozen doc comment.
- **CSRF via hx-headers**: layout's <body> gains
  `hx-headers='{"X-CSRF-Token": "<token>"}'` from the existing
  csrfToken func — every htmx request passes the header the W2
  verifier already accepts. No new mechanism.
- **PageHandler** sets the ctx flag from the header; no other change.

### 3. Internals

- `pkg/runtime`: requestCtx gains isHTMX (set in PageHandler);
  fragment render path (skip layout: execute the named template
  directly); Redirect branch on the flag. hooks bridge grows
  RenderFragment.
- Root: site.Fragment wires through hooks (W3 pointer dies);
  Redirect doc comment already says HX-Redirect.
- `examples/journal`: htmx.min.js vendored (pinned 2.x, fingerprinted
  name); home gains the inline form + #posts list of post-card
  fragments; post page gains the publish toggle; Save/Publish serve
  both worlds. Tests: HX-Request save → fragment (no <html>, has the
  card), plain save → 303 (the W2 test keeps passing untouched),
  HX-Request redirect path → 200 + HX-Redirect header, toggle swap,
  CSRF via X-CSRF-Token header (already covered), fragment escaping.
- No SDK surface changes; no cloud surface changes (no new grants,
  objects, or APIs — W3 is pure request-path).

### 4. Not in scope (W3 non-goals)

- Markdown (W4 — bodies still escape); auth (W5 — writes and the
  toggle stay open, documented); hx-boost/SSE/websockets (nothing in
  the spec asks); styled error fragments.

**Validation** (agent-side; the human gate is W6): journal tests +
a scripted curl walk (HX-Request vs plain against the same routes)
+ the full cycle on tako-test-01 — transcript in the task log.

**STOP — awaiting DX approval (and the two §1 scope calls) before
implementation.**

## W3 — implementation: Fragments + htmx — journal is interactive (2026-07-26)

Proposal approved with both §1 recommendations (htmx vendored; the
Published toggle now). Agent-validated per the batching; gates at W6.

**Runtime/SDK (frozen surface made true):** PageHandler sets IsHTMX
from the HX-Request header (the F10 fence holds — a request-shape
probe the framework never acts on itself); tako.Redirect is dual-mode
(plain → 303, htmx → 200 + HX-Redirect, pinned by a harness route
that always redirects); site.Fragment renders layoutless through the
same template set with the same funcs and escaping; fragment
responses skip the document-only CSP/referrer headers (partials
inject into a page that has them). HTML carries a fragment bit
through the hooks bridge.

**Template registry grew a page/partial split** — the real design
work of the slice: pages define the layout's "content" block and
would collide in one set, so each still clones; PARTIALS (no content
block — post-card.html, publish-toggle.html) parse into the shared
base every page sees, so {{ template "post-card.html" . }} works from
any page AND site.Fragment renders the same file as an htmx response.
One template, both uses — journal's home literally ranges the same
post-card partial that save returns as a fragment. (Found by the W2
escaping test breaking: post.html's toggle reference didn't exist in
its isolated clone.)

**journal:** htmx 2.0.4 vendored (50KB, content-hash-named
htmx.min.e209dda5.js — the first name, htmx.e209dda5.min.js, missed
the immutable regex because the hash must precede the FINAL
extension; caught by the smoke, renamed); Post gains Published
(indexed — the home query Where(Published)+OrderBy(-Created) derives
the composite `posts (published, -created)`, journal's first);
layout gains the deferred script tag + body hx-headers carrying the
CSRF token (zero per-form wiring); home is an inline hx-post form
swapping post-cards into #posts with a no-JS fallback to the same
handler's 303; the post page carries the publish toggle that swaps
just its button. Tests: fragment-vs-303 from one handler, HX-Redirect
contract, toggle actually flips across two swaps, fragments escape
like pages, all W2 tests untouched and green.

**Live cycle (tako-test-01):** deploy created the composite index and
built it; after the usual ~60s IAM window the htmx save returned the
post-card fragment from the cloud, the plain save 303'd, the toggle
swapped its button, home listed via the composite query, the vendored
htmx served immutable; destroy clean. Full local sweep green.

**Discovered work (not done):** standing items unchanged; new: the
partial/page classifier is a regex over the source ({{ define
"content" }}) — honest but implicit; if it ever misclassifies a real
site, an explicit partials/ subdirectory convention is the fix.

W3 complete agent-side. W4 (markdown, sanitized by default) proposal
next; human gate at W6.

## W4 — DX proposal: Markdown, sanitized by default (2026-07-26)

Spec §2.11's markdown paragraph + settled decision #13 are normative
(goldmark + bluemonday, sanitized default, WithTrustedHTML the
explicit opt-out — the libraries and posture are settled, not scope
calls). Frozen surface made true: `site.Markdown`, `{{ markdown }}`,
`tako.WithTrustedHTML`. Gates at W6.

### 0. The headline

journal's post page changes one template line:

```html
<article class="body">{{ markdown .Body }}</article>
```

and author-supplied markdown renders as real HTML — headings, links,
code blocks — with the XSS door closed by default: goldmark renders,
bluemonday sanitizes, and a `<script>` or `onclick` in a post body
simply vanishes from the output. `site.Markdown(ctx, src)` is the
same pipeline for handler code. Trusted content is an explicit,
site-level opt-out — `tako.Site(dir, tako.WithTrustedHTML())` — that
journal deliberately does NOT take (its authors are W5-authenticated
strangers until proven otherwise).

### 1. Scope calls needing your decision (before anything else)

1. **Sanitizer = bluemonday UGCPolicy (RECOMMENDED).** "Strict
   policy" in spirit: UGCPolicy is bluemonday's
   user-generated-content profile — it allows exactly the elements
   markdown produces (p/em/strong/a/code/pre/lists/headings/img),
   forces rel="nofollow" on links, and strips everything else
   (scripts, handlers, styles, iframes). The literal StrictPolicy()
   strips ALL tags, which would make markdown rendering pointless.
   Alternative: hand-rolled allowlist — more work, same result,
   drift risk.
2. **The opt-out validates OUTSIDE journal (RECOMMENDED).**
   WithTrustedHTML skips sanitization (goldmark with raw-HTML
   passthrough) — for admin-authored content the app owner trusts.
   journal never takes it, so the opt-out is pinned by unit tests +
   an analyzer fixture (option accepted on Site, recorded in the
   graph), not by the example. The W6 gate judges journal's DEFAULT:
   a script-tag post renders inert. Alternative: a second demo app or
   a journal flag — scope creep for one boolean.

### 2. Semantics being locked

- **Pipeline**: goldmark (CommonMark + GFM tables/strikethrough/
  autolinks, hard line breaks off) → bluemonday UGCPolicy. With
  WithTrustedHTML: goldmark with unsafe raw HTML passed through,
  no bluemonday. Fenced code renders as <pre><code class="language-…">
  (classes allowed for future highlighting).
- **`{{ markdown .Body }}`**: template func, sanitized per the SITE's
  declared posture; returns template.HTML (already-safe by
  construction — the sanitizer is the guarantee).
- **`site.Markdown(ctx, src)`**: same pipeline, returns tako.HTML
  usable as a fragment (htmx preview endpoints).
- **Analyzer**: WithTrustedHTML accepted on Site, recorded on the
  resource (graph + summary show `trusted-html` so the posture is
  visible in review); the not-yet text dies.
- **Determinism**: pure-Go, no I/O — renders are pure functions.

### 3. Internals

- deps: github.com/yuin/goldmark + github.com/microcosm-cc/bluemonday
  (both pure Go, the spec's named pair).
- `pkg/runtime`: markdown.go (render + sanitize, policy built once);
  RegisterSiteTemplates wires the real {{ markdown }} (site posture
  threaded from codegen); hooks.RenderMarkdown for site.Markdown.
- `examples/journal`: post.html's <pre> becomes {{ markdown .Body }};
  the escaping test flips meaning — the script-tag body must now be
  ABSENT (stripped), not entity-escaped; new tests: headings/links/
  code render, nofollow on links, onclick stripped.
- Analyzer fixture: site with WithTrustedHTML → graph records it;
  runtime unit test: trusted mode passes raw HTML.
- No cloud surface changes.

### 4. Not in scope (W4 non-goals)

- Syntax highlighting, footnotes, KaTeX (later, additive);
  per-render trust overrides (the posture is per-site, deliberately);
  markdown in fragments beyond what {{ markdown }} already gives.

**Validation** (agent-side; human gate at W6): journal tests + a
scripted walk (markdown post renders; XSS post inert — the W6
script's §4 judgment) + full cycle on tako-test-01.

**STOP — awaiting DX approval (and the two §1 scope calls) before
implementation.**

## W4 — implementation: Markdown, sanitized by default (2026-07-26)

Proposal approved with both §1 recommendations (UGCPolicy; opt-out
validated outside journal). Agent-validated; gates at W6.

**Pipeline (pkg/runtime/markdown.go):** goldmark (CommonMark + GFM
tables/strikethrough/autolinks, WithUnsafe so raw HTML reaches the
sanitizer) → bluemonday UGCPolicy (+ class on <code> for fence
languages), built once. RenderMarkdown(src, trusted): the trusted
posture skips bluemonday — raw HTML passes. Deps: goldmark v1.8.4,
bluemonday v1.0.27 (pure Go, the spec's named pair).

**Posture threading:** RegisterSiteTemplates takes SiteOptions;
TrustedHTML flows analyzer → graph (visible in summary as
`(trusted-html: markdown renders raw)` — a security posture belongs
in review output, not buried in JSON) → codegen literal → runtime.
{{ markdown .Body }} is real (returns template.HTML — safe by
construction, the sanitizer IS the guarantee); site.Markdown renders
through hooks as a fragment (htmx preview endpoints). WithTrustedHTML
leaves siteNotYet; journal deliberately never takes it.

**journal:** post.html's <pre> became {{ markdown .Body }} — the one-
line change the proposal promised. The W2 escaping test flipped
meaning as planned: the script-tag body must now VANISH (bluemonday),
not entity-escape; the test also pins h1/em/nofollow/pre-code-
language rendering and bans the payload (scoped to inline content —
the layout's own htmx <script src> tripped a blanket ban on the first
run, honest catch). TestTrustedHTMLOptOut pins both postures at the
runtime level.

**Live cycle (tako-test-01):** deploy, then a markdown post with an
embedded <script>alert("xss")</script>: the page rendered h1/em/
nofollow-link, and the payload count was ZERO in both forms (no
alert(, no &lt;script fallback) — stripped, not escaped, from the
cloud. Destroy clean; full sweep green (examples' go.mod files picked
up the new indirect deps via tidy).

W4 complete agent-side. W5 (auth) proposal next — the last slice
before the W6 milestone gate.

## W5 — DX proposal: Auth — Identity Platform, _session, dev users (2026-07-26)

The last slice before the W6 milestone gate. Spec §2.9 + §4.7 + Part
3b are normative. Frozen surface made true: `tako.Auth`,
`WithGoogle`/`WithEmail`, `WithAuth`, `ctx.User`/`MaybeUser` (real
identities — TestApp.SignIn already works). Nothing added.

### 0. The headline

Auth is one declaration and one option:

```go
var auth = tako.Auth(tako.WithGoogle())

var Save = tako.Page("POST /p", save, tako.WithAuth())      // 401 without a user
```

The framework injects `/api/_session`: the browser signs in with
Identity Platform's client flow (Google button), posts the Firebase
ID token there once, and gets an HttpOnly `_session` cookie — SSR
pages and fetches share one validation path in the shim. `ctx.User()`
is guaranteed on WithAuth routes; journal's writes close (`Save` +
`Publish` gain WithAuth, posts gain an Owner only the author may
toggle), and the security summary's new warning line (the discovered-
work item from your W2 question) reports any PUBLIC service still
exposing mutating routes without WithAuth. Locally there is no GCP
dependency at all: tako dev injects a "sign in as test user" bar —
dev-minted sessions, same cookie, same shim path.

### 1. Scope calls needing your decision (before anything else)

1. **Session = signed cookie, server-stateless (RECOMMENDED).** The
   _session exchange verifies the Firebase ID token (Identity
   Platform's public JWKS), then mints tako's own HMAC-signed cookie
   (uid, email, name, expiry ≤ 7d; key from the framework bucket,
   created on first deploy — like local's signing.key). No session
   store, no revocation list in v0: sign-out clears the cookie,
   expiry bounds the window. Alternative: Firestore-backed sessions
   with revocation — a datastore write per sign-in and a read per
   request, for a capability no example demands yet.
2. **CSRF binds to the session when present (RECOMMENDED).** The W2
   double-submit token continues for anonymous browsers; once a
   _session exists, the CSRF token derives from it (HMAC of the
   session id) — a stolen anonymous token cannot ride an
   authenticated session. This was the standing W2 note.
3. **Identity Platform config is preflight, not reconciled
   (RECOMMENDED).** Enabling Identity Platform and its Google
   provider is a console one-time (it rides a marketplace-style
   enablement plus OAuth consent screen — genuinely interactive).
   Preflight probes the API and prints the exact console steps when
   absent; tako never mutates auth config. Alternative: reconcile
   provider config via the Admin API — possible for defaults, but
   the consent screen is unavoidably manual anyway.

### 2. Semantics being locked

- **`/api/_session`**: POST {idToken} → verify (aud/iss/exp against
  the project, JWKS cached) → Set-Cookie `_session` (HttpOnly,
  Secure in cloud, SameSite=Lax, ≤7d). DELETE → clear (sign-out).
  Injected only when the app declares Auth.
- **WithAuth**: 401 `sign in to continue` (json for api, plain for
  pages) when no valid session; ctx.User() guaranteed past it.
  MaybeUser everywhere else; User() on a public route without a
  session keeps panicking (the honest contract).
- **User**: ID (stable uid), Email, Name — from the verified token.
- **Dev bar (tako dev)**: a small injected banner (documents only)
  listing test users (alice, bob + free-text); selecting one mints a
  dev session cookie signed with the local key. The generated main is
  unchanged in the cloud (the bar injects only under local backend).
- **journal**: Save/Publish gain WithAuth; Post gains Owner (uid);
  publish checks ownership (403 `only the author can publish`);
  home/read stay public; layout shows the user or a sign-in link.
  W6's demo signs in as alice locally and via Google in the cloud.
- **Security summary**: public services now append
  `⚠ N mutating route(s) without WithAuth: POST /x …` — the W2
  review item, actionable now that WithAuth exists.

### 3. Internals

- Root: AuthHandle recorded (providers into the graph); WithAuth into
  handler options (analyzer records RequiresAuth).
- `pkg/analyzer`: auth resource kind real (not-yet dies); Handler
  gains RequiresAuth; summary shows `auth (google)` and per-route 🔒.
- `pkg/runtime`: session.go (JWKS verify, cookie mint/verify/clear,
  _session handler), WithAuth wrapping in PageHandler/JSONHandler
  (401 text), requestCtx.user set from the cookie, CSRF derivation
  per call 2, dev bar middleware (local only).
- `pkg/codegen`: mount /api/_session when Auth declared; pass
  RequiresAuth into handler wrappers.
- Preflight: identitytoolkit API probe + provider-absent text with
  console steps (only when Auth declared).
- `examples/journal`: as §2; app_test uses TestApp.SignIn (the F7
  harness path) plus HTTP-level session tests.

### 4. Not in scope (W5 non-goals)

- WithEmail flows beyond the declaration (Google is journal's
  provider; email/password validates when an example demands it);
  roles/permissions; account linking; MFA; session revocation lists;
  the dev bar in cloud builds.

**Validation** (agent-side; the human gate is W6 — its script will
walk sign-in in the browser): unit + journal tests, dev-bar smoke,
full cycle on tako-test-01 with Identity Platform enabled once by
hand per preflight's printed steps (I will report the exact console
friction encountered — it becomes the preflight text).

**STOP — awaiting DX approval (and the three §1 scope calls) before
implementation.**

### W5 amendment (2026-07-26, per review): sessions are Datastore-backed

Call 1 is overridden by the user: sessions live in the entity store,
not in a signed cookie. The revised design:

- **The cookie carries only an opaque random id** (128-bit hex,
  HttpOnly, Secure in cloud, SameSite=Lax). No claims in the cookie,
  nothing to forge, no signing key needed for sessions.
- **The record is a framework-internal entity**, kind `_tako_session`
  (single leading underscore — legal in Datastore, impossible from
  user declarations, excluded from destroy/prune DATA listings by its
  prefix but PURGED with the namespace on destroy): {uid, email,
  name, csrf, created, expires}. Stored through the same
  backends.Entity hook as user entities — bbolt locally, Datastore in
  the cloud, one code path; the dev bar mints the same records.
- **Per-request cost is a Get** on routes that consult the user
  (WithAuth always; MaybeUser when the cookie is present). No
  in-process caching in v0 — honest and revocation-coherent.
- **Revocation is real**: sign-out DELETEs the record; an operator
  can delete sessions in the console. Expired records read as absent
  and lazily delete on touch (bulk cleanup: discovered work for the
  cron/scheduler era).
- **CSRF simplifies** (absorbing call 2): the token is STORED in the
  session record — authenticated CSRF checks compare against it;
  anonymous browsers keep the W2 double-submit cookie.
- **Grants/preflight**: declaring Auth makes the app an entity-store
  consumer — Needs.Datastore and the project datastore.user grant
  derive from Auth itself (journal already has entities; an
  auth-only app would otherwise deploy without its session store).
  The security summary explains: `datastore [user — sessions]` when
  auth alone demands it.
- Call 3 (Identity Platform via preflight guidance) stands as
  recommended.

## W5 — implementation: Auth — Identity Platform, Datastore sessions, dev users (2026-07-26)

Approved as amended (sessions Datastore-backed; calls 2 absorbed, 3 as
recommended). The last pre-milestone slice.

**Sessions (pkg/runtime/session.go):** the cookie is an opaque random
id; the record is a `_tako_session` entity through hooks.Entity —
bbolt locally, Datastore in the cloud, one code path — holding
uid/email/name/csrf/expiry. Revocation is REAL and was proven live:
the record was visible in the Datastore console (namespace prod),
sign-out deleted it, the still-held cookie died with a 401, zero
records remained. Expired records read as absent and lazily delete.
ID-token verification is pure stdlib: RS256 against securetoken
x509 certs (cached per Cache-Control), iss/aud/exp checked against
TAKO_PROJECT. /api/_session: POST {idToken} → session; POST
dev_user=… → session on the LOCAL backend only (cloud answers 400);
DELETE → sign-out. The dev bar injects into HTML documents locally
only (buffering middleware; generated main identical across
backends) with alice/bob buttons and sign-out.

**WithAuth + CSRF binding:** RequireAuth() HandlerOption on
Page/JSON/Blob handlers — no session → 401 `sign in to continue`
(exact text pinned); past it ctx.User() is guaranteed (requestCtx
carries the user; MaybeUser works everywhere). Signed-in CSRF truth
is the SESSION's stored token (a stolen anonymous token cannot ride
an authenticated session); anonymous browsers keep W2's double-submit
cookie. Templates gain {{ user }}/{{ signedIn }}.

**Toolchain:** Auth extracts (providers recorded; `auth (google)` in
summaries; missing provider = tako/auth-provider); WithAuth sets
Handler.RequiresAuth (🔒 in every listing; codegen passes
runtime.RequireAuth()); codegen mounts /api/_session + the dev-bar
wrap when Auth is declared. Grants: Auth alone now derives project
datastore.user for every route-serving service (sessions are entity
records) and Needs.Datastore + the identitytoolkit API probe.
Destroy purges _tako_session with the namespace but does NOT list it
as DATA (revocable credentials, not content — proven: destroy warned
only about posts); prune ignores _-prefixed kinds. The security
summary now warns `⚠ N mutating route(s) without WithAuth` on public
services — journal triggers none (its writes closed).

**journal:** Auth(WithGoogle); Save/Publish carry WithAuth; Post
gains Owner; publish enforces `only the author can publish` (403);
layout shows the user (name, else email) or "not signed in". Tests:
anonymous 401, signed-in-but-tokenless CSRF 403, ownership 403
(bob vs alice's post), REVOCATION (sign-out then the old session's
write 401s), plus every W2–W4 test reworked through dev sign-in.

**Live cycle (tako-test-01) — Identity Platform enablement friction,
recorded for the preflight text:** `gcloud services enable
identitytoolkit.googleapis.com` + one REST call
(identityPlatform:initializeAuth, which requires the
`x-goog-user-project` quota-project header on ADC user credentials —
THE gotcha) fully initialized auth with NO console visit. The Google
provider's OAuth consent screen remains the one genuinely manual
step (the W6 browser walk needs it). Agent-side cloud validation
used the email provider (enabled via the admin API) + an API-key
signInWithPassword to mint a REAL ID token: anonymous write 401,
garbage token 401, dev_user rejected in the cloud, the real token
minted a session, the signed-in write PRG'd, the session record
appeared in Datastore and died on sign-out. Deploy's probed list
grew identitytoolkit; destroy clean; test API key deleted after.

**Discovered work (not done):** bulk expired-session cleanup (cron
era); the consent-screen/Google-provider preflight text can only say
"console → Identity Platform → Providers → add Google" (verified
wording lands after the W6 walk); dev-bar Content-Length rewrite
buffers whole documents (fine for dev).

W5 complete agent-side. W6 (the milestone assembly) next.

## W6 — the milestone assembly: journal, complete (2026-07-26)

No new feature code — W6 is the assembly and its gate script.
`validate/journal.md` is the unified local+cloud walk (F9 style):
graph review (🔒s, auth line, composite), the dev-bar sign-in +
fragment-swapping write flow, the XSS judgment (stripped, not
escaped), sessions-as-records (including operator revocation from
the console, live), the suite as user-style tests, and the cloud leg
with real Google sign-in — whose ONE manual prerequisite (console →
Identity Platform → Providers → add Google) is exactly the friction
W5's live run isolated. Everything the script judges was agent-
validated in the W3/W4/W5 slices; the milestone walk verifies those
claims end to end in one sitting.

**GATE REQUEST — W6 MILESTONE: journal complete (closes W3–W6).**
Run `validate/journal.md` §§1–6 (the Google provider console step in
§6 is the only manual setup) and record ONE verdict in
docs/validation-log.md. This is Stage 3's mid-point DX review —
notes beyond pass/fail are welcome; W7 (`tako gen client ts`, shop
is born) follows on a pass.

## W5 follow-up: auth preflight completes its promise (2026-07-26)

The user's cloud deploy surfaced a gap: no instruction anywhere in
the deploy flow about the Google-provider console step — it lived
only in validate/journal.md. Two causes, both confirmed: the test
project was pre-initialized by W5's live validation (API enabled,
auth initialized, email provider on), AND the shipped preflight only
probed the API — less than the approved proposal's "prints the exact
console steps when absent". Closed: Needs.AuthProviders (the declared
providers) replaces the bool; preflight now checks Identity Platform
initialization (`/config`) and each declared provider
(defaultSupportedIdpConfigs for google, signIn.email for email) via
the quota-project-header-aware admin calls, and a missing one is a
BLOCKING gap with the console steps — proven live: journal's plan on
tako-test-01 now stops with "the Google sign-in provider is not
configured … console → Identity Platform → Providers → add Google …
a one-time interactive step tako cannot do for you" (exactly the
user's situation). docs/tako-cloud-permissions.md gains the Identity
Platform section as the durable documentation home, with an explicit
marker that the FINAL user-facing developer documentation must carry
it (that documentation is its own future deliverable — noted, per
user direction, as a standing requirement). validate/journal.md §6
now lets preflight narrate the step instead of front-loading it.

## W6 fix: the browser half of sign-in existed nowhere (2026-07-26)

The user's gate walk found it immediately: the deployed journal had
no way to sign in — W5 shipped and proved the SERVER half (verify,
sessions, revocation) with a hand-minted token, but no sign-in UI,
no client config, and the run.app origin wasn't an authorized
sign-in domain (the popup would have refused it even with a UI).
"Auth is one declaration" means the framework owes the flow; shipped:

- **GET /api/_session/signin** (framework-owned, mounted by codegen
  with the DECLARED providers): locally a dev-user page; in the cloud
  the standard Identity Platform browser flow (Firebase Auth compat
  from gstatic — Google popup, and an email/password form when
  declared), one POST of the ID token to /api/_session, then ?next=.
- **Deploy discovers the client config** (initialization auto-
  provisions the public browser API key; firebaseSubdomain → auth
  domain) and injects TAKO_AUTH_APIKEY/TAKO_AUTH_DOMAIN; it also
  REGISTERS the app's own service hosts as authorized sign-in
  domains, narrated (`auth journal-….run.app authorized for
  sign-in`) — the permissions doc now draws the line explicitly:
  trust config (providers, consent) is manual; mechanical own-URL
  registration and client-config reads are the deploy's.
- **journal's layout** links "sign in" → the page and shows a
  sign-out form when signed in; the dev sign-in page honors next.

Verified live on tako-test-01: the deploy narrated the domain
registration; the signin page serves with the Google button, the SDK,
and the baked config; the Google provider is enabled (the user's
console click, per preflight's instruction, had landed); layout links
resolve. The interactive Google popup itself remains the user's W6
§6 judgment — everything up to the click is now machine-verified.

## W6 fix 3: placeholder OAuth clients caught at preflight (2026-07-26)

The walk's next find: the Google popup answered `Error 401:
invalid_client` — the Identity Platform provider form had accepted
placeholder text as the OAuth client (clientId "tako-test-01"), and
nothing validates it until Google's endpoint refuses the first
sign-in. Real web client ids end in .apps.googleusercontent.com, so
preflight now rejects placeholders with the full verified recipe
(consent screen → OAuth web client with the
<project>.firebaseapp.com/__/auth/handler redirect URI → paste into
the provider) — proven live: journal's plan on tako-test-01 now
stops on exactly the user's state. The permissions doc and gate
script carry the corrected wording (the earlier "accept the consent
defaults" was a shortcut this walk falsified — the "verified wording
lands after the W6 walk" note, cashed in).

## W6 fix 4: auth setup automated to its honest maximum (2026-07-26)

The user pushed on the right axis: the growing instruction list was
under-automated. Reworked — EnsureAuthClient now OWNS the auth
surface: it initializes Identity Platform when absent (mechanical,
narrated), enables the email provider when declared (the declaration
is the decision), rejects missing/placeholder Google clients with a
BLOCKING error whose primary instruction is the one-toggle Firebase
console route (Firebase auto-provisions the OAuth client Google
gives no public API for; the GCP three-step recipe is the fallback
for org-restricted consoles), injects the browser client config, and
registers both Cloud Run host forms as authorized domains.
`--plan` runs every check without mutating (would-do narration).
Provider gap detection moved OUT of Preflight into this one place;
the permissions doc rewrote its section around the automation list +
the single manual step. Live: the placeholder state blocked with the
new text until the user's real OAuth client landed
(119764066724-….apps.googleusercontent.com — verified), after which
the plan runs clean. The answer to "documented in a way that blocks
setup or honestly automated": both — everything mechanical is
automated, and the one manual step is enforced by a deploy-blocking
error that carries its own instructions.

## W6 fix 5: destroy answers "everything" honestly (2026-07-26)

The user asked whether destroy takes care of everything set up — the
audit found four gaps, three of them env-isolation class, and (a
process lesson) two earlier patches that had silently no-op'd via
unasserted replaces:

1. **Env-scoped destroys deleted the SHARED composite index** —
   indexes serve every namespace, and pr-99's teardown put prod's
   `posts (published, -created)` into DELETING twice during the fix
   cycle (restored each time). Shared kinds (repo, indexes) now
   survive env-scoped destroys; the closing line names them.
2. **A DELETING index observed as present** — plans skipped while it
   died. DELETING now reads as absent.
3. **Grant convergence on dead targets** — deleted SAs/services
   answer 200-tombstones on policy reads but 404/403 on writes;
   partial destroys wedged forever. Non-project targets now converge
   past 400/403/404 on both paths.
4. **Sign-in domains and session purge actually wired** — destroy now
   REMOVES the domains tako registered (`✗ sign-in domain …`, before
   the env-scoped return so previews clean up too) and purges
   _tako_session (both had been lost to failed patches). W5's
   leftover test user deleted from Identity Platform.

Verified by a full pr-99 cycle: deploy registered both host forms,
destroy removed both and kept the shared index (READY throughout),
prod untouched. What destroy DELIBERATELY leaves, now documented:
Identity Platform initialization/providers/OAuth client (project-
level trust config, shared by every env), user accounts in Identity
Platform, Secret Manager secrets, enabled APIs, the Datastore
database, and the auto-provisioned browser API key.

## W6 MILESTONE — verdict recorded (2026-07-26)

Pass, recorded in docs/validation-log.md — closes W3, W4, W5, W6. The
gate walk ran validate/journal.md on tako-test-01 AND repeated the
full flow on a fresh project (tako-test-02), which caught the last
bug: initializeAuth built its URL on /admin/v2 instead of /v2 (the
branch had never executed where auth pre-existed) — fixed in 5c9cd9e
with HTML error bodies compressed to one line. Standing requirement
carried forward: the Identity Platform manual-step instruction MUST
appear in the final developer documentation. Journal is complete.
W7 begins Stage 3's back half; the next human gate is W11.

## W7 — DX proposal: `tako gen client ts` — shop is born (2026-07-26)

Spec §2.11's last piece: "a typed TypeScript client for every `api`
endpoint (param/response types from the Go signatures, session auth
pre-wired), so the frontend talking to the backend is generated, not
hand-maintained." A CLI subcommand — NO SDK surface change (the F10
freeze holds; precedent: `gen graph --json` landed post-freeze as CLI
growth). shop is born as the consumer: journal showed the hypermedia
path; shop is the "website with an API" path.

### 0. The headline

```
cd examples/shop && go run ../../cmd/tako gen client ts
  client   .tako/gen/client/ts/api.ts — 3 endpoints, 5 types
           (1 private endpoint excluded: browsers cannot mint OIDC)
```

The generated file is plain fetch, one ES module, zero dependencies,
strict-mode clean:

```ts
import { createClient, ApiError } from "./api";

const api = createClient();                 // same-origin; session cookie rides along
const products = await api.listProducts();  // typed: Product[]
try {
  await api.placeOrder({ productId: products[0].id, qty: 2 });
} catch (e) {
  if (e instanceof ApiError && e.status === 401) {
    location.assign("/api/_session/signin?next=/");   // journal's machinery, reused
  }
}
```

### 1. Scope calls needing your decision (before anything else)

1. **shop declares `tako.Auth(tako.WithEmail())` (RECOMMENDED).**
   Checkout needs sign-in, and email/password finally gets the example
   that demands it (the deferred W5 non-goal closes). It is the ONE
   provider machine-testable end to end — no browser, no OAuth popup:
   cloud validation creates a throwaway user over the admin API,
   `signInWithPassword` for a real ID token, drives the session
   exchange, deletes the user. The W6 email automation (auto-enable on
   declaration) gets its live end-to-end exercise. Alternative: Google
   again — browser-only validation, nothing new proven.
2. **shop's page is TS bundled by `WithBuild` (RECOMMENDED):**
   `tako.WithBuild("npx esbuild src/app.ts --bundle --outdir=dist", "dist")`
   (shape illustrative) — the generated client runs in a real browser
   bundle, typechecked by the toolchain that consumes it. Node is
   needed on the deploying machine only (like Hugo in W1's variant),
   NEVER by `go test` / `tako test`. Alternative: commit a hand-compiled
   .js asset — no Node anywhere, but nothing typechecks anywhere either.
3. **Output goes to `.tako/gen/client/ts/api.ts` by default, `--out`
   to relocate (RECOMMENDED).** Settled decision 3: generated code
   lives under .tako/gen, human-readable; the frontend imports from
   there (shop does) or `--out web/src/api.ts` drops it into an
   existing frontend tree. Alternative: default straight into the site
   dir — convenient but plants generated code in user-owned space.

### 2. Semantics being locked

- **Coverage: `api` endpoints only.** Pages are server-rendered;
  workers/subscribers/crons have no browser surface. `private`
  (IAM-only) endpoints are excluded WITH a comment naming them —
  exclusion is visible, never silent.
- **Names:** endpoint var → lowerCamel method (`ListProducts` →
  `listProducts`); a collision is a generation error with file:line.
- **Type mapping:** string→string, bool→boolean, int/int64/float64→
  number, time.Time→string (RFC 3339, documented), []T→T[],
  map[string]T→Record<string, T>, *T→T | null, nested exported
  structs→interfaces. `json` tags rule response/body field names
  (`json:"-"` omitted, `,omitempty`→optional `?`). An unsupported
  field type (chan, func, interface) is a generation error naming the
  field, file:line.
- **Wire = the generated decoders, mirrored:** `path:` fields become
  positional args in route order; `query:` fields an options object;
  remaining fields the JSON body. `tako.Blob` results → the method
  returns the raw `Response` (caller streams); Blob REQUEST fields →
  endpoint excluded with a comment (multipart is the signed-URL
  story, not the JSON client's).
- **Session auth pre-wired:** `credentials: "same-origin"` so the W5
  `_session` cookie rides every call; non-GET methods attach
  `X-CSRF-Token` from the layout's meta tag when present (free
  defense-in-depth for cookie-authed JSON; server behavior unchanged).
  `createClient({ baseUrl?, headers? })` — baseUrl for driving a
  deployed api from outside the origin, headers for non-browser
  callers (validation passes the session cookie explicitly; Node's
  fetch ignores `credentials`).
- **Errors:** non-2xx → `throw new ApiError(status, message)` parsed
  from tako's error body, falling back to statusText. No silent
  retries (v0's stance, same as §2.12).
- **Deterministic:** endpoint source order, no timestamps —
  regenerating an unchanged app yields a byte-identical file.

### 3. shop v0 (the W7 slice)

- `app.go`: `products` entity (Name, PriceCents), `orders` entity
  (Owner indexed, Created); `tako.Auth(tako.WithEmail())`;
  `ListProducts` GET /api/products, `GetProduct` GET
  /api/products/{id}, `PlaceOrder` POST /api/orders 🔒 (unknown
  product → 404 "unknown product"), `AddProduct` POST /api/products 🔒
  (v0 has no admin concept — settled decision 11; any signed-in user
  stocks the shelf, documented as demo scaffolding shop will outgrow).
- `web/`: index.html + `src/app.ts` importing the generated client —
  list products, place an order, 401 → sign-in link. Bundled per §1.2.
- Tests (user-style): list/get, order 401 signed-out, order placed
  with a dev session, 404 unknown product, ownership on order listing
  if listed. `tako test` needs no Node (WithBuild not run by tests —
  the known gap stands).

### 4. Internals

- `cmd/tako`: `gen client ts` beside `gen graph`; `--out` flag.
- `pkg/clientgen` (new): runs beside the analyzer with go/types
  access (AppGraph Bindings carry scalars only; body/response type
  TREES need types.Type — same reason codegen works from the typed
  load). Golden tests in pkg/clientgen/testdata: input package →
  expected api.ts; error cases assert exact message + file:line.
- **Cloud errors & permissions: none new.** Generation is local
  static analysis. shop's deploy rides existing surfaces — the email
  provider auto-enables via W6's EnsureAuthClient (this time on a
  declaration that means it).

### 5. Not in scope (W7 non-goals)

- Other languages (`gen client <lang>` namespace leaves room); a Go
  client (§2.12 typed calls are the Go story — W9); npm packaging (a
  file, not a package — users own their build); retry/backoff;
  streaming/SSE; multipart upload methods; coordination on orders
  (double-stock races are W8's Lock demo, deliberately).

**Validation** (agent-side; the human gate is W11): clientgen
goldens; shop suite; `npx tsc --noEmit --strict` on the generated
client; full cycle on tako-test-01 — deploy, throwaway email user →
signInWithPassword → session cookie, a Node script importing the
generated client places a real order against the deployed api (the
gate criterion verbatim), 401 without the cookie judged, destroy.
Transcript in the task log.

**STOP — awaiting DX approval (and the three §1 scope calls) before
implementation.**

## W7 — implementation + agent-side validation: tako gen client ts; shop is born (2026-07-27)

Landed as approved, all three scope calls as recommended (9c8d88b).
`pkg/clientgen` walks Req/Res type TREES via the new
`analyzer.AnalyzeTyped` (the AppGraph carries scalar bindings only);
`Handler` now records `WithPrivate` so exclusions are graph-visible.
Goldens pin the emitted TypeScript byte for byte (shortlink: body +
path + query; gallery: the exclusion story — multipart upload excluded
WITH its reason, blob downloads return the raw `Response`) and the
diagnostics (`tako/client-type` for chan / non-string map keys,
`tako/client-name` for method collisions — URLFor/UrlFor both
camel-case to urlFor), plus a two-runs-byte-identical determinism pin.

**examples/shop v0:** products/orders api (writes 🔒), email/password
auth, TS frontend importing the COMMITTED generated client
(web/src/api.ts; TestGeneratedClientIsFresh pins it against the Go
signatures — drift fails the suite), bundled by WithBuild (esbuild via
npx; Node on the deploying machine only — `go test` and `tako test`
never need it). Suite: catalog/order flow, 3× anonymous-401 texts,
unknown-product 404s, owner-scoped orders, 422 validation texts.

**Validation transcript (agent-side; the human gate is W11):**
- `tsc --noEmit --strict` clean on the generated clients of shortlink,
  gallery, AND shop (including shop's app.ts importing it — the types
  work in a real consumer).
- Local: `tako test` green; dev walk on :8090 — home + bundled app.js
  200, dev-session curl flow (add/list/get/order/list, 401 + 404
  texts), then drive.mjs (Node + the generated client against dev):
  typed results and `ApiError 401 "sign in to continue"` /
  `ApiError 404 "unknown product"` as instanceof-checkable errors.
- Cloud (tako-test-01): deploy 6m42s — WithBuild narrated (⛅ site …
  2 files), composite `orders (owner, -created)` built, both Run host
  forms authorized for sign-in, email provider found already enabled
  (the declaration was satisfied — no narration, correctly), security
  summary derived shop-sa + datastore.user, NO mutating-routes ⚠
  (all writes carry WithAuth). First requests 403'd on runQuery for
  ~40s (fresh-SA IAM propagation; the honest runtime-403 error text
  fired) then converged untouched.
- THE GATE CRITERION, verbatim: created w7test@example.com over the
  API-key signUp, `signInWithPassword` → real RS256 ID token →
  `POST /api/_session {idToken}` → Datastore-backed `_session` cookie
  → **drive.mjs (Node, the generated client, baseUrl = the deployed
  URL, Cookie in ClientOptions.headers) listed products and PLACED A
  REAL ORDER (2 × cloud tea = 1050¢)**, listed it back, and surfaced
  anonymous-401 + unknown-404 as typed ApiErrors. Sign-out deleted
  the session record (dead cookie → 401); the throwaway user was
  deleted.
- Destroy: DATA named `products, orders` (sessions purged silently —
  credentials, not content), both `✗ sign-in domain` lines fired,
  project left clean.

**Discovered work (not done, per scope):**
- Multi-app projects: a full app destroy deletes the artifact repo
  `tako` and the framework bucket, which are shared ACROSS APPS on
  the project (journal + shop). Env-scoped destroys already spare
  them; app destroys should count sibling apps first. (No harm this
  walk — the project had no other app.)
- Fresh-SA IAM propagation: the first ~1 min of runtime Datastore
  calls can 403 with the "deploy bug" error text. A deploy-time
  readiness probe (or propagation wording in that text) would spare
  the alarm.
- Orphaned dev processes accumulate across sessions (seven stale
  `tako dev` roots found this walk); `tako dev` could reap or at
  least list its own strays.

W8 (Lock, WithSingleflight, Singleton — the §4.4 lease) follows; its
DX proposal is the next stop.

## W8 — DX proposal: Coordination — Lock, WithSingleflight (Singleton: scope call) (2026-07-27)

Spec §2.5 + §4.4: one lease mechanism (a GCS object write guarded by
ifGenerationMatch, {owner, expiry}, heartbeated at TTL/3, living in
the framework bucket that already hosts the DEPLOY lease — same CAS
pattern, now generalized to runtime) behind three user-facing faces.
Frozen surface made true: `tako.Lock` / `Do` / `TryDo`,
`tako.WithSingleflight()`. Raw leader election stays unexposed
(settled: spec §"non-goals").

### 0. The headline

Both demands come from the spec's own examples:

```go
// gallery — Part 3 verbatim: overlapping slow cleanups collapse to one.
var Cleanup = tako.Cron("every 24h", cleanupOrphans, tako.WithSingleflight())

// gallery — Part 1's sample lock, made real: rebuilding the warmed
// feed must not run twice concurrently, across ALL instances.
var (
    relock  = tako.Lock("reindex")
    Reindex = tako.API("POST /api/reindex", reindex)
)

func reindex(ctx tako.Ctx, _ struct{}) (ReindexRes, error) {
    ran, err := relock.TryDo(ctx, rebuildFeed)   // false: someone's mid-rebuild
    return ReindexRes{Started: ran}, err
}
```

Dev narration when the lease saves you:
```
↻ cron Cleanup skipped — an overlapping run holds the lease
```

**The teaching line (doc comments + summary):** Lock is for critical
SECTIONS — cross-entity rebuilds, external systems, sequential
cursors. Single-entity atomicity is `Tx`'s job; reaching for Lock
there is the anti-pattern, and the docs say so where Lock is defined.

### 1. Scope calls needing your decision

1. **Singleton DEFERS to W10 (RECOMMENDED).** No example honestly
   demands a long-lived exactly-one loop yet — every candidate I
   tried (order dispatcher, price sync) is either a queue's job
   (events are the preferred style, settled #6) or a Tx's job, and
   shipping a contrived loop teaches the anti-pattern. W10 (Jobs —
   the batch-profile slice) is its natural sibling: min=1/max=1
   provisioning + a lease-guarded loop land together there, still
   inside the W11 gate. The W8 gate criterion ("two instances
   provably serialize") is Lock's anyway. Alternative: implement
   Singleton now on a shop "dispatcher" loop (plan-as-written).
2. **Lock takes NO options in v0 (RECOMMENDED).** TTL 30s /
   heartbeat 10s / campaign poll 2s are framework policy; an option
   surface (WithTTL) invites tuning nobody needs yet and the frozen
   API stays frozen. The analyzer rejects any option with an exact
   text. Alternative: WithTTL(d) now.
3. **Reindex rebuilds gallery's warmed feed (RECOMMENDED)** — a real
   cross-entity operation the example already owns (warmFeed exists
   since F8), observable in validation via a start/end ledger the
   rebuild writes. Alternative: a synthetic slow section.

### 2. Semantics being locked

- **Do(ctx, fn)**: campaigns until held (2s poll + jitter), runs fn
  while heartbeating every 10s, releases on return. If the lease is
  LOST mid-flight (heartbeat CAS fails — clock skew, forced release),
  fn's ctx cancels; Do returns ctx.Err() after fn unwinds. Errors
  from fn pass through verbatim.
- **TryDo(ctx, fn)**: one CAS attempt; (false, nil) when held
  elsewhere — never blocks, never retries.
- **Lease identity**: `leases/run-<app>-<env>-<name>` in the
  framework bucket — env-scoped like every runtime resource (pr-42's
  reindex never blocks prod's); the deploy lease keeps its
  `leases/deploy-…` namespace, untouched.
- **Crash safety**: no release on crash — the TTL expires (≤30s) and
  the next campaigner wins. Heartbeats extend from the HOLDER's
  generation only (CAS), so a usurper can't be double-held.
- **WithSingleflight**: codegen wraps the cron invoke in TryDo on
  lock name `cron-<handler>`; a skipped run logs the ↻ line (dev and
  cloud) and is NOT an error — dead-letter machinery never sees it.
- **Local backend**: in-process lock table (mutex + waiters), same
  semantics including TryDo false and ctx-cancel on forced loss (test
  hook); no emulators (settled #9). Conformance is the contract:
  mutual exclusion (interleaving-free ledger from N contending
  goroutines), TryDo held/free, reacquire after release, crash-TTL
  expiry (gcp: expire a stale lease object; local: clock hook).
- **Grants**: an app whose graph reaches a Lock (or singleflight
  cron) grants its runtime SA objectAdmin on `leases/*` of the
  framework bucket — derived like every grant, one summary line:
  `roles/storage.objectAdmin on tako-test-01-tako/leases (lease: reindex, cron-Cleanup)`.
- **Plan/graph visibility**: `lock reindex` resource line; cron line
  gains `(singleflight)`; no new provisioned objects (the framework
  bucket already exists for the deploy lease).

### 3. Internals

- `pkg/backends`: Lease contract (Acquire(name, owner, ttl) CAS /
  Heartbeat / Release / observe) + conformance chapter Lease;
  `local/` in-process table; `gcp/` GCS object CAS (the deploy
  lease's pattern, factored to be shared).
- `pkg/runtime`: hooks for Do/TryDo (lock.go's panics die); cron
  registration grows a singleflight flag; ↻ narration.
- `pkg/analyzer`: Lock constructor → resource kind "lock" (const
  name; error text for non-const); WithSingleflight recorded on cron
  handlers (`singleflight` in the graph); Lock leaves the not-yet
  list (Singleton stays, pointer updated to W10 if §1.1 approved);
  edges: handler → lock var, verb "lease".
- `pkg/codegen`: TryDo wrapper for singleflight crons; lease-grant
  need surfaces through the existing needsOf path.
- `examples/gallery`: Cleanup gains WithSingleflight; relock +
  Reindex + rebuildFeed (+ ledger entity for the serialization
  proof); suite grows contention tests (two goroutine Reindex calls
  → one runs; AdvanceCron overlap → one).
- **Cloud errors & permissions:** no new APIs, no new objects. The
  one new failure mode: framework-bucket object write denied →
  the existing four-way taxonomy applies (verb "acquire the
  reindex lease", role roles/storage.objectAdmin). Preflight probes
  nothing new (storage already probed).

### 4. Not in scope (W8 non-goals)

- Singleton (per §1.1 — W10 with Jobs); raw leader election (never);
  lock options/TTL tuning; fencing tokens beyond ctx-cancel;
  cross-app locks (lease names are app+env scoped by construction);
  Redis/Datastore lease backends.

**Validation** (agent-side; the human gate is W11): conformance
(local + gcp) Lease chapter; gallery suite; dev walk — slow-cron
overlap narrates ↻, two concurrent reindex curls → one `started:
true`; cloud on tako-test-01 — deploy (grant line judged), TWO
concurrent reindex POSTs against min-2-instance gallery serialize
(ledger shows disjoint windows), ↻ line in cloud logs on a forced
cron overlap, destroy clean. Transcript in the task log.

**STOP — awaiting DX approval (and the three §1 scope calls) before
implementation.**

## W8 — implementation + agent-side validation: Lock + WithSingleflight (2026-07-27)

Landed as approved, all three scope calls as recommended (caa47b1):
Singleton deferred to W10, zero Lock options, gallery reindex as the
real critical section.

**Shape:** `backends.Lease` — three CAS ops (TryAcquire/Extend/
Release, fencing tokens = GCS generations / a local counter);
campaigning, ttl/3 heartbeats, and loss-cancellation are SHARED logic
(DoLease/TryDoLease), so local and gcp cannot diverge semantically.
gcp leases are ifGenerationMatch object writes in the framework
bucket (`leases/run-<app>-<env>-<name>` — env-scoped; the deploy
lease keeps `leases/deploy-…`). Lock.Do/TryDo wire through hooks;
WithSingleflight wraps the cron handler AT DECLARATION (dev ticker,
AdvanceCron, future scheduler all inherit); ↻ narration on skip.
Lock use derives objectAdmin on the framework bucket, which is now
ALWAYS a grants target (dropping lock use converges to revocation);
plans grew one `iam grants bucket <project>-tako` line everywhere.
Discovered while wiring gallery: Cleanup had declared
WithSingleflight since F8 — accepted, recorded in the graph, and
IGNORED until now; the declaration finally means something.

**Validation transcript (agent-side; the human gate is W11):**
- Conformance Lease chapter 7/7 on local AND live GCS (tako-test-01):
  exclusion + non-reentrancy, TTL crash-expiry, stale fencing tokens
  can't extend or release, DoLease serializes 3 contenders
  (interleaving-free ledger), TryDo held/free, heartbeats carry a run
  to 3× its ttl uncancelled, forced loss (unconditional object
  delete — the operator move) cancels the run ctx and surfaces the
  lost-lease error.
- gallery suite: reindex repairs (torn thumb demoted, feeds rebuilt,
  ledger window recorded), deterministic collapse (the test occupies
  the very lease the handler campaigns for; ledger stays at one
  entry), cron collapse via the pinned `cron-cleanupOrphans` name —
  an orphan survives the collapsed run and dies on the free one.
- Dev walk (:8090): banner shows `lock reindex` and
  `cron Cleanup — every 24h (singleflight)`; reindex endpoint + runs
  ledger live. (Two zombie dev processes from earlier sessions
  squatted 8090 again — the loud port error named them both times.)
- Cloud (tako-test-01): deploy 9m14s; security summary line verbatim:
  "roles/storage.objectAdmin on the framework bucket (leases —
  Lock/singleflight)"; the ⚠ open-mutating-routes warning now lists
  POST /api/reindex (gallery's documented no-auth posture — the
  system stays honest). Six photos seeded through the real pipeline.
  **THE GATE CRITERION, exceeded: FOUR concurrent reindex POSTs
  against the deployed service — exactly ONE held the GCS lease and
  ran (6 photos verified, 252ms window), three answered
  started:false. One ledger entry. A second round ran and the ledger
  shows two disjoint windows.** Destroy clean (8 entities incl. the
  reindex-runs ledger; framework bucket last).
- Cron singleflight in the CLOUD is untriggerable today (crons are
  inert until the scheduler feature — the ⚠ says so at deploy); its
  collapse is pinned by suite + conformance. Noted for the scheduler
  slice.
- Cache-warming footnote from the walk: /api/photos read as 0 for up
  to a minute after seeding — the per-instance feed cache being
  honest (v0's documented posture), not a pipeline failure.

W9 (cross-service Endpoints — typed calls, OIDC, deadline budgets;
shop splits into services) follows; its DX proposal is the next stop.

## W9 — DX proposal: cross-service Endpoints — typed calls, OIDC, deadline budgets (2026-07-27)

Spec §2.12 verbatim: package = service; an EXPORTED Endpoint var is the
RPC surface; `.Call` is deliberately one word louder than a function
call. No call-site rewriting anywhere — the endpoint value carries a
transport the generated boot hook wires per environment. The frozen
`Endpoint.Call` doc comment already promises all of this; W9 makes the
remote half true. shop splits into services.

### 0. The headline

```go
// billing/billing.go — a new PACKAGE, therefore a new SERVICE (mkdir, not YAML)
package billing

// Exported Endpoint var = callable from other service packages.
// WithPrivate: IAM-only ingress — browsers and curl get 403 at
// Google's front door; only granted SAs invoke.
var Charge = tako.API("POST /charge", charge, tako.WithPrivate())

type ChargeReq struct {
    OrderID    string `json:"orderId"`
    AmountCents int   `json:"amountCents"`
}
type Receipt struct {
    ID          string `json:"id"`
    AmountCents int    `json:"amountCents"`
}

// shop/app.go
import "shop/billing"

func placeOrder(ctx tako.Ctx, req PlaceOrderReq) (Order, error) {
    …
    receipt, err := billing.Charge.Call(ctx, billing.ChargeReq{OrderID: o.ID, AmountCents: o.TotalCents})
    …
}
```

- **dev + tests**: direct in-process invocation — a breakpoint steps
  from placeOrder straight into charge.
- **cloud**: HTTP/JSON stub, OIDC ID token minted from shop-sa via
  the metadata server (audience = billing's URL, validated natively
  by Cloud Run — no shared secrets). The deploy summary prints the
  call graph and the derived `run.invoker` grant.

```
  plan
    + create  run service shop-billing        (private)
    …
Security summary:
  shop-billing runs as shop-billing-sa@… — ingress: IAM-only (callers: shop-sa)
  call graph: shop.PlaceOrder → billing.Charge
```

### 1. Scope calls needing your decision

1. **Remote calls ride the endpoint's OWN route (RECOMMENDED).** The
   stub POSTs to `https://<billing-url>/charge` with the same
   path/query/body wire shape the generated decoder already speaks;
   auth is the OIDC header. One surface — an operator can curl a
   private endpoint with their own identity token, and Cloud Run's
   IAM (not tako middleware) is the enforcement. Alternative: a
   dedicated `/_tako/call` envelope — a second wire format to keep
   honest forever, for no capability gained.
2. **Deadline budgets: X-Tako-Deadline, 500ms margin, 1s floor
   (RECOMMENDED).** The stub propagates the caller's remaining
   deadline minus 500ms as an absolute timestamp header; the callee's
   ctx honors it, so chains fail fast instead of stacking timeouts.
   Below the 1s floor the call fails immediately with "deadline
   budget exhausted before calling billing.Charge". Alternative: no
   propagation in v0 (stacked-timeout debt from day one).
3. **Cloud Run realities surface as DEPLOY-time ⚠, analyzer ERRORS
   only for structure (RECOMMENDED).** Analyzer errors: calling a raw
   handler FUNC across packages (hides the RPC boundary — error text
   points at the Endpoint var), and call CYCLES between services.
   Deploy ⚠ (it knows profiles): a sync edge into a scale-to-zero
   callee ("give shop-billing tako.WithProfile(tako.Latency) or make
   it async"), and sync chains deeper than one hop. Alternative:
   everything at analysis time — but profile-dependent judgments
   don't belong in the analyzer.

### 2. Semantics being locked

- **Visibility is Go's**: only EXPORTED Endpoint vars cross packages;
  unexported stay service-internal. The analyzer error for raw
  cross-package handler-func calls names the fix.
- **WithPrivate becomes real** (recorded in the graph since W7): the
  Run service is created WITHOUT the public invoker binding; callers'
  SAs get `run.invoker` derived from call edges. Browsers/curl → 403
  at the Google front end. The TS client already excludes private
  endpoints with a visible reason (W7 anticipated this).
- **No silent retries** — a typed call must not invent duplicate
  charges; errors surface to the caller verbatim (typed status
  errors cross the wire: billing's 422 is placeOrder's 422 to
  handle, not a 500).
- **Call edges derive grants**: caller-SA → `run.invoker` on callee;
  revoking it breaks the call with a READABLE error naming the grant
  (the gate criterion) — and a re-deploy converges it back.
- **Trace context propagates** (request id header) so `tako logs`
  correlates a call chain.
- **Pages can Call too** (an Endpoint is an Endpoint); workers/crons
  calling endpoints derive the same grants.

### 3. Internals

- Root SDK: Endpoint gains an unexported transport hook (no surface
  change — the freeze holds); hooks bridge carries per-var remote
  transports set by the generated main from deploy-injected
  `TAKO_CALLEE_<SERVICE>` URLs.
- `pkg/analyzer`: call edges (`Kind: "call"` — the Edge type has
  carried the shape since C-phase), cycle detection (error), raw-func
  error; goldens for shop's new two-service graph.
- `pkg/codegen`: billing's main mounts /charge like any route; shop's
  main wires billing.Charge's remote transport at boot.
- `pkg/runtime`: OIDC token mint via metadata server (audience =
  callee URL, cached until expiry — the Signer already speaks
  metadata); deadline header honored in the handler shim.
- `pkg/provision`: private services drop the public invoker;
  `run.invoker` from call edges; deploy injects callee URLs
  (deterministic names — same trick as auth's deterministic hosts);
  call-graph + callers list in the security summary; the two ⚠s.
- `examples/shop`: billing package (fake processor — deterministic
  receipt ids, 422 on zero amounts); PlaceOrder charges before
  storing; Order gains ReceiptID; tests — in-process Call, receipt
  on the order, billing's 422 surfacing through placeOrder, private
  exclusion note in the regenerated TS client.
- **Cloud errors & permissions:** revoked/missing `run.invoker` →
  the readable text ("shop-sa cannot invoke shop-billing — the
  derived grant run.invoker is missing; re-run tako deploy");
  callee cold-start timeout → the deadline error names the budget;
  404 from a stale callee URL → "billing's URL changed — re-run
  tako deploy". Taxonomy per docs/tako-cloud-permissions.md.

### 4. Not in scope (W9 non-goals)

- WithIdempotencyKey / retries (spec defers explicitly); signed
  user-identity propagation (spec: off by default — a later opt-in);
  VPC / ingress=internal (later WithVPC with stated costs);
  `topology:` co-location config (the endgame, not the slice);
  streaming; cross-MODULE calls (packages share the app's module).

**Validation** (agent-side; the human gate is W11): analyzer goldens
incl. cycle + raw-func error texts; shop suite (breakpoint story =
tests step through Call in-process); cloud on tako-test-01 — deploy
the split shop, curl billing directly → 403 at the front door, place
an order through the site → receipt from a real OIDC hop, REVOKE
run.invoker by hand → next order fails with the readable grant error
(the gate criterion), re-deploy converges the grant back, deep-chain
and cold-start ⚠s judged, destroy. Transcript in the task log.

**STOP — awaiting DX approval (and the three §1 scope calls) before
implementation.**

## W9 — proposal amendment: automatic session propagation (user-directed, 2026-07-27)

§1 gains a fourth approved call: **cross-service calls carry the
user automatically.** The stub attaches the caller's session ID as a
header when the calling ctx has one; the callee resolves it against
the SAME `_tako_session` store every service already reads (W5's
Datastore-backed sessions are what make this cheap — verification is
a record lookup, not crypto). `ctx.User()` in billing is alice, the
same verified, revocable identity as in shop; a worker or cron
calling the same endpoint has no session and `MaybeUser()` is
honestly false; deleting the session record kills the identity
mid-chain. WithAuth on a private endpoint = IAM-authenticated caller
AND a signed-in user behind it. No new exported symbols — automatic,
so the F10 freeze holds. Spec fix-up recorded for §2.12's "off by
default" sentence: that caution priced the SIGNED-header design; the
session-record architecture replaces it. Threat model documented
with the feature: presenting a session id is equivalent to
presenting the session cookie — no new authority is minted.
Service identity stays at the IAM layer (no ctx.Caller() in v0).

## Stage 3 pacing note (user-directed, 2026-07-27)

The user directed mid-W9: proceed through the remaining slices WITHOUT
per-slice stops — the next human check is the W11 milestone gate. W10's
DX proposal is still WRITTEN here (the record stands and is reviewable
at the gate), but it auto-proceeds with its recommended options
instead of pausing.

## W9 — implementation + agent-side validation: cross-service Endpoints (2026-07-27)

Landed as approved with all four calls (9255a56 + the billing boot fix
below): route-as-wire, X-Tako-Deadline 500ms/1s, deploy-⚠s for
economics, AUTOMATIC session propagation.

**Discoveries while building:**
- Call cycles need NO analyzer rule: calling b.Step requires importing
  b, and package = service makes any service cycle an import cycle Go
  itself refuses. The spec's "analyzer rejects call cycles" is
  satisfied by construction; §2.12 gets a fix-up note.
- The freeze held with zero new exported symbols: Endpoint learned its
  package at construction (reflection at declaration, never the
  request path) and Call consults a hooks registry the generated main
  fills — nothing registered means direct in-process, which IS the
  dev/test/co-located story.

**Validation transcript (agent-side; the human gate is W11):**
- Unit wire contract: OIDC bearer + session id + margin-reduced
  deadline + trace id on the wire; billing's 422 typed through;
  front-door 403 → the run.invoker remediation; sub-floor budget fails
  before the network; a callee resolves a propagated session id and
  rejects a forged one (the lookup IS the verification).
- shop suite: ChargedBy=alice across the in-process hop, 422
  verbatim, anonymous callers honestly anonymous, order carries its
  receipt; analyzer error fixtures raw-call + mixed-ingress pinned.
- Cloud (tako-test-01, two services): plan created billing with
  IAM-only ingress; summary printed "ingress: IAM-only (callers:
  shop-sa)" and "call graph: shop.PlaceOrder → billing.Charge"; the
  cold-start ⚠ fired (billing is scale-to-zero, priced honestly).
  Direct curl to billing → 403 at Google's front door. An
  email-minted session placed an order THROUGH the OIDC hop (receipt
  on the order); operator curl with a personal identity token +
  X-Tako-Session resolved chargedBy to the real uid — session
  propagation live on the callee. **THE GATE CRITERION: hand-revoked
  run.invoker → orders failed with the exact readable text naming
  the grant; re-deploy planned precisely `~ update iam grants run
  billing` and orders healed.** Destroy unregistered BOTH services'
  four sign-in domains and purged 10 entities.
- **Fixed during validation:** billing's generated main skipped
  runtime.Boot() (a resource-less package) so propagated sessions
  resolved to nobody — chargedBy came back empty in the cloud while
  local tests passed (TestApp boots everything). Codegen now boots
  when the APP declares auth, not just when the package declares
  resources. The fresh-grant IAM propagation window (~1–2 min) showed
  twice more, wearing the honest error text both times.

W10 (Jobs + the deferred Singleton, batch profile) proceeds without a
stop per the user's pacing directive; W11 assembles shop complete and
is the next human gate.

## W10 — DX proposal: Jobs + Singleton, the batch slice (2026-07-27; auto-proceeds per pacing directive)

Spec §2.7 verbatim: `tako.Job[T](name)` + `tako.JobHandler(job, fn)` —
long-running compute as a Cloud Run JOB (no request timeout), triggered
from any handler with `job.Run(ctx, params)`; `WithParallel(n)` maps to
task-count fan-out. Plus the W8-deferred `tako.Singleton(name, fn)`:
exactly-one long loop via min=1/max=1 + the §4.4 lease. Frozen surface
made true; nothing added.

### 0. The headline (shop's demands)

```go
// Settlement outlives any request deadline: sum the day's orders into
// a report, however long that takes.
var (
    settle      = tako.Job[SettleParams]("settle")
    Settle      = tako.JobHandler(settle, runSettle)
    StartSettle = tako.API("POST /api/settle", startSettle, tako.WithAuth())
)
func startSettle(ctx tako.Ctx, req SettleReq) (SettleStarted, error) {
    return SettleStarted{Started: true}, settle.Run(ctx, SettleParams{Day: req.Day, ThrottleMs: req.ThrottleMs})
}

// Sequential invoice numbers: the honest singleton — a strictly
// monotonic, bookkeeping-grade sequence is genuinely SERIAL work (Tx
// contention on a hot counter is the anti-pattern), and exactly-one
// across revisions is what min=1/max=1 + the lease guarantee.
var Invoicer = tako.Singleton("invoicer", assignInvoices)
```

### 1. Scope calls (auto-adopted, recommended options)

1. **Local jobs ride the queue dispatcher** — `Run` publishes to an
   internal `_job:<name>` queue whose consumer is the handler; tests
   get DrainQueues/WaitIdle semantics for jobs FOR FREE and the
   TestApp surface stays frozen. Cloud runs the real Cloud Run Jobs
   API. Alternative: bare goroutines (invisible to draining).
2. **Job params ride execution overrides** as
   `--tako-job-params <json>` container args; the generated job main
   decodes and runs. Retries are Cloud Run's maxRetries=3; v0 makes no
   Permanent distinction for jobs (documented). Alternative: params
   via a staging entity (an extra write + cleanup for nothing yet).
3. **Singleton is a min=1/max=1 run SERVICE** (concurrency 1, CPU
   always on) whose generated main campaigns DoLease("singleton-<name>")
   forever around fn and serves a bare health endpoint — reusing the
   entire run-service machinery instead of inventing a resource.
   Deploy handoff = lease handoff (spec §4.4 verbatim). fn returning
   is a restart with backoff, narrated; fn ctx cancels on lease loss.
4. **Triggering a job derives run.invoker ON THE JOB** (Cloud Run
   jobs accept the same role for jobs.run) + a
   `TAKO_JOB_<NAME>` physical-name env injection, mirroring W9's
   callee URLs.

### 2. Semantics being locked

- Job fn is the ONE async shape `func(Ctx, T) error`; local retries
  come from the dispatcher (5 × backoff → dead-letter JSON on disk),
  cloud retries from maxRetries=3 — both documented, not identical
  (jobs are compute, not messaging; the conformance contract stays
  queue-level).
- `Run` returns once the execution is HANDED OFF (queued locally,
  execution created on gcp) — never waits for completion.
- WithParallel(n) → taskCount n; the per-task index is NOT surfaced
  in v0 (no example demands it; documented pointer).
- Singleton fn: runs while the lease is held; loss cancels ctx; the
  loop re-campaigns. Two revisions during deploy = one holder,
  provably (the W8 machinery, reused).
- Analyzer: Job/JobHandler/Singleton leave not-yet; const names;
  `run` edges from Run call sites (the verb existed in the Edge
  vocabulary since C-phase); JobHandler without its Job (or twice) is
  an error; jobs partition into their OWN deployable (batch profile),
  singletons into min1 services.

### 3. Not in scope (W10 non-goals)

- Per-task index/sharding API; job progress/status queries; cron →
  job triggers (scheduler slice); Permanent for jobs; job-to-job
  chaining; execution overrides beyond params.

**Validation** (agent-side; W11 is the gate): suite (job through
DrainQueues, invoice numbers strictly sequential under concurrent
orders); cloud — deploy shop (job `settle` in the plan + run.invoker
on it, invoicer service min=1), trigger a settlement with ThrottleMs
sized to OUTLIVE the service request timeout (the gate criterion:
the execution completes anyway — judged from execution state + the
report entity), invoice numbers assigned by exactly one instance,
destroy removes the job. Transcript in the task log.

## W10 — implementation + agent-side validation: Jobs + Singleton (2026-07-27)

Landed per the auto-adopted proposal (d585a7b + 1bd9c4e live fixes).
Local jobs ride the queue dispatcher (DrainQueues covers them, the
TestApp wires them, failures dead-letter inspectably); cloud jobs are
real Cloud Run JOBS — params over --tako-job-params, maxRetries 3,
3600s timeout. Singletons are min=1/max=1 concurrency-1 services
looping DoLease("singleton-<name>") — the W8 machinery, reused whole.
The not-yet rule now covers only WithDomain/WithCDN.

**Validation transcript (agent-side; W11 is the human gate):**
- Suite: a settlement runs to completion through the local queue path
  (report 2 orders/1200¢), invoice numbers strictly sequential in
  arrival order and idempotent on re-sweep.
- Cloud (tako-test-01, FOUR deployables): plan created `run job
  shop-settle` + `iam grants runjob`; deploy printed the job line
  ("no URL, no request timeout"); invoicer live at min=1.
- **Six orders → invoices [1,2,3,4,5,6], strictly sequential**, all
  assigned by the exactly-one lease-holding instance.
- **THE GATE CRITERION: a settlement with throttleMs=60000 over 6
  orders — the trigger returned instantly, the execution ran the job
  body for 6m0.3s (DOUBLE the 300s service request timeout) and
  Completed successfully; reports/2026-07-27 reads 6 orders /
  4250¢, exactly the sum.** User deleted, destroy removed the job,
  all four services, four+ sign-in domains, 10 entities.
- **Fixed live (all wearing the honest error texts first):**
  (1) singleton loops never derived the framework-bucket lease grant
  — the invoicer 403-looped until grants covered kind singleton;
  (2) run.invoker does NOT include run.jobs.runWithOverrides — the
  params channel needs run.developer, now derived scoped to the one
  job; (3) the v0 aggregate-composite "never under-indexes" claim
  BROKE when orders' query shapes split — (owner, invoiced,
  -created) served neither query; shop sorts in memory and the fix
  is logged below.

**Discovered work:**
- Per-query composite derivation (the aggregate tuple under-indexes
  split query shapes — hit live; the graph comment's claim was
  falsified).
- Stale composite indexes from mid-development schema changes are
  invisible to destroy (three lingered; deleted by hand). Index
  orphan detection wants to exist.
- Job/singleton services reuse the async "OIDC push · redelivery"
  ingress text in the security summary — wrong flavor, cosmetic.
- WithProfile is accepted everywhere and extracted NOWHERE (all
  services run default knobs) — pre-existing, noticed while wiring
  job profiles.

## W11 — MILESTONE ASSEMBLED: shop, complete — awaiting the human gate (2026-07-27)

Stage 3's final gate is ready. validate/shop.md is the single walk
(local + cloud, F9 style) closing W7, W8, W9, and W10 — and with them
STAGE 3. Everything below is already agent-validated per slice
(transcripts above); the gate is the human's DX review of the whole:
the generated TS client as the frontend contract, four deployables
from one package tree, every trust edge live (private front door,
revoke-and-heal, six-minute settlement, exactly-one invoicer), and
the analyzer's error texts as product.

Full tree green at this commit; all five example modules green. The
verdict row in docs/validation-log.md closes the stage. Standing
requirement carried since W6: the Identity Platform manual step is in
docs/tako-cloud-permissions.md and MUST land in the final developer
documentation (a Stage 4 deliverable). Stage 4 decomposition
(scheduler, custom domains, per-query composites, the operator era)
follows the verdict.

## W11 gate-walk fixes: dev's multi-service shape, reload loop, orphan leash (2026-07-29)

Three dev-loop finds from the user's validate/shop.md walk:
1. The walking-skeleton "exactly one app service" guard predated
   §2.12 — dev now serves the one PUBLIC app service and collapses
   jobs, singletons, and PRIVATE callees (billing) into its process;
   Call is direct locally, so private route services need no server.
2. WithBuild's output (web/dist) retriggered the watcher every cycle —
   dev restarted forever. Build-output dirs are now the loop's own
   writes, filtered like .tako.
3. The recurring zombie class dies: dev's service child holds a stdin
   leash (a pipe only the dev process writes); when tako dev exits —
   Ctrl-C, SIGKILL, closed terminal — stdin EOFs and runtime.Serve
   shuts the child down. Verified live: kill -9 on the parent took
   the child with it, port freed. Four incidents of orphaned dev
   binaries holding ports/locks across sessions motivated this.

## W11 gate-walk fixes, round 2: session state made visible (2026-07-29)

User reports from the §3 walk, all addressed:
1. "Signed off immediately / state not preserved" — shop's page never
   REFLECTED the session: the header always read "sign in to buy",
   whoever was signed in. shop gains `GET /api/me` (a public probe,
   honest about anonymity) and the header now renders "signed in as
   alice · sign out" with a working sign-out (DELETE /api/_session +
   reload). The generated client picked up me() — 7 endpoints — and
   the alice/bob difference is now visibly whose orders render.
2. The settle 401 — validate/shop.md's `-b <session>` placeholder was
   unusable as written; §3 now carries the concrete two-line
   cookie-jar recipe (verified live: {"started":true} + the ⚙ line).
3. Invoice timing — behaving as designed; the script now says the
   judgment is that numbering happens BY ITSELF and sequentially, not
   that the 2s sweep delay is observable.

## W11 gate-walk fixes, round 3: reset story, dev bar state, §2 framing (2026-07-30)

1. Deleting .tako/ left an unstockable empty shop — the page had no
   add-product affordance. It now grows a "stock the shelf" form
   (signed-in users; addProduct through the generated client — more
   showcase, not less), and validate/shop.md §3 states the reset
   story: .tako/data IS the local database, deleting it is the
   documented wipe, and an empty shelf is the walk's real starting
   point.
2. The dev bar showed every control always, with inconsistent labels
   ("sign in as alice" next to bare "bob", sign-out while anonymous).
   It now follows the session: anonymous → "sign in as alice / as
   bob"; signed in → "signed in as X · switch to Y · sign out".
3. §2's purpose was unclear (why diff? why npx? is any of this the
   user's job?). Rewritten: the FINAL USER runs one command and their
   own toolchain typechecks; the diff and npx lines are THIS GATE
   proving determinism and strict-mode type soundness behind that
   command — explicitly not user workflow.

## W11 gate-walk find: fresh-repo image-propagation race (2026-07-30)

The user's fresh-cycle deploy failed at the first service create with
"Image '…' not found" seconds after the ⛅ push lines — Artifact
Registry eventual consistency on a just-created repo's first writes.
Remedy: re-run (stateless reconciliation converges). Documented in
validate/shop.md §6. **Discovered work (deferred while the user
deploys):** bounded not-found retry in the run-service create/update
wait, mirroring the C4 SA-429 pacing precedent, so first deploys on
fresh projects never trip on this.

## STAGE 3 COMPLETE — W11 verdict recorded (2026-07-30)

The user validated validate/shop.md (destroy deferred to later today
— the destroy judgment stands from three prior full cycles). Stage 4
decomposed in tako-agent-plan.md: R0 fit & finish, R1 example-driven
docs (the Identity Platform documentation requirement lands there),
R2 quick-start README, R3 API review 2 + publish. Per-slice gating
resumes — the batching amendment expired with W11.

## R0 — DX proposal: fit & finish the gates demanded (2026-07-30)

Four debts, all example-demanded by Stage 3's walks, no new SDK
surface:

### 1. Per-query composite derivation (the shop debt)

The analyzer today derives ONE aggregate composite per entity; shop's
split query shapes (owner+created in listOrders, invoiced-only in the
invoicer) proved it can UNDER-index — the documented "never
under-indexes" claim was false and listOrders lost its OrderBy to an
in-memory sort. The fix: scanQueries records one tuple PER QUERY
CHAIN (filters in struct order, then OrderBys), dedupes, drops
single-property tuples (served by built-ins). `Resource.Composite`
([]string) becomes `Composites` ([][]string); provisioning emits one
index per tuple. shop's listOrders regains
`.Where("Owner").OrderBy("-Created")`; gallery/shortlink derive
exactly what they derive today (one chain each).

### 2. Orphaned-index detection (the schema-churn debt)

Three stale composites lingered in tako-test-01 after mid-walk schema
changes — invisible to destroy (it deletes desired shapes only).
Observed-but-undesired MANAGED-KIND indexes become plan lines
(`orphaned (kept) datastore index …`) like every other orphan, and
`tako prune` deletes them WITHOUT --data: indexes are derivable from
code, never data. Kind-scoping keeps foreign indexes (kinds tako
never managed) untouched.

### 3. WithProfile extraction (the honesty debt)

WithProfile(tako.Batch|Latency) is accepted by the analyzer and
recorded NOWHERE — gallery's makeThumb has claimed batch since F5 and
runs default knobs. The handler's profile lands in the graph, worker/
subscriber services inherit it, deploy applies the knobs, and the
summary shows it. Api/page handlers with WithProfile set their whole
service's profile; conflicting declarations in one service are an
analysis error with both positions.

### 4. Summary ingress text for jobs and singletons (cosmetic)

`shop-settle` and `shop-invoicer` currently print the async "OIDC
push · redelivery" line. Jobs say "a Cloud Run Job — no ingress;
executions run on demand"; singletons say "ingress: none serving —
the loop runs while this instance holds the lease".

**Not in scope:** query-grouping beyond chains (Or/In arrive never in
v0); index MERGING optimizations; scheduler; domains.

**Validation** (then a normal human gate): analyzer goldens (shop
grows two composites; an under-index regression fixture pins the
shop shape), suites, and one full cycle on a FRESH project
(tako-test-02) judging: both shop composites in the plan, gallery's
worker with batch knobs live, a deliberately-orphaned index surfacing
in the plan and dying under prune, clean destroy.

**STOP — awaiting R0 DX approval.**

## Stage 4 amendment (user-directed, 2026-07-30)

Standing approval for the stage: proceed through R0–R3 without
per-slice permission stops; verdicts collect in the validation log
for the user's review sweep. NEW deliverable added to the stage: a
CLI UNIFORMITY review (docs/cli-review-1.md, api-review style) —
command discoverability, uniformity and explicitness of side
effects, the confirmation ladder (--yes vs typed names vs nothing),
the gen namespace — findings plus fixes.

## R0 + CLI review — implementation + validation transcript (2026-07-30)

R0 landed (6849c62) and validated live on FRESH tako-test-02:
- **Per-shape composites:** shop derives exactly ONE tuple —
  (owner, -created) for listOrders, whose OrderBy is back — while the
  invoicer's single-field filter correctly rides a built-in (the
  proposal guessed two tuples; the derivation is smarter than the
  proposal). Gallery/shortlink derive today's shapes unchanged.
  An analyzer test pins shortlink's exact tuple set.
- **WithProfile honored:** gallery-makethumb carries profile "batch"
  in the graph and LIVE knobs 2 cpu / 2Gi / concurrency 10 versus the
  app service's 1 / 512Mi / 80 — after claiming batch since F5 and
  running defaults ever since. Mixed claims in one service are
  tako/mixed-profile with both positions.
- **Orphan indexes:** planted a stray composite on kind photos; the
  plan surfaced `? orphaned (kept) datastore index photos (…)` while
  the legit index skipped; `tako prune` deleted it (stateless tier —
  indexes are derivable, never data) and left (owner, ready) READY.
- **Summaries:** jobs say "a Cloud Run Job — no ingress; executions
  run on demand"; singletons say "the loop runs while this instance
  holds the lease".
- Destroy left tako-test-02 empty; the R0 secret was removed too.

CLI review (683a634, docs/cli-review-1.md): the confirmation LADDER
was real but unstated — now stated in help and pinned in the review
doc: reversible/derivable = no ceremony; risky-recoverable = deploy's
y/N with --yes as the CI pre-answer for THAT PROMPT ONLY; data-gone-
forever = typed confirmations --yes never touches. Help regrouped by
side-effect tier with build --push called out at the boundary; bare
`tako gen` prints its namespace; --canary accepts both flag forms.
Deliberate non-uniformities (typed app name vs typed word) kept and
justified. R0 + the CLI review await the user's verdict sweep.

## R1–R3 — docs, README, API review 2 (2026-07-30)

R1 (45f22d6): docs/guide/ — five tutorial chapters grounded in the
real examples plus the local and cloud stories; the Identity Platform
manual step now lives in USER-FACING docs (docs/guide/cloud.md),
closing the W6 standing requirement. R2 (same commit): README.md —
five minutes from go install to a live URL and back down. R3
(docs/api-review-2.md): the freeze held wholesale — zero exported
symbols added/renamed/re-shaped since F10, everything behind it made
real and gate-proven; six rules re-checked and holding; no demotions.
Publication blockers that are the OWNER's: a LICENSE choice, the
push, the v0.1.0 tag, and the clean-machine README run (which IS the
R2+R3 gate). Stage 4's work is complete pending the user's verdict
sweep (R0/CLI, R1, R2, R3) and those owner actions.

## Stage 5 proposal — footprint escape hatch + the feature reference (2026-07-30) — AWAITING APPROVAL

User-requested, two asks: (a) an escape hatch where the user DEFINES
the compute footprint values instead of picking a named profile;
(b) alongside the quick README, a full step-by-step document per
feature. Proposed as two slices, S1 then S2 (S2 documents S1).

### S1 — `WithFootprint`: explicit compute, DX proposal

Profiles stay the recommended path (named intent, framework-owned
values, spec §4.6). Footprint is the escape hatch: pin exactly the
knobs you mean, inherit the rest.

```go
var Thumbs = tako.Subscribe(photos.Uploaded, makeThumb,
	tako.WithProfile(tako.Batch),          // optional base; else default
	tako.WithFootprint(tako.Footprint{     // overrides, field by field
		CPU:          4,                   // 1, 2, 4, 8
		Memory:       "8Gi",
		Concurrency:  4,
		MaxInstances: 25,
	}),
)
```

Semantics:
- **Layering:** resolved knobs = profile base (or "default"), then
  each NON-ZERO Footprint field overrides. Zero field = inherit.
  (Consequence: an explicit min=0 over a min=1 base is expressed by
  choosing a min=0 base profile, not by MinInstances: 0 — the zero
  value is "inherit". Recorded as a known trade; alternatives are
  pointers or sentinels, both worse to write.)
- **Projection:** like WithProfile — footprint claims project onto
  the SERVICE; conflicting claims in one service are
  `tako/mixed-footprint` with both positions (mirror of
  mixed-profile). Same footprint declared twice is fine.
- **Analyzer validation at build time** (`tako/footprint`, errors
  are product): CPU ∈ {1,2,4,8}; Memory parses and meets Cloud Run's
  CPU minimums (4 cpu needs ≥ 2Gi); 1 ≤ Concurrency ≤ 1000;
  0 ≤ Min ≤ Max ≤ 1000. Exact texts golden-tested.
- **Singletons:** CPU/Memory only; Min/Max/Concurrency are the
  lease's correctness knobs → positioned error explaining exactly-one.
- **Jobs:** CPU/Memory apply to tasks; Min/Max/Concurrency invalid
  (executions are not services) → positioned error.
- **Costs visible (rule 6):** deploy plan prints resolved knobs when
  they differ from the profile default; min>0 keeps the standing ⚠
  always-on-instances pricing line.
- Footprint values are part of the spec hash → changing them plans
  `~ update run service`.
- Non-goals: request timeout, execution environment, GPU — later
  explicit slices if an example demands them.
- Surface note: first exported-symbol addition since the F10 freeze
  (Footprint struct + WithFootprint). Legal as its own task, but if
  the owner wants v0.1.0 tagged from today's head, this lands as
  v0.2.0; tag order is the owner's call.
- Example demanding it (no feature without one): gallery's makeThumb
  gets the real values above — batch profile alone under-provisions
  image resize.

Gate S1: goldens (valid + all error shapes) + gallery live on a test
project showing the resolved knobs in the plan and on the service.

### S2 — The feature reference: step-by-step per feature

The guide (R1) is example-driven chapters; the README (R2) is five
minutes. Missing: a per-feature reference you can enter sideways.
Proposed: `docs/reference/` — ONE page per feature, every page the
same rubric so the shape teaches: **Declare** (the var, the
options) → **Use** (handler-side calls) → **Local** (what dev/test
actually do) → **Cloud** (what deploy creates, IAM derived, what it
costs) → **Test it** (TestApp pattern) → **Errors you'll meet**
(the analyzer/runtime texts verbatim) → **Not yet** (honest limits).

Pages (18): api, page+fragments, site, entity (queries,
transactions, composite derivation), queue+worker, topic+subscribe,
bucket+signed-urls+events, cron+singleflight, lock, job, singleton,
auth+sessions, cross-service calls+private, ts-client, cache,
secret+metrics, profiles+footprint (documents S1), cli (the ladder).
Index in docs/reference/README.md; guide and README link into it.
Every snippet lifted from a real example or compiled in a scratch
module — no pseudocode.

Gate S2: human enters three pages cold and follows them verbatim.

**STOP — awaiting Stage 5 approval (S1 DX + S2 scope + slice order).**

## Stage 5 approved + amended (user, 2026-07-31)

User: "proceed with everything here" — S1 + S2 approved, per-slice
permission stops pre-answered for the stage. The deferred shop
destroy on tako-test-01 is DONE and verified by the user
("everything worked there"). NEW slice added by the user: a website
example using HUGO and one using HTMX — same or different examples
delegated to the agent. Design call: journal already IS the deep
htmx SSR example, so ONE new example `blog` — a Hugo-generated site
(WithBuild with a real generator, unexercised until now) carrying
htmx dynamic islands (a comments fragment) — shows the two compose.
Stage 5 order becomes: S1 footprint → S2 blog example → S3 feature
reference (the reference then documents both).

## S1 — implementation + validation transcript (2026-07-31)

Landed (58c8d8f) and validated live on tako-test-02:
- **The declaration:** gallery's resizer pins ONLY what the batch
  profile can't know — `Footprint{Memory: "4Gi", Concurrency: 4,
  MaxInstances: 5}` — and both summaries carry it: gen graph prints
  `profile batch + footprint (memory 4Gi, concurrency 4, max 5)`.
- **Build-time judgments (all golden):** field errors name the legal
  set (CPU ∈ {1,2,4,8}; Mi/Gi form, 128Mi–32Gi; concurrency/max
  1–1000); RESOLVED errors judge the combination against the profile
  base (`CPU: 8` alone → "resolves to 8 vCPUs with 512Mi memory — 8
  vCPUs need at least 4Gi; pin Memory too"); mixed-footprint names
  both claimants; jobs/singletons refuse Min/Max/Concurrency with
  the honest scoping texts.
- **Live:** gallery-makethumb deployed with exactly
  {cpu 2, memory 4Gi} / containerConcurrency 4 / maxScale 5 /
  minScale 0 — the three pins landed, cpu and min INHERITED from
  batch; the app service beside it kept 1/512Mi/80. Second
  `deploy --plan`: 0 updates (footprint is spec-hash).
- **Cost at decision time:** a scratch module pinning
  `MinInstances: 2` printed at PLAN time `⚠ footprint on warmfp
  keeps 2 instance(s) always on — they bill continuously whether
  traffic comes or not` (follow-up: the warning moved from the
  post-apply summary to the plan — the plan is where you decide).
- Destroy left tako-test-02 empty; secret deleted.

validate/footprint.md is the gate script. Awaiting the user's
verdict row for S1.

## S2 — the blog example (Hugo + htmx), implementation + validation (2026-07-31)

User-requested website examples: hugo and htmx, "same if that makes
sense". Same: journal stays the deep htmx SSR chapter; blog is the
STATIC-GENERATOR story — Hugo owns the pages, tako owns the build
(WithBuild finally exercised by a real generator) and the dynamic
islands (comments fragments + entity).

Design points that survived contact:
- Fragment templates ride Hugo's static/ into public/templates — the
  subtree a Site never serves raw (verified live: GET
  /templates/comments.html → 404). Tests read the SAME files from
  site/static, so go test never needs hugo.
- The example DEMANDED a runtime fix: templates/layout.html was
  hard-required, but a generator-owned site renders only fragments.
  layout.html is now optional; site.Render without one says
  "fragments-only sites use site.Fragment" at call time. Journal
  (the layout path) stayed green.
- Comment identity = RelPermalink; the value round-trips into the
  no-JS redirect, so only site-local paths pass (422 otherwise —
  an open redirect is not a feature). Composite (post, created)
  derived from the one query.

Validated: unit suite green without hugo; dev loop reran hugo on a
content save (⛅ line, reloaded in 1.1s) and served the edit; both
comment paths live on tako-test-02 (htmx re-render with the comment;
no-JS 303 to the real static URL); deploy printed the honest
⚠ mutating-route-without-auth for anonymous comments; destroy named
`datastore kinds comments` under DATA and left the project empty
(one IAM-propagation wait on the fresh datastore.user grant, the
known window). validate/blog.md is the gate script; guide chapter
06-blog.md + README/guide indexes updated. Awaiting the user's
verdict row for S2.

## S3 — the feature reference (2026-07-31)

docs/reference/: 18 pages + index, ONE page per feature, every page
the same rubric (Declare · Use · Local · Cloud · Test it · Errors
you'll meet · Not yet; cli.md bends it and says so). Written by five
parallel agents each grounded in the actual code — snippets lifted
from the examples, error texts verbatim from the analyzer goldens
and runtime sources, the TS client's determinism claim re-proven by
running the generator twice — then reviewed: rubric uniform across
all pages, every relative link resolves, spot-checked verbatim texts
match their sources. README and the guide index link in; the
reference links back.

Fix-ups riding along: validate/footprint.md quoted the pre-move
wording of the always-on warning (it now prints WITH the plan and
names the service); footprint.md's warning example no longer
implies gallery triggers it.

**Discovered work (logged, not done):** WithRateLimit is declared in
options.go but consumed nowhere — no backend, no analyzer error, no
not-yet text. It should either gain the Cloud Tasks wiring the spec
sketches or refuse with a positioned not-yet like WithDomain/WithCDN.
Surfaced by the queue.md agent; queue.md documents it under Not yet.

Awaiting the user's verdict row for S3 (gate: enter three pages cold
and follow them verbatim).

## Gap audit — what remains unimplemented (2026-07-31, user-requested)

Four-way sweep: SDK surface vs consumption, implementation plan vs
repo, tech spec vs code, and the project's own logged deferrals.

**In-plan promises not delivered:** Cloud Scheduler for cron (Phase
H; crons NEVER fire in the cloud — deploy warns, runtime sets
cronInert; also gates cron expressions and makes gcp singleflight
moot); Cloud Tasks (Phase F; WithDelay local-only, gcp Publish
errors); WithRateLimit (Phase F; fully inert); custom domains
(Phase G; tako/not-yet); migrating tako rename (Phase I; detection
shipped, the command didn't).

**Silent traps found (worse than deferred — no error, no effect):**
WithRateLimit is accepted and dropped; in fact ANY option on
Queue()/Topic() declarations is silently swallowed — extractCarrier
never reads declaration options. Unlike WithDomain/WithCDN nothing
fails loudly. Also: gcp SignedPut does not enforce WithMaxSize at
all and silently drops wildcard WithContentType (local enforces
both; conformance skips); metrics are logs-only even in cloud (gcp
boot wires the LOCAL metrics logger); Cache in cloud is per-instance
(documented, at least).

**Usefulness gaps:** WithParallel provisions N tasks but there is no
task-index/count API — every task gets identical params, shard-blind
(and local runs ONE task regardless); TestApp cannot exercise the
WithAuth shim or routing (direct Call bypasses it) and testCtx
.IsHTMX() is hardcoded false.

**Doc debt:** spec Part 3's gallery listing still uses tako.Static /
tako.Templates — superseded by Site, never fixed up; wouldn't
compile.

**Deliberate not-yets confirmed honest:** WithDomain, WithCDN,
shared cache, VPC, Postgres, websockets, external topics, styled
error pages, multi-service dev routing, dlq replay, session cache,
email account-creation UI. Canary/promote/rollback, dlq inspection,
prune/destroy confirmations: verified COMPLETE.

Awaiting the user's pick of what to plan next.

## Stage 6 proposal — close the gaps (2026-07-31) — AWAITING APPROVAL

User picked ALL FOUR audit candidates. Proposed order: honesty
first (it fixes the surfaces the rest build on), then the three
feature slices. Each is one gate.

### T1 — Honesty batch (no new features; loud where silent)

- **Queue()/Topic() declaration options:** extractCarrier learns to
  read them; anything unknown → tako/invalid-option like every other
  constructor. WithRateLimit specifically → a positioned
  tako/not-yet ("rate-limited queues arrive with the Cloud Tasks
  feature — T3") so the trap closes TODAY even before T3 lands.
- **gcp WithMaxSize gets REAL:** V4 signed URLs support the
  x-goog-content-length-range condition — sign it, and the gcp
  backend enforces size like local does. The TS client and docs
  gain the required header on upload PUTs; conformance unskips.
- **Wildcard WithContentType stays local-only-enforced** (GCS cannot
  condition on a wildcard): documented in bucket.md + doc comment,
  exact types keep working on both.
- **TestApp.IsHTMX:** testCtx grows an opt-in — proposal:
  `app.Ctx(user).AsHTMX()` returning a ctx whose IsHTMX() is true
  (one new method on the TestApp ctx, not on tako.Ctx).
- **Spec Part 3 fix-up:** the gallery listing drops dead
  tako.Static/tako.Templates for the real tako.Site.
- (TestApp cannot exercise the WithAuth shim — RECORDED as a
  limitation in testapp docs; the fix is a routing-level test
  harness, its own future slice, not snuck in here.)
- Gate T1: goldens for the new errors; conformance green with the
  gcp size cap enforced live once (scripted curl over the limit →
  GCS 400).

### T2 — Cloud Scheduler: crons fire in the cloud

- No new user surface. Every Cron declaration provisions a Cloud
  Scheduler job (`+ create scheduler job <svc>-<var>`) targeting
  POST /_tako/cron/<name> on the service with OIDC as the service's
  own SA (the async-push pattern); runtime route verifies the
  identity and invokes the handler (singleflight lease already
  collapses overlap — now real across instances).
- Schedule syntax GROWS: "every <duration>" (mapped: every Nm →
  `*/N * * * *`-style; every 24h → `0 0 * * *`) plus raw 5-field
  cron expressions, both validated at analysis time with positioned
  errors. The warnInertCrons ⚠ dies; destroy deletes scheduler jobs.
- Preflight: cloudscheduler API; grants: scheduler's OIDC → run
  .invoker handled via the service SA (no new roles for the
  deployer beyond the API).
- Example demanding: gallery's Cleanup (every 24h, singleflight) —
  finally true in the cloud.
- Gate T2: gallery deploy shows the scheduler job; a fast test cron
  ("every 1m" on a scratch app) observed firing twice in the cloud
  with singleflight collapsing a forced overlap; destroy clean.

### T3 — Cloud Tasks: WithDelay in gcp + WithRateLimit for real

- Publish(..., WithDelay(d)) works on gcp: delayed messages route
  through a Cloud Tasks queue (HTTP task → the worker's push
  endpoint, OIDC, scheduleTime = now+d). Queue(name,
  WithRateLimit(n)) provisions the Tasks queue with
  maxDispatchesPerSecond=n and routes ALL its traffic through Tasks
  (spec §2.4: same API, provisioner's choice).
- Analyzer: declaration options now real (T1's extractor); a queue
  is Tasks-backed iff it declares WithRateLimit or any publish site
  uses WithDelay (edge-detected). Plan prints which backend each
  queue got.
- Local: delay already honored; rate limit → token-bucket throttle
  in the local dispatcher; conformance cases for both.
- Example demanding: shortlink grows link EXPIRY — creating a link
  with a TTL publishes an expire message WithDelay; a rate-limited
  webhook-notify queue caps outbound calls. (Shortlink is the queue
  chapter; expiry is a real product behavior.)
- Gate T3: conformance both backends; live: an expire lands ~d
  later on gcp (observed), rate cap visible in Tasks queue config.

### T4 — Job task sharding (WithParallel useful)

- New surface (the ONE addition): `ctx.Task() (index, count int)` on
  tako.Ctx — (0,1) everywhere except inside a parallel job task,
  where it reports CLOUD_RUN_TASK_INDEX/COUNT (gcp) or the local
  fan-out's index. Local RunJob runs N tasks (goroutines) honoring
  WithParallel instead of ignoring it.
- Example demanding: shop's settle gains WithParallel(4) and shards
  orders by ID hash across tasks — the report merges (idempotent by
  task-disjoint writes + the existing sequence lease for invoices
  stays serial).
- Gate T4: shop suite proves disjoint shard coverage locally;
  live: one settle execution shows 4 tasks, each logging its
  (index/count), report totals identical to the serial run.

Cost note: T2+T3 add two Google APIs (cloudscheduler, cloudtasks)
to preflight; both bill per-job/per-task at trivial volumes.

**STOP — awaiting Stage 6 approval (order T1→T2→T3→T4; say if you
want a different order or to trim any slice).**

## Stage 6 approved + amended (user, 2026-07-31)

"proceed; make sure that there's a escape hatch as well for
controlling the footprint in all of these just like with the APIs
and other computes." Amendment folded into T2: tako.Cron accepts
WithProfile and WithFootprint (projected onto the hosting service
like API/Page — a cron-only package finally has a way to shape its
service); T3's workers and T4's jobs already carry the S1 escape
hatch (workers full footprint; job tasks CPU/Memory). Scheduler/
Tasks queues themselves are not compute — nothing to shape there.

## T1 — honesty batch, implementation (2026-07-31)

- **Carrier declaration options validated:** Queue/Topic/Cache/Secret/
  Counter/Histogram declarations now read their options — unknown →
  tako/invalid-option naming what the constructor accepts (Cache
  keeps WithTTL); WithRateLimit on a Queue → positioned tako/not-yet
  pointing at the Cloud Tasks feature (T3 replaces it with the real
  thing). The silent-swallow trap is closed.
- **gcp WithMaxSize is REAL:** SignPut signs GCS's
  x-goog-content-length-range condition — signing it makes the header
  REQUIRED (a PUT without it fails the signature; with it, GCS caps
  the bytes). New tako.UploadHeaders(opts...) hands clients the
  value; the LOCAL proxy now demands the same header when MaxSize is
  set (403 with a pointer at UploadHeaders), so forgetting it fails
  in dev, not production. Conformance: EnforcesMaxSize param DELETED
  — both backends run the size-cap cases (+ a new
  missing-header-refused case); wildcard content-type remains the
  one pinned platform gap. Gallery's UploadTicket carries
  uploadHeaders (TS client golden gained the field).
- **TestApp.HTMX(as...):** a Ctx whose IsHTMX() is true — dual-mode
  handlers testable down both branches; contract pinned in
  extras_test.
- **Spec Part 3 fix-up:** tako.Static/tako.Templates (dead API — 
  wouldn't compile) replaced with the real tako.Site + site.Render.
- Docs: bucket.md upload-header contract; queue.md WithRateLimit now
  documented as LOUD.

## T2 — Cloud Scheduler for cron, implementation (2026-07-31)

Crons are real everywhere. internal/schedule grew both grammars —
"every <duration>" (validated to MAP onto unix-cron: minutes dividing
60, hours dividing 24, exactly 24h) and 5-field cron expressions
(*, */N, values, ranges, lists; Next() drives the local ticker) —
one parser for analyzer, runtime, and provisioner. Every cron
provisions `scheduler job <svc>-cron-<name>` (Cloud Scheduler v1;
no labels on Scheduler, so identity+spec-hash ride the description)
firing POST /_tako/cron/<name> with an OIDC token as the service's
own SA; the dispatch route verifies the Google-signed token
(pure-stdlib RS256, same discipline as the session path) against
deploy-injected TAKO_CRON_AUDIENCE/INVOKER — safe on public
services, default-deny locally (unit-pinned 401/403). warnInertCrons
is DELETED — the warning's condition no longer exists. Preflight
gains cloudscheduler.googleapis.com. Cron accepts WithProfile/
WithFootprint (the stage amendment): a cron-only package can shape
its service. Fixtures: cronschedule repurposed (a cron EXPRESSION is
now legal — the old fixture passed!) to the two new judgments;
cronopt text lists the grown option set. cron.md rewritten; stale
inert-cron references swept from job.md/validate-gallery.

## T2 — validation transcript (2026-07-31)

Live on tako-test-02 (validate/scheduler.md):
- **Gallery:** plan `+ create scheduler job gallery-cron-cleanup`; NO
  inert-cron ⚠ anywhere; the live job: `0 0 * * *`, Etc/UTC, POST
  …/_tako/cron/Cleanup, oidcToken as gallery-sa. The route trusts
  nobody: bare POST → 401, bogus bearer → 403 (same texts unit-pinned).
  Forced run: `gcloud scheduler jobs run` → `POST /_tako/cron/Cleanup
  → 200 (491.9ms)`, `cron ok cron=Cleanup took=475ms`.
- **Self-firing + exactly-one (scratch ticktock):** `every 1m` cron
  writing a tick then sleeping 90s. Ticks landed at :55:04 and :57:04
  — STRICTLY alternate minutes under an every-minute schedule, nobody
  curling anything: the schedule fires itself, and overlap collapses
  (Scheduler serializes attempts per job; the §4.4 lease guards
  every other trigger — W8's cross-instance proof stands behind it).
- **Two REAL fixes the gate caught:**
  (1) ticktock's framework-bucket lease grant VANISHED on first
  deploy — a policy write 400s when its member SA was created seconds
  earlier (IAM propagation), and the destroy-convergence carve-out
  swallowed it. grants.apply now retries through the window and only
  converges when the target truly reads back gone; otherwise it fails
  LOUDLY. (2) destroy of a pre-Tasks app failed observing cloudtasks
  (API never enabled): observation now treats a disabled API as an
  empty inventory (disabledAPI helper; scheduler+tasks Lists).
- Both destroys clean: scheduler jobs listed and deleted with
  everything else; 0 services, 0 scheduler jobs left.

## T3 — Cloud Tasks, implementation (2026-07-31)

One declaration, one broker choice: a queue that declares
WithRateLimit OR has a WithDelay publish site (detected AT THE CALL
SITE by the analyzer — Resource.Delayed) gets a Cloud Tasks queue in
front of its worker (`<env-name>-tako`, maxDispatchesPerSecond,
maxAttempts 5 mirroring the redelivery contract). The gcp backend
routes ALL of that queue's publishes through Tasks (the cap must see
every message): the task POSTs the standard push envelope to the
worker's /_tako/push with the WORKER's own OIDC identity — the same
trust edge as Pub/Sub push, one broker over. Deploy injects
TAKO_TASKS_<QUEUE>=resource|pushURL|workerSA; grants give publishers
cloudtasks.enqueuer on the queue + serviceAccountUser on the worker
SA (a new tasksQueue grant target). Locally the dispatcher paces
(SetRate slot schedule; generated mains emit SetQueueRate from the
declaration); TestApp does not pace. WithRateLimit validates 1–500
(tako/rate-limit). Shortlink is the demanding example: TTL'd links
publish their own expiry WithDelay (with a re-claim guard — expiry is
a claim about ONE link), and the archive walks at WithRateLimit(2).
HONESTY: Tasks has no dead letter — 5 attempts then GONE, stated in
queue.md's Not yet. validate/tasks.md is the gate script.

## T4 — job task sharding, implementation (2026-07-31)

The ONE surface addition of the stage: `ctx.Task() (index, count)` on
tako.Ctx — (0,1) everywhere except inside a parallel job task, where
Cloud Run's CLOUD_RUN_TASK_INDEX/COUNT flow through RunJobMain, and
LOCALLY the runtime finally honors WithParallel: N joined tasks per
execution with real shard indices (RegisterJob and the TestApp job
wiring both; hooks.HandlerParallel reads the declaration). sdk_lock
regenerated for the interface method (implementations: requestCtx,
testCtx, rerootedCtx forward). Shop's settle is the demanding
example: WithParallel(4), orders shard by fnv(ID)%count, each task
Puts an idempotent ShardReport, every task attempts the merge and
whoever sees all shards writes the identical report — no leader, no
lock, retry-safe (pinned by TestSettleShardsAreDisjointAndMergeOnce:
disjoint cover, exact totals, re-run doesn't double-count).
validate/sharding.md is the gate script.

## T3 — validation transcript (2026-07-31)

Live on tako-test-02 (validate/tasks.md), after TWO gate catches:
- **Ordering bug (mine, fixed):** tasks queues applied AFTER the
  grants pass, so the enqueuer grant found no target and converged to
  nothing — first publish 403'd with the honest grants-are-derived
  text. KindTasksQueue now applies with the pre-service
  infrastructure; the redeploy planned exactly
  `~ update iam grants tasksQueue …` and healed.
- **Preflight-as-UX:** the first deploy stopped with "the
  cloudtasks.googleapis.com API is disabled — enable it: …" — one
  gap, one copy-pasteable fix.
- **The delay, observed:** link fuse23201 created 13:41:38 with
  ttlSeconds 90 → the task's scheduleTime read 13:43:08Z (create+90s
  exactly) → GET went 404 at 13:43:11 — Cloud Tasks held the
  message, the service didn't. Both hops logged: shortlink-expire
  push 204 at :43:08, then shortlink-archive (through the
  rate-limited queue) 204 the same second.
- **The cap, live:** archive-tako describe → maxDispatchesPerSecond
  2.0, maxAttempts 5 (the mirrored redelivery count); local pacing
  pinned by TestSetRatePacesDelivery. Destroy listed and deleted
  both tasks queues; DATA named the kinds.

## T4 — validation transcript (2026-07-31)

Live on tako-test-02 (validate/sharding.md): email walker minted via
the shop recipe; 6 orders totaling 5250¢; POST /api/settle
{day:"t4"} → ONE execution, spec.taskCount 4, succeededCount 4, four
"job Settle ok" task logs (165–817ms). Datastore: reports/t4 =
{orders 6, totalCents 5250} — EXACTLY the placed orders — and all
four settle-shards (t4#0…t4#3) present, the disjoint subtotals the
merge summed. Local: TestSettleShardsAreDisjointAndMergeOnce pins
disjoint cover + idempotent re-run; Task() is (0,1) outside a job.
Destroy clean; walker account deleted; tako-test-02 pristine (0
services, 0 tasks queues, 0 scheduler jobs).

STAGE 6 agent-side work complete: T1 96aee41, T2 d23b059, T3
98ff235, T4 7b89082 (+ this validation commit). Awaiting the user's
verdict rows for T1–T4.

## Stage 7 proposal — the operator era (2026-07-31) — AWAITING APPROVAL

User priorities, in order: domains/CDN, metrics export, dlq replay;
plus a shared-cache PLAN (recipe or design, implementation optional).

### U1 — WithDomain + WithCDN: one load balancer, both stories

Cloud Run's legacy domain-mappings are regional and second-class; the
real architecture — and the reason domains and CDN are ONE slice — is
a global external Application Load Balancer with a serverless NEG:

- `tako.Site(..., tako.WithDomain("blog.example.com"))` provisions:
  global static IP, managed SSL cert, serverless NEG → the app
  service, url-map + HTTPS proxy + forwarding rule (plus the auth
  authorized-domain registration). HTTP→HTTPS redirect included.
- `tako.WithCDN()` (requires WithDomain — analyzer-enforced with a
  positioned error) flips Cloud CDN on the SAME balancer's backend:
  static assets cached at the edge, dynamic routes pass through.
- **The manual step is DNS, and it is preflight-as-UX:** the plan
  prints the A/AAAA records to create at the registrar; deploy
  converges once the cert provisions (can take ~15–60min on first
  DNS propagation — narrated, resumable, stateless like everything).
- **Cost honesty:** an ALB is standing money (~$18/mo + egress).
  The plan prices it with a ⚠ the way min-instances are priced.
- Example demanding: blog (a blog IS the thing that wants a domain
  and edge caching). New provision kinds: address, cert, NEG,
  backend-service, url-map, target-proxy, forwarding-rule.
- **Gate caveat:** the live walk needs a REAL domain the owner
  controls; agent-side validation covers derived objects, plan
  rendering, and refusal texts — the DNS-to-green-cert walk is the
  human gate (validate/domain.md will script it for your domain).

### U2 — Metrics export: Counter/Histogram → Cloud Monitoring

- The gcp backend finally implements backends.Metrics: custom
  metrics `custom.googleapis.com/tako/<name>` via monitoring v3,
  batched in-process and flushed every 60s (and at shutdown);
  histograms as distribution values, counters as cumulative.
- Grants: roles/monitoring.metricWriter (project) for every service
  that touches a metric edge — derived, like everything.
- Local stays logs (unchanged contract); observability.md points at
  Metrics Explorer with the exact metric names.
- Example demanding: gallery (uploads counter, thumb_ms histogram —
  declared since F8, cloud-visible at last).

### U3 — dlq replay

- `tako dlq <service> --replay <id>` and `--replay --all`:
  re-publish the dead-lettered payload to the worker's delivery path
  (Pub/Sub publish to the source topic with the original attributes),
  then ack it off the DLQ — replay is a MOVE, not a copy. Local
  mirror: re-dispatch from .tako/data/deadletter and remove the file.
- Ladder position: tier 2 (recoverable — the message returns to
  at-least-once delivery; a failing replay dead-letters again).
  --replay never composes with --ack in one invocation.
- Example demanding: gallery's dead-letter path (the boom caption).

### U4 — Shared cache: a PLAN, not (yet) a feature

Deliverable is docs/shared-cache-plan.md + a recipe:
- **Recommendation: recipe-first.** A Memorystore-class dependency
  (VPC connector, standing $$) contradicts the zero-infra defaults;
  the honest v0 answer is a documented RECIPE — cache-aside on an
  Entity with a TTL field and lazy expiry (works today, no new
  surface, consistent cross-instance for read-heavy small values) —
  plus guidance on when per-instance Cache is simply right.
- The plan documents the FUTURE in-framework shape if scale demands
  it: `tako.Cache(..., tako.WithShared())` backed by Memorystore,
  what it would provision (connector, instance, grants), its costs,
  and the trigger conditions ("revisit when…").
- reference/cache.md links the recipe; no SDK change.

Order: U1 → U2 → U3 → U4 (U4 is a writing slice; it can interleave).

**STOP — awaiting Stage 7 approval. One flag: U1's live gate needs a
domain you control — tell me which domain to script the walk
against (or approve with the agent-side gate only and walk DNS at
your own pace).**

## U1 — revised DX proposal: WithDomain on Cloud Run domain mappings (2026-07-31) — AWAITING APPROVAL

Stage 7 was approved with U1 specified as a global external
Application Load Balancer. **The user asked, before any code, what the
$18/mo actually buys and what the alternatives are.** The research
that followed changed the answer, so this supersedes the U1 section of
the Stage 7 proposal. The stage's own rule — "STOP between slices only
if something contradicts the approved proposal" — is why this stops.

### What the research found

Four options front a Cloud Run service. All four serve static AND
dynamic, because in tako a Site is EMBEDDED IN THE SERVICE BINARY
(W1): `examples/blog` is one package, one service, Hugo static in
`site/public` plus Page handlers at `/frag/comments`. There is no
separate static origin to route to.

| option | standing | per GB | CDN | verdict |
|---|---|---|---|---|
| global external ALB + serverless NEG | **$18.25/mo** | $0.008/GiB processing + egress; CDN $0.08/GiB + $0.0075/10k lookups | real switch | Google's recommendation; only path to Cloud Armor / IAP / path routing / static IP |
| Firebase Hosting rewrite | $0 | 360 MB/day free, then **$0.15/GB** | included, always on | GA, but ~6 REST calls, globally-unique site IDs, v1beta1 customDomains, Firebase ToS |
| **Cloud Run domain mappings** | **$0** | Cloud Run egress **~$0.085–0.12/GB** | none | one REST call, auto certs — Google labels it preview |
| Cloudflare in front | $0 | free tier | yes | outside GCP; unprovisionable by a GCP-only reconciler. Recipe only |

Corrections to the approved proposal's premises, in order of how much
they moved the decision:

1. **The $18.25 is real and is a floor.** GCP charges a FLAT
   $0.025/hour covering the FIRST 5 forwarding rules, not per rule —
   so the HTTP→HTTPS redirect rule rides free, and the figure is
   $18.25/mo whether the blog serves one request or none.
2. **Firebase Hosting does not save money; it costs slightly more.**
   Hosting bills transfer at $0.15/GB whether the response was an edge
   cache hit or a Cloud Run passthrough. Cloud Run's own egress is
   ~$0.085–0.12/GB and its free tier (2M requests, 180k vCPU-s, 360k
   GiB-s/month) absorbs v0-scale compute entirely. Hosting buys
   latency and fewer instance-seconds, NOT a smaller bill. This
   inverted the original recommendation.
3. **Neither the ALB nor Firebase can lock down Cloud Run ingress.**
   Serverless NEG backends and Hosting rewrites BOTH require the
   service to allow unauthenticated invocations. Handoff item 3 (the
   `internal-and-cloud-load-balancing` hardening) is therefore moot in
   every option — deleted from scope, not deferred.
4. **Firebase works fine on a project tako deployed itself.** "A
   Firebase project IS a Google Cloud project with additional
   Firebase-specific configurations enabled"; adding Firebase is
   additive (a `firebase:enabled` label, shared IAM/billing). Not a
   blocker — just not a reason.

### Decisions (user, 2026-07-31)

- **Backend: Cloud Run domain mappings.** Cheapest per GB, one
  provision kind, certs auto-issued and auto-renewed, and one mapping
  per service is exactly the shape the chosen scope wants.
- **Scope: any service** — sites and API-only alike.
- **`WithCDN` is RETIRED from the SDK**, not deferred. With no load
  balancer there is nothing for it to switch, and a permanent not-yet
  on a reviewed symbol is a lie we would carry forever.

### The preview risk, and why it is contained

Google says domain mappings are *"not production-ready and are not
supported at General Availability… not recommended for production
services"* (latency), limited to ~10 regions (asia-east1,
asia-northeast1, asia-southeast1, europe-north1, europe-west1,
europe-west4, us-central1, us-east1, us-east4, us-west1 — tako's
default us-central1 is in), root path only, 64-char limit, no
wildcard, TLS 1.0/1.1 not disableable.

`tako.WithDomain("blog.example.com")` is a DECLARATION OF INTENT.
Whether tako satisfies it with a domain mapping, a Hosting site, or a
balancer is tako's business. If Google retires domain mappings, the
applier is swapped and users redeploy — their code never changes.
That is the analyzer-plus-reconciler design doing its job, and it is
the whole reason the cheap option is not a trap.

**Honesty inversion:** the approved proposal called for a cost ⚠. There
is no standing money here, so a money ⚠ would be noise. The real cost
is MATURITY, so that is what the plan prints (text below).

### Two tasks, in order

**U1a — API task (its own commit, reopens api-review-2).** Retire
`WithCDN` from options.go; re-document `WithDomain` as a SERVICE-level
option rather than a site-level one. Regenerate testdata/sdk_lock.golden.
Update docs/api-review-2.md (the not-yet-pointer row), reference/site.md,
spec §2.11 + the §246–257 sample, and the `siteopt` fixture (which
currently uses WithCDN as its bad input). Existing user code calling
`tako.WithCDN()` stops compiling with Go's own `undefined:
tako.WithCDN` — the honest signal for a deleted symbol; no analyzer
rule pretends otherwise.

**U1 — the feature.** `WithDomain` accepted on `tako.Site`,
`tako.API`, and `tako.Page`, attaching to the OWNING SERVICE — the
same propagation `WithProfile` already uses (examples/gallery/app.go:79
declares it on a Worker and it lands on the service). **No new
exported symbol is required by the widened scope.**

### User code

```go
// examples/blog — a site's service gets the domain
var site = tako.Site("./site/public",
    tako.WithBuild("hugo --minify", "./site/public"),
    tako.WithDomain("blog.example.com"))

// examples/shortlink — an API-only service, no Site anywhere
var Create = tako.API("POST /links", create, tako.WithDomain("api.example.com"))
```

### CLI transcript

```
tako deploy · blog → tako-test-02 (us-central1)

  analyzing    1 service, 1 site, 3 handlers, 1 entity
  generating   .tako/gen
  preflight    ok (run, iam, artifactregistry, storage, datastore)
  building     1 service image(s), digest-pinned
  plan
    + create  domain mapping        blog.example.com → blog
    ~ update  run service           blog
  ⚠ blog.example.com uses a Cloud Run domain mapping — Google ships
    these as preview and does not recommend them for production
    (latency); no standing charge. Moving to a load balancer later is
    a tako change, not a change to your code.

  lease        acquired (memo@host on tako-test-02/blog/prod)
  ⛅ image      us-central1-docker.pkg.dev/…/blog@sha256:…
  ✓ blog → https://blog-1234567890.us-central1.run.app

  domain  blog.example.com  awaiting DNS
    create these records at your registrar:
      blog.example.com  A     216.239.32.21
      blog.example.com  A     216.239.34.21
      blog.example.com  A     216.239.36.21
      blog.example.com  A     216.239.38.21
    then re-run tako deploy — the certificate provisions once the
    records resolve (usually ~15 minutes, up to 24 hours).
```

Second run, records in place:

```
  domain  blog.example.com  certificate ACTIVE → https://blog.example.com
```

Deploys stay stateless and resumable: every run re-reads
`status.conditions` and narrates where the certificate actually is.

### Error texts

Analyzer (`path:line:col: message (tako/rule-id)`):

```
app.go:12:24: tako.WithDomain needs a bare domain name — no scheme, port, path, or trailing dot; got "https://blog.example.com/" (tako/domain)
app.go:12:24: tako.WithDomain needs a fully-qualified domain — "blog" has no dot (tako/domain)
app.go:19:31: package blog already declares the domain "blog.example.com" (app.go:12) — one service answers on one domain (tako/domain-conflict)
```

Deploy-time (region is a flag, not a declaration, so preflight owns it):

```
  ✗ Cloud Run domain mappings are not available in europe-west2.
    Supported: asia-east1, asia-northeast1, asia-southeast1,
    europe-north1, europe-west1, europe-west4, us-central1, us-east1,
    us-east4, us-west1. Deploy to one of these, or drop
    tako.WithDomain and use the run.app URL.
```

### Cloud errors & permissions

**Manual steps (two, both preflight-as-UX).** Domain OWNERSHIP
verification is required once per base domain per project, and it is
genuinely interactive (Search Console) — tako never fakes it:

```
  ✗ blog.example.com is not verified for tako-test-02.
    Verify the base domain once: gcloud domains verify example.com
    (opens Search Console), then re-run tako deploy.
```

Then DNS, as transcribed above. Both are narrated, both are resumable.

**Failure modes, distinct texts** (per the C-phase rule):
- **401** → the standing ADC text from `Classify`.
- **403 on `run.domainmappings.create`** → `you lack permission to
  create the domain mapping on <project> — grant: … --role
  roles/run.admin` (no new API: domain mappings live on
  `run.googleapis.com`, already in `preflightAPIs`, and `run.admin`
  is already a preflight perm — **no preflight gap to add**).
- **403 SERVICE_DISABLED** → the existing `disabledAPI` path; `List`
  gets the same treatment as every other observer so a destroy of an
  unrelated app never breaks.
- **409 already exists / mapped elsewhere** → `blog.example.com is
  already mapped in this project to <other> — a domain answers for one
  service; remove the other mapping or pick another name`. tako never
  sets `forceOverride`: silently stealing another service's domain is
  exactly the kind of destruction §4.6a forbids.
- **404 on the route** → cannot happen by construction: the mapping
  kind applies AFTER `KindRunService` (see apply order below).

**Runtime grants: none.** A domain mapping grants nothing and is
granted nothing — no `grants.go` change, no new target type.

### Provisioner shape

One kind, `KindDomainMapping`, appended to `reconcileOrder` after
`KindSchedulerJob`; destroy reverses. In deploy.go's explicit Apply
chain it lands AFTER `KindRunService` (the mapping names a live route;
this is the T3 ordering lesson applied) and needs no grant pass.

`pkg/provision/gcp/domains.go` — List/Create/Update/Delete + Kind()
against
`https://<region>-run.googleapis.com/apis/domains.cloudrun.com/v1/namespaces/<project>/domainmappings`.
`metadata.labels` IS supported on this resource, so the managed marker
is REAL tako labels — no description marker, no name-suffix hack.
Body: `{apiVersion: domains.cloudrun.com/v1, kind: DomainMapping,
metadata: {name: <domain>, namespace: <project>, labels: …},
spec: {routeName: <service>, certificateMode: AUTOMATIC}}`. LiveHash
reproduces `{"routeName": …, "certificateMode": …}` exactly.
`spec.routeName` is immutable in practice → re-pointing a domain at a
different service classifies as **ClassReplace**, so the plan says so
before anything happens. Not a long-running operation: no compute
operations waiter is needed, which deletes a whole chunk of the
approved plan's machinery.

### Examples and the gate

**Deviation, stated:** examples stay domain-free in the tree. A domain
is not inert — it demands a real DNS record and a verified base domain,
and `go test ./...` plus every routine deploy would drag both. The
declaration is covered by analyzer goldens and a plan-rendering test;
`validate/domain.md` adds `tako.WithDomain` to blog for the walk and
removes it after, the way validate scripts already parameterize on
`BASE=<cloud URL>`. This trades a literal reading of "no feature
without an example demanding it" for not wiring a manual DNS step into
the acceptance suite.

`validate/domain.md`, parameterized on `<your-domain>`:
1. Agent-side: goldens (both new rules), plan rendering with the new
   kind, the maturity ⚠, the unsupported-region refusal, the
   unverified-domain refusal, ClassReplace on re-pointing, destroy
   order and clean removal.
2. Live: verify → deploy → the A records printed → user creates them →
   re-run → certificate ACTIVE → `curl https://<your-domain>` →
   destroy leaves tako-test-02 pristine.

**Everything except certificate-ACTIVE is validatable agent-side with
a placeholder domain** (the mapping sits `awaiting DNS`; document that
a never-verified mapping still deletes cleanly).

### Recorded as designed-but-unshipped (Discovered work)

- **The load balancer path** — full object list, $18.25/mo floor, and
  what it uniquely buys (Cloud Armor, IAP, path routing across
  services, static IP, wildcard certs). Trigger: an example needs a
  WAF, or one domain fanning across several services.
- **Firebase Hosting path** — GA rather than preview, edge CDN
  included; trigger: domain mappings are retired, or an example needs
  edge caching that Cloud Run headers cannot deliver.
- Both go in `docs/domain-options.md`, linked from reference/site.md,
  so the swap is a decision with a written basis, not a rediscovery.

**STOP — awaiting approval of this revised U1 (backend, WithCDN
retirement, and the examples deviation), plus the domain to script
validate/domain.md against.**

## U1a + U1 — implementation and agent-side validation (2026-07-31)

Approved 2026-07-31 with "use a placeholder domain for now".

### U1a (010648d) — WithCDN retired

Symbol deleted from options.go; `WithDomain` re-documented site-level →
SERVICE-level (signature unchanged, so its sdk_lock row is untouched;
`WithCDN`'s row is gone). api-review-2 records the post-review removal.
The `siteopt` fixture used `WithCDN()` as one of its bad inputs and now
uses `WithSingleflight()`.

**Dead machinery removed with it:** `siteNotYet` held exactly two
entries, and U1 flips the other one on. An empty map plus its check is a
`tako/not-yet` rule that can never fire, so the whole mechanism went —
the T1 honesty standard applied to the analyzer's own code. The `notyet`
fixture was the ONLY coverage of `tako/auth-provider`, so it was renamed
to `authprovider` and reduced to that one case rather than deleted.

### U1 — the shape that shipped

`WithDomain` is accepted on `tako.Site`, `tako.API` and `tako.Page`, and
projected onto the owning SERVICE by `applyDomains` — the same pass
`applyProfiles` makes for `WithProfile`. That is what lets an API-only
service have a domain, which was the user's chosen scope, and it needed
NO new exported symbol.

Four rules, all golden-pinned, all reachable:
- `tako/domain` — shape only (scheme, port, path, trailing dot, no dot,
  wildcard, uppercase, empty/malformed label, illegal character, >253).
  Backend limits (the 64-char cap, the region list) are deploy's, so the
  analyzer stays backend-agnostic.
- `tako/domain-conflict` — one service, one domain.
- `tako/domain-unreachable` — a site's domain in a package where nothing
  serves public traffic.
- `tako/const-name` — a non-constant argument.

**A rule was redesigned mid-implementation because it was dead code.**
The first cut refused domains on job and async-only SERVICES — but
`WithDomain` is only accepted on API/Page/Site, so a worker or job can
never carry a claim (it fails as `tako/invalid-option` first) and those
branches could not fire. The genuinely reachable case is a SITE whose
package has no public service, so `applyDomains` now resolves the site's
MOUNTING service (codegen's `siteOf` choice) and errors when there is
none. Shipping an unreachable validator would have been the same
dishonesty T1 was about.

Provisioning is one kind, `KindDomainMapping`, last in `reconcileOrder`
(destroy reverses, so it comes off first). `pkg/provision/gcp/domains.go`
speaks `domains.cloudrun.com/v1` on the REGIONAL run host. Domain
mappings are Knative-shaped, so the managed marker is REAL LABELS and the
spec hash a real annotation — no description marker (scheduler.go) and no
name suffix (tasks.go). `spec.routeName` is immutable and there is no
update verb, so `Update` is delete+create, classified `ClassReplace` with
the consequence stated first. tako never sets `forceOverride` (a unit
test pins that): taking a domain another service holds is the silent
destruction §4.6a forbids — the 409 explains instead.

### Agent-side validation, live on tako-test-02

`ker.gs` was already verified on the project, so `blog.ker.gs` — a
subdomain with no DNS behind it — served as the placeholder and exercised
everything except a green certificate.

**Both preflight gates, live, before anything was built:**

```
preflight found 2 gap(s):
  Cloud Run domain mappings are not available in europe-west2 — deploy to one of asia-east1, asia-northeast1, asia-southeast1, europe-north1, europe-west1, europe-west4, us-central1, us-east1, us-east4, us-west1 (tako deploy --region <region>), or drop tako.WithDomain and use the run.app URL
  blog.example.com is not verified for tako-test-02 — prove you own it once: gcloud domains verify example.com (this opens Search Console), then re-run tako deploy
```

The verification check reads `run.googleapis.com/v1/projects/<p>/authorizeddomains`
and is advisory: if the LISTING fails we do not gate on it, because the
authoritative check is at create time and names the same fix. Verifying a
parent covers its subdomains.

**Plan** — the mapping plans last, after the run service, with the
maturity ⚠ and no money ⚠:

```
    + create  run service blog
    …
    + create  domain mapping blog.ker.gs
    7 creates, 1 updates, 1 skips, 0 orphans
  ⚠ blog.ker.gs answers on a Cloud Run domain mapping — Google ships these as preview and does not recommend them for production (latency); no standing charge. Moving blog to a load balancer later is a tako change, not a change to your code
```

**Deployed** (6m44s). Live object read back: labels `tako-app=blog
tako-env=prod`, annotation `tako-spec-hash=bd1e450d461fea87`, spec
`{routeName: blog, certificateMode: AUTOMATIC}`, status
`DomainRoutable=True`, `Ready=Unknown/CertificatePending`, and one
resource record. Re-deploy: **`skip domain mapping` — no drift**, so
LiveHash reproduces SpecHash against a real object.

**Security summary, judged:** `public ingress on blog.ker.gs AND its
run.app URL (a domain mapping adds an address, it does not close one)`.
Confirmed live: the run.app URL answered 200 throughout; `blog.ker.gs`
resolved to nothing (000), as a domain with no records must.

**Destroy** listed the mapping, deleted it FIRST, and left the project
pristine (0 mappings, 0 services). A never-resolved mapping deletes
cleanly — the documented claim, checked.

### Two UX bugs the walk caught and fixed

1. **The first deploy did not print the DNS records** — the one run that
   most needs them. Cloud Run populates `status.resourceRecords` a beat
   AFTER create returns, so the immediate read came back empty and fell
   through to a bare "certificate provisioning". `narrateDomains` now
   waits up to 30s for records or readiness.
2. **A converged re-run printed nothing at all.** `✓ no changes` returned
   before the narration — breaking the exact instruction the previous run
   had given ("re-run tako deploy"). The no-change path now narrates too:
   the mapping is unchanged, its STATUS is not.

A third finding, smaller: Cloud Run returns record names RELATIVE to the
zone (`blog`, not `blog.ker.gs`), which is what a registrar's host field
wants — printing the FQDN would get `blog.ker.gs.ker.gs` created. The UI
names the zone instead of rewriting the records, and an apex domain's
unlabeled records render as `@`.

### Deviations and decisions recorded

- **Examples stay domain-free** (approved deviation from "no feature
  without an example demanding it"): a domain demands a real DNS record
  and a verified base domain, which would drag both through `go test
  ./...` and every routine deploy. `validate/domain.md` adds the option
  for the walk and removes it after. Declaration coverage is golden and
  unit tests, not an example.
- **Ingress hardening deleted from scope, not deferred**: no option
  (mapping, Firebase Hosting, or serverless NEG) can restrict Cloud Run
  ingress — all three require unauthenticated invocations.
- The custom domain joins the auth authorized-domains set on deploy and
  is unregistered on destroy, alongside the run.app hosts.
- `tako gen graph` now names the domain on the service line.

### Discovered work

- **Load balancer and Firebase Hosting: designed, not shipped.**
  `docs/domain-options.md` carries the full four-way comparison with
  live pricing, the object list each would provision, their frictions
  (Firebase's globally-unique 30-char site IDs, its v1beta1
  customDomains API, its separate ToS; the LB's $18.25/mo floor and
  compute operations waiter), and the TRIGGER conditions for each.
- Domain mappings are preview; if Google retires them, the applier is
  swapped and users redeploy — no user code changes. That indirection is
  why the cheap option is not a trap, and it is written down.

## U1 — follow-up: the API-only path, proven and documented (2026-08-01)

User review of U1 asked whether a domain on an API-only service is
actually possible, and observed that tako has no concept of grouping
endpoints into a service — the grouping is derived. Both halves were
worth answering: the capability existed and worked, the DOCUMENTATION
did not.

**Proven live on tako-test-02** with `examples/shortlink` (API-only, no
`tako.Site` anywhere), `WithDomain("api.ker.gs")` on ONE endpoint
(`Create`):

- `gen graph`: `service shortlink (…, domain api.ker.gs)` — and the
  three worker services carved out of the SAME package carry none.
- Deployed (`--env u1`, 2m5s). Live mapping read back:
  `routeName: u1-shortlink`, labels `tako-app=shortlink tako-env=u1`,
  record `api CNAME ghs.googlehosted.com.`
- Security summary put the domain on the API service and left the three
  workers on `ingress: OIDC push from its own SA only`.
- Destroy `--env u1` removed the mapping first; example restored.

**The documentation gap, fixed.** `WithDomain` was only described in
`reference/site.md` — a page an API-only user has no reason to open —
and `reference/api.md` did not mention it at all, while its quoted
`invalid-option` golden still listed the pre-U1 option set (a stale
error text, which this project treats as a product bug).

- **New `docs/reference/domains.md`** is the option's home, because it
  belongs to no single constructor. It states the derivation explicitly
  — package = service, each worker/subscriber carved into its own, a
  site embedded in its package's app service — and shows shortlink's
  real four-service graph so "which service does this address?" has a
  visible answer. Also documents that envs do NOT prefix a domain
  (globally unique by nature), so two envs claiming one name is a 409
  the user resolves, not something tako papers over.
- `api.md`: option listed, stale golden corrected, and the distinction
  drawn — `WithAuth`/`WithPrivate`/scaling describe the ROUTE they sit
  on, `WithDomain` addresses the whole SERVICE.
- `pages.md` names it; `site.md` keeps the one-line framing and defers;
  `reference/README.md` indexes it. Domain error texts now live in ONE
  page so they cannot diverge.
- `validate/domain.md` gains §8, the API-only walk.

**Discovered work (not U1's):** redeploying shortlink to prod failed with
`create tasks queue expiries-tako: … a queue with this name existed too
recently` — Cloud Tasks blocks reusing a deleted queue's name for ~7
days, so a destroy/redeploy cycle inside that window cannot converge.
The T3 walk deleted those queues on 2026-07-31. tako has no text for
this; the raw API message is what surfaces. Worth its own honesty fix
(name the wait, suggest `--env`), unscheduled.

## Stage 8 proposal — the explicit service (2026-08-01) — AWAITING APPROVAL

Origin: the U1 review. `WithDomain` on an arbitrary API endpoint felt
wrong, and the inventory that followed showed the feeling was right and
not about domains at all — tako has THREE service-level options
(`WithProfile`, `WithFootprint`, `WithDomain`) declared at route level,
and a long list of decisions the user cannot reach at all.

User decisions, 2026-08-01:
- The four footprint profiles stay as they are.
- Job and Singleton must stop being forced profiles — both overridable.
- Every fixed knob (request timeout, CPU throttling, job timeout, job
  retries) becomes overridable; naming is mine to propose.
- Every messaging/retry constant becomes overridable; naming is mine to
  propose.
- Service naming should follow one pattern for API/Page/Cron the way
  workers already do (open question, below).
- **Option B adopted:** a package-level `tako.Service` declaration is
  the home for service-level options. Package = service HOLDS.

### V0 — the Latency lie (smallest, independent, do first)

`options.go:26` promises Latency means "CPU always on". `cpuIdle` is
never set anywhere in the repo, so Latency gets Cloud Run's default —
THROTTLED, identical to Default. The SDK documents a behavior the
provisioner does not implement.

Same comment claims "high concurrency" while the table gives Latency 40
against Default's 80. Here the TABLE is right — fewer concurrent
requests per instance is exactly how you buy tail latency — so the fix
is the comment, not the number.

V0 wires `cpuIdle: false` for always-on and corrects the comment. No new
surface; a doc-comment change plus one provisioner field. This is the T1
honesty standard applied to the one profile that promised the most.

### V1 — `tako.Service`: the keystone

```go
package blog

var svc = tako.Service(
	tako.WithDomain("blog.example.com"),
	tako.WithProfile(tako.Latency),
	tako.WithFootprint(tako.Footprint{Memory: "1Gi"}),
)
```

One per package, no per-handler binding — the exact shape `tako.Site`
and `tako.Auth` already have, so it introduces no new concept, only a
new home. Two-or-more in a package is `tako/one-service`, mirroring
`tako/one-site`.

**Where each option may be declared, after V1:**

| option | `tako.Service` | Worker/Subscribe/Job/Singleton | API/Page |
|---|---|---|---|
| `WithProfile`, `WithFootprint` | yes | yes | **no longer** |
| `WithDomain` | yes | no (unreachable) | **no longer** |

The principle: a service-level option is declared where it is
UNAMBIGUOUS. For a worker, one handler IS the service, so the handler is
an honest home (`examples/gallery/app.go:79` keeps reading well). For an
API service with twelve endpoints there is no honest home, so the
declaration moves to the service. Removing them from API/Page is a
breaking API change and its own task.

### V2 — the fixed knobs, named

Proposal: **fold per-service runtime into `Footprint` and the profile
table** rather than invent a second mechanism. Timeout and CPU
allocation resolve exactly like CPU and memory do — a profile default,
an optional override, one `Over()` layering. Two new columns, two new
fields, zero new machinery:

| profile | … | timeout | cpu always-on |
|---|---|---|---|
| default | … | 300s | no |
| latency | … | 300s | **yes** |
| batch | … | 900s | no |
| singleton | … | 3600s | **yes** |

```go
tako.WithFootprint(tako.Footprint{Timeout: 5 * time.Minute, CPUAlwaysOn: true})
```

Job execution is NOT service shape, so job knobs go on the JobHandler:

```go
var Settle = tako.JobHandler(settle, tako.WithParallel(4),
	tako.WithTimeout(2*time.Hour), tako.WithRetries(1))
```

`WithTimeout` reading as "how long one unit of work may run" — a request
for a service, a task for a job — is a deliberate reuse, not a clash.

**Alternative if `Footprint` should stay purely about SIZE:** a separate
`tako.Limits{Timeout, CPUAlwaysOn}` with its own `WithLimits`. Costs a
second struct and a second layering path for no behavioral gain. Not
recommended, but it is the honest fork in the road.

**Job and Singleton stop being forced** (`desired.go:220,223`): the
profile becomes the DEFAULT for those kinds rather than an overwrite.
Open question on singleton semantics below.

### V3 — delivery, named

Mirror `Footprint` exactly — a struct of optional fields over a table of
defaults, because that mechanism is already understood:

```go
var clicks = tako.Queue[Click]("clicks", tako.WithDelivery(tako.Delivery{
	MaxAttempts: 3,
	MinBackoff:  5 * time.Second,
	MaxBackoff:  time.Minute,
	RateLimit:   2,
}))
```

Defaults stay today's: 5 attempts, 10s→300s backoff, no rate cap.
`WithRateLimit` is absorbed as `Delivery.RateLimit` and REMOVED — the
`WithCDN` precedent, its own API task with its own `sdk_lock`
regeneration. (Keeping both would give one knob two spellings.)

Cron carries scheduler's constants, which are schedule facts, not
delivery facts:

```go
var Cleanup = tako.Cron("0 9 * * *", cleanup,
	tako.WithTimezone("America/Mexico_City"),
	tako.WithDeadline(10*time.Minute))
```

`WithTimezone` is the one with real user demand — "9am" means 9am
somewhere, and `Etc/UTC` is currently silent and unchangeable.

### V4 — service naming (OPEN QUESTION — needs your call)

Today: API/Page/Cron → `<package>`; Worker/Subscribe/Job/Singleton →
`<package>-<varname>`. You want one pattern.

With V1 there is finally a var to name the app service after, so
`<package>-<varname>` becomes available to it — `var api =
tako.Service(...)` in package `blog` → service `blog-api`, exactly the
worker convention.

**The cost is the reason this is a question, not a decision:** a service
NAME is its identity in every provisioned object. Renaming `blog` to
`blog-api` is a §4.6a rename — new service, new SA, new grants, new push
subscriptions, and the old ones become orphans the plan flags as rename
suspects. Every deployed app pays a full replace once.

Three ways, and I do not think this is mine to pick:

- **(a) Opt-in.** Declaring `tako.Service` names the service
  `<package>-<varname>`; not declaring it keeps `<package>`. Consistency
  when you ask for it, no forced migration, but two conventions coexist.
- **(b) Always `<package>-<varname>`**, with `tako.Service` required in
  any package that has API/Page/Cron handlers. Fully uniform; forces a
  declaration on hello-world and renames every existing app.
- **(c) Keep `<package>`** for the app service and accept that the app
  service is genuinely the package's, while satellites are named after
  the handler that spawned them. No migration, no new consistency.

If V4 lands at all it should land WITH V1 in one commit: two renames of
the same services across two releases is twice the migration for no
reason.

### Open questions

1. **V4 above** — (a), (b), or (c)?
2. **Singleton overrides: how far?** CPU and memory are plainly the
   user's. `MaxInstances` is not obviously so — the lease is what makes
   a singleton exactly-one, so `max: 3` is not incorrect, merely two
   instances paying to lose a race. Refuse it (`tako/singleton-max`),
   warn, or allow silently?
3. **`Footprint` vs a separate `Limits`** for timeout and CPU
   allocation — recommendation is fold, but it is a taste call about
   what the word "footprint" covers.

### Sequencing, and what it displaces

V0 → V1 (+V4) → V2 → V3. V1 is the keystone: it is the home V2's and
V3's declarations want, so it goes first even though V2 is the one that
started this.

Every slice from V1 on is an SDK surface change, so each reopens
api-review-2 and regenerates `sdk_lock` as its own task — the standing
rule, applied four times.

**This displaces U2 (metrics export), U3 (dlq replay) and U4 (shared
cache plan), which remain unstarted.** U1 also still holds an unsigned
gate verdict (validate/domain.md §§4 and 6 need a domain with published
DNS). Whether Stage 8 preempts U2–U4 or follows them is yours to say.

### Decisions on the open questions (user, 2026-08-01)

1. **V4 naming: opt-in (a).** Declaring `var api = tako.Service(...)`
   names the service `<package>-<varname>`; a package that declares none
   keeps `<package>`. No invented names, no ceremony in the quickstart —
   the R2 five-minute path is untouched. Two conventions coexist by
   design: the bare package name IS the honest name for a service nobody
   bothered to name.
2. **Singleton `MaxInstances` is REFUSED** (`tako/singleton-max`): the
   lease makes a singleton exactly-one, so a max above 1 only buys
   instances that pay to lose a race. CPU, memory and timeout are the
   user's; the shape that defines the primitive is not.
3. **Fold into `Footprint`** — no separate `Limits`. Timeout and CPU
   allocation are footprint columns, resolved by the same `Over()`.

Renaming breaks existing deployments and that is ACCEPTED: there are no
production apps yet, so the migration cost the naming question turned on
is zero. Examples are renamed with the slice.

**Sequencing confirmed:** Stage 8 (V0 → V1+V4 → V2 → V3) runs FIRST;
U1's human gate (validate/domain.md §§4, 6) is validated after it; then
U2, U3, U4. U2/U3 were going to be written against a surface V1 moves,
so this ordering avoids writing them twice.

**APPROVED 2026-08-01 — implementation follows.**

## V0 — the Latency lie, fixed (2026-08-01)

`options.go` promised Latency meant "CPU always on"; `cpuIdle` was set
nowhere, so Latency was throttled exactly like Default. Now wired:
`analyzer.CPUAlwaysOn(profile)` feeds the run spec, the applier emits
`resources.cpuIdle`, and `liveHash` reads it back.

The same comment claimed "high concurrency" while the table gave Latency
40 against Default's 80. The TABLE was right — fewer requests per
instance is how tail latency is bought — so the comment was corrected,
not the number, and footprint.md now explains why it reads backwards.

**Live on tako-test-02.** Latency: `run.googleapis.com/cpu-throttling:
'false'`, min 1, max 100, concurrency 40. Flipping back to Default
planned `~ update run service hello` and read back `cpuIdle: true`.

**The drift trap, checked in both directions.** Cloud Run's v2 GET
returns `cpuIdle: true` for a throttled service and OMITS it when false,
so a naive round-trip could have reported permanent drift on every
default service — the majority of them. Verified live both ways, and
`TestRunServiceEmitsCPUIdle` pins the round-trip offline. Re-deploy
converged to `skip`; destroy clean.

No new exported symbol (`CPUAlwaysOn` is analyzer-internal), so no
sdk_lock change; the SDK edit is a doc comment.

## V1 + V4 — tako.Service, and the name it gives (2026-08-01)

```go
package blog

var web = tako.Service(
	tako.WithDomain("blog.example.com"),
	tako.WithProfile(tako.Latency),
	tako.WithFootprint(tako.Footprint{Memory: "1Gi"}),
)
```

One per package (`tako/one-service`), optional, no per-handler binding —
the shape `tako.Site` and `tako.Auth` already have, so it adds a home
rather than a concept. `ServiceHandle` carries no methods: the
declaration IS the value.

**Where the three service-level options may now be declared:**

| option | `tako.Service` | Worker/Subscribe/Job/Singleton | API/Page | Site |
|---|---|---|---|---|
| `WithProfile`, `WithFootprint` | yes | yes | **refused** | n/a |
| `WithDomain` | yes | no (unreachable) | **refused** | **removed** |

The principle held to: a service-level option is declared where it is
UNAMBIGUOUS. A worker is one handler, so the handler IS the service and
`examples/gallery/app.go:79` still reads well. An API service has many
routes and no honest first among them. `tako/service-option` names the
move: *"tako.WithProfile describes the whole service, not one route —
declare it on tako.Service instead"*.

`WithDomain` came OFF `tako.Site` too. Site is unambiguous, so by the
principle it could have stayed — but two homes for one knob is the same
"one knob, two spellings" objection that made V3 absorb `WithRateLimit`.
One home.

**V4, opt-in as decided:** a declared `tako.Service` names the app
service `<package>-<var>` (`blog-web`); an undeclared package keeps the
bare package name. The rename lands on handlers BEFORE `partition()`, so
one pass does it. Satellites are untouched — they are already named
after the handler that spawned them.

### A third dead rule, removed

`tako/domain-conflict` cannot fire any more: `WithDomain` is accepted
only on `tako.Service`, and a package declares at most one. Handler-level
domain claims became impossible, so `Handler.Domain` went with the rule
and `applyDomains` collapsed to a projection plus the one reachable
refusal (`domain-unreachable`). Third time this stage that shipping the
option in its right place deleted a rule that existed to police the
wrong place.

### Validation

Fixtures reworked: `domain` (shape text + position — the exhaustive
shape coverage is `TestDomainProblem`), `domainunreachable`,
`serviceoption` (all three options on routes), `oneservice`;
`domainconflict` deleted. Unit tests pin the naming both ways.

**Live on tako-test-02:** blog with `var web = tako.Service(...)` →
`✓ blog-web → https://blog-web-…run.app`, SA `blog-web-sa`, domain
mapping `blog.ker.gs` on the renamed route, DNS records printed.
Destroy removed the mapping first, then `run service blog-web`. Clean.

`sdk_lock` regenerated: `+ func Service(opts ...Option) *ServiceHandle`,
`+ type ServiceHandle struct{}`. Deliberate, and the only additions.

Docs: new `reference/service.md`; `domains.md`, `api.md`, `pages.md`,
`site.md`, `footprint.md` and the reference index updated;
`validate/domain.md` §1 and §8 now declare through `tako.Service` and
expect the renamed service.

## V2 — the fixed knobs, unfixed (2026-08-01)

**Folded into `Footprint`, as decided** — no second mechanism. Timeout
and CPU allocation resolve exactly like CPU and memory: a profile
default, an optional override, one `Over()`.

| profile | … | Timeout | CPU between requests |
|---|---|---|---|
| default | … | 300s | throttled |
| latency | … | 300s | **allocated** |
| batch | … | 900s | throttled |
| singleton | … | 3600s | **allocated** |

Singleton allocates CPU because its loop runs BETWEEN requests — a
throttled singleton is a loop that stops running, which the primitive
never survived; V0 only noticed the Latency half of this.

**`CPUAlwaysOn` is a `Toggle`, not a `bool`.** In a struct whose zero
value means inherit, a bool cannot say "explicitly off". `Inherit`/`On`/
`Off` — the one new type, and the pattern any future boolean knob takes.

**Job and Singleton profiles became DEFAULTS.** `partition()` assigns
them; `applyProfiles` can override; `desired.go`'s two overwrite lines
are gone. The option was accepted and ignored before — the S6 audit's
exact complaint. Visible in the goldens: `shop-settle` now reads
`profile batch` where it used to claim `default` while the provisioner
quietly did something else.

**Singleton `MaxInstances` refused** (`tako/singleton-max`) as decided;
`MinInstances` and `Concurrency` too (they are what keep the loop's host
alive and alone). CPU, Memory and Timeout are the user's.

**`WithRetries(n)` on JobHandler**, 0–10. Jobs have no dead letter, so a
high ceiling would only promise something nothing catches. JobHandler
also accepts `WithProfile` now.

**Two ceilings, not one.** A service's request timeout caps at 60m, a
job task's at 24h. `footprintArg` cannot know which it is parsing, so it
refuses only what is impossible everywhere (>24h) and `applyFootprints`
refuses what is impossible for THIS service, naming `tako.Job` as the
answer for work that outlives a request.

### Live on tako-test-02 (`--env v2`)

shop's settle job with `WithRetries(1)` + `Footprint{Memory: "4Gi",
Timeout: 2*time.Hour}` read back: `maxRetries: 1`, `timeout: 7200s`,
`taskCount: 4`, `memory 4Gi`. Destroy clean.

**A text I falsified and fixed in the same slice:** the security summary
hardcoded *"retries: 3"*. The moment retries became declarable that
line became a lie, so it reads `jobRetriesOf(graph, svc)` now — verified
printing `retries: 1` live.

`sdk_lock` regenerated: `+ WithRetries`, `+ type Toggle`, `+ Inherit/On/
Off`, and `Footprint` gains `Timeout` and `CPUAlwaysOn`.

## V3 — delivery and schedule, declared (2026-08-01)

```go
var archive = tako.Queue[Expiry]("archive", tako.WithDelivery(tako.Delivery{
	RateLimit: 2, MaxAttempts: 5,
	MinBackoff: 30 * time.Second, MaxBackoff: 10 * time.Minute,
}))

var Cleanup = tako.Cron("0 9 * * *", cleanupOrphans, tako.WithSingleflight(),
	tako.WithTimezone("America/Mexico_City"), tako.WithDeadline(10*time.Minute))
```

`Delivery` mirrors `Footprint` exactly — a struct of optional fields over
a table of defaults (`DeliveryDefaults`: 5 attempts, 10s→300s, no cap) —
because that mechanism is already understood. **`WithRateLimit` is
absorbed as `Delivery.RateLimit` and removed**: one knob with two
spellings is worse than a breaking change (the `WithCDN` precedent).

**The bounds are the BACKENDS' bounds, stated as the framework's.**
`MaxAttempts` is 5–100 because that is what Pub/Sub's dead-letter policy
accepts, and the error says so rather than clamping a number the user
wrote down. `MinBackoff`/`MaxBackoff` cap at 10m; min > max is refused
("backoff grows, it does not shrink").

**`RateLimit` on a topic is refused.** A topic fans out to subscribers
with independent fates, so there is no single consumer to pace — the
error names the fix (cap the queue each subscriber feeds).

**`WithTimezone` is checked against the tz database AT ANALYSIS TIME**
via `time.LoadLocation`, so a misspelled zone is a build error rather
than a 3am surprise. `WithDeadline` is 15s–30m (Cloud Scheduler's range
for an HTTP target) and points at `tako.Job` for anything longer.

### Live on tako-test-02 (`--env v3`)

gallery with `Delivery{MaxAttempts: 8, MinBackoff: 20s, MaxBackoff: 2m}`
on the thumbnails queue and the cron above. Read back:

- push subscription `v3-gallery-makethumb`: `retryPolicy
  {minimumBackoff: 20s, maximumBackoff: 120s}`, `deadLetterPolicy
  {maxDeliveryAttempts: 8}`
- scheduler `v3-gallery-cron-cleanup`: `0 9 * * *`,
  `America/Mexico_City`, `600s`

Re-deploy converged to **55 skips, 0 updates** — the new hash fields
(timeZone, deadline, attempts) reproduce from live state, so no drift.
Destroy clean, scratch secret deleted, project pristine.

### The second falsified text, found the same way as the first

The security summary hardcoded *"redelivery: 5 attempts with backoff"*.
V3 made attempts declarable, so for a queue with `MaxAttempts: 8` that
line became a lie — exactly what V2 hit with *"retries: 3"*. Both now
read the declaration, and `TestSummaryReadsDeclaredCountsNotDefaults`
pins both so the third one cannot happen quietly.

That is the pattern of this whole stage: **every constant promoted to a
knob leaves a sentence somewhere that used to be true.** Worth grepping
for recited numbers whenever a default becomes declarable.

### Docs

`queue.md` gains the contract table and the four refusals; `cron.md`
gains timezone + deadline; `api.md`, `guide/02-shortlink.md`,
`validate/tasks.md` and the spec migrated off `WithRateLimit`;
`api-review-2.md` records all four Stage 8 surface changes in one table.
Historical records (C.md, api-review-1, validation-log, implementation
plan) keep the old name — they are records of what was true then.

**examples/shortlink migrated** to `WithDelivery` and is the demanding
example: archiving is idempotent and never urgent, so it declares slower,
fewer retries than the framework default — a real reason to override,
not a demo of the syntax.

## Stage 8 complete — agent-side (2026-08-01)

V0 51c6c18, V1+V4 0ec5659, V2 2f85be1 (+48b3860), V3 b787317. Full
suite green; all six examples green. tako-test-02 pristine (0 services,
0 scheduler jobs, 0 tasks queues, 0 domain mappings, scratch secret
deleted).

### What the stage was actually about

The user's question was "should WithFootprint be first class?" The
inventory that answered it found something narrower and worse: tako had
three service-level options declared at ROUTE level, and about a dozen
decisions the user could not reach at all. Both halves are now closed.

### Four things the stage kept finding

1. **Options in the wrong place breed rules that police the wrong
   place.** Retiring `WithCDN` emptied `siteNotYet` and killed the whole
   `tako/not-yet` mechanism. Moving `WithDomain` to `tako.Service`
   killed `tako/domain-conflict` and `Handler.Domain`. An earlier cut of
   `tako/domain-unreachable` was itself unreachable and had to be
   redesigned. Three dead rules, none of which could ever have fired.
2. **Every constant promoted to a knob leaves a sentence that used to be
   true.** "retries: 3" (V2) and "redelivery: 5 attempts" (V3) both
   became lies the moment their values became declarable. Now pinned by
   `TestSummaryReadsDeclaredCountsNotDefaults`.
3. **Accepted-and-ignored is the worst failure mode**, and it existed:
   `WithProfile` on a job or singleton was overwritten at provision
   time. The goldens carried the lie — `shop-settle` read `profile
   default` while batch was deployed. It reads `profile batch` now.
4. **Round-trips are where drift hides.** Cloud Run's v2 GET returns
   `cpuIdle: true` but OMITS it when false; a naive read-back would have
   reported permanent drift on every default service. Checked live in
   both directions.

### Ready for the U1 gate

`validate/domain.md` is updated for the post-Stage-8 shape (declarations
go through `tako.Service`; expect `blog-web`). Its agent-side sections
were re-walked live during V1 — blog deployed as `blog-web` with the
mapping on the renamed route, destroy clean.

**§4 (DNS → certificate ACTIVE) and §6 (a live re-point replace) need a
domain whose records you can publish.** Everything else is walked.

### Discovered work (unscheduled)

- **Cloud Tasks name reuse blocks redeploy for ~7 days.** Redeploying
  shortlink to prod failed with `a queue with this name existed too
  recently` after the T3 walk deleted those queues. tako surfaces the
  raw API message; it should name the wait and suggest `--env`. Worked
  around with `--env u1` throughout.
- `pkg/analyzer/analyzer.go` and `edges.go` are not gofmt-clean
  (pre-existing, unrelated to this stage).
- The agent plan (`docs/tako-agent-plan.md`) stops at Stage 4; Stages
  5–8 live only in this log and the Stage 7 handoff.

## Index waits — measured, then halved (2026-08-01)

User: "creating a datastore index takes a long time, and even when the
console shows it done it still hangs a little longer."

**Measured rather than guessed.** A probe index on tako-test-01, both
signals polled:

| moment | elapsed |
|---|---|
| operation `startTime` | 0:00 |
| index state → `READY` (serving; the console shows this) | **4:08** |
| operation → `done` (what tako waited on) | **5:50** |
| | **1:42 waiting after queries already worked** |

All 41 historical index operations on that project ran 5m50s–6m59s on
empty databases, so the ~6 minutes is a flat server-side cost, not
proportional to data. The 1m42s tail was ours.

### Two fixes

1. **Wait on the index's STATE, not the create operation.** `READY` is
   the condition that actually matters — it means the index answers
   queries — and it arrives ~1m42s earlier. The operation is no longer
   polled at all; `ERROR` state is the failure signal.
2. **`Create` starts a build and returns; `Wait` collects them.**
   Datastore builds indexes concurrently (visible in the July 24
   operations: two overlapping builds finishing the same second), but
   `Apply` is serial and `Create` used to block, so N indexes cost N ×
   6 minutes for work the server was happy to overlap.

`Wait` is called after the image push, so builds run under the grant
passes, the notifications and the push — most of a deploy — instead of
adding to them. It waits for every DESIRED index, not just the ones this
run created, so a deploy resumed mid-build still refuses to put services
in front of an index that cannot serve them (previously `List` reported
a `CREATING` index as present and the deploy sailed past it).

### Live, two indexes at once

```
  index        idxprobeA (x, y) building…
  index        idxprobeB (x, y) building…
both creates returned after 2.099s
  index        waiting for 2 build(s) — Datastore takes ~4 minutes per index
  index        idxprobeA (x, y) ready (4m0s)
  index        idxprobeB (x, y) ready (4m5s)
BOTH INDEXES SERVING after 4m7s
```

Old behavior for the same two: ~5m50s + ~5m50s = **~11m40s**. New:
**4m07s**. A single-index app drops ~5m50s → ~4m, and on a converged
deploy `Wait` is one list call that prints nothing.

### Note

Validating this collided with the user's in-flight U1 walk: a scratch
deploy of blog to tako-test-01 hit their live `blog.ker.gs` mapping. The
409 guard did its job — *"tako will not take it from whatever holds
it"* — so nothing of theirs moved, and the scratch env was destroyed
env-scoped, leaving `blog-app` and the mapping intact. The error path
got an unplanned live validation.

## U1 gate catch — the replace raced its own delete (2026-08-01)

The U1 walk's §6 (re-point is a REPLACE) failed live on the user's
tako-test-01:

```
  ⚠ replacing domain mapping blog.ker.gs (route → blog-web): delete + create…
update domain mapping blog.ker.gs: blog.ker.gs is already mapped in tako-test-01 —
a domain answers for one service, and tako will not take it from whatever holds it…
```

**Cloud Run's mapping delete is ASYNCHRONOUS.** `Delete` returns as soon
as the mapping is marked for deletion; the resource keeps answering GET
for a moment after. `Update` (delete + create, because `routeName` is
immutable and there is no update verb) created immediately and raced its
own teardown — then reported the 409 as if the USER were holding the
domain, which is the worst possible reading of a failure tako caused.

Measured live on tako-test-02: the mapping answers GET for **~1.4s**
after Delete returns. Small, and reliably fatal, because the create
fires in microseconds.

**Fix:** `Update` waits for the mapping to truly read back gone
(`waitGone`, 2s poll, 2m ceiling) before creating — converging on
OBSERVED state rather than on a call's return value, the same discipline
grant revocation already follows. Validated live end to end: the replace
now completes in ~1s.

State left by the failure: the mapping was deleted and not recreated, so
the domain had no mapping. The DNS records are unaffected, so a re-run
after the fix re-provisions without a second propagation wait.

### Discovered work

`waitGone` is validated live but not unit-tested: `gcp.Client` has no
injectable host, so no applier in `provision/gcp` can be tested against
a fake HTTP server. Adding that seam would make this whole package
testable offline (the `domains_test.go` round-trip tests already work
around it by exercising `body()` alone). Worth doing; not mid-gate.

## U2 — DX proposal (metrics export + graceful shutdown) — 2026-08-01

**Status: awaiting approval.** Two halves, one slice, in this order —
U2a exists because U2b needs it, and the user directed it explicitly
("this sigterm issue might also result in going into a lame duck mode
where inflight requests finish being processed and then no more
requests are accepted and then it shuts down").

The Stage 7 handoff assumed a shutdown path to hook. There is none:
`pkg/runtime/http.go:108` is `http.ListenAndServe`, and the only exit
is `os.Exit`. So U2a is not a metrics detail — it changes how every
tako service dies, and it is worth having on its own merits.

### U2a — the lame duck

**Behavior.** On SIGTERM (cloud) or SIGINT (Ctrl-C locally):

1. **Stop accepting.** The listener closes; new connections are
   refused, keep-alive connections are told `Connection: close`.
2. **Finish in flight.** Requests already executing run to completion.
3. **Flush.** Shutdown hooks run — in U2b, that is the metrics batcher.
4. **Exit 0.**

This is exactly `http.Server.Shutdown`, which is why the whole change
is small. What it buys beyond metrics: a revision swap stops cutting
responses mid-write, and Cloud Run's own "instance is draining" period
stops being indistinguishable from a crash.

**Budget.** Cloud Run sends SIGTERM and SIGKILLs 10s later
([container contract](https://docs.cloud.google.com/run/docs/container-contract)).
tako splits that: **7s drain, 2s flush, 1s margin.** Locally the drain
is 2s so Ctrl-C stays snappy. A second signal exits immediately —
the standard escape hatch for a wedged handler.

**Console.** Dev, where the drain is visible and interesting:

```
^C
⏻ draining — 2 requests in flight
⏻ stopped (0.3s)
```

Cloud, one line to stdout so it lands in the same log stream as
requests:

```
tako: SIGTERM — draining (3 in flight)
tako: stopped (1.4s)
```

When the drain budget runs out, the line names the consequence rather
than hiding it:

```
tako: 1 request did not finish within 7s — cutting it off (SIGKILL follows in ~3s)
```

In-flight count comes from an atomic in `logRequests`, which already
wraps every request.

**Three exits, not one.** All of these currently skip cleanup:

- `Serve` — the signal path above. Covers app services, workers,
  subscribers, singletons: every generated main funnels through it
  (`pkg/codegen/codegen.go:184,238,399,401`).
- `RunJobMain` (`pkg/runtime/job.go:66`) — runs hooks after the
  handler returns, on **both** the success and the `os.Exit(1)` failure
  path. A job that records metrics and then exits currently loses all
  of them.
- The dev stdin leash (`http.go:98`) — `os.Exit(0)` today, which runs
  no defers. Takes the same graceful path.

**Seam.** `hooks.Shutdown []func(context.Context)` — ordered, internal,
no new SDK surface. Backends register at boot.

### U2b — Cloud Monitoring export

**`pkg/backends/gcp/metrics.go`,** an in-process batcher behind the
existing two-method contract. Accumulate in memory; flush every 60s and
on shutdown to `projects.timeSeries.create`.

- Counters → `custom.googleapis.com/tako/<name>`, **CUMULATIVE INT64**,
  running total since process start, `startTime` pinned to process
  start.
- Histograms → same prefix, **CUMULATIVE DISTRIBUTION**, exponential
  buckets: `scale 1, growthFactor 2, numFiniteBuckets 18` → finite
  range 1…131072, plus under/overflow. One const.
- Never blocks a caller: `Add`/`Observe` take a mutex and return; the
  flush is a background goroutine.

**Why cumulative matters:** a dropped flush loses nothing. The next one
carries the true running total, because the total lives in memory, not
in the wire format. Only the shutdown flush is unrecoverable — which
is precisely why U2a comes first.

**Bucket sanity check against the demanding metric.** gallery's
`thumb_ms` (`examples/gallery/app.go:33`) times thumbnail generation on
small images — expect roughly 5–500ms, landing in buckets 3–9 of 18.
2× resolution is coarse for tight percentiles and honest for v0; the
doc says so rather than implying p99 precision tako can't deliver.

**Correction to the handoff — the resource is not enough.** The
handoff says to use the `cloud_run_revision` monitored resource. Its
labels are exactly `project_id, service_name, revision_name, location,
configuration_name` ([verified](https://docs.cloud.google.com/monitoring/api/resources))
— **no instance identity.** Every instance of a revision would write
the same cumulative series, and concurrent writers to one series is
either a rejection or garbage. So each point also carries a metric
label **`instance`** (metadata server `instance/id`, falling back to
`hostname-pid` like `leaseOwner()` already does). Series stay
per-instance; Metrics Explorer sums across them, and the doc says to.

**`location` needs a new env var.** `TAKO_REGION` is not injected today
(checked `pkg/provision/desired.go:231`). One line in the env block —
we know the region at deploy time, so deriving it from the metadata
server at runtime would be strictly worse.

**Failure handling** — fire-and-forget all the way down, since the
contract has no error return to surface anything through:

- 4xx → log the API text once, drop the batch. Retrying a malformed
  batch forever is worse than losing it.
- 5xx → one retry after 2s, then drop.
- 200 series max per call; batch and page.

**Wiring:** `boot.go:98`'s `hooks.Metrics = local.NewMetricsLog()`
becomes the gcp store. Local is unchanged — dev keeps its 📈 lines.

### Grants, preflight, tests

**Grant: `roles/monitoring.metricWriter` on the project, to every
service SA, whenever the app declares any metric.** The handoff asked
whether edges exist. They don't — `pkg/analyzer/analyzer.go:175` builds
counters and histograms as carriers, and only `Secret` gets a
`resourceVars` entry among the F8 primitives, so no usage edge is
derived and per-service derivation is impossible without new analyzer
work. This mirrors the secret precedent exactly (package-wide because
boot is package-wide), and the security summary will say so in those
words rather than implying tako narrowed it.

**Preflight:** `monitoring.googleapis.com` when any metric is declared.

**Conformance:** a new metrics chapter — the suite has none. Local
asserts in-process totals. For gcp, the flush is unit-tested against a
fake HTTP server by giving the store a `baseURL` field defaulting to
the real host. That sidesteps `gcp.Client`'s missing HTTP seam (the
same gap logged as discovered work for `provision/gcp`) without
widening the client in a slice that isn't about that.

**No new SDK symbols** — `Counter` and `Histogram` already exist, the
shutdown seam is internal. `sdk_lock.golden` does not move.

### Docs

- `reference/observability.md` — the "No Cloud Monitoring export"
  not-yet bullet goes; "no metric labels/dimensions" **stays** (still
  true for users). Gains: metric naming, the ≤60s flush latency, the
  Metrics Explorer path, the `instance` label and how to aggregate
  across it, and the cost line (ingestion-priced, scaling with metrics
  × instances — trivial at 2 metrics and a handful of instances).
- A lifecycle section for the drain: what SIGTERM does, the 7s/2s
  split, and that a handler outliving the budget is cut off.
- `reference/footprint.md` — `Timeout` caps one request; the drain
  budget is separate and shorter. Worth one sentence, because a 300s
  timeout next to a 10s SIGKILL invites the wrong assumption.

### Gate

`validate/metrics.md`: deploy gallery to tako-test-02, upload a photo,
wait ≤2 flushes, read back `custom.googleapis.com/tako/uploads > 0` and
a `thumb_ms` distribution; kill a revision mid-flight and prove the
drain (in-flight request completes, final flush lands); destroy clean.

### Open, and deliberately not decided here

- **Raising Cloud Run's 10s grace period.** It appears configurable,
  but it is a footprint knob and this slice does not add one. Logged as
  discovered work; the 7s/2s split assumes the default.
- **Stage 8 (V0–V4) has no gate verdict row.** Built under a "do this
  first" instruction, agent-side validated, ungated. Needs a call:
  its own gate, or ride along with U2's.

### Approved with amendments — 2026-08-01

User: *"approved, fold Stage 8 into U2's gate / do make the grace
period a footprint knob if possible, make other relevant settings knobs
as well / can't the region be read from the cloud run metadata for
location?"*

**1. Stage 8 folds into U2's gate.** `validate/metrics.md` grows a
Stage 8 section covering V0–V4; one verdict row covers both.

**2. The grace period is not tako's to give — corrected.** I proposed
logging it as a knob "if configurable". It is not. Checked both
discovery documents live:

```
$ curl -s "https://run.googleapis.com/$discovery/rest?version=v1" | … RevisionSpec
containers, timeoutSeconds, imagePullSecrets, serviceAccountName,
volumes, containerConcurrency, enableServiceLinks, runtimeClassName, nodeSelector

$ … version=v2 | … GoogleCloudRunV2RevisionTemplate
… timeout … (no grace/termination field)
```

No `terminationGracePeriodSeconds` in v1's `RevisionSpec` or v2's
`RevisionTemplate`, and no `gcloud run deploy` flag. **Cloud Run's 10s
SIGTERM→SIGKILL window is fixed.** (The "configurable up to 60 seconds"
claim I surfaced earlier came from a blog post, not the docs. It is
wrong for services.)

So the knob is not *how long* the grace period is — nobody can change
that — but **how tako spends it**:

- **`Footprint.Drain time.Duration`** — how much of the window goes to
  finishing in-flight requests; the remainder goes to flushing.
  Default 7s. **Refused above 9s** with a positioned analyzer error,
  because a value above the fixed ceiling is a promise tako cannot
  keep — the same discipline `Delivery.MaxAttempts` already follows
  ("tako will not quietly clamp yours"):
  `drain 30s exceeds Cloud Run's fixed 10s shutdown window — SIGKILL
  lands first, so anything above 9s is a promise tako cannot keep
  (tako/drain-range)`
- Profiles set it: `default`/`latency` 7s, `batch` 7s, `singleton` 7s.
  One number for now — no profile has a reason to differ yet, and a
  table column that never varies is a lie waiting to happen.

**3. Other settings promoted to knobs.** Cloud Run v2's
`ResourceRequirements` has exactly three fields, and tako set two of
them:

```
limits, cpuIdle, startupCpuBoost
```

- **`Footprint.StartupBoost Toggle`** ships. This is the same class of
  bug V0 fixed: `Latency`'s promise is tail latency, cold start is the
  dominant term, and tako was leaving the one knob that addresses it
  unset. Profiles: `latency` On, everything else Off.
- **Declined, no example demands them** (CLAUDE.md's rule):
  `executionEnvironment` (gen1/gen2), `sessionAffinity`, `h2c`,
  `healthCheckDisabled`, and the metrics flush interval. Listed here
  so the next reader knows they were considered, not missed.

**4. Region from the metadata server — user is right, adopted.**
`TAKO_REGION` is dropped from the proposal. `/computeMetadata/v1/instance/region`
returns `projects/<num>/regions/<region>`; the last path segment is the
`location` label. Three reasons this beats injecting it:

- The metadata server is **already required** for the `instance` label
  (Cloud Run injects no instance id env var, and its hostname is not
  one), so region costs nothing extra — same client, same boot.
- It cannot go stale or disagree with reality.
- Adding an env var to the service spec changes every service's spec
  hash, so it would have forced a revision swap on every app to carry
  a value the instance can already read.

The store takes `location` and `instance` as construction params so
tests inject them; `BootGCP` does the metadata lookup. If the metadata
server is unreachable, export is skipped with one logged line — that
means we are not on Cloud Run, which is not an error worth crashing a
service over.

### Knob review — 2026-08-01 (second pass)

User: *"I do want to expose sessionAffinity … A question about h2c
it's whether we are supporting HTTP2 for our server … Make sure to
remove configurations or knobs that have no impact."*

**`SessionAffinity` ships.** `Footprint.SessionAffinity Toggle` → v2
`RevisionTemplate.sessionAffinity`. Off in every profile — it trades
away even load spreading, so it is opt-in per service, never a
profile's idea. Refused on jobs (nothing serves) and on **singletons**:
max=1 means affinity has exactly one instance to choose from, which is
the definition of a knob with no impact.

**`executionEnvironment` stays out** — user's call, and the reason is
right: tako always wants the current generation, and a knob whose only
other value is "older sandbox" is a downgrade switch.

**(Superseded the same day — see "HTTP/2 shipped" below.)**

**h2c is not a knob we declined — it is a feature we have not built.**
`pkg/runtime/http.go` builds a plain `http.Server` and calls
`ListenAndServe`; nothing wires `golang.org/x/net/http2/h2c` (x/net is
an INDIRECT dependency — `go.mod:26`). Without TLS, Go serves HTTP/1.1
only. Cloud Run's `h2c` setting tells the front end to forward
**cleartext HTTP/2** to the container, so turning it on against today's
runtime would break every request, not merely fail to help.

Supporting it means: promote `golang.org/x/net` to a direct dependency,
wrap `Serve`'s handler in `h2c.NewHandler(h, &http2.Server{})`, then
expose the Cloud Run setting — in that order, because the setting is
only safe once the server can answer. Worth it for gRPC-style streaming
or many-small-requests clients; nothing in the five examples asks yet.
Logged as discovered work, NOT as a footprint knob.

**Dead config found and removed: `TAKO_DRAIN` was going into jobs.**
The env map is built once and shared by both the run-service and the
run-job specs, so every job container carried a drain budget for a
listener it does not have — while the analyzer refuses `Drain` on a job
footprint in the same breath. Now injected only on the service path,
pinned by `TestJobsCarryNoServiceOnlyEnv` (which asserts BOTH
directions: absent on jobs, present on services).

`sessionAffinity` is the **third** v2 field on this service that is
omitted-when-false (`cpuIdle`, `startupCpuBoost`, `sessionAffinity`),
so it went into `liveHash` with the other two and the drift-trap test
grew a `sticky` case — affinity is nobody's profile default, so an
explicit footprint is the only way it is ever true, which is exactly
the case a forgotten `liveHash` field would break.

## HTTP/2 shipped — and the answer to "can we do real TLS HTTP/2?" — 2026-08-01

User: *"I'd be interested in knowing if you can implement an HTTP2
server so we receive HTTP2 traffic not HTTP plain text but actual HTTP2
traffic; if this is not possible or is too complicated … then just do
the clear text HTTP2, although the non-clear text is preferred. Also
validate how this configuration is supposed to work on Google Cloud
Run."*

### The validation, first — TLS-terminating HTTP/2 is impossible here

Not hard, not complicated: **architecturally excluded.** Cloud Run's
frontend terminates TLS for every service and forwards to the container
without it. Google's own words, from the container contract and the
HTTP/2 page:

> "The container shouldn't implement any transport layer security
> directly. TLS is terminated by Cloud Run."

> "Google's frontend-serving infrastructure terminates TLS and then
> forwards the `h2c` traffic to Cloud Run and to your container through
> an encrypted channel."

Three consequences worth writing down, because the preference in the
question rests on an assumption the platform does not permit:

1. There is no configuration, port, or certificate arrangement that
   lets a Cloud Run container terminate TLS. h2c is not the fallback —
   it is the ONLY shape HTTP/2 can take on the last hop.
2. **"Cleartext" describes the application protocol, not the wire.**
   The frontend-to-container hop is encrypted by Google's
   infrastructure either way, so h2c does not expose traffic that TLS
   would have protected.
3. The client-facing half is **already** HTTP/2 over TLS with or
   without this setting. Browsers negotiate h2 with the frontend today.
   The knob only decides whether the frontend downgrades to HTTP/1.1
   before handing off — Cloud Run's default is to downgrade, except for
   native gRPC, which needs HTTP/2 and works by default.

So the user's preferred option loses nothing that was available, and
their fallback is the whole feature.

### What shipped

- **The server always speaks HTTP/2.** `Serve` wraps every handler in
  `h2c.NewHandler(…, &http2.Server{})`, which upgrades an HTTP/2
  preface and passes HTTP/1.1 through untouched.
  `golang.org/x/net` moved from indirect to a direct dependency.
  Unconditional on purpose: Cloud Run stops downgrading the moment the
  setting is on, so a container that could not answer h2c would fail
  EVERY request, not degrade. Always-capable makes the platform toggle
  unable to break anything.
- **`Footprint.HTTP2 Toggle`** → Cloud Run v2's port NAME
  (`containers[].ports[].name`: `h2c` or `http1` — the API's whole
  vocabulary for this). Off in every profile, matching Cloud Run's own
  default. Refused on jobs and singletons.
- Local check, the one Google's docs prescribe:
  `curl -i --http2-prior-knowledge http://localhost:8080/`

### The bug this turned up in U2a

**`http.Server.Shutdown` does not wait for HTTP/2 requests.** h2c
hijacks the connection; `Shutdown` marks a hijacked conn
`StateHijacked` and stops tracking it, so it returns while HTTP/2
streams are still running. The lame duck built two commits ago would
have silently covered HTTP/1.1 only — and would have looked correct,
because every existing drain test used HTTP/1.1.

Fixed with `waitIdle`: after `Shutdown` returns, wait for the in-flight
COUNTER to reach zero inside the same budget. The counter lives in
`track()`, above the protocol, so it is the one thing that knows about
a hijacked request.

**Proven, not assumed** — with the `waitIdle` call disabled,
`TestDrainWaitsForHTTP2Requests` fails exactly as predicted:

```
--- FAIL: TestDrainWaitsForHTTP2Requests (0.40s)
    shutdown_test.go:257: an in-flight HTTP/2 request was abandoned —
    Shutdown does not track hijacked connections, so the counter must
```

`serveOn` in the test helper now mirrors `Serve`'s real wiring, h2c
wrapper included, so no drain test can pass against a server shaped
differently from the one that ships.

### Note for a later slice

`Footprint` is drifting from "compute values" into "the per-service
Cloud Run knob bag": `SessionAffinity` is routing and `HTTP2` is
protocol, neither of which is compute. The layering still reads fine
(profile + overrides) and splitting it would be an API change of its
own, so this is a naming observation, not a proposal.

## Drain removed; shutdown is adaptive — 2026-08-01

User: *"I'm also not interested on having the drain setting. It feels
very meaningless. Instead, detect when the last connection was closed,
when the last request was served, give it up to six to seven seconds …
and then flush metrics in the last two seconds. But if the connections
have already been completed before that six to seven seconds mark, then
flush immediately and shut down."*

Right, and the knob was worse than meaningless — it was a number a user
had to reason about to get behavior tako can just observe.

`Footprint.Drain` is gone: SDK field, `DrainSeconds` in the graph, the
profile column, the `TAKO_DRAIN` env var, the `drainrange` fixture, and
`Drain` from the job/singleton refusal texts.

**What replaces it is a watch, not a budget.** `drain` returns the
moment the in-flight count hits zero — it always did, via `Shutdown`
plus `waitIdle`; the ceiling was only ever a cap on the wait. So the
early-exit behavior was already there and the knob was decoration on
top of it.

What genuinely changed is the **flush budget**, which used to be
`10s − configured drain − 1s` — a constant derived from a setting
rather than from what happened. It is now measured from the moment
SIGTERM landed:

```go
flushBudget(signalled) = shutdownWindow − time.Since(signalled) − killMargin   (floor 1s)
```

So an instance that drains in 40ms — the common case, because Cloud Run
stops routing to an instance BEFORE it signals — hands ~9s to the
flush instead of the 2s a fixed split would have allowed. Time the
drain does not use is repaid rather than lost. A drain that runs to the
7s ceiling still leaves the floor.

Two tests dropped with their subject rather than being kept alive:
`TestJobsCarryNoServiceOnlyEnv` (there is no service-only env var
anymore) and `TestProfilesDrainInsideTheWindow` (no per-profile drain).
Replaced by `TestIdleInstanceDrainsImmediately` and
`TestFlushGetsWhateverTheDrainDidNotUse`, which test the behavior the
user actually described.

## PROPOSAL (not scheduled): splitting Footprint by what it configures

Raised by the user after `SessionAffinity` and `HTTP2` landed in
`Footprint`. Worth doing; needs its own task, because it is an SDK
change and CLAUDE.md says those are never a side effect.

### The problem

`Footprint`'s doc says it "pins explicit compute values." Today:

| field | what it really is |
|---|---|
| `CPU`, `Memory` | compute |
| `Concurrency`, `MinInstances`, `MaxInstances` | scaling |
| `CPUAlwaysOn`, `StartupBoost` | compute behavior |
| `Timeout` | request lifecycle |
| `SessionAffinity` | routing |
| `HTTP2` | wire protocol |

The last three are not compute in any reading. The name stopped
describing the type two slices ago, and the refusal texts show the
strain — "a job footprint sets CPU, Memory and Timeout only" now has to
enumerate five separate reasons why the rest do not apply.

### Option A — rename only (cheapest)

`Footprint` → `Shape`, `WithFootprint` → `WithShape`. One type, honest
name, mechanical migration.
*Against:* "shape" is vague, and the type keeps growing without a
principle for what belongs in it. Treats the symptom.

### Option B — split in two (RECOMMENDED)

Two option constructors, mirroring two questions a user actually asks
separately:

```go
// How much machine, and how many of them.
tako.Service(tako.WithFootprint(tako.Footprint{
    CPU: 2, Memory: "1Gi", Concurrency: 40,
    MinInstances: 1, MaxInstances: 100,
    CPUAlwaysOn: tako.On, StartupBoost: tako.On,
}))

// How requests reach it, and how long they may stay.
tako.Service(tako.WithTraffic(tako.Traffic{
    Timeout:         30 * time.Second,
    SessionAffinity: tako.On,
    HTTP2:           tako.On,
}))
```

`Footprint` keeps its original meaning and its name — no migration for
the fields people actually set. `Traffic` takes the three that never
belonged. `Concurrency` stays in `Footprint`: it is how many requests
fit in one instance, which is sizing, not routing.

*For:* every field's home is derivable from the type name; the
refusal texts split into two short ones ("a job has no traffic
settings — nothing serves") instead of one five-clause sentence;
profiles keep supplying both, so `ProfileKnobs` gains a second table
and loses nothing.
*Against:* two options where there was one, and `Timeout` moving is the
one genuinely breaking change for existing code.

### Option C — three-way (compute / scaling / traffic)

Splits `MinInstances`/`MaxInstances`/`Concurrency` out again into
`Scaling`. *Against:* CPU and concurrency are chosen together in
practice — gallery's `Memory: "4Gi", Concurrency: 4` is one decision,
and splitting it across two options makes the common case worse. Too
fine.

### Recommendation

**Option B.** Do it as its own task, with `sdk_lock.golden`
regenerated deliberately, the analyzer's `Footprint` split to match,
and one gate covering both option constructors. No production apps, so
`Timeout` can move outright rather than being aliased.

## Discovered work + a gate-script defect — 2026-08-01

### `tako destroy` has no `--plan` (user-reported)

`runDeploy` accepts `--plan` and stops after printing the plan;
`runDestroy` does not. Verified in `cmd/tako/deploy.go:784` — its arg
loop rejects anything unrecognized, so `tako destroy --plan` fails with
a bare `unknown argument "--plan"`.

The interesting part is that destroy **already builds the preview**: it
observes everything, prints `tako destroy · <app> (<env>) in <project>
— this deletes:` with every object, then a `DATA — deleted with the
app, gone forever:` block, and only then prompts for the app name. So
the information exists; there is just no way to see it **without
entering the confirmation flow**. Today the only way to preview a
destroy is to start one and deliberately type the wrong name.

That matters more than a missing flag usually would, because destroy is
the one irreversible command (§4.6a: data destruction is never
automatic). Being able to inspect it in CI, or in a review, or just
cautiously, without arming the prompt is the point.

Two things to fix together:

1. `--plan` on destroy: observe, print the same block, exit 0 before
   the prompt.
2. Destroy's unknown-argument error should list what it accepts, the
   way deploy's does (`tako deploy accepts --plan, --canary=<pct>,
   --yes, --project, --region, --env`). Right now it names the bad
   argument and nothing else.

Not scheduled — logged for its own slice.

### `gcloud monitoring time-series list` does not exist

Caught while making §4 of `validate/metrics.md` executable, after the
user reported the section did not say how to actually upload anything.
The gate script and `docs/reference/observability.md` both told the
reader to run:

```
gcloud monitoring time-series list --filter '…'
```

There is no such command. `gcloud monitoring` covers dashboards,
policies, snoozes and uptime checks — not time series:

```
ERROR: (gcloud.monitoring) Invalid choice: 'time-series'.
```

`alpha`/`beta` variants are not installed by default either, so they
are no answer for a walk that has to be runnable as written. The
command came in from the Stage 7 handoff (`docs/stage7-handoff.md:209`,
hedged as "or the API") and I carried it forward without running it.

Replaced everywhere with the REST call, **verified live** against
`run.googleapis.com/request_count` on tako-test-01 before being written
down:

```
curl -s -G -H "Authorization: Bearer $(gcloud auth print-access-token)" \
  --data-urlencode 'filter=metric.type="custom.googleapis.com/tako/uploads"' \
  --data-urlencode "interval.startTime=$AGO" \
  --data-urlencode "interval.endTime=$NOW" \
  "https://monitoring.googleapis.com/v3/projects/$PROJECT/timeSeries"
```

**The lesson is the one this project keeps re-learning:** a validation
step nobody has executed is a guess with a command prompt in front of
it. The gate would have blocked at §4 with a gcloud error that says
nothing about metrics.

§4 also now spells out the upload — service URL via `gcloud run
services describe metrics-gallery` (env prefix included), three
`curl -F` uploads of the §2 PNG, and a `read_metric` helper — plus what
each field in the response has to show and why an empty `location` is a
bug rather than a partial success.

## Gate catch — a named port with no number — 2026-08-01

First live deploy of U2 (gallery → tako-test-01, `--env metrics`) got
through preflight, plan, images, and a 3m9s index build, then died at
the first run service:

```
create run service metrics-gallery: create the run service: Violation in
CreateServiceRequest.service.template.containers[0].ports[0].container_port:
must be 0 < port_number < 65536. (HTTP 400 from run.googleapis.com)
```

**Cause:** the HTTP/2 slice sends the container port to carry its NAME
(`h2c` or `http1` — the v2 API's whole vocabulary for the protocol),
and I sent the name alone:

```go
"ports": []map[string]any{{"name": portName(...)}},
```

`containerPort` is not optional next to it. Omitted, it marshals as
absent and the API reads 0, which is a *violation*, not a default.
Every service in every app would have failed to create — the feature
was completely broken on the cloud path and completely green in tests.

**Fix:** send `containerPort: 8080` alongside the name, from a named
`defaultPort` const whose comment ties it to `pkg/runtime`'s `Serve`
fallback (Cloud Run injects PORT from this number, so the two must not
drift). `run_cpu_test.go` now asserts the port is inside Cloud Run's
stated range, which is the API's rule written down where a test can
enforce it.

### Why the tests were green

`TestRunServiceEmitsCPUIdle` marshals `body()` and unmarshals it into
the live-shape struct. That catches round-trip drift — which is what it
was built for, and it did catch three real ones — but it cannot catch a
payload the API REJECTS, because no API is involved. The test asserted
`ports[0].Name` and never looked at the number.

This is the **third** time the missing HTTP seam in `pkg/provision/gcp`
has cost a live failure (after `waitGone` and the domain verbs), and
the first where the bug was a straightforward schema violation a fake
server would have caught instantly. Promoting that from "discovered
work" to a scheduled slice now looks overdue: an injectable host on
`gcp.Client` plus a fake that validates required fields would have
turned a failed cloud deploy into a red unit test.

Recovery needed nothing special — deploy is stateless and resumable, so
a re-run picks up from the partial env (the index was already built and
read back READY).

## Gate catch — cloud_run_revision is not writable — 2026-08-01

The second live U2 failure, and the more serious one: **metrics never
arrived at all.** Reading them back returned the API's empty result,
`{"unit": "{not_a_unit}"}`, and the service logged (user-reported):

```
WARN tako: metrics flush failed … err="HTTP 400 …: One or more TimeSeries
could not be written: … resource.type=\"cloud_run_revision\" …: metric
cannot be written to cloud_run_revision, see
https://cloud.google.com/monitoring/custom-metrics/creating-metrics#which-resource
for a list of writable resource types"
```

**Cloud Monitoring permits user-defined metrics only against a fixed
set of monitored resource types, and `cloud_run_revision` is not one of
them.** Confirmed by listing the writable set off a live descriptor —
it contains `generic_task`, `generic_node`, `gce_instance`, the k8s and
aws types, `global`, and no Cloud Run type at all.

The Stage 7 handoff specified `cloud_run_revision`, and I verified its
LABELS against the docs (that is where the missing-instance-identity
finding came from) without ever checking that it can be WRITTEN to.
Verifying half a claim reads exactly like verifying it.

**Fix:** `generic_task`, which is the type Google points serverless
writers at, and whose schema fits better than the one it replaces:

| generic_task label | value |
|---|---|
| `namespace` | `<app>-<env>` — prod and a preview env never merge |
| `job` | the tako service name, as the user wrote it |
| `task_id` | the instance |
| `location` | the region |

`task_id` makes the custom `instance` metric label unnecessary — the
resource now carries instance identity natively, which is what
`cloud_run_revision` could not do and what the whole per-instance-series
argument was about. The revision moves to a `revision` metric label: no
extra series (`task_id` is finer-grained) and a metric shift becomes
traceable to a deploy.

Proven before writing the fix: a probe write to `generic_task` with
these exact labels was **accepted**, and read back. That is the step
whose absence caused this.

### Two failures, one cause

Both live U2 failures — the named port with no number, and this — are
the same class: **a payload the API refuses, in a package with no HTTP
seam, so every test passed.** The unit tests marshal `body()` and read
it back; nothing validates against the API's rules.

`pkg/provision/gcp` and `pkg/backends/gcp` both need an injectable host
(`gcp.Client` already wraps an `*http.Client`; `MetricsStore` already
takes a `base`, which is why its wire format IS unit-tested — it just
tested the wrong resource type). This has now cost four live failures
across the stage (waitGone, the domain verbs, the port, the resource
type). It should be scheduled, not carried as discovered work.

### Cleanup performed

The metric descriptors auto-created by the FAILING writes carry an
`instance` label the new code does not set. Deleted from tako-test-01
(note: the type's slashes must NOT be percent-encoded in the delete
path — encoded returns 400). The old revision recreates them on every
failed flush until it is replaced, so the delete is a post-redeploy
step; `validate/metrics.md` §4c now says so.

## Gate catch — §6 checked the wrong API, on the wrong service — 2026-08-01

User ran §6's V0 check and got no matches:

```
gcloud run services describe metrics-gallery … --format yaml | grep -i "cpuIdle\|startupCpuBoost"
```

Two independent defects, either of which alone makes the step
unrunnable.

**1. Wrong representation.** tako writes `cpuIdle` and
`startupCpuBoost` through the Cloud Run **v2** API. `gcloud run
services describe` renders the **v1/Knative** view, where those surface
— if at all — as `run.googleapis.com/*` annotations. Grepping v2 field
names against v1 YAML matches nothing and reads like a failed
assertion. §6 now queries v2 directly with a `knobs` helper.

**2. Wrong service — and no right one exists.** `metrics-gallery` runs
the `default` profile, whose values for both knobs ARE the defaults, so
even the correct v1 annotations would be absent. Worse, checked against
the graph:

```
service gallery              profile default
service gallery-onuploaded   profile default
service gallery-makethumb    profile batch + footprint
service gallery-notifyowner  profile default
service gallery-warmfeed     profile default
```

**No gallery service uses the latency profile**, and latency is the only
profile that sets either knob. V0's positive case was unobservable on
this app no matter which API you asked.

§6 now does both halves: the NEGATIVE case against the shipped app
(default and batch must read back `cpuIdle: true` and no
`startupCpuBoost` — proof tako does not set them spuriously), and the
POSITIVE case behind a one-line temporary edit.

**The edit goes on a SUBSCRIBER, not on `tako.Service`.** Declaring
`tako.Service` renames the app service to `<package>-<var>`
(`analyzer.go:207`), which would orphan `metrics-gallery` mid-walk.
`NotifyOwner` already owns its own service, so `WithProfile(Latency)`
there is an in-place update.

### Bonus: the live read confirmed three other things

Captured from the deployment while fixing this, and now the expected
output in §6:

- **V2 is real on the wire:** 300s for the default service, 900s for
  batch.
- **Footprint layering is real:** makethumb takes `cpu 2`/`timeout 900s`
  from the batch profile and `memory 4Gi`/`concurrency 4`/`max 5` from
  its footprint.
- **The port fix is live:** `ports: [{"name": "http1",
  "containerPort": 8080}]`.

Also worth recording: `minInstanceCount` is omitted when 0, joining
`cpuIdle`, `startupCpuBoost` and `sessionAffinity` in the
omitted-when-false family. Four fields on one resource where absent
means false — the reason `liveHash` has to mirror the API's shape
exactly rather than the shape tako sends.

# Stage 9 — DX proposals (U3, V5, V6) — 2026-08-01

**Status: awaiting approval.** Three slices, gated separately. U3 is
the feature; V5 is a small CLI gap the U2 walk exposed; V6 is the SDK
change already argued for and now scheduled.

## U3 — `tako dlq --replay`

Goal: a dead letter can be put back into at-least-once delivery, and
leaves the DLQ when it is. A MOVE, not a copy.

### Surface

```
tako dlq                              # list services + retained counts   (exists)
tako dlq <service>                    # peek, consumes nothing            (exists)
tako dlq <service> --ack              # drain what was printed            (exists)
tako dlq <service> --replay <id>      # NEW — re-deliver one, then remove
tako dlq <service> --replay --all     # NEW — re-deliver everything
```

`--ack` and `--replay` are mutually exclusive (tier-2 ladder: no typed
confirmation, both are recoverable — a replayed message that fails
again simply dead-letters again).

### Messages need stable ids, and today they have none

Peek prints payload, reason and age; nothing addressable. The cloud
path holds a Pub/Sub **ackId**, which is per-pull and useless as a
handle — the id you saw is invalid by the time you type it.

So the slice adds an `id` to the listing: Pub/Sub's `messageId` in the
cloud (stable for the message's life), the file stem locally. Peek
prints it, and `--replay` accepts **any unique prefix**, the way git
takes short SHAs:

```
$ tako dlq gallery-makethumb
gallery-makethumb — 2 retained
  14672041893  2m ago   attempts 5   decode image: unknown format
    {"photo":"izupocvqy7bva6ax","caption":"boom"}
  14672041955  2m ago   permanent    caption rejected
    {"photo":"a7x2k9wq1mz","caption":""}

$ tako dlq gallery-makethumb --replay 1467204189
↩ replayed 14672041893 → thumbnails (1 message)
```

### The decision this slice turns on: topic-sourced replay

For a **queue** it is easy — one worker, so republishing to the queue's
topic re-delivers to exactly the thing that failed.

For a **topic** it is not. A topic fans out to every subscriber, and a
dead letter belongs to exactly ONE of them. Republishing to the source
topic re-runs the message through the subscribers that already handled
it fine — duplicate emails, duplicate charges. Pub/Sub has no
inject-into-one-subscription API.

Three ways out:

**(a) POST straight at the failed subscriber's push endpoint**, with an
OIDC token the operator mints. What the Stage 7 handoff suggested.
*Against:* the CLI becomes the broker — no retry, no dead-lettering, no
attempt counter, so a replay that fails is simply lost; it needs the
operator to satisfy `run.invoker` on a private service (they hold
`run.admin`, which is not the same); and it has no local equivalent, so
local and cloud stop matching.

**(b) Publish with an addressing attribute and let Pub/Sub filter.**
Add `tako-replay-to: <subscriber>` and give every push subscription a
filter accepting only unaddressed messages or ones addressed to it.
*Against:* subscription filters are IMMUTABLE, so adopting this
replaces every existing subscription, and a filter expression is a new
thing to get wrong in a place where getting it wrong silently drops
production traffic.

**(c) RECOMMENDED — same attribute, filtered in the runtime.**
Republish to the source topic with `tako-replay-to: <subscriber>`; the
push handler acks-and-drops anything addressed to someone else.

*For:* no infrastructure change and no subscription replace; the
message rides the REAL delivery path, so retries, the attempt counter
and dead-lettering all behave exactly as they do normally — a replay
that fails again lands back on the DLQ, which is the semantics the gate
has to prove; and local can do precisely the same thing, so the
conformance suite covers both. `pubsubEnvelope` already parses
attributes (`pkg/runtime/push.go:37`) and just needs to return them.
*Cost:* the other subscribers each receive one message and ack it
without work. Cheap, and visible in logs rather than hidden.

Queue replays need no attribute at all — one worker, nothing to
address.

### Local

The CLI is a different process from `tako dev`, and when dev is down
there is no broker to publish into. So local replay **moves the file**
into `deadletter/replay/`, and the dispatcher drains that inbox on boot
and on each watch tick.

Processless, works whether dev is up or down, and keeps the CLI from
needing the bbolt lock. The move is also the "leaves the DLQ" half of
the contract, done atomically by rename.

### Errors

```
--ack and --replay do the opposite things to the same message — pick one
no dead letter matching "1467" in gallery-makethumb — tako dlq gallery-makethumb lists what is there
"146" matches 3 dead letters — use more characters
--replay needs a service (tako dlq <service> --replay <id>)
```

### Gate

`validate/dlq-replay.md`: gallery's `boom` caption dead-letters live;
`--replay` returns it; the worker fails it again (still boom) → back on
the DLQ, proving the move rather than a copy; then fix and replay for
the success path. A topic-sourced replay must show the OTHER subscriber
NOT re-running. Local equivalent scripted in the same file.

## V5 — `tako destroy --plan`

Found during the U2 walk. `tako deploy` takes `--plan`; `tako destroy`
does not, and rejects it with a bare `unknown argument "--plan"`.

Destroy already BUILDS the whole preview — every object, plus the
`DATA — deleted with the app, gone forever:` block — and then prompts.
The information exists; there is no way to see it without arming the
prompt. Today the only way to preview the one irreversible command is
to start it and deliberately type the wrong name.

```
tako destroy --plan --project p --env metrics
```

→ the exact block destroy prints today, then exit 0 before the prompt.
Nothing observed, nothing deleted, safe in CI.

Also fixed here: destroy's unknown-argument error should list what it
accepts, the way deploy's does.

```
unknown argument "--plna" (tako destroy accepts --plan, --project, --region, --env)
```

Gate: folds into the next walk that destroys anything — no separate
script.

## V6 — split `Footprint` from `Traffic`

The proposal argued earlier in this log, now scheduled. `Footprint`
says it "pins explicit compute values"; `Timeout` is a request
lifetime, `SessionAffinity` is routing and `HTTP2` is a wire protocol.

```go
tako.Service(tako.WithFootprint(tako.Footprint{
    CPU: 2, Memory: "1Gi", Concurrency: 40,
    MinInstances: 1, MaxInstances: 100,
    CPUAlwaysOn: tako.On, StartupBoost: tako.On,
}))

tako.Service(tako.WithTraffic(tako.Traffic{
    Timeout:         30 * time.Second,
    SessionAffinity: tako.On,
    HTTP2:           tako.On,
}))
```

`Concurrency` stays in `Footprint` — how many requests fit in one
instance is sizing, not routing. `Timeout` moving is the only genuinely
breaking change; no production apps, so it moves outright rather than
being aliased.

Falls out of it: the job and singleton refusal texts split into two
short ones instead of one sentence enumerating five unrelated reasons.

`ProfileKnobs` grows a second table; `sdk_lock.golden` regenerates
deliberately; the analyzer's `Footprint` splits to match. Gate:
`validate/footprint.md` extended to cover both option constructors.

## V6 landed — and a dead SDK field it exposed — 2026-08-01

`Footprint` split into `Footprint` (compute) and `Traffic`
(request-facing), Option B from the proposal. `Timeout`,
`SessionAffinity` and `HTTP2` moved; `Concurrency` stayed, because how
many requests fit in one instance is sizing.

`ProfileKnobs` gained `TrafficKnobs` beside it. That split is a small
piece of honesty on its own: batch differs from default in how long ONE
unit of work may run, not in how clients talk to it, and the two tables
now say so separately.

The refusal texts got the payoff the proposal predicted — one sentence
enumerating five unrelated reasons became two short ones:

```
a job footprint sets CPU and Memory only — executions are tasks, not
serving instances, so there is no concurrency, no scaling, no idle CPU
to throttle and no cold start to boost; …
a job's only traffic setting is Timeout — an execution serves nobody, so
there are no clients to keep affinity with and no connections to
negotiate HTTP/2 over
```

Note the job case is now RIGHT in a way it was not before: a job task
genuinely has a timeout (24h of it), and lumping that in with affinity
and HTTP/2 under one refusal had been hiding that.

### The dead field

Regenerating `sdk_lock.golden` showed `Drain time.Duration` still on
`Footprint`. **It was never removed.** The commit that dropped the drain
knob removed `DrainSeconds` from the analyzer, `TAKO_DRAIN` from the
provisioner, the profile column and the fixture — but the edit to
`options.go` was a scripted string replace that did not match, and I did
not verify it. The suite stayed green because nothing reads the field.

So for four commits `tako.Footprint` had a knob a user could set that
was read by nothing: the exact "accepted and silently dropped" failure
the S6 audit named and the codebase has a rule against. It is the second
time in this stage a silent no-op replace has slipped through
unverified — the fix is not care, it is asserting the match:

```python
assert old in s, "block not found — do not guess, look again"
```

Removed for real, with that assertion.

## Two gate-script defects of the same shape — 2026-08-01

User: *"I don't know how to generate the output referred to in 'The
analyzer refuses what Cloud Run would refuse'."*

§3 of `validate/footprint.md` listed error texts under "Break it on
purpose, read the texts, put it back" and **never named the command**.
The reader is told what they should see and left to guess how. Same
shape as §4 of `validate/metrics.md` ("upload two or three photos"),
and the same fix: exact edits, the exact command
(`go run ../../cmd/tako gen graph`), and real captured output.

Every transcript in the rewritten §3 was produced by editing
`examples/gallery/app.go`, running the command, and reverting — not
written from memory.

### And a fixture that had stopped testing its own name

Verifying §3's "a second, different footprint on another handler" claim
turned up that `testdata/errors/mixedfootprint/` used two `tako.API`
handlers — which since V1 are refused by `tako/service-option` before
`mixed-footprint` can fire. The fixture passed, its golden was green,
and it asserted **the wrong rule under the right name.**

That raised the real question: is `tako/mixed-footprint` reachable at
all now? Workers, subscribers, jobs and singletons each own their
service; `tako.Service` is one per package. Two claims on one service
looked impossible — the dead-rule pattern that has already cost three
deletions this stage.

It is reachable, through **crons**: they run in their package's app
service and accept `WithFootprint`, so two of them collide. Fixture
rewritten to use crons, and it now produces the rule it is named for.
§3d says why crons are the only route, so the next reader does not
re-derive it.

Worth noting the near-miss: had the fixture used any other handler kind,
the honest outcome would have been deleting the rule. A green test on
the wrong assertion is how a dead rule survives.

## §3d needed two edits, and I wrote one — 2026-08-01

User: *"I don't understand the instructions on 3d. Two claims, one
service."*

They had already made the edit the step described — a second cron,
`Cleanup2`, carrying `Footprint{CPU: 2}` — and gotten no error, because
the step was wrong in a way that reads as correct.

`mixed-footprint` fires on DISAGREEMENT between two claims. gallery's
`Cleanup` pins nothing, so adding one footprinted cron produces exactly
one claim, which is simply applied:

```
service gallery (package gallery, profile default + footprint (cpu 2))
```

The step said "a second cron with a different footprint from
`Cleanup`'s" — but `Cleanup` HAS no footprint, so "different from" had
no referent. Both edits are needed and I wrote one.

Rewritten as two steps, with the intermediate state shown on purpose:
add the second cron, observe that one claim is legal and applied, THEN
give the first cron a different footprint and watch the collision. The
non-error is worth seeing — it is what makes the rule about
disagreement rather than about counting declarations, and the rewrite
says that explicitly.

Third gate-script defect of the same family in two days (metrics §4
"upload two or three photos", footprint §3 with no command, this). The
common root is describing an edit instead of writing it, and the fix
that keeps working is to perform the walk and paste what came back.

## U3 bug — local dead letters never decoded — 2026-08-01

User, running `validate/dlq-replay.md` §2: *"no dead letters under
.tako/data/deadletter"* — with the file sitting right there on disk.

**`tako dlq --local` found nothing, and local replay could never have
worked either.** The dispatcher writes a record whose `payload` is RAW
JSON (an anonymous struct shadowing the field as `json.RawMessage`, so
the file reads well for a human). Both readers — the CLI's
`readLocalDead` and the dispatcher's own `DrainReplays` — declared it as
`backends.DeadLetter`, whose `Payload []byte` `encoding/json` fills only
from a base64 STRING. Every real record failed to unmarshal, and both
readers skipped silently on error.

So U3's entire local half was dead on arrival, in a way no test noticed.

### Why the tests passed

`writeReplay`, the fixture helper, marshalled `backends.DeadLetter`
directly — producing base64 payloads, a shape **nothing in production
writes**. Four tests exercised the drain against a file format that
does not exist. They were not weak assertions; they were correct
assertions about the wrong input.

This is the sharpest version of a lesson this stage keeps teaching: a
fixture that constructs its own input tests the fixture as much as the
code. The metrics wire format had the same shape of miss (unit-tested
thoroughly, against the wrong resource type).

### Fix

The on-disk shape is now a named type, `deadLetterFile`, declared once
and used by the writer AND by an exported `DecodeDeadLetter`. Both
readers go through it, so neither can drift again. `writeReplay` builds
that type, and a new `TestOnDiskDeadLetterDecodes` produces a REAL dead
letter through a real failed delivery and decodes the file the CLI
reads — closing the loop the fixture had opened.

Two smaller things fell out:

- The listing printed the payload with the file's indentation inline,
  so a record came out ragged across three lines. Compacted for display
  (`onOneLine`); the file stays pretty-printed, which was its point.
- A replayed payload comes back re-indented, since `MarshalIndent`
  reformats the embedded JSON. Invisible to handlers, which unmarshal —
  the test compares semantically and says why.

Validated live end to end afterwards: produce, list, replay by prefix,
drain, re-dead-letter with a NEW id. That transcript replaced the
invented one in §2, which also now says that `no dead letters`
immediately after a replay is CORRECT (the record is in the inbox), and
what a stale dev server holding port 8080 looks like — both states the
walk hits in practice.

## Release pass — docs cleanup + code review (2026-08-01, user-directed)

Out-of-plan, requested before release: prune dead markdown, make the
guide and reference good enough to use the framework without reading
the code, and review the code for standardization, duplication and
engineering practice. Findings and proposals live in
`docs/code-review-1.md`; this entry records what moved.

**Docs cleanup.** Exactly one file was dead: `docs/C.md`, byte-identical
to the first 3075 lines of this log, added by a stray redirect in
`5542f67` and carried for a week. Deleted. Everything else earns its
place — all 28 `validate/*.md` are cited by a signed verdict (skeleton
under its gate id, G0, rather than by filename), and the review
documents, decision records and plans are records of what was true when
written. `stage7-resume.md` is the one file with an expiry date: it is
disposable once U4 lands.

**Docs gaps closed.** Three, found by diffing the locked SDK surface and
the analyzer's rule IDs against the doc corpus:

- `docs/reference/testing.md` — there was no page for the test surface.
  `NewTestApp`, `DrainQueues`, `AdvanceCron`, `Chaos`, `SetSecret` and
  `SignIn` were scattered across eight "Test it" sections, and
  `app.Advance` (the fake clock) and `app.HTMX` appeared nowhere at all.
- `docs/reference/errors.md` — 38 analyzer rules, 10 of them documented
  nowhere. Now an index of every rule with its verbatim golden text and
  what it protects, plus the boundary between analysis errors, boot
  refusals, preflight and unwired-backend panics.
- `ctx.Log()` — the only `Ctx` method with no mention anywhere. Added to
  observability.md, together with the honest limitation behind it (P1
  below).

One stale claim fixed: observability.md still described the `instance`
metric label that U2 retired when metrics moved to `generic_task`.

**Code fixes (all zero-behavior-change, suite + six examples green).**

- `Config.Name` and `gcpb.Prefixed` were two implementations of the env
  prefix rule — with `"prod"` as a literal in one and a constant in the
  other — under a doc comment claiming they were shared so "the names
  services read are the names deploys create". They agreed by luck. One
  function, one constant now.
- Ten `err.(*apiError)` type assertions became `errors.As` via
  `asAPIError`, and six copies of the 404 predicate became `notFound`.
  The taxonomy is intact today only because `Classify` is always called
  at the leaf; `provision.Apply` wraps with `%w` two frames up, so the
  invariant was a convention nobody had written down.
- `gofmt` and `go vet` are clean for the first time (one unformatted
  test file; one leaked `context.WithDeadline` cancel).

**Proposals, not done** (ranked in `code-review-1.md`): P1 logging never
became cloud-shaped — no `slog` handler anywhere, so severity is lost in
Cloud Logging and `logRequests` still prints the dev format in
production under a comment promising Phase E would replace it; P2 the
injectable HTTP host (130 hand-built API URLs, and the reason four
failures this stage were only findable live); P3 `pkg/runtime` is a
compatibility contract with no lock and no stated policy; P4 "not wired"
answers in two mechanisms and three phrasings; P5 `ctx.User` panics with
two texts; P6 the CLI exits two ways; P7 `pkg/devserver` has no tests;
P8/P9 small duplication and `deploy.go` at 1070 lines holding three
commands.

P1 is the one to decide before release — it is user-visible behavior,
not tidiness.

## P1 — logging becomes cloud-shaped (DX proposal, 2026-08-01)

From `docs/code-review-1.md`. Two defects, one slice: the framework
installs no `slog` handler, so severity is lost in Cloud Logging; and
`logRequests` still prints the dev-console format in production under a
comment promising Phase E would replace it.

**No new SDK surface.** `ctx.Log()` call sites do not change. This slice
is entirely about where the bytes go and what shape they arrive in.

### Verified first (2026-08-01, docs.cloud.google.com)

Cloud Logging's special JSON fields, confirmed against the structured-
logging reference rather than memory:

| Purpose | Key | Value |
|---|---|---|
| severity | `severity` | a LogSeverity string — `DEBUG INFO NOTICE WARNING ERROR CRITICAL ALERT EMERGENCY` |
| message | `message` | string |
| timestamp | `time` | ISO 8601 |
| trace | `logging.googleapis.com/trace` | `projects/<PROJECT-ID>/traces/<TRACE-ID>` |
| span | `logging.googleapis.com/spanId` | string |
| request | `httpRequest` | LogEntry HttpRequest record |

The Cloud Run page confirms `logging.googleapis.com/trace` with the
format `"projects/%s/traces/%s"` is what nests a container's log lines
under their request in the Logs Explorer.

**The load-bearing detail:** slog spells its levels `DEBUG INFO WARN
ERROR`, and Cloud Logging wants `WARNING`, not `WARN`. A JSON handler
alone does not fix severity — the rename is the fix.

### The shape: the handler follows the backend

- **`BootLocal`** (tako dev, tako test, NewTestApp) — unchanged. The dev
  console is a product; JSON in it would be a regression.
- **`BootGCP`** — `slog.SetDefault` with a JSON handler on stdout, whose
  `ReplaceAttr` renames `level`→`severity` (with the value mapped),
  `msg`→`message`, and `time`→ISO 8601.

One seam, matching the one that already decides every other local/cloud
difference. Nothing in between: no flag, no `tako.WithLogging`.

### What a user sees

Local, unchanged:

```
GET /api/photos → 200 (14.2ms)
```

Cloud, one line per request (wrapped here for reading):

```json
{"time":"2026-08-01T20:52:19.412504Z","severity":"INFO","message":"GET /api/photos → 200",
 "httpRequest":{"requestMethod":"GET","requestUrl":"/api/photos","status":200,
   "latency":"0.014s","responseSize":812,"userAgent":"curl/8.7.1","protocol":"HTTP/1.1"},
 "logging.googleapis.com/trace":"projects/tako-test-01/traces/105445aa7843bc8bf206b12000100000"}
```

and a line the handler wrote, correlated with it:

```json
{"time":"2026-08-01T20:52:19.418113Z","severity":"ERROR","message":"notify failed",
 "req":"r-8f2c1d","err":"dial tcp: i/o timeout",
 "logging.googleapis.com/trace":"projects/tako-test-01/traces/105445aa7843bc8bf206b12000100000"}
```

In the Logs Explorer that second line nests under the first, and
filtering `severity>=ERROR` finds it. Today neither is true.

`tako logs` needs no change — its `severity` column starts carrying real
values instead of `DEFAULT` for everything.

### Trace correlation

Cloud Run sends `X-Cloud-Trace-Context: TRACE_ID/SPAN_ID;o=1`. When
present, the request's logger gains the trace and span fields;
`TAKO_PROJECT` is already injected at deploy, so no metadata call is
needed. Absent (local, tests, push deliveries), nothing is added — the
existing `req` attribute keeps doing the correlating.

Cron, job, singleton and push loggers keep their `cron=`/`job=`/
`handler=` attributes and gain severity; they get no `httpRequest`,
because they are not requests from a client.

### The one open question — who owns the default logger?

A generated main calls `runtime.Boot()` first and gives user code no
seam before it, so a user who calls `slog.SetDefault` in an `init()`
would be overwritten by `BootGCP`.

**Recommended (A): tako sets it in the cloud, unconditionally, and the
docs say so.** The framework already owns the service's shape, its
identity and its wiring; owning the log format in the cloud is the same
kind of derivation, and it is the only way `tako logs` and severity
filtering mean anything by default. No example demands an override yet,
and "no feature without an example demanding it" applies.

**(B): the same, with `TAKO_LOG=raw` opting out** to Go's default
handler. Five lines, and an env var that exists for a user who has not
turned up yet.

Recommending A.

### Docs

`reference/observability.md`'s Logging section is rewritten around the
real behavior, and the two "Not yet" bullets this slice creates —
severity not surviving, request logs unstructured — are deleted rather
than reworded, because they stop being true.

### Test plan

- Unit: `ReplaceAttr` maps all four slog levels to LogSeverity strings
  (the `WARN`→`WARNING` case is the regression that matters); a line
  with a trace header carries the exact `projects/P/traces/T` string; a
  line without one carries no trace key at all; local boot leaves the
  default handler alone.
- `validate/logging.md` (new): deploy gallery, curl an endpoint, read
  the entries back with `gcloud logging read` and confirm severity is
  `ERROR` not `DEFAULT`, `jsonPayload` is a record not a text blob, and
  a handler line nests under its request in the Logs Explorer.

### Non-goals

`--follow` / streaming; log-based metrics or alert policies; sampling; a
level knob; `httpRequest` on non-HTTP executions; changing anything
about local output.

**STOP — awaiting approval, and a pick on the open question.**

## P1 — logging becomes cloud-shaped (2026-08-01)

Approved with option A: tako owns the default logger in a deployed
service. Proposal above; what shipped matches it.

**`pkg/runtime/logging.go`** (new) holds the whole seam. `BootGCP` calls
`useCloudLogging(project)` before anything else can log; `BootLocal`
deliberately does not, so the dev console is untouched.

The load-bearing part is not the JSON — it is `ReplaceAttr`. slog spells
its levels DEBUG/INFO/**WARN**/ERROR and Cloud Logging recognizes
DEBUG/INFO/**WARNING**/ERROR, so a plain `slog.NewJSONHandler` would
still have lost every warning. `severityOf` maps the four, rounding
between-levels DOWN the way slog's own `Level.String` reports them
(5 is "WARN+1", not ERROR). `level`→`severity`, `msg`→`message`,
`time`→ISO 8601; everything the caller attached survives untouched.

Field names verified against docs.cloud.google.com rather than recalled
(both cloud.google.com URLs now 301 to it), and the test proves the
rename is load-bearing: disabling the `slog.LevelKey` case fails
`TestCloudLinesCarryTheRenamedKeys` with *"the line still carries
'level' — the rename did not happen"*, and passes again restored.

**Trace correlation.** `X-Cloud-Trace-Context: TRACE/SPAN;o=1` becomes
`logging.googleapis.com/trace = projects/<TAKO_PROJECT>/traces/<TRACE>`,
so every line a handler writes nests under its request in the Logs
Explorer. `TAKO_PROJECT` was already injected, so no metadata call. With
no project (local, tests) NO trace key is written at all — a trace field
pointing at nothing is worse than none, and `TestNoTraceKeyWithoutCloudRun`
pins that.

**Request logs.** `logRequests` no longer decides the format; it hands
`logRequest` the status, size and duration and that picks. Local: the
same terse line as before, byte for byte. Cloud: an `httpRequest` record
with method, url, status, latency, responseSize, userAgent, remoteIp and
protocol, at a severity that follows the status (5xx→ERROR, 4xx→WARNING),
so a burst of 500s is findable with the same filter as the handler errors
behind them. `statusWriter` grew a `Write` to count the body — it only
had `WriteHeader` before, so responseSize would have been 0 forever.

Three copies of `slog.Default().With("req", reqID)` (http.go, blob.go,
page.go) became one `requestLogger`, which is also what makes the trace
arrive on all three kinds rather than whichever one got edited.

Docs: observability.md's Logging section rewritten around real behavior,
and the two "Not yet" bullets P1 created were DELETED, not reworded —
they stopped being true. New `validate/logging.md` reads the entries back
with `gcloud logging read` and checks `severity>=WARNING` returns
something (before this slice that query returned nothing, ever) and that
the payload is `jsonPayload`, not `textPayload`.

### Discovered work (logged, not done)

**A cron has two identities.** `var Cleanup = tako.Cron("every 24h",
cleanupOrphans)` is addressed as `cleanupOrphans` from a test
(`app.AdvanceCron`) and as `Cleanup` in the cloud (`RegisterCron`, and
the Scheduler URI `/_tako/cron/Cleanup`) — the handler function's name
in one place, the package-level variable's in the other. User-raised
(2026-08-01): the tako-specific cron endpoint wants revisiting, with the
HTTP endpoint derived from the VARIABLE name.

Worth noting the endpoint already is the variable name; the divergence
is on the test side, so unifying probably means `AdvanceCron` taking
`"Cleanup"` — a breaking change to a test-facing method, which is why it
is its own task and not a side effect of this one.

It also had a documentation tail: `TestApp.AdvanceCron`'s doc comment
claimed the function name was "the same identity the wiring uses", which
is false, and that claim was copied into `docs/reference/testing.md`
when that page was written earlier today. Both corrected to state the
split plainly; the fix itself stays unscheduled.

## P2 — the injectable HTTP host (2026-08-02)

From `docs/code-review-1.md`. No DX proposal: this slice adds no user
surface, changes no user-visible behavior and no error text. It is a
refactor whose product is a seam.

**`Client.url(host, path, args...)`** in both `pkg/provision/gcp` and
`pkg/backends/gcp`, with the API hosts as named constants. Every URL
either package builds now goes through it — 130 literals gone. When
`Client.Base` is set the host becomes the FIRST PATH SEGMENT, so one
fake server serves every API and a test can assert *which* API was
addressed, not merely that something was.

A pleasant side effect: preflight's API-enablement list and the URL
builders now share the same constants. They were two hand-maintained
lists of the same strings.

Two deliberate non-conversions:

- `signed.go`'s returned URL. That is handed to the CALLER, not called
  by tako; redirecting it at a test server would hand out links that
  point nowhere. It uses `"https://" + storageHost` with a comment
  saying why, and a test pins the distinction.
- `pkg/runtime`'s two Google certificate endpoints (JWKS for session
  and cron token verification). They are not deploy-target calls and
  do not belong to either Client.

### The payoff, demonstrated rather than asserted

`TestCreatedServiceSendsAPortNumber` is the U2 regression: a container
port carrying a name and no number, which Cloud Run reads as 0 and
refuses — *every service in every app* failed to create. Reintroducing
the exact defect (renaming the `containerPort` key) makes the test fail
with:

```
containerPort is absent from map[__gone__:8080 name:h2c] — Cloud Run
reads that as 0 and refuses the service
```

and restoring it passes. That bug was unit-testable all along; nothing
could see the request body.

Six more that were impossible before: domain mappings going to the
REGIONAL host (a global-host call 404s, and the difference was
invisible), a SERVICE_DISABLED 403 arriving as the exact
`gcloud services enable …` command, a plain 403 naming the role, a
disabled API observing as an EMPTY inventory rather than a failure
(destroy converging past an API the app never enabled), Google's HTML
error page becoming a diagnosis instead of a paste, and the host
constants being well-formed. On the backends side: env namespace in the
Datastore URL, the physical bucket name in the GCS path, and the derived
dead-letter topic in the publish.

`MetricsStore` kept its own `base` field — it predates this and its
tests point at one server without redirecting the whole client — but it
now builds that default from the shared host constant.

### Discovered work (logged, not done)

**`pkg/backends/local` replay tests flake under load.** One full-suite
run had `TestReplayRedeliversAQueueMessage` fail with *"worker saw
<nil>"* and `TestReplayReachesOnlyTheFailedSubscriber` with *"the failed
subscriber ran 0 times"*; three subsequent full runs and a `-count=3`
run of the package alone were clean, so it is load-dependent, not a
break from this slice.

It should not be dismissed as flake. `WaitIdle` returned WITHOUT error
while the replayed message had not been delivered, which means idle was
observed before `DrainReplays`'s hand-off registered as in-flight. That
is a real ordering gap in the local dispatcher, and the same gap would
make `app.DrainQueues()` return early in a user's test. Worth its own
look.

## X1 — one cron, one name (DX proposal, 2026-08-02)

User-chosen direction (2026-08-02): **the variable name wins
everywhere.** Today a cron has two identities:

```go
var Cleanup = tako.Cron("every 24h", cleanupOrphans)
```

| addressed from | by | name |
|---|---|---|
| a test | `app.AdvanceCron` | `cleanupOrphans` — the FUNCTION |
| the generated service | `RegisterCron` | `Cleanup` — the VARIABLE |
| Cloud Scheduler | the URI | `/_tako/cron/Cleanup` |
| the singleflight lease | `cron-<name>` | `cleanupOrphans` |

### Why it is not a one-line fix

`tako.Cron` derives its name with `fnName(fn)` — reflection on the
function pointer (`handlers.go:113`). A Go value does not carry the name
of the variable it is assigned to, so **the SDK can only ever see the
function name.** The variable name exists solely in the AST, which is why
the analyzer has it and the generated `RegisterCron` call carries it.

A test has no analyzer and no generated code. That is the whole problem,
and it is why this is its own task.

### Proposed: the analyzer records the mapping; tests read it

`tako dev`, `tako test`, `tako build` and `tako deploy` all run the
analyzer already. Have it write, alongside the generated services:

```
.tako/crons.json
{"gallery": {"Cleanup": "cleanupOrphans", "Rebuild": "rebuildFeed"}}
```

`NewTestApp` walks up from the working directory for `.tako/` (the
module root; a multi-package app like shop has one at the top) and loads
it. `AdvanceCron` then resolves a VARIABLE name through it.

### What a user writes

```go
app.AdvanceCron("Cleanup")   // was: app.AdvanceCron("cleanupOrphans")
```

**Breaking, deliberately** — that is the point of having one name. The
migration is mechanical and the error does it for you, because the
mapping knows both sides:

```
tako: AdvanceCron("cleanupOrphans"): crons are addressed by their variable
  name — use "Cleanup" (the deployed cron is /_tako/cron/Cleanup)
```

Without a mapping on disk at all:

```
tako: AdvanceCron("Cleanup"): no cron mapping under .tako — run tako test
  (or tako dev once) so the analyzer can record which var declares which cron
```

### The cost, stated plainly

**`NewTestApp` gains a dependency on generated state**, which it does not
have today: a bare `go test ./...` on a fresh clone currently exercises
crons fine, and after this it needs `tako test` (or one `tako dev`) to
have run first. That is a real regression in the "your tests are just Go
tests" story, accepted knowingly in exchange for one name.

Two mitigations worth deciding on:

1. `tako test` already regenerates before running, so the documented
   path stays seamless. The gap is only bare `go test` on a clone with
   no `.tako/`.
2. `.tako/crons.json` could be committed rather than ignored. It is
   small, human-readable, and derived — the same category as the
   generated mains, which ARE committed today.

Recommending (2): commit it, for the same reason the generated services
are committed.

### Also in scope

- **The singleflight lease key** moves to the variable name too, so
  `cron-Cleanup` is the key everywhere. One transitional deploy has old
  and new instances holding different keys, which means a single
  overlapping run could double-fire during the rollout. Worth naming in
  the docs; not worth a migration shim for a v0.
- `AdvanceCron`'s doc comment and `docs/reference/testing.md`'s "Mind
  which name" section both revert to a plain statement once there is one
  name.

### Not in scope

The `/_tako/cron/{cron}` dispatcher stays as it is. Replacing it with a
generated route per cron is the third option from the same question and
wants its own slice: the dispatcher exists so ONE OIDC-verified route
serves every cron without colliding with app routes, and unpicking that
is a routing design, not a naming one.

### Test plan

- Analyzer golden: `.tako/crons.json` content for gallery and shop.
- `AdvanceCron("Cleanup")` runs the body; `AdvanceCron("cleanupOrphans")`
  fails with the migration text naming `"Cleanup"`.
- No mapping on disk → the run-tako-test error, not a confusing miss.
- The three existing gallery call sites migrate, and `tako.Cron`'s own
  wiring, the Scheduler URI and the lease key are asserted to agree.

**STOP — awaiting approval.**

## X1 — one cron, one name: direction A (DX proposal, 2026-08-02)

Supersedes the X1 proposal above, which is WITHDRAWN: it put a mapping
file under `.tako/` and made `NewTestApp` read it, costing the "your
tests are just Go tests" property. User-rejected on exactly that ground,
and rightly.

**Direction A: the declaring variable is the identity, and nothing is
derived by reflection.**

### The audit that picked the direction

Every service tako deploys is already named from its declaring variable:

| declaration | service |
|---|---|
| `var Web = tako.Service()` | `gallery-web` |
| `var MakeThumb = tako.Worker(thumbs, makeThumb)` | `gallery-makethumb` |
| `var NotifyOwner = tako.Subscribe(ready, notifyOwner)` | `gallery-notifyowner` |
| `var Reindex = tako.JobHandler(job, reindex)` | `gallery-reindex` |
| `var Invoicer = tako.Singleton("invoicer", assignInvoices)` | `shop-invoicer` |

Verified against the generated tree: `gallery`, `gallery-makethumb`,
`gallery-notifyowner`, `gallery-onuploaded`, `gallery-warmfeed`. So the
cron ENDPOINT using the variable name was never the anomaly — it is the
only part of the cron that follows the house rule.

The outlier is `fnName(fn)`, reflection on the function pointer, which
is the sole place a function name becomes an identity. It stayed
invisible because for workers, subscribers and jobs the function name is
never user-facing — `NewTestApp` addresses those by the queue or topic
they BIND. **Cron is the only handler whose function name a user types**,
because it binds nothing and `AdvanceCron` had nothing else to use.

### The argument that settles the shape

Tako tests are already value-addressed, everywhere:

```go
Create.Call(ctx, req)        // the Endpoint value
links.Get(ctx, "gh")         // the Entity value
thumbs.Publish(ctx, msg)     // the Queue value
app.AdvanceCron("cleanupOrphans")   // ← a STRING. the only one.
```

`AdvanceCron` is the exception, and the string it takes is the one name
the framework does not otherwise use. Passing the value removes both
problems at once and needs no mapping, no generated state, and no
reflection — the variable IS the identity, so referencing it directly is
the most faithful thing a test can do.

```go
app.AdvanceCron(Cleanup)
```

Bare `go test` keeps working, which was the constraint.

### The SDK delta

Small and coherent:

- `TestApp.AdvanceCron(name string)` → `AdvanceCron(h Handler)`.
- `tako.Singleton(name string, fn, opts...)` → `tako.Singleton(fn, opts...)`
  (see below).
- `Handler` gains an unexported registration id so a value can be matched
  without comparing func pointers; its `name` field stops being an
  identity and becomes what it honestly is — a log label.
- **`tako.Cron`'s signature does not change.** It keeps taking a schedule.

`fnName` survives only to label log lines (`handler=makeThumb`), which is
a fine use for a function name and a poor one for an identity. That
distinction is the whole slice.

### Singleton, made uniform

`Singleton` is the one declaration where a declared name and a deployed
name are already different things: it takes `"invoicer"`, uses it as the
lease/binding identity, and names its service from the VARIABLE anyway.
Under direction A the argument is redundant — the variable already says
it — so it goes:

```go
var Invoicer = tako.Singleton(assignInvoices)   // was ("invoicer", assignInvoices)
```

Cron and Singleton then read the same way: neither names itself, both are
identified by their variable. One declaration in the repo
(`examples/shop/app.go:102`) plus its docs.

### The one real subtlety: the singleflight lease key

Today the key is `cron-<functionName>`, computed in the SDK at
declaration time — the only place that can see a function name. Under A
it must be the variable name, which the SDK cannot see.

Proposed: **the runtime owns the identity, not the SDK.**
`RegisterCron("Cleanup", …)` and `RegisterSingleton("Invoicer", …)`
already carry the variable name from the analyzer; the lease key becomes
that. In `NewTestApp` no Register call happens, so the key falls back to
the registration id — process-local, which is all a test's lease needs,
since the lease only has to distinguish crons WITHIN a process there.

Worth stating plainly in the docs: **one transitional deploy has old and
new instances holding different lease keys**, so a single overlapping
`WithSingleflight` run could double-fire during that rollout. Not worth a
migration shim for v0; worth a sentence in `cron.md`.

### Error texts

Passing something that is not a cron:

```
tako: AdvanceCron(MakeThumb): that is a worker, not a cron — workers run
  when their queue delivers, so publish to the queue and DrainQueues
```

Passing a zero Handler (a var that was never assigned a constructor):

```
tako: AdvanceCron: the handler is empty — pass the var a tako.Cron was
  assigned to, like app.AdvanceCron(Cleanup)
```

The old string form stops compiling, which is the migration notice; Go
says `cannot use "cleanupOrphans" (untyped string constant) as
tako.Handler value`. Blunt but unambiguous, and it cannot be missed.

### Migration cost

Three `AdvanceCron` call sites (gallery `app_test.go:142`,
`coordination_test.go:121` and `:130`), one `Singleton` declaration
(`shop/app.go:102`), and the docs that quote them —
`reference/cron.md`, `reference/singleton.md`, `reference/testing.md`,
`guide/03-gallery.md`, `guide/05-shop.md`. `testing.md`'s "Mind which
name" section is deleted rather than reworded: it stops being true.

### Test plan

- `AdvanceCron(Cleanup)` runs the body; the wrong-kind and zero-Handler
  texts are golden.
- The cron's endpoint, its Scheduler job name and its lease key are
  asserted to be the SAME string, from one source — the assertion that
  would have failed before this slice.
- A cron declared with an inline closure works and is stable: the earlier
  measurement showed reflection yields `func1`/`func2` and RENUMBERS when
  a cron is added above them, so this pins that the identity no longer
  depends on declaration order.
- Analyzer goldens for the Singleton signature change.
- A bare `go test ./...` (no `tako test`, no `.tako/`) still exercises
  crons — the property the withdrawn proposal traded away.

### Non-goals

The `/_tako/cron/{cron}` dispatcher keeps its shape; a generated route
per cron is a routing design and its own slice. Worker/Subscribe/
JobHandler service naming is untouched — it already follows the rule.
Direction B (declared names everywhere, resource-style) is recorded as
considered and rejected: it would rename production infrastructure in
every deployed app to win a consistency argument.

**STOP — awaiting approval.**

## X1 — one cron, one name: shipped (2026-08-02)

Direction A, approved. The declaring variable is the identity; nothing
is derived by reflection.

**SDK delta** (the lock test caught it, as designed, and was regenerated
deliberately — only these two lines moved):

```
- func (*TestApp).AdvanceCron(name string)      + func (*TestApp).AdvanceCron(cron Handler)
- func Singleton(name string, fn, opts...)      + func Singleton(fn, opts...)
```

`Handler` gained an unexported `id` (declaration order, from 1, so a
zero Handler is distinguishable and can be refused by name). `name`
survives as what it honestly is — a log label. `nextHandlerID` reserves
the id BEFORE the Handler is built, because the singleflight closure has
to capture it.

**The declared name reaches the SDK from the runtime**, not the other
way round: `RegisterCron("Cleanup", …)` and `RegisterSingleton` already
carried the analyzer's variable name, and now call
`hooks.SetHandlerIdentity`. `leaseKey` resolves at invoke time.

### Two things the work surfaced that the proposal had not

**1. A real regression, caught by a golden.** Dropping Singleton's name
argument shifted its function to `call.Args[0]`, and
`analyzer.go` still read `funcArg(pkg, d.call, 1)` for the edge walk.
Result: **every resource edge from `shop.Invoicer` disappeared** — and
those edges are what derive IAM grants, so the singleton would have
deployed and then been denied its own Datastore. The shop appgraph
golden failed with the edges missing, which is exactly what goldens are
for; after the fix the only diff is the intended `"binding"` rename.

**2. The lease key is the one place a second name survives, on
purpose.** The plan was an id-based fallback in tests. That broke
gallery's `TestCleanupSingleflightCollapses`, which holds
`tako.Lock("cron-cleanupOrphans")` to simulate an overlapping run — a
legitimate thing to write, and `cron-#3` is not nameable. So
`NewTestApp` now seeds identities from the FUNCTION name
(`registerTestIdentities`), keeping the key writable in tests, while
deployed services key on the declared name. Both are stable within their
scope, which is all a lease requires; the divergence is internal and
documented rather than hidden.

### Tests

`cron_identity_test.go`: the value is what runs; the wrong kind is
refused with the route that handler DOES take ("workers run when their
queue delivers…"); a zero Handler is refused by name; **two crons over
one function stay distinct** — the case that motivated the direction,
since reflection gave them `func1`/`func2` and RENUMBERED both when a
cron was added above them; and the lease key follows the declared name
once registered.

The old failure mode cannot be reproduced in code that compiles, which
is the point: `AdvanceCron("cleanupOrphans")` is now a build error.

### Docs

`testing.md`'s "Mind which name" section is DELETED, not reworded — it
stopped being true. `cron.md` gains the identity paragraph and the
transitional-deploy caveat about the lease key. `singleton.md`,
`guide/local.md`, `guide/05-shop.md`, `validate/gallery.md` and the
spec's §2.8 table migrated. `docs/task-log.md` and `api-review-1.md`
keep the old signatures — they are records of what was true then.

Migration was three `AdvanceCron` call sites, one `Singleton`
declaration, and `compile_check_test.go`. Full suite green, six examples
green, gofmt and vet clean.

## The replay ordering bug, and P3–P9 (2026-08-02)

Closes `docs/code-review-1.md`. The bug first, then the review's
remaining findings in the order approved.

### The bug: DrainReplays returned before the work was counted

`replay` handed the message to `go d.dispatch(…)`, and the in-flight
count is incremented INSIDE dispatch — so `DrainReplays` could return
with nothing counted, and `WaitIdle` answered "idle" for a message that
had not been delivered. In a user's suite that is `app.DrainQueues()`
returning early: the silent flake generator behind the one failure seen
during P2.

It was `go` for a reason — `dispatch` takes `d.mu` (via `chaosPlanFor`)
and `replay` held it. Fixed by resolving the carrier under the lock
(`routeFor`) and dispatching after releasing it, synchronously; dispatch
still launches delivery in its own goroutine, but counts the work before
returning.

**Honest about the test.** `TestDrainReplaysCountsTheWorkBeforeItReturns`
is a CONTRACT test, not a deterministic regression test. Reintroducing
`go d.dispatch` still passes it — the window is a few instructions wide
and the stray goroutine essentially always wins, which is exactly why
the bug survived as a rare flake rather than a failing test. Two
attempts to force it (a blocked consumer with a short WaitIdle; a direct
in-flight read) both lost the race. What the test does buy: the
invariant is written down and checked, so removing the counting outright
fails loudly.

`sync.WaitGroup` became a small `inflightCount` so the count can be READ
— the invariant "work is counted before this call returns" is not
observable through `Wait` alone.

### P3 — pkg/runtime's compatibility promise

Both halves, per the decision. `docs/reference/README.md` gains "what is
API, and what is not": the root package is the API and is locked; `pkg/…`
is internals and may change in any release, with the two consequences
that actually bite (generated code in the user's repo names ~30 runtime
symbols; example tests import runtime deliberately).

And `pkg/runtime/testdata/runtime_lock.golden` — 51 entries,
deliberately WEAKER than the SDK lock: names and signatures only, types
by name, so internals stay free. A diff there is not a refusal, it is a
release note.

### P4 — one condition, one mechanism

`Lock.Do`/`TryDo` and `Job.Run` now panic with the same sentence every
other unwired resource uses. The argument that already justified
panicking for `Entity.Get` applies to them unchanged: their error
returns mean something else, so an unwired error is indistinguishable
from "not found" or "your function failed". A panic cannot be mistaken
for either.

`Site` keeps returning an error, and the docs now say why rather than
leaving it looking arbitrary: it is the one case that is not a wiring
mistake — templates compile INTO a generated service, so there is
nothing `NewTestApp` could have wired.

### P5, P6

`Ctx.User` → `ctx.User` in the runtime panic, matching testapp's. The
two remediations stay: the fix genuinely differs between a request and a
test.

Every CLI command exits through `fail`, which now carries `tako: `.
Four commands had inlined their own copy of the exit and six printed the
error bare. The prefix earns its place when a command shells out — `tako
test` runs `go test`, and its failures are go's.

### P7 — the dev loop has tests

The pure decisions: the banner's counts (four cases, including the
zero-endpoints pluralization), the ignore-my-own-writes filter (without
which the loop reloads forever on `.tako/` writes), which service the
loop runs, and both refusals — an app with nothing public, and a
private-only package. The process half stays on the gate scripts.

### P8, P9

One generic `pickByPrefix` behind both dlq resolvers, taking the lister
command as a parameter so each refusal still names where to look. The
four `plural` copies stay — four unexported two-line helpers in four
packages is idiomatic Go, and a shared `internal/text` would be worse.

`deploy.go` 1070 → 806, with `logs.go` (66) and `destroy.go` (228)
carved out. Pure movement; verified by running the CLI.

Full suite green, six examples green, gofmt and vet clean.

## P1 follow-up: the gate caught a duplicate request log (2026-08-02)

Two findings from the live walk of `validate/logging.md`, one a real
defect in P1 and one benign.

### 1. Tako was logging every request twice — P1 shipped this

The console showed two entries per request:

| logName | source | content |
|---|---|---|
| `run.googleapis.com%2Frequests` | Cloud Run's frontend | full URL, requestSize, serverIp, `0.191464485s`, trace, spanId, traceSampled, **severity already derived from status** |
| `run.googleapis.com%2Fstdout` | tako | `/api/photos`, `0.188980s`, no sizes, no serverIp |

**Cloud Run already writes a request log for every request**, including
to private push endpoints, and already maps status to severity — the
404 in the walk arrived as `WARNING` from the platform without tako's
help. P1's request-log half therefore added nothing, doubled
request-log volume (billed), and made every request appear twice.

I did not check whether the platform already did this before proposing
it. The proposal argued from what Cloud Logging *accepts* rather than
from what Cloud Run *already emits* — the same shape of miss as U2's
`cloud_run_revision` metric assumption, where the half that was checked
made the other half easy to skip.

**Fixed by deletion:** `logRequest` is local-only now. The dev console
line is unchanged; deployed, the platform's record is the request log.
`statusWriter` loses the byte counting it grew for `responseSize`, and
`levelFor`/`clientIP`/`anyToAttrs` go with it. The valuable half of P1 —
handler lines carrying severity and the trace that nests them under the
platform's request entry — is untouched.

**And a second defect the same evidence exposed:** `tako logs` has no
`logName` filter, so it was already ingesting the platform's request
entries — and rendering them BLANK, because their content lives in
`httpRequest` and nothing read it. So the CLI was showing a blank line
AND a duplicate for every request. It now renders them as
`POST /api/photos → 200 (191.5ms)`, matching the dev console, which
means removing tako's copy costs the CLI nothing and gains it the
platform's richer fields.

`validate/logging.md` §6 was wrong too, and would have passed anyway: it
told the reader to expect `jsonPayload` on an arbitrary entry, but a
line whose only content is a message legitimately arrives as
`textPayload` with the special fields lifted out. Rewritten to read a
line with handler attributes, and — the point of the section now — to
count entries per request and fail on a duplicate.

### 2. The post-deploy 403 window is expected

`POST /_tako/push → 403 … lacks {run.routes.invoke} permission`, five
times over two minutes, then silence.

Checked rather than assumed: `reconcileOrder` puts `KindIamGrants`
BEFORE `KindPushSubscription`, and `grants.go` adds
`roles/run.invoker` for each worker/subscriber's SA on its own service.
So the grant is written before the subscription exists — this is IAM
propagation through Cloud Run's front door, and Pub/Sub's retries carry
the messages through it. At-least-once means nothing is lost. Boot
already retries Secret Manager for 90s for the identical reason.

Not a code change; a documentation gap. `guide/cloud.md` gains "the
first two minutes after a deploy" with the exact text, why it happens,
and the two commands that confirm recovery (a 204 on the same endpoint,
and an empty DLQ) — plus what it means if the 403s never stop, which IS
a tako bug. `topic.md` points at it, and the gate script gained §8b so
the next walker meets it as an expected step rather than a scare.

## Pre-release deploy/destroy cycle — run 2026-08-02

`validate/release-cycle.md` (new, not a feature gate): the short walk
proving the two riskiest changes of the release pass reach real
infrastructure together. Run against tako-test-01, `--env x1`, on
examples/gallery. **All five sections pass.**

| § | claim | result |
|---|---|---|
| 1 | P2's 130 rewritten URLs provision real infrastructure | 45 creates, 5 services up in 2m31s |
| 2 | X1: the cron carries its VARIABLE's name | job `x1-gallery-cron-cleanup` → `/_tako/cron/Cleanup` |
| 3 | and the route actually resolves | `POST /_tako/cron/Cleanup → 200 (526.3ms)`, then `cron ok` |
| 4 | every observation path reads back what creation wrote | re-plan: **0 creates, 0 updates, 55 skips, 0 orphans** |
| 5 | destroy leaves nothing | no `x1-*` service, job, topic, subscription, SA or bucket |

§2 is the one that mattered: `cleanupOrphans` — the function name X1
retired as an identity — appears nowhere in either column. §4's full
convergence is the strongest available evidence for P2, since a URL that
reads differently from the one that wrote would surface as a spurious
create.

### What the run found

**A third `tako logs` defect, fixed and verified live.** Cloud Audit
Logs share the `cloud_run_revision` resource, so they matched the
filter; they carry a `protoPayload` nothing rendered, and arrived as
blank lines — six of them in a twelve-line window. Worse than ugly: a
deploy emits enough audit entries to crowd the 200-entry page budget and
push the app's own logs off the end. Now excluded in the filter, with a
defensive skip for any entry that still renders empty. Re-ran against
the live service: clean.

That is three defects in one command found by two walks — blank platform
request entries, the duplicate tako wrote next to them, and now audit
noise. All three came from the same root: the query says
`resource.type="cloud_run_revision"` and nothing had ever asked what
ELSE lands under that resource type.

**Destroy met a real transient and converged.** The first run failed
partway with `delete the dead-letter keep subscription: The service was
unable to fulfill your request [code=8a75] (HTTP 503 from
pubsub.googleapis.com)`. Re-running the identical command finished the
teardown — which is the stateless-reconciliation design working exactly
as intended, and worth having seen once rather than assumed.

Logged, not done: **destroy does not retry transient 5xx.** It converges
on re-run, so nothing is lost, but a user meeting a 503 mid-teardown has
to know to re-run. Deploy already retries elsewhere (the image
eventual-consistency loop in `RunServices.Create`); the same treatment
for 5xx during apply would make this invisible.

**The script itself had a gap the run caught:** gallery declares a
secret and every env gets its own, so preflight stops on
`x1-notifier-key` before anything is built. §1 now creates it and §5
deletes it — destroy never touches secrets by design, so leaving that
out would have left one behind on every run.

Also observed, pre-existing and unrelated: `conf-signer` (service
account) and `conf-91c17217-evbkt-uploaded-keep` (subscription) survive
in tako-test-01 from gcp-backend conformance runs. Not tako-managed
resources — the conformance suite creates them under its own `conf-`
prefix — but they should be cleaned up by the suite rather than
accumulating.

## Destroy retries transients; conformance stops leaking (2026-08-02)

The two items the release-cycle walk logged.

### Destroy (and every idempotent call) retries transients

`Client.do` retries 5xx, 429 and connection errors — but only for GET,
DELETE and PUT. POST and PATCH are deliberately excluded: a retried
create can make two of something, and a deploy that has re-observed the
world is the safe way back from one of those.

Two backoffs (1s, 3s) and then the error surfaces. Short on purpose — a
real outage should read as an error the user can act on, not as a
command that appears to hang.

This is what the walk's `503 from pubsub.googleapis.com` deserved:
reconciliation already converged on re-run, so nothing was ever lost,
but the user had to know to re-run. Converging is the design; making a
person drive it for a blip is not.

Four tests, all on the fake-server seam P2 built: a 503 on DELETE
retried and succeeding; a 503 on POST surfacing after ONE call; a 403
surfacing immediately (an answer, not a hiccup); and the body being
re-sent rather than consumed by the first attempt — the bug this shape
of retry invites.

### Conformance stops leaking, two ways

**The cleanups were silent.** Both were
`if r, err := c.HC.Do(req); err == nil { r.Body.Close() }` — which
swallows the STATUS as well as the error, so a 5xx leaked the resource
and said nothing. Replaced with `deleteResource`, which retries
transients, treats 404 as success, and `t.Errorf`s anything left: a
suite that creates cloud resources and does not remove them is a suite
that quietly costs money.

**And cleanups only run if the process lives to run them.** A run killed
mid-chapter leaks whatever it had made, which is how
`conf-91c17217-evbkt-uploaded-keep` was still in tako-test-01 weeks
later. `sweepStaleConformance` now runs once per run, before anything is
created, deleting `conf-*` Pub/Sub resources whose env prefix is not
this run's — anything else belongs to a run that is over. Subscriptions
before topics, since a topic with a live subscription refuses to go.

Verified live: a `TestTopicGCP` run swept exactly the stale subscription
the release walk had found —

```
gcp_test.go:36: conformance: swept subscriptions conf-91c17217-evbkt-uploaded-keep left by an earlier run
--- PASS: TestTopicGCP (14.25s)
```

— and left the project with zero topics and zero subscriptions
afterwards, its own included.

**Correction to the release-cycle entry:** it listed `conf-signer`
alongside the subscription as a leftover to clean up. That was wrong.
The SA is deliberate and documented in the harness — creating one and
waiting out signBlob propagation costs 90 seconds, so it persists across
runs like the conformance composite index. It is not swept, and should
not be.

## Release cycle, second run (2026-08-02)

Re-run of `validate/release-cycle.md` after the transient-retry change,
against tako-test-01 `--env x1`. **All five sections pass again**: 45
creates and five services, `/_tako/cron/Cleanup` from the variable name,
`POST /_tako/cron/Cleanup → 200 (481.4ms)` then `cron ok`, a re-plan at
0 creates / 0 updates / 55 skips / 0 orphans, and a destroy leaving no
`x1-*` resource of any kind.

**What this run did NOT prove.** Destroy completed in a single pass with
no 5xx, so the retry never fired. The run confirms the change caused no
regression; it does not confirm the retry works in production, because a
transient cannot be summoned on demand. The deterministic evidence stays
the four fake-server tests. Saying otherwise would be claiming a live
proof that did not happen.

Retries are now narrated — `↻ DELETE <resource>: HTTP 503 — retrying in
1s` on stderr — because a silent retry hides degradation: a deploy that
takes twice as long should say why. Without it this re-run could not
have distinguished "no transient occurred" from "a transient occurred
and was swallowed", which is the question the run was meant to answer.

### The propagation window is wider than documented

Both runs hit `The IAM principal lacks {run.routes.invoke} permission`
firing the cron, ~60 seconds after deploy — not only on the Pub/Sub push
endpoints, which is how the previous entry and `guide/cloud.md`
described it. Any identity tako grants `run.invoker` and then
immediately uses is exposed: push subscriptions AND the Cloud Scheduler
job.

One asymmetry worth stating, and now stated in the docs: **Pub/Sub
retries, Cloud Scheduler does not queue.** A push message inside the
window is redelivered and nothing is lost; a cron that fires inside it
is simply skipped, and the next scheduled run is the recovery. For an
`every 24h` cron that is a day, which is why the walk fires the job by
hand rather than waiting.

`guide/cloud.md` broadened to both callers with that asymmetry called
out, `cron.md` gains a pointer, and `release-cycle.md` §3 now tells the
walker to expect the 403 and gives the until-loop that rides it out —
both runs needed it, so the script should not pretend otherwise.

## API review 3 — options, scoping, the namespace (2026-08-02) — AWAITING DECISIONS

Owner-requested, triggered by the alerting design: designing a new
primitive surfaced that nobody could say which options apply where, and
that `tako.Email` (a notification channel) would collide with
`tako.WithEmail` (password sign-in). Full review in
`docs/api-review-3.md`. Docs only — no code changed.

Scope is narrower than review 1 (six rules over every symbol) and
different from review 2 (did the freeze hold, is this publishable). One
question: when a user types `tako.`, can they tell which options apply
where, and can two symbols be confused for one another.

**Timing is the whole reason this is worth doing now.** `git tag` is
empty; R3's verdict says push and v0.1.0 remain owner actions. Every
rename in the review is free today and a v2 discussion after the tag.

### Defect found and proven, not argued

`tako.Bucket` and `tako.Entity` accept any option and silently drop it.
`extractBucket` (resources.go:88) and `extractEntity` (resources.go:43)
read `call.Args[0]` and never look at `call.Args[1:]`. Proven with the
real CLI against a scratch module:

```
var (
    photos = tako.Bucket("photos", tako.WithTTL(time.Minute))
    album  = tako.Entity[Photo]("album", tako.WithSingleflight())
)
...
    jobs.Publish(ctx, "x", tako.WithTTL(time.Minute))
    photos.SignedPut(ctx, "k", time.Minute, tako.WithSingleflight())

$ tako gen graph
optprobe: 2 services
  service optprobe (package optprobe, profile default)
    GET /hello → Hello (app.go:19)
    bucket photos
    entity album
    queue jobs
```

Zero errors on four illegal uses. Two vanish forever; two become 500s at
request time.

This regresses a rule the project already wrote down —
resources.go:328, "an option that is accepted and silently dropped is
worse than an error (the S6 audit found WithRateLimit doing exactly
this)". Same root cause as the code-review findings: an invariant held
by convention across 14 hand-written sites in 5 files, with nothing
asserting that every constructor declares an option set. The fix is
small and independent of the redesign; it should not wait for it.

### What the review establishes

- **`Option` is three types wearing one name** — analysis-enforced
  (18 constructors), silently dropped (2), runtime error (3
  operations). `options.go:11` promises "fails analysis, not
  production", which is false for every operation option.
- **The matrix** — 23 producers × 23 sinks = 529 combinations, 39
  legal (7.4%). The other 92.6% compile. That table did not exist
  anywhere in the repo; assembling it from the analyzer's own error
  texts was most of the review.
- **Six families fall out and are nearly disjoint**: route,
  service/compute, site, auth, carrier, operation. The clustering is
  the design; the surface just doesn't express it.
- **Collisions**: `Redirect`/`WithRedirect` (high), `Auth`/`WithAuth`,
  `WithEmail`/a future `Email`, `Handler`/`JobHandler`, and six generic
  single words in the package namespace (`On` `Off` `Inherit`
  `Default` `Batch` `Latency`).
- **One concept, two spellings** — `WithRetries` (0–10) vs
  `Delivery.MaxAttempts` (5–100); five unrelated names for time; three
  for concurrency. Review 2's own principle when it retired
  `WithRateLimit`: one knob with two spellings is worse than a
  breaking change.

### Recommendation

Hybrid: config structs for declarations (the direction tako already
took three times — `Delivery`, `Footprint`, `Traffic` are structs
wrapped in a `With…` only to satisfy a variadic), delete the option
tier on the three operations, apply the renames regardless, and ship
the defect fix now. Config structs delete `invalid-option` and
`service-option` outright; range validation stays in the analyzer where
it belongs.

**STOP — five decisions in §10 of the review.** The load-bearing ones:
whether to accept a spec Part 1 rewrite (rule 2's "options all live in
one namespace" does not survive config structs), and whether this
precedes alerting (recommended — alerting adds a family and the
`Email` collision).

### Discovered work

- A completeness test — every constructor in the lock must appear in an
  option-set table — belongs with the §2 fix whichever remedy is chosen.
  It is what makes the defect class unrepresentable rather than fixed
  once.

## X2 — DX proposal: config structs replace the Option namespace (2026-08-02) — APPROVED

Owner approved 2026-08-02: do this refactor before alerting; rewriting
spec Part 1 rule 2 is accepted; the targeted renames are accepted with
the names below. Follows `docs/api-review-3.md`.

### The shape

**Seven optionless constructors lose the variadic entirely.** This is
the review's §2 defect fixed by construction, not by a new check:
`tako.Bucket("photos", tako.WithTTL(x))` becomes a compile error
("too many arguments"), unrepresentable rather than merely detected.

```go
tako.Bucket("photos")
tako.Entity[Photo]("album")
tako.Job[Params]("settle")
tako.Secret("notifier-key")
tako.Counter("uploads")
tako.Histogram("thumb_ms")
tako.Lock("nightly")
```

**Carriers take the struct they already had.** `Delivery` was always a
config struct wrapped in a `With…` to satisfy a variadic; the wrapper
goes:

```go
tako.Queue[Thumb]("thumbs", tako.Delivery{MaxAttempts: 10})
tako.Topic[Uploaded]("uploaded")                  // zero value = defaults
tako.Cache[FeedPage]("feed", time.Minute)         // 0 = never expires
```

**Handlers and services take one config struct each:**

```go
tako.API("GET /photos", list)
tako.API("POST /photos", create, tako.Route{SignIn: true})
tako.API("POST /charge", charge, tako.Route{Private: true})
tako.Page("GET /p/{slug}", show, tako.Route{SignIn: true})

tako.Service(tako.ServiceConfig{Domain: "shop.example.com", Profile: tako.ProfileLatency})
tako.Worker(thumbs, makeThumb, tako.Compute{Profile: tako.ProfileBatch})
tako.Subscribe(uploaded, warmFeed, tako.Compute{})
tako.Cron("every 24h", cleanup, tako.CronConfig{Singleflight: true, Timezone: "America/Mexico_City"})
tako.Runner(settle, settleOrders, tako.RunnerConfig{Parallel: 8, Attempts: 3})
tako.Singleton(invoice, tako.Footprint{Memory: "512Mi"})
tako.Site("web", tako.SiteConfig{SPA: true, Build: tako.Build{Command: "npm run build", Out: "dist"}})
tako.Auth(tako.AuthConfig{Google: true})
```

**Operations lose their option tier**, which deletes the runtime-error
class the review found (an illegal option reaching production as a 500):

```go
jobs.Publish(ctx, msg)
jobs.PublishAfter(ctx, msg, 4*time.Hour)

policy := tako.PutPolicy{MaxSize: 5 << 20, ContentType: "image/*"}
url, err := photos.SignedPut(ctx, key, time.Minute, policy)
headers := policy.Headers()        // was the free function UploadHeaders
```

### Type-naming convention (stated once, applies forever)

- **Bare noun** when the struct is shared by more than one constructor:
  `Route` (API, Page), `Delivery` (Queue, Topic), `Compute` (Worker,
  Subscribe), plus the existing shared value types `Footprint`,
  `Traffic`, `Build`, `PutPolicy`.
- **`<Ctor>Config`** when it serves exactly one constructor:
  `ServiceConfig`, `CronConfig`, `RunnerConfig`, `SiteConfig`,
  `AuthConfig`.

Fields are flattened rather than embedded — `CronConfig{Singleflight:
true, Profile: …}` reads better at the call site than a nested
`Compute:` field, and the duplication lives in the type definitions
where no user sees it.

### Renames (the residual that config structs cannot reach)

| From | To | Why |
|---|---|---|
| `JobHandler` | `Runner` | the only handler constructor named `<Noun>Handler`; `JobConfig` would also read as config for `tako.Job`. Renaming resolves both |
| `Default`, `Latency`, `Batch` | `ProfileDefault`, `ProfileLatency`, `ProfileBatch` | `tako.Default` is indefensible in a package namespace, and doubly so beside structs whose zero value already means default |
| `On`, `Off`, `Inherit` | `ToggleOn`, `ToggleOff`, `ToggleInherit` | three more generic words reclaimed |
| `WithRetries` | `RunnerConfig.Attempts` | one concept, one word, aligned with `Delivery.MaxAttempts` (ranges stay different and documented) |

`Redirect` and `Email` are freed as a side effect — the first for page
handlers, the second for alerting's notification channel.

### Namespace arithmetic

Deleted: 23 option funcs + the `Option` type = **24**.
Added: `Route`, `ServiceConfig`, `Compute`, `CronConfig`, `RunnerConfig`,
`SiteConfig`, `AuthConfig`, `Build`, `PutPolicy` = **9**.
**Net −15**, and every remaining name is either a constructor, a type,
or a helper — no more free-floating adjectives.

Two analyzer rules die outright: `invalid-option` and `service-option`.
Field-level range validation (`Attempts` 0–10, `Deadline` 15s–30m,
`Timezone` a real IANA zone, `Delivery` ranges) stays — that is domain
validation, not "does this option belong here".

### Slices

1. **X2a — SDK surface.** New types, new signatures, `Option` deleted;
   `sdk_lock.golden` regenerated. Root package only.
2. **X2b — analyzer.** Read composite literals instead of option calls;
   delete the two rules; update goldens and `docs/reference/errors.md`.
3. **X2c — codegen, runtime, devserver.** Whatever reads options today.
4. **X2d — examples.** All six plus their tests; they are the
   acceptance suite.
5. **X2e — docs.** `reference/`, `guide/`, and spec Part 1 rule 2.
   Historical records (`api-review-1/2/3`, `task-log`,
   `stage7-handoff`, `tako-implementation-plan`) keep the old names —
   they record what was true then.
6. **X2f — validate scripts**, then full suite + conformance(local).

Blast radius measured before starting: **343 call sites across 83
files**, of which the historical docs are excluded by the rule above.

### Non-goals

Alerting (next, on the new surface). Any behavior change: this refactor
moves knobs, it does not alter what they do. The one deliberate
exception is the seven constructors that lose their variadic, where the
behavior change is "an illegal call no longer compiles".

## X2 — implemented: config structs replace the Option namespace (2026-08-02)

Shipped all six slices. `Option` and its 23 constructors are gone;
`invalid-option` and `service-option` are gone with them.

### The surface

sdk_lock: **148 → 134 entries**. Deleted 24 (23 option funcs + the
`Option` type), added 10 (`Route`, `Compute`, `ServiceConfig`,
`CronConfig`, `RunnerConfig`, `SingletonConfig`, `SiteConfig`,
`AuthConfig`, `Build`, `PutPolicy`) plus `PublishAfter` and
`PutPolicy.Headers`.

Renames landed as proposed: `JobHandler` → `Runner`,
`Default/Latency/Batch` → `ProfileDefault/Latency/Batch`,
`On/Off/Inherit` → `ToggleOn/Off/Inherit`, `WithRetries` →
`RunnerConfig.Attempts`. `Redirect` and `Email` are freed.

### The defect is unrepresentable now, not fixed

The seven optionless constructors lost their config parameter, so
api-review-3 §2 is a compile error rather than a rule:

```
$ tako gen graph
tako: app.go:14:33: too many arguments in call to tako.Bucket
	have (string, tako.PutPolicy)
	want (string)
```

And a route that tries to size its service — the old `service-option`
rule — is now Go's own:

```
app.go:5:54: unknown field Footprint in struct literal of type tako.Route
```

Caught earlier, says less: the analyzer used to point at `tako.Service`
by name. That trade is written down in `reference/errors.md` rather than
left for someone to discover.

### Two corrections to the approved proposal

**The proposal would have silently removed a capability.** It said
`Singleton` takes a bare `tako.Footprint` and `RunnerConfig` has no
traffic settings. But the old analyzer accepted `WithTraffic` on both
and honored its Timeout — so shipping the proposal as written would have
deleted the job task timeout and the singleton loop timeout with no
error anywhere. Both configs gained a plain `Timeout time.Duration`
(and `SingletonConfig` exists after all). The two `traffic` rules that
policed "a job's only traffic setting is Timeout" are now structural:
there is no `SessionAffinity` field to set.

**`footprintvalue` had silently stopped testing footprint values.** Its
six golden lines were all `service-option` — when V1 moved footprints
off routes, the fixture kept passing while checking nothing about
footprints. Rewritten to declare them where they are legal; it now
asserts five real value errors (CPU 3, Memory "1GB", Concurrency 2000,
non-constant Memory, min>max). Coverage restored, not added.

### Fixtures

Deleted 8 whose rule no longer exists (cronopt, lockopt, siteopt,
subscribeopt, workeropt, invalidoption, serviceoption, mixedprofile) —
every one is a compile error today. Added 2 for the rules that replaced
them: `configliteral` (a config held in a variable; a computed field)
and `oneconfig` (two configs where one is read — the one mistake the
variadic still allows, and silently-dropped configuration is what X2
existed to end).

### Validation

`go test ./...` green including conformance(local) 15.4s; all six
examples green; `go vet` and `gofmt` clean; sdk_lock, runtime_lock,
codegen and appgraph goldens regenerated and diffed line by line — the
gallery graph's only change is line numbers from the multi-line
`tako.Compute` literal, with profile, footprint and singleflight intact.

Live: `tako dev` on hello analyzed, generated, and served
`GET /hello → 200 (0.2ms)`.

Docs: every `reference/` and `guide/` page migrated, spec Part 1 rule 2
rewritten (with the X2 amendment note and the 7%-legal figure),
`reference/errors.md` rebuilt around `config-literal`/`one-config` plus
the new "some mistakes now belong to the compiler" section, and every
`validate/` script updated. Historical records keep the old names.

### Discovered work

`tako.Cache`'s TTL became a required positional parameter (0 = never
expires). That is a real ergonomic change beyond "move the knobs" and
should be called out in the release notes rather than buried here.

## Y1 — DX proposal: alerting on metrics (2026-08-02) — AWAITING APPROVAL

Scope set by the owner across two rounds: **metrics only, no logs**;
**threshold and sustained period are required on every condition**; the
`tako.Incident` topic + Subscribe story is **cut** (an alert delivered
by your own app goes quiet exactly when it matters, and
`tako.Webhook(yourOwnEndpoint)` is the same capability with no new
surface). Written against the post-X2 config-struct surface.

**Which example demands it** (the standing rule): gallery. It has a
dead-letter queue nobody watches and a nightly cron whose failure is
invisible for 24 hours. `Stuck` and `Missed` below are gallery's.

### Declaring

```go
var (
    Oncall = tako.Email("ops@example.com")
    Room   = tako.ChatSpace("spaces/AAAA1234")
    Hook   = tako.Webhook("https://example.com/alerts")
)

var (
    Slow    = tako.Alert(tako.Above(thumbMs, 2000, 5*time.Minute), Room)
    Stalled = tako.Alert(tako.Below(ups, 1, 30*time.Minute), Oncall)
    Broken  = tako.Alert(tako.Above(tako.ErrorRate(gallery), 0.05, 5*time.Minute), Oncall, Room)
    Stuck   = tako.Alert(tako.Above(tako.DeadLetters(thumbs), 0, 5*time.Minute), Oncall)
)
```

Signatures:

```go
func Email(address string) *ChannelHandle
func ChatSpace(space string) *ChannelHandle
func Webhook(url string) *ChannelHandle

func Alert(when Condition, notify *ChannelHandle, also ...*ChannelHandle) AlertHandle

func Above(m Metric, threshold float64, sustained time.Duration) Condition
func Below(m Metric, threshold float64, sustained time.Duration) Condition
func Absent(m Metric, window time.Duration) Condition

func ErrorRate(svc *ServiceHandle) Metric
func DeadLetters[T any](q *QueueHandle[T]) Metric
```

`Metric` is a sealed interface — the `EventSource[T]` pattern at
`topic.go:40` — implemented by `*CounterHandle`, `*HistogramHandle`, and
the derived expressions. Nothing outside the SDK can satisfy it, so the
analyzer can enumerate every subject.

Four shape notes:

- **No config struct, deliberately.** Threshold and period are required,
  so they are parameters (X2's rule: required config is a parameter,
  optional config is a struct). Nothing is optional in v1 —
  `autoClose` and `severity` are framework-owned, the same call `tako.Lock`
  already makes about its lease policy. No example demands them.
- **`notify` is a required parameter, `also` is the variadic.** An alert
  nobody receives is a bug, so it does not compile. No analyzer rule.
- **Channel names come from the variable** (X1): `var Oncall` deploys as
  `x1-gallery-channel-oncall`. The argument is the address, not the name.
- **Alerts are app-scoped**, like `tako.Topic` — they run nowhere, so the
  analyzer must not partition them into a service.

### Why conditions take handles, not metric names

This is the load-bearing argument, not sugar. Your metrics land on
`generic_task` with **one cumulative series per instance**;
`observability.md:187` already tells users to aggregate across `task_id`
when charting. A condition that forgets is per-instance noise or
silence.

Passing the handle means tako knows the type and writes the aggregation:
**Counter → `ALIGN_RATE` + `REDUCE_SUM`; Histogram → `ALIGN_PERCENTILE_95`
+ `REDUCE_MAX`.** A string cannot carry that. Renaming a queue also
renames its alert's filter instead of orphaning it.

### The platform metrics behind the derived conditions

Verified live against tako-test-01 (`projects.metricDescriptors`):

| condition | metric | verified |
|---|---|---|
| `ErrorRate(svc)` | `run.googleapis.com/request_count`, DELTA INT64, labels `response_code,response_code_class,route`, resource `cloud_run_revision` | **yes** |
| `DeadLetters(q)` | `pubsub.googleapis.com/subscription/dead_letter_message_count`, DELTA INT64, label `response_code`, resource `pubsub_subscription` | **yes** |
| `Runs(cron)` — "the cron stopped firing" | *unconfirmed* | **no** |

**`Runs(cron)` is not in the surface above, and that is the point.** A
`starts_with("cloudscheduler.googleapis.com/")` query against
tako-test-01 returned **zero** metric descriptors, and three
documentation fetches came back truncated without the section. I will
not pin a metric name I have not seen. Decision 3 below.

### Destinations map 1:1 onto platform channels

`email`, `google_chat`, `webhook_tokenauth`, `pubsub` are all native
Cloud Monitoring notification-channel types (verified against the
channel-type reference). tako writes **zero delivery code**, and
delivery survives the app being down — which is the whole reason not to
route alerts through your own service.

### Cloud errors & permissions

**Deployer** gains two roles in `docs/tako-cloud-permissions.md:56`:
`roles/monitoring.alertPolicyEditor`, `roles/monitoring.notificationChannelEditor`.
The monitoring API is already enabled by preflight (`preflight.go:99`)
and the runtime SA already has `roles/monitoring.metricWriter`
(`grants.go:96`), so the runtime side needs nothing.

| failure | text |
|---|---|
| 403 on policy create | `you lack permission to create the alert policy on <project> — grant: gcloud projects add-iam-policy-binding … --role roles/monitoring.alertPolicyEditor` |
| 403 API disabled | existing `SERVICE_DISABLED` branch — `the monitoring.googleapis.com API is disabled on <project> — enable it: …` |
| Chat space not installed | `Google Chat space <id> has not installed the Cloud Monitoring app — install it in the space (a Workspace admin may need to approve), then re-run` |
| email unverified (see decision 4) | text pending the live check |

**Two ordering hazards, both already-seen shapes:**

1. Channels must exist before policies reference them → `reconcileOrder`
   puts `KindNotificationChannel` before `KindAlertPolicy`.
2. A Pub/Sub channel needs `roles/pubsub.publisher` for
   `service-<num>@gcp-sa-monitoring-notification.iam.gserviceaccount.com`,
   an agent Google creates **lazily on your first Pub/Sub channel**.
   That is the GCS-service-agent shape `notifications.go:77` already
   solves with a bounded 90s retry, and the `run.routes.invoke`
   propagation shape both release-cycle runs hit. It must reuse that
   pattern deliberately, not rediscover it live.

Both `AlertPolicy` and `NotificationChannel` carry `userLabels`, so
label-based reconciliation works unchanged — no state file, spec-hash
in the labels.

### Local: nothing is evaluated, and it says so

An alert is evaluation over a time window; local has no engine, and a
hand-rolled one would fire on different semantics than Cloud
Monitoring's aligners. **Conformance is law** (settled decision 9), and
the honest move for something that cannot be made conformant is not to
fake it. So:

- `tako dev` lists declared alerts at boot: `3 alerts — evaluated in the
  cloud only`.
- `tako test` gets **nothing**. With the Incident story cut there is no
  delivery path to drive in-process.

**This is the weakest part of the feature and I want it stated plainly
rather than discovered**: alerting will have no local test surface at
all. Its whole correctness story is the analyzer goldens, the
provisioner's fake-server tests, and one live gate walk.

### Surface cost

13 new exported symbols (`Email`, `ChatSpace`, `Webhook`,
`ChannelHandle`, `Alert`, `AlertHandle`, `Condition`, `Metric`, `Above`,
`Below`, `Absent`, `ErrorRate`, `DeadLetters`). sdk_lock 134 → ~147.
Two new provisioner kinds. No new analyzer *option* rules — X2 removed
that category — but new binding rules (a channel that is not a declared
var; `Absent` on a metric that cannot be absent).

### Slices

1. **Y1a** — SDK surface + `Metric` sealing; sdk_lock.
2. **Y1b** — analyzer: alert/channel resources, subject resolution
   through the existing edge machinery, goldens.
3. **Y1c** — provisioner: two kinds, `reconcileOrder`, the pubsub
   service-agent grant with the bounded retry, fake-server tests.
4. **Y1d** — gallery declares `Stuck`; preflight + permissions doc;
   `docs/reference/alerts.md`.
5. **Y1e** — live gate: deploy, force a dead-letter, watch the email
   arrive, destroy clean.

### Decisions

1. **Pub/Sub as a destination?** You listed it twice. It works, but with
   the `Incident` type cut the payload is Google's JSON that your own
   struct must mirror — tako would not own or version it. Include with
   that caveat documented, or defer until something demands it?
2. **`ErrorRate(svc *ServiceHandle)` only covers the app service.** A
   worker service (`gallery-makethumb`) is identified by its `Handler`
   var. Extend `ErrorRate` to accept a `Handler` too (precedent:
   `AdvanceCron(cron Handler)`), or ship app-service-only in v1?
3. **`Runs(cron)`** — cut from v1, or block Y1a until I confirm the
   Cloud Scheduler metric live by deploying a scheduler job and reading
   its descriptors? It is the highest-value alert in the set ("your
   nightly job stopped running") and the only unverified one.
4. **Email verification** — I could not confirm whether channels created
   through the API deliver without address verification. It must be a
   live check before Y1a, not a doc reading.
5. **No local testability** (above) — accept, or does that change your
   appetite for the feature?

**STOP — awaiting approval on the shape and decisions 1–5.**

## Y1 — owner decisions (2026-08-02)

1. **Pub/Sub destination: IN.** `tako.PubSub(topic)` joins Email,
   ChatSpace and Webhook. The caveat that stands: with `tako.Incident`
   cut, the message body is Google's own incident JSON and the app
   defines its own struct for it. `reference/alerts.md` will carry a
   copy-pasteable struct with the fields that matter
   (`incident.incident_id`, `.state`, `.summary`, `.url`,
   `.condition_name`) — documented, not frozen into the SDK, so a
   Google payload change is a doc edit rather than an API break.
2. **`ErrorRate` extended to worker/subscriber services.** It accepts a
   `*ServiceHandle` (the app service) or a `Handler` var — the
   `AdvanceCron(cron Handler)` precedent — so
   `tako.ErrorRate(MakeThumb)` alerts on the thumbnail worker's own
   deployable.
3. **`Runs(cron)`** — resolved by live probe rather than by choosing
   blind (see the entry below).
4. **Verification is instructions, not automation.** tako does not
   drive channel verification or the Chat app install. When a channel
   needs a human step, the deploy prints what to do next and a
   **built console deep link** to the exact page. Same treatment as the
   Identity Platform manual step (W6). This applies to every channel
   type, not just email.
5. **No local test surface: accepted**, with the honest consequence
   recorded — alerting's correctness rests on analyzer goldens,
   provisioner fake-server tests, and the live gate walk.

## Y1 — decision 3 resolved by live probe: Cloud Scheduler publishes NO metrics (2026-08-02)

Rather than ask the owner to choose between cutting `Runs(cron)` and
blocking on an unknown, the unknown was measured.

**Method.** Created `y1-metric-probe` (a `* * * * *` HTTP job) on
tako-test-01, fired it, confirmed it attempted
(`lastAttemptTime 2026-08-02T19:09:05Z`), waited out ingestion, then
paginated the FULL metric-descriptor list for the project — all
**8,667 descriptors** — matching on both the `cloudscheduler` name
prefix and the `cloud_scheduler_job` monitored-resource type.

**Result: zero.** 66 descriptors contain "scheduler" and every one
belongs to Cloud SQL, Composer, GCE or Anthos. Nothing writes to
`cloud_scheduler_job`, even though that resource type is registered
(labels `project_id,location,job_id`). Cloud Scheduler publishes no
Cloud Monitoring metrics. The probe job was deleted; the project is
clean.

**So a "did my cron run" alert cannot be built from a Cloud Scheduler
metric — and does not need one.** The cron reaches its service over
HTTP, and `run.googleapis.com/request_count` carries a **`route`**
label (verified: labels `response_code,response_code_class,route`,
resource `cloud_run_revision`). X1 made the route derivable from the
declaring variable — `var Cleanup` → `/_tako/cron/Cleanup` — so tako can
write the filter itself:

```go
var Missed = tako.Alert(tako.Absent(tako.Runs(Cleanup), 26*time.Hour), Oncall)
```

→ `metric.type="run.googleapis.com/request_count" AND
resource.labels.service_name="<cron's host service>" AND
metric.labels.route="/_tako/cron/Cleanup"`, as a
`conditionAbsent` over 26h.

`Runs(cron)` is therefore **IN v1**, on a verified metric, with no
dependency on logs.

**What it does and does not catch**, to be stated in the reference page:
it fires when no request reached the route in the window — which covers
"Scheduler never fired" AND "fired but never arrived", arguably better
than a scheduler-side metric would. It does NOT catch "fired, arrived,
and the handler failed" — that is `ErrorRate`, a separate alert.

**Note the shape this settles:** the one condition that would have
needed logs is the one the metrics-only scope appeared to rule out, and
it turned out to be expressible in metrics after all because tako owns
the route name. Nothing in the metrics-only decision needs revisiting.

## X2 — cloud verification: full release cycle on tako-test-01 (2026-08-02) — PASS

Ran `validate/release-cycle.md` end to end to close the gap that X2
shipped green locally but had never reached real infrastructure. All
five sections pass; the project is clean.

### The claim X2 needed to prove

```
compute: 2 cpu / 4Gi / concurrency 4 / instances 0–5
         (profile batch + footprint memory 4Gi, concurrency 4, max 5)
```

That is `tako.Compute{Profile: tako.ProfileBatch, Footprint: tako.Footprint{
Memory: "4Gi", Concurrency: 4, MaxInstances: 5}}` — the declaration X2
rewrote — arriving at Cloud Run with every value intact. The knobs moved
without dropping any.

Also verified live rather than only in goldens: the renamed security
text `⚠ 3 mutating route(s) without Route{SignIn: true}`.

### Section results

| § | check | result |
|---|---|---|
| 1 | deploy, five services | pass — 45 creates, 7 updates, 3 skips, 2m47s |
| 2 | cron carries its VARIABLE's name | pass — `x1-gallery-cron-cleanup` → `/_tako/cron/Cleanup`, no `cleanupOrphans` anywhere |
| 3 | the cron actually runs | pass — `POST /_tako/cron/Cleanup → 200 (557.4ms)` then `cron ok` |
| 4 | API serves + re-plan converges | pass — `GET /api/photos → 200`; `0 creates, 0 updates, 55 skips, 0 orphans` |
| 5 | destroy leaves nothing | pass — one pass, no 5xx; no `x1-*` in run, pubsub, service accounts or scheduler |

### The walk paid for itself in preflight

The FIRST deploy attempt died before touching anything:

```
tako: check permissions: Quota exceeded for quota metric 'Read requests'
and limit 'Read requests per minute' of service
'cloudresourcemanager.googleapis.com' (HTTP 429)
```

Not an X2 regression — the analyzer had already produced the right graph
(`5 services, 9 endpoints, 1 worker, 3 subscribers, 1 cron`). The bug is
underneath, and it is the same family as the destroy 5xx fix:
**`testIamPermissions` is a POST**, and `idempotent()` tested the HTTP
method alone, so a rate limit on a call that reads nothing but IAM
killed a deploy that had created nothing. The most retry-worthy call in
the run was the one call that could not retry.

Google's convention puts custom methods after a colon, and several
of the most-called ones are reads wearing POST. `idempotent` now takes
the URL and allows exactly four: `:testIamPermissions`, `:getIamPolicy`,
`:runQuery`, `:runAggregationQuery`. Deliberately an **allowlist, not a
pattern** — `:commit`, `:setIamPolicy`, `:publish`, `:pull` and
`:acknowledge` are colon-suffixed POSTs too, and retrying any of them
duplicates work. Both halves are pinned:
`TestReadOnlyPostsAreRetried` and
`TestMutatingCustomMethodsAreNotRetried`.

Note the quota was charged to project number 764086051850 — the shared
gcloud OAuth client, not tako-test-01. ADC calls without a quota project
land in a global pool, so this is a blip a user can hit through no fault
of their own, which is exactly the case that should retry.

**Honest limit: the new retry never fired in this run.** The 429 did not
recur on the second attempt. The run proves no regression on the path
that matters; the deterministic evidence is the two fake-server tests.

### Two script statements were stale; both fixed

- §3 said "**Expect a 403 first on a fresh deploy**". This run fired the
  cron successfully on the FIRST attempt. The propagation window is real
  but not deterministic — the two earlier runs hit it, this one did not.
  Reworded to "likely but not certain", still with the loop.
- §5 said "Nothing is retried automatically today, so this is on you to
  notice". False since 58d566b. Rewritten to describe the retry and its
  `↻` narration.

A gate script that overstates what will happen trains the walker to
mistrust it, so both were corrected rather than left as folklore.

## Y1a — alerting SDK surface (2026-08-02)

`alert.go`: 11 functions, 2 structs, 2 sealed interfaces. sdk_lock
**134 → 151**. Nothing runs — every value is a declaration read at
analysis time, and no code path here executes locally or in a request.

### What the types enforce that a rule would otherwise have to

- **`Alert(when, notify, also...)`** takes the first channel as a
  PARAMETER. An alert nobody receives does not compile, so there is no
  "alert has no destination" rule to write, forget, or golden.
- **`Metric` is sealed** (`metricSubject() metricRef`, unexported), the
  `EventSource[T]` pattern. Only `*CounterHandle`, `*HistogramHandle`
  and the derived expressions satisfy it, so the analyzer can enumerate
  every subject an alert could name.
- **`Target` is sealed** the same way, over `*ServiceHandle` and
  `Handler` — which is how decision 2 landed: `ErrorRate(MakeThumb)`
  alerts on the worker's own deployable, `ErrorRate(svc)` on the app
  service, and nothing else type-checks.
- **Threshold and window are parameters, not config** (X2's rule:
  required config is positional). `Absent` takes no threshold at all —
  absence IS the condition — so an unused zero cannot be passed.

### Why conditions take handles

Recorded here because it is the design's load-bearing claim: metrics are
written one cumulative series per instance under `generic_task`, so the
aggregation must be chosen per subject. Taking the handle lets tako
derive it from the TYPE — counter → rate + sum across instances,
histogram → p95 + max — which a metric-name string could not carry.
Getting this wrong produces an alert that looks configured and is
per-instance noise.

### Deliberate omissions

No config struct: `autoClose` and `severity` are framework-owned, the
same call `tako.Lock` makes about its lease policy, and no example
demands them yet. No `tako.Incident`: `PubSub(topic)` carries Google's
JSON and the app declares its own struct over it — frozen into the SDK,
a Google payload change would be an API break.

### Validation

`compile_check_test.go` gained the user-style declarations — six alerts
across all four channel kinds, including `PubSub` over a topic declared
LATER in the same var block, which exercises rule 1's corollary that
constructors are side-effect-free records and init order cannot matter.
`TestDeclarationsAreRecords` passes, so nothing panicked at init. Full
suite, vet and gofmt clean.

Next: Y1b (analyzer — alert/channel resources, subject resolution,
goldens).

## Y1a amendment — the aggregation is the question, not a default (2026-08-02)

Owner asked for a p99 option on histograms and for the same scrutiny of
any other metric kind with a hidden knob. Both turned up real problems,
and the histogram one was a design that could not have shipped.

### Probed against the live API, not the docs

Y1a hardcoded `ALIGN_PERCENTILE_95` for histograms. The reference says
percentile aligners are valid for "GAUGE and DELTA metrics with
distribution values" — and tako writes histograms as **CUMULATIVE
DISTRIBUTION** (`custom.googleapis.com/tako/thumb_ms`, confirmed on
tako-test-01). Creating real alert policies against that real metric:

```
ALIGN_PERCENTILE_99 + REDUCE_MAX   -> HTTP 400  the aligner cannot be applied to
ALIGN_PERCENTILE_95 + REDUCE_MAX   -> HTTP 400  metrics with kind CUMULATIVE and
ALIGN_PERCENTILE_50 + REDUCE_MEAN  -> HTTP 400  value type DISTRIBUTION
ALIGN_MEAN          + REDUCE_MEAN  -> HTTP 400
ALIGN_DELTA         + REDUCE_MEAN  -> HTTP 200
```

**Y1a's hardcoded p95 would have 400'd at deploy on every histogram
alert.** Two-stage aggregation is the shape that works, also verified:

```
[{ALIGN_DELTA, REDUCE_NONE}, {ALIGN_PERCENTILE_99}]        -> HTTP 200
[{ALIGN_DELTA, REDUCE_NONE}, {ALIGN_PERCENTILE_95, REDUCE_MAX}] -> HTTP 200
```

Y1c must emit the two-stage form. Every probe policy was created
disabled and deleted; the project ends with 0 alert policies.

### The same scrutiny found a worse bug in counters

`Below(ups, 1, 30*time.Minute)` — Y1a's own stall example — meant
"fewer than one upload in half an hour" to a reader, and
`ALIGN_RATE + REDUCE_SUM` to the provisioner: a rate under one PER
SECOND, which is almost always true. It would have fired constantly
while looking correct. Both readings are legal
(`ALIGN_RATE` and `ALIGN_DELTA`, both HTTP 200 on the live counter) —
which is exactly why picking one silently was wrong.

### The fix: raw handles are no longer metrics

`*CounterHandle` and `*HistogramHandle` no longer implement `Metric`. A
recording is not a question; the reading is written where it is read:

```go
tako.Rate(uploads)    tako.Count(uploads)
tako.P99(thumbMs)     tako.P95(…)  tako.P50(…)  tako.P05(…)
```

Verified with the real CLI — all three ambiguous forms are now compile
errors, not analyzer rules:

```
cannot use lat (variable of type *tako.HistogramHandle) as tako.Metric value in
  argument to tako.Above: *tako.HistogramHandle does not implement tako.Metric
cannot use ups (variable of type *tako.CounterHandle) as tako.Metric value ...
cannot use ups (variable of type *tako.CounterHandle) as *tako.HistogramHandle
  value in argument to tako.P99
```

**Only four percentiles, and that is Google's limit, not a v1 scope
cut**: the Aligner enum offers 99, 95, 50 and 05 and nothing else, so
there is no P90 to add later. Documented on the constructors so nobody
proposes one.

`DeadLetters` keeps a single reading deliberately — a per-second
dead-letter rate is not a question anyone asks — and says so rather
than leaving it as an unexplained asymmetry.

sdk_lock **151 → 157**.

## Y1 — alignment: measured limits, and one that kills an example (2026-08-02)

Owner asked what decisions alignment forces. It forces three, and the
answer to one of them is that a promised alert cannot be built.

`alignmentPeriod` and `duration` are different knobs and Y1a's probes
conflated them by setting them equal. `alignmentPeriod` is the bucket
each series is reduced into; `duration` is how long the breach must
persist. Measured against the live API (every probe policy created
disabled and deleted; project ends at 0 policies):

| field | accepted | refused |
|---|---|---|
| `alignmentPeriod` | 60s … 90000s | 30s (`too short`), 172800s |
| `conditionThreshold.duration` | ≤ 86400s (24h) | 93600s — *"Durations longer than 24[h]"* |
| **`conditionAbsent.duration`** | **≤ 84600s (23h30m)** | 84660s — *"Durations longer than 23h30m"* |

`conditionAbsent` also **requires** an aggregation with a
`perSeriesAligner` — *"An aggregation with a perSeriesAligner is
required"* — so absence is not the config-free condition it looks like.

### The decision that has no good answer: daily crons

Y1's flagship example was
`tako.Alert(tako.Absent(tako.Runs(Cleanup), 26*time.Hour), Oncall)` —
"tell me when the nightly job stops running". **It is not
expressible.** Absence caps at 23h30m, which is SHORTER than a daily
cron's own period, so any window tako could set would fire on a cron
that ran perfectly and merely ran late. And a threshold condition is not
a substitute: a cron that never fires produces no time series at all,
and threshold conditions do not evaluate on missing data — which is
exactly why `conditionAbsent` exists.

So the honest boundary: **`Absent(Runs(cron), …)` works for crons that
run more often than roughly every 11h45m** (window = 2x period), and
cannot reliably watch a daily one. This is Google's ceiling, not a
tako choice, and it needs an owner decision rather than a quiet cap.

### Decision 2: alignment is per-READING, not one global policy

There is no single alignmentPeriod that serves every metric, because
the reading changes what the period MEANS:

- `Count(c)` — the alignment period IS the counting window. With
  `alignmentPeriod=60s`, `Below(Count(ups), 1, 30m)` means "fewer than
  one upload per MINUTE, for 30 minutes"; with
  `alignmentPeriod=30m, duration=0` it means "fewer than one upload in
  30 minutes". Only the second matches what the call site reads like.
- `Rate(c)` — units are per-second whatever the period, so the period
  is just smoothing. 60s with `duration=window` is the standard shape.
- `P99(h)` and friends — the period is the bucket the percentile is
  computed over. 60s with `duration=window` again.
- `ErrorRate` — a ratio; same as Rate.

**Recommendation (no new API surface):** derive it per reading —
`Count` and `DeadLetters` use `alignmentPeriod = the stated window` with
`duration = 0`; `Rate`, `ErrorRate` and the percentiles use
`alignmentPeriod = 60s` with `duration = the stated window`. The call
site keeps reading in English either way, and `reference/alerts.md`
states the mapping so nobody has to infer it.

The alternative — exposing an `Every(…)` knob — adds a parameter whose
correct value is derivable in every case tako supports.

### Decision 3: threshold durations over 24h

`sustained` above 24h must be refused at ANALYSIS time with the real
ceiling in the message, not passed through to a deploy-time 400.
Ditto absence over 23h30m.

**STOP — decisions for the owner:**

1. **Daily crons.** Options: (a) ship `Runs`/`Absent` with the 23h30m
   cap and an analyzer error naming it, documenting that daily crons
   cannot be watched this way; (b) cut `Runs` from v1 and say so; (c)
   have the cron increment a Counter and watch `Count` instead — which
   evaluates only while the series still exists, so it trades a hard
   limit for a subtler failure. My recommendation is (a): the limit is
   real, and an error that names it teaches more than a missing feature.
2. **Alignment derivation** as recommended above, or expose the knob?

## Y1 — exposing alignment (owner direction), and what it does NOT fix (2026-08-02)

Owner: expose the alignment windows rather than abstracting them away —
"let some of these details leak, maybe create an easy way to configure
them, but not necessarily hiding the knobs". Adopted; it matches how
tako already treats cost and footprint (surfaced, never hidden).

**But the accompanying hypothesis — that absence then covers every
case — is false, and was measured rather than argued:**

```
absent For=26h, align=60s     -> HTTP 400  Durations longer than 23h30m
absent For=26h, align=3600s   -> HTTP 400  Durations longer than 23h30m
absent For=26h, align=86400s  -> HTTP 400  Durations longer than 23h30m
PromQL duration=26h           -> HTTP 400  (same ceiling)
PromQL duration=48h           -> HTTP 400  (same ceiling)
```

The cap is validation on the `duration` FIELD. It does not move with
alignment, and switching condition types does not escape it —
`conditionPrometheusQueryLanguage` refuses the same durations, so there
is no back door. **A daily cron still cannot be watched by absence**,
and no amount of exposed knobs changes that.

Worth stating concretely why 23h30m is not "close enough" for a daily
cron: a job at 00:00 has its last data point 23h30m ago at 23:30, so the
condition goes true at 23:30 and resolves at 00:00 — a false page every
single day, forever.

### The shape: tako.Window

```go
type Window struct {
    For   time.Duration // how long the condition must hold
    Every time.Duration // the alignment period — the bucket each series is reduced into
}

tako.Above(tako.P99(thumbMs), 2000, tako.For(5*time.Minute))
tako.Below(tako.Count(uploads), 1, tako.Window{Every: 30 * time.Minute})
tako.Absent(tako.Runs(Sweep), tako.For(2*time.Hour))
```

`tako.For(d)` is the easy path; the struct is there when the bucket
matters. `Every` reads correctly in both directions — "the p99 computed
every minute", "uploads counted every 30 minutes" — because it IS the
period each series is reduced into, which is the honest description.

Zero `Every` derives per reading and the reference states the mapping:
60s for rate, ratio and percentile readings (units are independent of
the bucket, so the bucket is only smoothing); for COUNT readings there
is no defensible default — the bucket is the counting window, and
`Count(x)` with an unstated period means nothing — so the analyzer
refuses it and says what to write.

Measured bounds become analyzer errors with the real ceilings:
`Every` 60s–90000s, `For` ≤24h on thresholds, ≤23h30m on absence.

**STILL OPEN — the daily-cron decision from the previous entry.**
Exposing alignment did not resolve it. Options unchanged: (a) ship
`Runs`/`Absent` with the cap and an analyzer error that names it and
explains the daily-cron consequence; (b) cut `Runs`; (c) counter
heartbeat. Recommendation remains (a).

## Y1 — PromQL lookbacks defeat the 23h30m cap; daily crons ARE watchable (2026-08-02)

Owner asked whether `increase(my_cron_job_success_total[26h]) == 0`
would work instead of `conditionAbsent`, and said not to take their word
for it. Researched and probed. **The approach is right and it resolves
the open decision — the specific query is not, and only the probe
showed why.**

### Why it works: the range selector is not the duration field

The 23h30m ceiling is validation on `duration` — how long a breach must
persist. A PromQL **lookback** lives inside the query, where no such
check applies:

```
absent_over_time(...[26h]), duration=0s   -> HTTP 200 ACCEPTED
absent_over_time(...[7d]),  duration=0s   -> HTTP 200 ACCEPTED
absent_over_time(...[26h]), duration=60s  -> HTTP 400
   "Retest windows are not supported in PromQL queries with lookbacks longer than 1d1h."
```

So a long lookback must pair with `duration: 0s` — which is the right
shape anyway: with a 26-hour window there is nothing to retest.

### Why `increase(...) == 0` is the wrong query, proven not argued

Both forms are ACCEPTED by the API, so a create-time check would have
passed either. Querying live data separates them:

```
increase(request_count{route="/_tako/cron/Cleanup"}[26h])        -> 0 series
increase(request_count{route="/_tako/cron/Cleanup"}[26h]) == 0   -> 0 series
absent_over_time(request_count{route="/_tako/cron/Cleanup"}[26h]) -> 1 series
```

`increase()` over a range with no samples returns NOTHING, and `== 0`
filters nothing, so the alert never fires — the exact silence it was
meant to break. `absent_over_time` returns 1 precisely when the range is
empty. This is the classic PromQL missing-data trap, and it would have
shipped as an alert that looked correct and never fired.

Both readings have a use and the reference should say so:
- **`absent_over_time`** — "the cron stopped running" (no data at all).
- **`increase(...) == 0`** — "the counter is still reported but has not
  moved", which needs the series to exist.

### Design consequence

`Absent` compiles to a PromQL `absent_over_time` condition with
`duration: 0s`, not to `conditionAbsent`. One code path, and the
23h30m cap disappears:

```go
var Missed = tako.Alert(tako.Absent(tako.Runs(Cleanup), tako.For(26*time.Hour)), Oncall)
```

`Window.For` on an Absent condition is therefore the LOOKBACK, which is
what a reader already assumes it is.

`Above`/`Below` stay on `conditionThreshold`: their 24h ceiling is not
painful, and threshold conditions render better in the console.

Also learned, and required for Y1c: a `custom.googleapis.com/*` selector
must carry a `monitored_resource` matcher —
`custom_googleapis_com:tako_uploads{monitored_resource="generic_task"}`
returned 3 series, the bare name errored with *"must specify a label
matcher on the 'monitored_resource' label"*. Name mapping is the
documented rule: first `/` becomes `:`, every other special character
becomes `_`.

**The daily-cron decision (a)/(b)/(c) is withdrawn — none is needed.**
The owner's question produced a fourth option that costs nothing and
removes the limit.

Every probe policy was created disabled and deleted; project ends at 0.

## Y1 — PromQL vs the aggregations model, settled with live data (2026-08-02)

Redeployed gallery to `x1`, drove 14 photos of three sizes through the
real pipeline so `thumb_ms` held a genuine spread, and asked both models
the same question.

**Control — aggregations model, two-stage via `timeSeries`:**

```
ALIGN_DELTA -> ALIGN_PERCENTILE_99   509.0133333333333
ALIGN_DELTA -> ALIGN_PERCENTILE_50   362.6666666666667
```

**PromQL, same distribution:**

```
histogram_quantile(0.99, rate(custom_googleapis_com:tako_thumb_ms_bucket{...}[15m]))  509.0133333333333
histogram_quantile(0.50, rate(...))                                                  362.6666666666667
histogram_quantile(0.87, rate(...))                                                  473.1733333333333
```

**Identical to the last decimal.** The two models compute the same
thing; there is no semantic divergence to fear. `_count` = 14 and
`_sum` = 4216 (mean 301.1) match the control's distribution exactly.

### The detail that made the earlier probe look negative

A Cloud Monitoring DISTRIBUTION is exposed to PromQL through the
standard Prometheus decomposition — `_bucket`, `_count`, `_sum` — and
NOT under its bare name:

```
custom_googleapis_com:tako_thumb_ms{...}         0 series
custom_googleapis_com:tako_thumb_ms_bucket{...}  21 series
custom_googleapis_com:tako_thumb_ms_count{...}   1 series  ['14']
custom_googleapis_com:tako_thumb_ms_sum{...}     1 series  ['4216']
```

The earlier probe queried the bare name, got `0 series`, and I could not
tell "wrong name" from "no data" because the env was destroyed. With
data present the same query still returns 0 — which is what makes the
suffix, not the data, the explanation.

### What this settles

1. **Percentiles work in PromQL over tako's histograms**, and agree
   exactly with the aggregations model.
2. **Arbitrary percentiles work.** `histogram_quantile(0.87, …)`
   returned 473.17. The "only four percentiles exist" limit recorded in
   the Y1a amendment is an artifact of the `Aligner` enum — a property
   of the API I happened to pick first, not a fact about the metric.
   `P99/P95/P50/P05` can become `Percentile(h, 0.87)`.
3. **One model is viable**, and strictly more expressive: PromQL already
   owns `Absent` (the 23h30m escape), and it can own the thresholds too.

### Cost of going one-model, stated honestly

PromQL conditions render as a query in the console rather than as an
editable threshold, so an operator reading the policy sees tako's
generated PromQL instead of a form. Against that: one code path in Y1c,
no aligner/reducer compatibility matrix (the CUMULATIVE+DISTRIBUTION
refusal that broke Y1a could not happen), arbitrary percentiles, and no
duration ceilings.

Y1c must encode two things the probes proved necessary: the
`monitored_resource="generic_task"` matcher (a bare custom-metric
selector errors) and the `_bucket` suffix for distributions.

**Decision for the owner: one model (PromQL) or two?** Recommendation:
one. The split existed because of the order I discovered things, and
every measured advantage now runs the same way.

## Y1a amendment 2 — one model (PromQL), Percentile, Window (2026-08-02)

Owner decided: one model. Everything compiles to PromQL, including the
thresholds that were going to stay on `conditionThreshold`.

### Surface

sdk_lock 157 → **156**: four named percentile helpers become one
function, and `Window`/`For` replace the bare duration.

```
- P99 / P95 / P50 / P05
+ Percentile(h *HistogramHandle, q float64) Metric
+ Window{For, Every} and For(d)
~ Above / Below / Absent now take a Window
```

`Percentile(lat, 0.87)` is the point: it answers 473.17ms on the same
data where the aligner path answers nothing, because Cloud Monitoring's
`Aligner` enum has exactly four percentile members and PromQL's
`histogram_quantile` has none of that limit. Y1a recorded "only four
percentiles exist, and that is Google's limit, not a v1 scope cut" —
that was true of the API I had picked and false of the platform. Kept as
one function rather than one function PLUS named sugar: review 2's rule
that one knob with two spellings is worse than a breaking change.

### Window: the knobs are stated, not inferred

`For` is how long a breach must persist; on `Absent` it is the lookback,
since absence has nothing to sustain. `Every` is the period each series
is reduced into — the rate window, the counting window, the percentile
bucket. Zero derives 60s for rate/ratio/percentile readings, where the
period only smooths. **Count has no default**: the period IS the count,
so `Count` with no `Every` is an analysis error rather than a guess —
the same call as making `Cache`'s TTL positional.

### Why one model won

Measured, all in this log: identical values to the aggregations path
(509.0133333333333 / 362.6666666666667 to the last decimal), arbitrary
quantiles, no duration ceiling (`[26h]`, `[7d]` both accepted), and no
aligner/reducer compatibility matrix — the CUMULATIVE+DISTRIBUTION
refusal that would have shipped a broken Y1a cannot arise.

Cost accepted: a policy renders in the console as tako's generated
query rather than an editable threshold form.

Y1c must encode what the probes proved: `monitored_resource="generic_task"`
on custom-metric selectors, the `_bucket` suffix for distributions, the
`{domain}:{name}` mapping (first `/` → `:`, other specials → `_`), and
`duration: 0s` whenever a lookback exceeds 1d1h.

Next: Y1b (analyzer) on a single code path.

## Y1a amendment 3 — the reading vocabulary, completed (2026-08-02)

Owner asked whether rate/sum/delta are needed beyond count and
percentile, and told me to verify `increase` is the right function
rather than assume it.

### Every PromQL function parses — which proves nothing

Probed 18 functions against the live endpoint. `increase`, `delta`,
`rate`, `irate`, `sum_over_time`, `avg_over_time`, `min/max/last/present/
absent_over_time`, `resets`, `histogram_quantile`, `predict_linear`,
`deriv` — **all PARSE**. Cloud Monitoring implements the full function
set, so availability is not the constraint and a parse check cannot
choose between them. That is the same trap as `increase(...) == 0`,
which also parsed and would never have fired.

The choice is semantic, and PromQL's semantics decide it:

- **`increase(v[w])`** — counter-only, reset-aware, extrapolates a
  partial window. **Correct for tako's metrics**, which are written
  CUMULATIVE per instance with series appearing and vanishing as
  instances scale — exactly the regime it exists for.
- **`delta(v[w])`** — defined for GAUGES: last minus first, no reset
  handling. On a counter an instance restart reads as a negative delta.
  Wrong here, and wrong quietly.
- **`avg_over_time` / `sum_over_time`** — operate on RAW samples. On a
  cumulative counter that averages running totals, which is not a rate,
  a count, or anything a user means.

So `increase` for Count, `rate` for Rate, and for the mean
`increase(_sum)/increase(_count)` — both halves counter-correct, rather
than `avg_over_time(_sum)` which would report the mean of running sums.
Verified arithmetically against the recorded distribution: 4216ms over
14 observations is 301.1ms, exactly what Cloud Monitoring reports as the
distribution's own mean.

**For the Y1e gate:** compare `increase` against `delta` on live data
across an instance scale event. The reasoning above is definitional, but
the failure it predicts has not been watched happen.

### The gap this closed

A histogram could be asked about its SHAPE and never its VOLUME — you
could alert that thumbnails got slow, but not that none were made. That
is the louder failure and it was unexpressible.

`Recording` (sealed over Counter and Histogram) now backs `Rate` and
`Count`, so "how fast" and "how many" work on either. `Mean` and `Sum`
are histogram-only. sdk_lock 156 → **159**.

| reading | counter | histogram | query |
|---|---|---|---|
| `Rate` | ✓ | ✓ | `rate(x[w])` / `rate(x_count[w])` |
| `Count` | ✓ | ✓ | `increase(x[w])` / `increase(x_count[w])` |
| `Sum` | — | ✓ | `increase(x_sum[w])` |
| `Mean` | — | ✓ | `increase(x_sum[w]) / increase(x_count[w])` |
| `Percentile` | — | ✓ | `histogram_quantile(q, rate(x_bucket[w]))` |

`Sum` included on owner instruction, overriding my "no example demands
it" deferral. Deliberately NOT added: `delta` (for a counter it IS
increase — one concept, three words), the raw cumulative value (resets
per instance, so alerting on it is a footgun), and `sum` across
instances (that is the cross-series reduction every reading already
applies, not a user-facing choice).

This is the fourth time a vocabulary looked complete because I had only
enumerated the mechanism I started from. The table above is the
enumeration that should have existed when `Metric` was first designed.

## Y2 — proposal: platform metrics and baseline alerts (2026-08-02) — AWAITING APPROVAL

Two owner asks that are their own slices, not amendments.

### Y2a — referencing Google's own metrics

The problem is scale, and it is measured: paginating this project's
descriptors returned **8,667 metric types**. A hand-written reference
page would be stale before it was committed, and a generated one would
be 8,667 rows nobody reads. So the answer is three-tiered, not one doc.

**1. Derived — no strings at all.** The metrics an app actually pages on
are about things tako already knows, so they take a Target and tako
writes the filter:

```go
tako.ErrorRate(svc)              // have
tako.Latency(svc, 0.99)          // run.googleapis.com/request_latencies
tako.Instances(svc)              // container/instance_count
tako.Saturation(svc)             // container/cpu/utilizations
```

Rename-safe, autocompletable, and impossible to point at a metric the
service does not emit.

**2. `tako.Platform` — the honest escape hatch** for the other 8,660:

```go
var Egress = tako.Alert(
    tako.Above(tako.Platform("run.googleapis.com/container/network/sent_bytes_count",
        tako.MetricOn(svc)), 1<<30, tako.For(10*time.Minute)),
    Oncall)
```

Strings, because the name IS Google's and pretending otherwise would be
a lie; the analyzer enforces constants and tako derives the PromQL
mapping (first `/` → `:`, other specials → `_`) plus the
`monitored_resource` matcher the probes proved mandatory.

**3. `tako metrics <search>` — a reference that cannot rot.** Queries
`projects.metricDescriptors` live and prints type, kind, value type and
labels, so a user finds the exact string from the terminal:

```
$ tako metrics network/sent
run.googleapis.com/container/network/sent_bytes_count
  DELTA INT64 · resource cloud_run_revision · labels: (none)
```

A live query beats a doc precisely because there are 8,667 of them and
Google adds more without telling us.

### Y2b — baseline alerts, derived from the graph

Owner: pre-built rules that do not need configuring from scratch. tako
already knows every service, queue, dead-letter topic, cron and its
schedule — the same graph that derives IAM can derive the alerts every
app wants and nobody writes:

```go
var Ops = tako.Baseline(Oncall, Room)
```

One declaration; the policies come from the graph:

| for every | alert |
|---|---|
| service | 5xx rate over 5% for 5m |
| queue with a dead letter | any dead letter in 5m |
| cron | no run in 2x its schedule — tako KNOWS the schedule, so `every 6h` gets a 12h lookback and a daily job gets 26h, which only works because `Absent` compiles to PromQL |
| job | a failed execution |

Three properties that make this tako-shaped rather than a template:

- **Derived, not copied.** Add a queue, get its dead-letter alert. The
  same argument as derived IAM.
- **Visible, not magic.** `tako deploy` lists every policy Baseline
  created, the way the security summary lists derived grants. A user
  who cannot see what was created cannot trust it.
- **Overridable.** An explicit `tako.Alert` on the same subject replaces
  the baseline's rather than doubling it — otherwise the first thing
  anyone does after adopting Baseline is get paged twice.

Open questions for the DX proposal: whether Baseline takes thresholds
(`tako.Baseline(Oncall, tako.BaselineConfig{ErrorRate: 0.02})`) or is
deliberately unconfigurable so it stays a floor rather than a second
configuration surface; and whether it belongs behind an opt-in at all,
given a user who declares it and never reads the summary has alerts they
did not write.

**STOP — approve Y2a and Y2b as slices before Y1b, after it, or not at
all?** They are additive to everything Y1 has settled; Y1b's analyzer
work does not block on either.

## Y2b revised — named situations instead of a Baseline bundle (2026-08-02)

Owner redirected: rather than `tako.Baseline(...)` deriving a bundle,
give pre-built alerts for KNOWN SITUATIONS that take a parameter and
require no knowledge of the metric, reading, or window — "dead-letter
queue got N messages", "cron failed N times".

Better than Baseline, and for a reason worth writing down: a bundle
gives you alerts you did not write and cannot see without reading the
deploy summary. A named situation is one line you chose, in your source,
that still hides everything you should not have to know.

### The shape

```go
var (
    Stuck   = tako.Alert(tako.DeadLettered(thumbs, 5), Oncall)
    Flaky   = tako.Alert(tako.CronFailed(Cleanup, 3), Oncall)
    Silent  = tako.Alert(tako.CronMissed(Cleanup), Oncall)
    Failing = tako.Alert(tako.Erroring(svc, 0.05), Oncall, Room)
    Crawling = tako.Alert(tako.SlowerThan(svc, 2*time.Second, 0.99), Room)
    Broken  = tako.Alert(tako.JobFailed(settle, 1), Oncall)
)
```

No metric names, no readings, no aggregation, no window. Each is a
`Condition`, so it composes with the existing `tako.Alert` and needs no
second declaration form.

### What each one knows that the user should not have to

| preset | derives |
|---|---|
| `DeadLettered(q, n)` | the queue's dead-letter subscription, `increase` over the default window |
| `CronFailed(cron, n)` | the cron's route from its VARIABLE (X1), filtered to `response_code_class="5xx"` |
| `CronMissed(cron)` | the route AND the schedule — `every 6h` becomes a 12h lookback, a daily job 26h, which only works because `Absent` compiles to PromQL |
| `Erroring(target, frac)` | 5xx over total for a service or one async handler |
| `SlowerThan(target, d, q)` | `request_latencies` at the quantile |
| `JobFailed(job, n)` | failed executions of that job |

`CronMissed` is the one that could not have existed before this week:
it needs the variable-derived route (X1), the schedule from the graph,
and a lookback past 23h30m (the PromQL finding).

### The knobs are defaulted, not hidden

Each preset takes an optional trailing `Window`, so the default is a
starting point rather than a ceiling:

```go
tako.CronFailed(Cleanup, 3)                       // default window
tako.CronFailed(Cleanup, 3, tako.For(time.Hour))  // your window
```

That keeps faith with the earlier direction — let the knobs leak — while
answering the actual request: the common case should be one short line.
The defaults are documented per preset in `reference/alerts.md`, never
inferred.

### Layering, stated once

- **Presets** for situations tako understands, because it owns the
  queue, cron, job and service.
- **The raw vocabulary** (`Above`/`Below`/`Absent` over
  `Rate`/`Count`/`Sum`/`Mean`/`Percentile`) for YOUR counters and
  histograms, which tako cannot name situations for.

Neither replaces the other, and the presets are written in terms of the
raw vocabulary rather than beside it — so there is one evaluation path,
and a preset is inspectable as the condition it expands to.

**`tako.Baseline` is withdrawn.** Its one genuine advantage — new queue,
free alert — is not worth alerts a user did not write.

**STOP — approve Y2a (platform metrics) and Y2b-revised (presets)?**
Neither blocks Y1b.

## Y2b — the preset catalogue, enumerated against tako's primitives (2026-08-02)

Owner asked what OTHER situations deserve presets, given the resources
tako offers. Walked every declarable primitive against real metric
descriptors on tako-test-01 rather than listing from memory — the
enumeration I should have done before proposing any of them.

| tako primitive | known failure | metric (verified) | preset |
|---|---|---|---|
| Queue | work gave up | `subscription/dead_letter_message_count` DELTA | `DeadLettered(q, n)` |
| Queue | **work is piling up** | `subscription/oldest_unacked_message_age` GAUGE | **`Backlogged(q, age)` — NEW** |
| Subscribe | a subscriber gave up alone | same, per-subscription | **extend `DeadLettered` to subscribers — NEW** |
| Cron | ran and failed | `request_count{route,5xx}` DELTA | `CronFailed(cron, n)` |
| Cron | stopped running | `request_count{route}` absence | `CronMissed(cron)` |
| Job/Runner | execution failed | `job/completed_execution_count` DELTA, label `result` | `JobFailed(job, n)` |
| Service | erroring | `request_count{response_code_class}` | `Erroring(t, frac)` |
| Service | slow | `request_latencies` DELTA DISTRIBUTION | `SlowerThan(t, d, q)` |
| Service | **capped and shedding** | `container/instance_count` GAUGE | **`AtCapacity(svc)` — NEW** |
| Singleton | the exactly-one loop is not running | `container/instance_count` GAUGE | **`SingletonDown(loop)` — NEW** |

### The four additions, and why each earns a symbol

- **`Backlogged(q, age)`** is the one I would most regret omitting.
  `DeadLettered` fires only after a message has exhausted its retries;
  a queue whose consumer is down or too slow shows nothing until then.
  Oldest-unacked-age is the signal that work is stuck NOW.
- **`AtCapacity(svc)` takes no parameter**, because tako already knows
  `MaxInstances` from the footprint. Same shape as `CronMissed` knowing
  the schedule: the graph supplies what the user would otherwise repeat.
- **`DeadLettered` on subscribers** follows tako's own doctrine that
  subscribers fan out with independent fates and dead-letter
  independently. Offering it only for queues would contradict the model
  the docs teach.
- **`SingletonDown(loop)`** is the only check for a primitive whose
  entire promise is "exactly one". A singleton serves nothing but its
  health check, so no request metric can see it; instance count can.

### Declined, with reasons rather than silence

- **Queue DEPTH** (`num_undelivered_messages`): dominated by
  `Backlogged`. Depth alone is normal under load; depth that is not
  draining is what age already says.
- **Bucket, Cache, Lock, Secret**: no failure a preset could name.
  Cache is per-instance in memory and emits nothing; Lock and Secret
  failures surface as handler errors, which `Erroring` already covers.
- **Entity**: `datastore.googleapis.com/api/request_count` carries
  `response_code`, but a Datastore failure reaches the user as a 5xx
  from the handler — a second alert on the same incident.
- **Site, Auth**: part of a service, covered by `Erroring`/`SlowerThan`.

Ten presets total, four of them new, each pointing at a metric confirmed
to exist with the kind and labels the query needs.

## Y2 — presets and Platform folded into the SDK (2026-08-02)

Nine presets, `tako.Platform`, and the `Consumer` seal. sdk_lock
159 → **167**.

### Three symbols REMOVED, not kept alongside

`ErrorRate`, `DeadLetters` and `Runs` are gone. Each was a raw Metric
whose only use was one obvious condition, and the preset says the same
thing better:

```
- tako.Above(tako.DeadLetters(thumbs), 5, tako.For(5*time.Minute))
+ tako.DeadLettered(thumbs, 5)
```

Keeping both would be two spellings of one knob — review 2's rule, and
the near-identical `DeadLetters`/`DeadLettered` pair would have been a
standing trap.

### Erroring and SlowerThan reach routes and sites (owner)

`request_count` and `request_latencies` both carry a **`route`** label,
verified on the live descriptors, so `Target` now seals over four
things: a Service, an async Handler, one `Endpoint` (API or Page), and a
`*SiteHandle`.

```go
Erroring(svc, 0.05)                       // the whole service
Erroring(List, 0.02)                      // ONE route
Erroring(site, 0.02)                      // a site's routes
SlowerThan(List, 2*time.Second, 0.99)     // that route's p99
```

Per-route was worth having and nearly missed: without it "checkout is
erroring" would have had to be spelled as "the service containing
checkout is erroring", which is a different and much noisier claim.

### What each preset knows so the user does not

`CronMissed` reads the SCHEDULE and doubles it — `every 6h` becomes a
12h lookback, daily becomes 26h — so there is no interval to repeat and
no way for the two to disagree. `AtCapacity` takes no threshold at all,
reading `MaxInstances` from the footprint. Both are the derived-IAM
argument applied to alerting: the graph already knows, so the user
should not have to say it twice.

Defaults are per preset and documented, with an optional trailing
`Window` on every one — defaulted, not hidden.

Next: Y1b, the analyzer, against a settled surface.

## Y1b — the analyzer learns alerts (2026-08-02)

Channels and alerts are now AppGraph resources. They are **app-scoped**,
like topics: they run nowhere, so they are never partitioned into a
service.

### Alerts resolve in a THIRD pass

Resources, then handlers, then alerts — because a condition names
handlers and routes (`CronMissed(Cleanup)`, `SlowerThan(Photos, …)`) and
every handler must already be in the graph before a subject can be
checked against it. This required hoisting the var scan out of the
resource loop into `declsOf`, which is now walked twice.

### Five rules, and why each exists

| rule | catches |
|---|---|
| `const-name` | a destination computed at run time — read at analysis time or not at all |
| `channel-binding` | `tako.Alert(cond, tako.Email("adhoc@…"))` — an inline channel tako never declared, so never created |
| `channel-binding` | `tako.PubSub` over a topic that is not a package-level var: the deploy has to grant the notification agent publisher on a real topic |
| `alert-subject` | **`CronMissed(Work)` where Work is a worker** |
| `quantile` | `SlowerThan(…, 99)` and `Percentile(lat, 99)` — a quantile is a fraction |

`alert-subject` is the one the type system cannot reach. Every async
handler is a `tako.Handler`, so `CronMissed(aWorker)` compiles cleanly;
only the graph knows which kind each var is. Verbatim:

```
app.go:28:41: tako.CronMissed watches a cron, but alerts.Work is a worker (tako/alert-subject)
```

The quantile rule earns its place by naming the mistake rather than the
range — "0.99 is the 99th percentile, not 99" — because passing 99 is
what everyone does once.

### Visible in the summary

An alert nobody can see is an alert nobody trusts, so `tako gen graph`
and the dev banner name the condition and every destination it will
wake:

```
    channel oncall → email ops@example.com
    channel bus → pubsub incidents
    alert Stuck — DeadLettered → oncall
    alert Piling — Backlogged → oncall, bus
    alert Missed — CronMissed → oncall
```

Verified end to end with the real CLI: four alerts across three preset
kinds and two channel kinds, with the Pub/Sub channel resolving to its
declared topic name.

Next: Y1c — the provisioner. Four things the probes pinned that it must
not rediscover: the `monitored_resource="generic_task"` matcher, the
`_bucket` suffix for distributions, the `{domain}:{name}` mapping, and
`duration: 0s` whenever a lookback exceeds 1d1h.

## Y1c — the query builder and the kinds; and a gap Y1b left (2026-08-02)

### Shipped

`pkg/provision/promql.go` turns a condition into the query Cloud
Monitoring evaluates, and every decision in it is one the probes forced:

- `promName` implements the documented mapping (first `/` → `:`, other
  specials → `_`), tested against five real metric types.
- Every selector carries `monitored_resource`, because a bare
  custom-metric selector is refused outright.
- Every histogram reading goes through `_bucket`/`_count`/`_sum`. A test
  fails if any query uses the bare name, which resolves to zero series
  WITH data present — the failure that looks exactly like "not firing
  yet".
- Percentiles keep `by (le)`; without it the buckets are summed away and
  the result is a quantile of nothing.
- Counters read through `increase`/`rate`, and a test refuses
  `delta(`, `avg_over_time(` and `sum_over_time(` by name — all three
  parse, so nothing but an explicit ban catches them.
- Absence is `absent_over_time`, and a test refuses `== 0`.
- `retestAllowed` caps at 25h, so a 26h lookback sends `duration: 0`.
- Ranges render in seconds: `1h30m0s` is not a legal range selector.

Eight tests, each pinning a fact that cost a live probe to learn.

`KindAlertChannel` and `KindAlertPolicy` are in `reconcileOrder`, last
and in that order: channels before policies because a policy names its
channels, and both after everything because a half-built app with alerts
already watching it would page on its own deploy.

That reordering changed destroy, and correctly: alerts now come off
FIRST. `TestDomainMappingPlansAfterItsService` asserted the domain
mapping was `order[0]`; it now asserts the two things that actually
matter — mapping before its service, and alerting before the mapping.
An alert that outlives the app it watches pages about a teardown in
progress.

### The gap: Y1b stored the condition's NAME, not its parameters

Starting Y1c surfaced it. The AppGraph records `Condition: "DeadLettered"`
and the channel list — enough to render the summary and check the rules,
and **not enough to build a query**. The provisioner needs the subject's
metric type and label matchers, the reading, the threshold and the
window, none of which the analyzer extracts today.

So Y1b is complete for what it claimed (resources, rules, visibility)
and incomplete for what Y1c needs. The remaining work is real analyzer
work — reading each condition's arguments, resolving preset subjects to
their metric types, and validating windows against the measured ceilings
(`Every` 60s–90000s, `For` ≤24h on thresholds) — not a wiring detail.

Stopping here rather than writing adapters against a graph that cannot
feed them. Next: **Y1b2**, condition parameter extraction, then Y1c's
adapters and fake-server tests on top of it.

## Y1b2 — condition parameters reach the graph (2026-08-02)

The gap Y1c found is closed. `AlertCondition` carries the condition
LOGICALLY — op, subject kind and name, reading, quantile, threshold, and
the window — and never a metric type or a physical name. The provisioner
maps those, because only it knows the env prefix and the deployed
service names: the same split every other kind already follows.

### Two unit bugs the graph made visible immediately

Rendering the parsed conditions is what caught them; neither would have
failed a compile or a test:

```
Piling  thr=600000000000   <- 10*time.Minute as raw NANOSECONDS
Slow    thr=2000000000     <- 2*time.Second, same
```

A Go duration constant reaches the analyzer as nanos, while the SDK's
own constructors convert (`age.Seconds()`, `d.Milliseconds()`). Stored
raw, a 10-minute backlog would have been compared against a threshold of
six hundred billion — an alert that can never fire, and one no test
would have noticed because the number is *there*. Fixed with a `unit`
per preset, and the fix is visible in the graph: `thr=600`, `thr=2000`.

The same pass caught a route subject with **no route**: `Erroring(List,
…)` resolved to kind `api` with an empty name, so there would have been
no `route=` matcher to build and the alert would have watched the whole
service while claiming to watch one endpoint. Now `route/GET /photos`.

Both are the same lesson as the rest of this feature: the value looked
present, so only reading it against what it MEANT exposed it.

### presetReadings is a table, not a switch

A new preset cannot be added without an entry — the completeness habit
the retry allowlist had to learn the hard way.

### Three window rules, carrying the measured ceilings

```
a Window's Every must be between 1m and 25h — Cloud Monitoring refuses anything else; got 30s
a threshold Window's For cannot exceed 24h — Cloud Monitoring refuses longer; got 172800s
tako.Count needs a period — write tako.Window{Every: …}; how long you are counting over IS the count, so there is no default worth guessing
```

Each refuses at analysis time what the API would refuse at deploy time,
with the real number in the message. The third is the Count decision
from the amendment, enforced rather than merely documented.

Eight rules now cover alerting. Next: Y1c's adapters, on a graph that
can finally feed them.

## Y1c — logical conditions become PromQL (2026-08-02)

`pkg/provision/alerts.go` maps the analyzer's logical conditions onto
Google's metric types, resources and label matchers. Every metric type
and every label name in it was read off the live descriptors, because a
wrong label produces a policy that watches NOTHING rather than an error
— the failure mode this whole feature keeps generating.

Six tests, each pinning a way the mapping could be quietly wrong:

- **`CronMissed` doubles the schedule it reads.** A daily cron gets
  `[172800s]` and forbids a retest window; a 6-hourly one gets
  `[43200s]` and allows it. This is the case that could not exist under
  `conditionAbsent`'s 23h30m ceiling.
- **Names carry the env prefix** — `subscription_id="x1-thumbs"`,
  `service_name="x1-gallery-makethumb"`. Without it a policy watches
  another environment's resources and simply never fires.
- **Route subjects filter to their route**, and the test asserts the
  matcher appears **twice** in an error-rate query. Filtering only the
  numerator would divide one route's errors by the whole service's
  traffic and read far too low — a wrong number, not a missing one.
  It also asserts the METHOD does not leak: tako's `"GET /api/photos"`
  is a route pattern, Cloud Run's label is a path.
- **`AtCapacity` reads MaxInstances from the footprint** (5 → `> 4.5`),
  so the ceiling lives in one place.
- **Gauges use neither `rate(` nor `increase(`.** An age or an instance
  count has no rate; taking one would be meaningless and the API would
  not complain.
- **Custom metrics are namespaced per env**, so prod and a preview
  never share an alert.

### Still ahead in Y1c

The GCP adapters (`List`/`Create`/`Update`/`Delete` for both kinds), the
desired-state objects, and the Pub/Sub notification service-agent grant
with the bounded retry `notifications.go:77` already models. The query
half is the part that was worth pinning first — it is where being wrong
is invisible.

## Y1 — DEFECT: Cloud Run's `route` label is empty, and four presets depend on it (2026-08-02)

Owner suggested API endpoints should export count/error/latency metrics
automatically. Before answering "the platform already does that", I
checked what the platform actually emits. It does not.

```
run.googleapis.com/request_count:     44 series, route='' on all 44
run.googleapis.com/request_latencies: 44 series, route='' on all 44
```

Two days, both metrics, every series from the real x1 deploy — after 15
uploads to /api/photos, a GET on the same route, and a cron firing
/_tako/cron/Cleanup. **The `route` label exists in the descriptor and is
never populated** for an ordinary Cloud Run service.

### What this breaks, in code already committed

| preset | behaviour |
|---|---|
| `CronMissed` | **fires constantly, forever.** `absent_over_time` over a selector matching NO series returns 1 — the alert is permanently true |
| `CronFailed` | never fires |
| `Erroring(route)` / `SlowerThan(route)` | never fire |

Service-level `Erroring`/`SlowerThan`, dead letters, backlog, capacity,
singletons and every custom-metric reading are unaffected: they filter
on `service_name`, which IS populated.

`CronMissed` is the worst outcome available — not a silent alert but a
permanent page, and it would have looked like tako working until someone
read the incident.

### The claim this invalidates

The 2026-08-02 entry "PromQL lookbacks defeat the 23h30m cap" concluded
that `Runs(cron)` could ride `request_count`'s route label. The lookback
finding stands; **the route half was wrong**. I verified the label
EXISTED in the descriptor and never checked that it had a VALUE — the
same shape as the earlier `increase(...) == 0` mistake, where the query
was accepted and returned nothing.

Six tests in `alerts_test.go` assert the matcher is PRESENT in the
generated query. Not one asserts it MATCHES anything, which no unit test
can. That is the honest limit of testing a query builder without a
cloud, and it is why the route assumption survived to here.

### The fix is the owner's suggestion, and it is now mandatory

tako must export its own per-endpoint metrics with labels it controls:

```
custom.googleapis.com/tako/requests     counter, labels: route, status_class
custom.googleapis.com/tako/request_ms   distribution, labels: route
```

Then `Erroring`, `SlowerThan`, `CronFailed` and `CronMissed` all read a
label tako populates, and per-route alerting works because tako is the
one writing the route.

This is not additive polish — four presets are wrong without it.

**It requires metric LABELS, which `observability.md` currently lists
under "Not yet": "No metric labels/dimensions".** So the export path
(U2's `MetricsStore`) has to grow a label dimension before the presets
can be correct. That is a real slice, not a wiring change.

**STOP — this needs a decision before more Y1c.** Options: (a) build
per-endpoint export with labels (Y3) and make the four presets ride it;
(b) drop the four route/cron presets to service-level only, losing
`CronMissed` entirely; (c) ship service-level presets now and add the
endpoint ones after Y3. Recommendation: (a), because `CronMissed` is the
single most valuable alert in the catalogue and (b) deletes it.

## Y3 — tako exports its own per-endpoint metrics (2026-08-02)

The fix for the route-label defect. Owner's framing was right and needed
no options: the metric should carry a route label alongside the service
and instance — and two of those three already existed.

`generic_task` has carried `job` (the tako service name) and `task_id`
(the instance) since U2. Only the ROUTE was missing, and it had to come
from tako because Cloud Run never populates its own.

### The label dimension

`backends.Metrics` grew a `labels` parameter. `tako.Counter` and
`tako.Histogram` pass **nil** — user-declared metrics still have no
dimensions, exactly as `observability.md` says. The parameter exists for
the metrics tako records about itself:

```
custom.googleapis.com/tako/requests     labels: route, status
custom.googleapis.com/tako/request_ms   labels: route
```

`MetricsStore` now keys series by `{kind, name, labels}` with the label
set encoded canonically (sorted), so the same labels always produce the
same series instead of two that drift apart.

### Cardinality is the design, not an afterthought

- **The route label is the PATTERN, not the path.**
  `/api/photos/{id}/image` is one series however many photos exist; a
  path label would be one series per id. Go 1.22's ServeMux reports the
  matched pattern, so this is the framework's own knowledge rather than
  string-munging.
- **Unmatched paths share `<unmatched>`.** Otherwise a scanner probing
  random URLs mints a series each and becomes a metrics bill.
- **Status is bucketed to 2xx/3xx/4xx/5xx**, never the raw code: five
  values at most.

Five tests cover exactly these, including the one that would have caught
the original defect — an assertion that the route label has the RIGHT
VALUE, not merely that a route label exists.

### Not duplication of the platform

Worth stating because the opposite mistake already happened once, in P1,
where tako wrote a request log Cloud Run already emitted. The difference
is measured: Cloud Run's `request_count` and `request_latencies` publish
a `route` label that is empty on every series. Per-endpoint alerting is
impossible from platform metrics; it is possible from these.

Recording is nil-safe: an unwired sink is normal in tests and in any
embedding that skips boot, and recording must never be why a handler
fails.

Next: point the four broken presets at these metrics, then Y1c's
adapters.

## Y3b — the four presets ride tako's metrics; and the wildcard trap (2026-08-02)

`cron_failed`, `cron_missed`, `error_rate` and `latency` now read
`custom.googleapis.com/tako/requests` and `.../request_ms` instead of
`run.googleapis.com/request_count`, scoped by `namespace` (app-env) and
`job` (the service) on `generic_task`.

### A second wildcard defect, caught before it shipped

Pointing the presets at the new metric surfaced it: the cron dispatcher
is mounted ONCE as `POST /_tako/cron/{cron}` and serves every cron in
the service. `r.Pattern` is therefore the wildcard, so all crons in a
service would have recorded into a single series — and `CronMissed` on a
healthy cron would fire whenever any OTHER cron beside it stopped.

The fix is `RefineRoute`: the route label is a POINTER in the request
context, because the middleware records after the handler returns and a
value written into a context inside a handler never reaches it. The cron
dispatcher names what it actually ran:

```go
RefineRoute(r, "/_tako/cron/"+name)
```

Two tests cover it — that two crons through one wildcard pattern record
under two distinct labels, and that an ordinary route still keeps its
pattern without refinement.

### The tests that had to change, and why that is right

Three assertions in `alerts_test.go` were encoding the WRONG metric:
they asserted `service_name=` (a `cloud_run_revision` label) and a route
value with the method stripped. Both were correct for
`run.googleapis.com/request_count` and both are wrong now:

- the service is the `generic_task` **`job`** label;
- the route value is the ServeMux **pattern verbatim**, method included,
  because that is what tako writes. Stripping the method — which the
  first version did, for Cloud Run's differently-shaped label — produces
  a matcher that never matches.

They now also assert the negatives that would let the bug back in: that
no query still reads `run_googleapis_com:request_count`, and that no
cron alert watches `{cron}`.

Full suite, vet and gofmt clean, conformance included.

Remaining in Y1c: the two GCP adapters, desired-state objects, and the
Pub/Sub notification service-agent grant.

## Y1c — the GCP adapters (2026-08-02)

`AlertChannels` and `AlertPolicies` implement `KindApplier`. Both carry
ownership and the spec hash in `userLabels`, so label-based
reconciliation works unchanged — no state file, same discipline as every
other kind.

### Five tests, each pinning a way this silently pages nobody

- **The destination lands in the type's own label key.** Google names it
  differently per channel type — `email_address`, `space`, `url`,
  `topic` — and a wrong key creates a channel that delivers nowhere
  while looking created. Table-tested across all four.
- **Foreign policies are invisible.** Neither API filters by label, so
  observation lists everything and keeps only what carries this app AND
  this env. The fixture includes a prod-env policy and a stranger's, and
  both must be ignored — the `pr-42`-destroying-prod class of bug from
  C6.
- **A policy naming a missing channel is refused**, not created. Google
  accepts a policy with an empty channel list happily; it just never
  tells anyone. Refusing names the channel in the error.
- **The policy carries the PromQL condition, its channels and all four
  ownership labels** — and asserts NO duration is sent when the lookback
  is long, which is the 1d1h retest rule the probes measured.
- **A disabled Monitoring API observes as empty**, so destroy never
  demands an API the app never enabled. The rule the T3 gate established
  for Cloud Tasks, applied here.

`provision.Observed` gained an `ID`, because these two kinds are created
with server-assigned names: a delete has to use what a list told it
rather than build a path from the name.

Channel and policy updates are delete-then-create: a channel's type
cannot be patched and a policy's condition is most of its spec, so
recreate is the honest shape rather than a partial update that leaves
one field stale.

Full suite, vet, gofmt and conformance(local) clean. `RefineRoute` is a
new `pkg/runtime` export, so `runtime_lock.golden` was regenerated
deliberately — it reaches generated code and belongs in release notes.

Remaining before the live gate: wiring `alertObjects` into the deploy
plan and the Pub/Sub notification service-agent grant.

## Y1c — deploy wiring and the Pub/Sub grant (2026-08-02)

`alertObjects` is in `Desired`, both adapters are routed in deploy and
registered as deleters in destroy, and observation covers them — so a
channel or policy that drifts is seen, and a destroy removes them.

### The grant that makes a Pub/Sub channel work at all

Cloud Monitoring delivers a Pub/Sub channel through **its own** service
agent, not through anything tako runs, so that agent needs
`roles/pubsub.publisher` on the topic:

```
service-<number>@gcp-sa-monitoring-notification.iam.gserviceaccount.com
```

Google creates it LAZILY, on the first Pub/Sub channel in the project —
the same shape as the GCS agent that `BucketNotifications` already rides
out with a bounded retry. The grant is written regardless: granting to
an agent that does not exist yet is accepted, and waiting for Google to
provision one would be a deploy that hangs.

### The test I nearly shipped vacuous

`TestPubSubChannelGrantsTheMonitoringAgent` first FAILED because my
assertion read the grant spec's shape wrongly (`bindings` is not
`[]map[string]any`). Loosening it to a string match made it pass — which
is exactly how a test starts asserting nothing.

So I mutated the code to check the test could still fail: changing the
role to `roles/pubsub.subscriber` **fails the test**, so it is reading
the role and not just the presence of the agent's name. Worth doing
because a green test that cannot fail is worse than no test — it is a
claim of coverage that is not there.

### End to end

`TestAlertsReachTheDesiredPlan` walks graph → objects → spec and pins
three things a unit of the query builder alone could not: the Pub/Sub
channel's target is the FULL env-prefixed topic path
(`projects/p/topics/x1-incidents`, the same name the topic reconciler
created), a 48h absence plans NO duration, and the policy's channel list
uses the short var names both adapters key on.

The CLI confirms the human path:

```
    channel oncall → email ops@example.com
    channel bus → pubsub incidents
    alert Stuck — DeadLettered → oncall
    alert Piling — Backlogged → oncall, bus
    alert Missed — CronMissed → oncall
    alert Slow — SlowerThan → oncall
```

Full suite, vet, gofmt and conformance(local) clean.

**Y1 is now complete except the live gate.** Everything from here is
what only a real project can answer: whether the generated queries match
real series, whether a channel delivers, and whether `CronMissed` fires
exactly once against a genuinely stopped cron.

## Y1e — the live gate: nine defects, and a delivered alert (2026-08-02)

gallery declares `Stuck` (DeadLettered) and `Silent` (CronMissed with a
5-minute window so it could be watched). Deployed to tako-test-01/x1,
driven, and destroyed clean.

**The alert reached the owner's inbox.** `tako.Alert(tako.CronMissed(
Cleanup, tako.For(5*time.Minute)), oncall)` → analyzer → PromQL →
policy → email, carrying `tako-app: gallery`, `tako-env: x1` and the
spec hash. That is the one claim nothing offline could make.

It took EIGHT deploys. Every failure was real:

| # | defect | only findable by |
|---|---|---|
| 1 | planned but never applied — deploy's phase list had no entry for the new kinds | running a deploy |
| 2 | GCP label values forbid uppercase; tako identities are exported Go vars | the API refusing it |
| 3 | a policy cannot name a metric that does not exist | the API refusing it |
| 4 | descriptors are not enough — PromQL resolves names from DATA | querying PromQL metadata |
| 5 | a CUMULATIVE point needs start < end strictly | the API refusing it |
| 6 | preset defaults lived only in the SDK, which the deploy never reads | reading back stored state |
| 7 | absence sent its lookback as a retest duration too, doubling the wait | reading back stored state |
| 8 | the notification subject named a Go variable, not a failure | receiving the mail |
| 9 | severity unset, so every subject read "No severity" | receiving the mail |

### Three layers of checking, each catching what the last could not

- **Was it accepted?** — plan output, exit code, HTTP 200. Caught 1–5.
- **Is what is STORED what I meant?** — reading policies back. Caught 6
  and 7, both of which had been accepted happily. Defect 7 is the
  instructive one: `CronMissed(For(5m))` would have fired at TEN
  minutes while claiming five — a wrong number in the direction that
  still looks like it works.
- **Is what is DELIVERED usable at 3am?** — the mail itself. Caught 8
  and 9. The alert was correct at every layer above and arrived nearly
  useless: `[ALERT - No severity] Silent on __missing__`.

`__missing__` is unavoidable — an absence on a series that does not
exist has no resource labels to attribute — so the human half carries
the meaning instead: every preset now generates a sentence,
`gallery/x1: cron Cleanup has not run`. Severity is framework-owned at
WARNING.

### What this says about the offline tests

They were sound and aimed at the wrong surface. Seven of nine defects
lived in SEAMS — between plan and apply, between an SDK default and a
source-reading deploy, between a correct policy and a legible
notification. The fake server could not have found any of them: it
encodes what I already believe the API accepts, so it validates my
model rather than Google's.

Three completeness tests came out of it and remain: apply-coverage (a
kind in reconcileOrder that deploy never applies), no-uppercase-labels,
and preset defaults applied where the deploy reads them. Each was
mutation-checked by breaking the code and confirming the test failed.

### Honest gaps

- **`Stuck` never fired.** Nothing dead-lettered during the walk. Its
  query is verified against the real metric and the policy was created,
  but only `CronMissed`'s transition was actually exercised.
- **Incident counting is not asserted.** Cloud Monitoring exposes no
  public v3 API for listing incidents, so "exactly once" was checked by
  running the policy's own query at both moments — quiet with data
  present, firing after the window — and by the single mail that
  arrived. That is a proxy, not a count.
- The email channel needs **no verification**: `verificationStatus` is
  absent on an API-created channel. That closes the Y1 decision-4
  question, which had been unresolved since the proposal.

Destroy removed policies before the channel and left the project clean.

## Y1e — CORRECTION: the gate was a FAIL, not a pass (2026-08-02)

The entry above ("nine defects, and a delivered alert") presents the
live gate as a pass on the strength of an email arriving. **That
conclusion is wrong and the entry stands uncorrected above only because
this log records what was believed at the time.**

The delayed half of the gate script reported after that write-up was
committed:

```
right after a run (cron alive → must be QUIET):  ABSENT (would fire)
after 7 minutes of silence:                      ABSENT (would fire)
```

`CronMissed` never went quiet. It was a **permanent false positive** —
precisely the failure predicted when the empty `route` label was found,
and shipped anyway. The mail proved DELIVERY, not correctness: a firing
alert and a correct alert look identical from an inbox, and the only
thing separating them is the assertion I did not wait for.

Two further defects, both mine:

- **10. `SetRequestMetrics` is never called.** The sink exists,
  `recordRequest` no-ops on nil, and no boot path wires it — so every
  per-endpoint metric vanishes. The live project held exactly one
  series, the deploy seed. The five tests pass because they inject the
  sink themselves: a test that supplies a dependency cannot notice a
  missing injection.
- **11. The `job` label disagrees three ways** — app writes
  `TAKO_SERVICE` (logical), the seed writes the app name, queries filter
  the env-prefixed name. Nothing ever matched.

What the process should have caught: the gate script contained the
right assertion (quiet-when-healthy) and I wrote the verdict before its
result came back. The lesson is not "test more" — it is that a
conclusion drawn from the half of the evidence that had arrived is a
guess wearing a result's clothes.

**Y1e is a FAIL. No validation-log row was written.** Resume brief with
exact next steps: `docs/y1-resume.md`.

## Y1f — the gate, re-run properly: QUIET then FIRING (2026-08-02)

The re-run of Y1e after fixing defects 10 and 11, plus a twelfth found
while writing the docs.

**Defect 10 — nothing wired the per-endpoint sink.** `SetRequestMetrics`
existed, `recordRequest` no-opped on nil, and no boot path called it.
Fixed in both `BootLocal` and `BootGCP`. Two tests: one boots and looks
at what boot left behind, one reads `boot.go` for the path no test can
boot. Mutation-checked — removing either call fails both.

**Defect 11 — the `job` label, three ways.** Settled on the LOGICAL
service name: `namespace` already carries `<app>-<env>`, so prefixing
`job` too is a second copy of the env free to disagree with the first.
Queries dropped `cfg.Name(...)`; the deploy seed moved to a `job` that
names no service (`tako-deploy`), because a seed labelled like a real
service would suppress an Absent condition for one window after every
deploy — an alert silenced at exactly the moment a bad deploy needs it.

The replaced test is the interesting part. `TestSubjectNamesCarryTheEnvPrefix`
asserted `job="x1-gallery-makethumb"` and passed, because it checked the
label's SHAPE and never asked whether anything wrote that value. The new
`TestJobLabelMatchesWhatTheServiceWrites` reads both sides — the
`TAKO_SERVICE` the deploy plan injects, and the matcher the query
builds — from the same graph.

**Defect 12, found writing the reference page.** `CronFailed` and
`JobFailed` document a 1-hour window; `presetEvery` shipped 5 minutes.
This is defect 6 exactly, one round later: the SDK default is a runtime
value and the deploy reads source. The test from that round asserted ONE
preset (`DeadLettered`) while its comment claimed the principle, so two
more were wrong the whole time. `TestEveryPresetsDefaultWindowSurvivesToTheDeploy`
now parses every preset's `windowOr` default out of `alert.go` and the
reading name out of the analyzer's table, and compares against
`presetEvery`. An hour is the right value: "failed 3 times" over five
minutes is a question an hourly cron can never answer yes to.

### The gate

```
=== T+3min: the cron just ran, so the alert MUST be quiet
QUIET (data present)
=== what series exist (route must be populated, job must match the query)
  3 series
    route='/_tako/cron/Cleanup' status='2xx' job='gallery' ns='gallery-x1' task='0015…9a719' last=2
    route='' status='' job='gallery'      ns='gallery-x1' task='deploy' last=0   ← last session's seed
    route='' status='' job='tako-deploy'  ns='gallery-x1' task='deploy' last=0   ← this deploy's seed
=== now 7 minutes of silence; the 300s window must expire
=== T+10min: no run in 7 minutes, so the alert MUST be firing
FIRING (absent)
```

Both halves, in that order. The middle block is the other half of the
proof: a populated `route`, a real instance `task_id`, and a cumulative
2 — the two cron runs. Last session's run had 88 series with `route=''`
and read the descriptor's schema as evidence.

The policy stored, verified against the API rather than the deploy's own
summary:

```
=== x1-Silent severity= WARNING
  cond : gallery/x1: cron Cleanup has not run
  query: absent_over_time(custom_googleapis_com:tako_requests{monitored_resource="generic_task",
         namespace="gallery-x1",job="gallery",route="/_tako/cron/Cleanup"}[300s])
```

### What changed in how this was checked

Three questions, run in order, instead of one: *was it accepted* (the
API said yes), *is what is stored what I meant* (read the policy back),
*and is the answer different when the world is different* (run the query
while healthy AND while broken). The third is the only one that can tell
a working alert from one that fires forever, and it is the one that was
skipped.

Every fix this round carries a mutation-checked test. Reverting any of
the three reproduces the original symptom in the failure message.

**Y1f: PASS.** Validation-log row written. `docs/y1-resume.md` retired.

### Discovered work

- `examples/gallery/app.go` declares `tako.Email("darchitect@gmail.com")`
  — the owner's real address, committed to the acceptance suite because
  the gate needed a live destination. Replaced with `ops@example.com`
  now that the walk is done; `validate/alerts.md` says to substitute a
  real one for the walk.
- `Stuck` (DeadLettered) is created and never fired — closing it needs a
  poisoned message driven through `thumbs` until retries are exhausted.
  Noted as a known gap in `validate/alerts.md` rather than implied
  covered.

## Y1f — delivery confirmed, and the one weak line in the mail (2026-08-02)

The owner's inbox, ~3 minutes before teardown:

```
[ALERT - Warning] gallery/x1: cron Cleanup has not run on __missing__
Alert firing · Warning
A PromQL query was observed at 1
Policy      x1-Silent
Condition   gallery/x1: cron Cleanup has not run
metric : custom.googleapis.com/tako/requests   value : 1
tako-app : gallery   tako-env : x1   tako-spec-hash : 29938b22458047d5
```

Defects 8 and 9 are closed by this: the subject was `Silent on
__missing__` with `[ALERT - No severity]` last session. The policy
labels ride through, so the mail names the app and env without opening
the console.

### Discovered work: the body says nothing a responder can act on

`A PromQL query was observed at 1` is Google's own generated summary,
and `value : 1` is `absent_over_time` returning true — technically
exact and operationally useless at 3am. Cloud Monitoring policies take
a `documentation` field whose content is rendered into the
notification, and tako already knows everything worth putting there:
which cron, its schedule, the lookback that expired, and the console
link to the service's logs. tako writes no `documentation` today.

Not done — Y1's scope was the condition and the channel, and this is
new surface. Logged for Y2.

## Y2 — the notification says something (2026-08-02)

Owner-directed after the Y1f mail arrived reading `A PromQL query was
observed at 1`. Cloud Monitoring cannot do better than that: it knows a
query returned a number and nothing about what the number meant. tako
knows the rest at deploy time, so it writes the body.

Verified against the v3 discovery document rather than recalled:
`documentation` takes `content` (markdown, ≤8192 chars), `mimeType`,
`links` (≤3, display name ≤63, rendered as the email's Quick Links) —
and **`subject`**, which replaces Google's generated subject line.

That last field corrects something Y1 stated as fact. `__missing__` was
documented as "unavoidable and correct" on an absence. It is unavoidable
in the DEFAULT subject; the override exists, and the subject now reads
`[ALERT - Warning] gallery/x1: cron Cleanup has not run` with no tail.
`reference/alerts.md` and `validate/alerts.md` both corrected, with the
old claim named rather than quietly deleted.

The body is STATIC text. Cloud Monitoring supports `${…}` variables, but
they resolve from the labels of the series that fired — and an absence
has no series, so a variable renders `null` in precisely the alert that
needs it most. Same trap as `__missing__`, one layer down.

### Three defects, and where each came from

**13 — the humanDuration seam.** The first version trimmed Go's string
form by suffix, so `10m0s` → `10m` → `1`. Caught by the table test, not
by reading. There is no seam in that string that is safe to cut;
rebuilt from components.

**14 — the dead-letter query names a subscription that does not exist.**
`DeadLettered(thumbs, 1)` compiled to `subscription_id="x1-thumbnails"`.
A queue deploys as a TOPIC; the subscription draining it is named after
its CONSUMER (`x1-gallery-makethumb`), because subscribers fan out and
each needs its own. So the query matched nothing and **`Stuck` could
never fire** — which the Y1 gate recorded as "created but never fired,
nothing dead-lettered". The truth was stronger and worse.

This is the third instance of one sentence: *a query matching nothing is
indistinguishable from a healthy system.* The empty `route` label, the
`job` label spelled three ways, and now this. Fixed with the same shape
of test that caught the second — both sides read off the same deploy
plan (`TestSubscriptionMatchersNameSubscriptionsTheDeployCreates`), and
mutation-checked.

**15 — two console links 404.** `/run/detail/<region>/<svc>/logs`
answers "URL not found"; the pages moved under `/observability/`. And
the Pub/Sub link carried defect 14's wrong name, so it answered "Unable
to find the resource you requested".

### How 14 and 15 were found

By opening the link. Not by a test, not by reading the code — by
loading the URL the notification carries in a browser and seeing
"Unable to find the resource you requested". That click is what first
asked whether `x1-thumbnails` named anything, and the answer took the
query down with it.

Three layers of checking have now each caught something the layer above
missed: *was it accepted* (the API said yes to every one of these),
*is what is stored what I meant* (the readback showed the documentation
arrived intact — and could not tell that a name inside it was wrong),
*is what is delivered usable* (a responder clicks the link, and the link
404s). Only the third layer exists at the width of a real incident, and
it is the one with no automation behind it.

### What is checked now

`TestEveryConditionKindSaysSomethingActionable` walks all eleven
readings and requires each body to name what broke, what it is not, and
what to run — plus the footer, a subject override, and 1–3 links.
`TestDeadLetterDocsPointAtTheConsumer` pins the link to the worker
rather than the app service; sending a responder to the wrong service's
logs is worse than sending them nowhere, because they will believe what
they find. `TestSchedulerCommandNamesTheDeployedJob` checks the
paste-able command against the plan's scheduler jobs — the one line in
the mail somebody will run without thinking.

### Discovered work

- The console link shapes are verified by hand, once. Nothing detects
  Google moving a page again; a link checker would need network access
  in CI and a deployed project, so this stays a known gap rather than a
  pretend test.
- `Stuck` still has not been observed firing. Defect 14 explains why it
  never could; the fix is proven by test and by the corrected query, not
  by an incident. Driving a poisoned message through `thumbs` remains
  the only way to close it.

## Y2 — the delivery cycle, and an alert that caught a real failure (2026-08-03)

Run to close the gap the previous entry left open: the body was proven
STORED, never proven DELIVERED.

### It was already delivered

The owner forwarded the 10:40 PM mail and it carries the full
documentation — headline, the "what it is not" paragraph, the schedule,
the paste-able command, the footer, and a Quick Links section. So the
body renders in a real inbox.

Whether it carried the CORRECTED links was answerable without guessing.
The mail prints `tako-spec-hash: 62ad0f3b284fa048`; today's deploy of
the fixed policy produced the same hash; and a local check confirmed the
hash is sensitive to a link URL (changing one character of one link
moved it from `b7030dc9d6bd9f05` to `5f7ab539fa3afff5`). Identical hash
therefore means identical links. The fix redeploy landed before that
incident re-opened — `AlertPolicies.Update` deletes and re-creates, so
the incident re-fired against the new policy.

One honest limit, visible in the mail: Google's own
`A PromQL query was observed at 1` is still there. The documentation is
appended under "Policy documentation", it does not replace the generated
summary. Only the SUBJECT is replaceable.

### The first run of the cycle failed, and the alert was right

```
=== the cron is alive, so the alert MUST be quiet
FIRING (absent)
  1 series: route='' status='' job='tako-deploy' task='deploy'
```

The cron series was missing entirely. Not a metrics bug — the cron had
**not run**:

```
status.code 7 (PERMISSION_DENIED)
403 The IAM principal lacks {run.routes.invoke} permission
```

A fresh deploy's scheduler invoker grant had not propagated. So
`CronMissed` reported "cron Cleanup has not run" about a cron that had
not run. **The feature was correct and the gate script was wrong.**

The script fired with `gcloud scheduler jobs run` and took its exit code
as success. That command exits 0 when the trigger is ACCEPTED; the
invocation that follows can 403, and the exit code never mentions it.
Checking an exit code where a status was needed — the same substitution
of shape for value that this walk was written to catch, one level up, in
the walk itself.

This is also the first time an alert here caught a failure it was not
staged to catch. `CronMissed`'s own body claims it fires when "the
schedule never fired, OR it fired and the service never answered". The
second clause had never been exercised. It has now, by accident.

`validate/alerts.md` §3 now requires `status.code` and a 200 in the
service log before anything is asserted, with this run named as why.

### The cycle, re-run against a landed cron

```
run SUCCEEDED on attempt 1 (status.code empty = OK)
  2026-08-03T10:46:34Z  200      ← the run that landed
  2026-08-03T10:34:59Z  403      ← the one that did not
=== the cron is alive, so the alert MUST be quiet
QUIET (data present)
=== after 7 minutes of silence, the alert MUST be firing
FIRING (absent)
```

Both halves, against the policy spec that ships.

### Discovered work

- Google's generated summary line cannot be suppressed. `documentation`
  appends; only `subject` replaces. Nothing to do about it — recorded so
  it is not rediscovered as a defect.

## Z2 — DX proposal: `DeadLettered` is blind to `tako.Permanent` (2026-08-03) — AWAITING APPROVAL

Found by the Z2 walk. Not a typo — a structural gap between what the
preset measures and what the word means.

### What happens now

A handler can reach the dead-letter topic two ways:

1. **Retry exhaustion.** The handler nacks five times, Pub/Sub gives up
   and forwards the message to the dead topic. `dead_letter_message_count`
   on the consumer's subscription increments.
2. **`tako.Permanent`.** Pub/Sub cannot express "nack without retry", so
   `pkg/runtime/push.go` publishes to the dead topic ITSELF and answers
   204. Pub/Sub sees a **successful delivery**. The metric never moves.

`DeadLettered` reads that metric, so **it cannot see path 2 at all** —
and path 2 is the common one: a malformed payload, a corrupt image, a
schema that no longer parses. The Z2 walk poisoned `thumbs` with an
undecodable image, watched the dead letter arrive (`tako dlq` shows
`decode image: image: unknown format`), and the alert stayed quiet for
twelve minutes because nothing was wrong with the metric — the metric
was right, and measuring the wrong thing.

`tako dlq` shows BOTH kinds. So the CLI and the alert disagree about
what "dead-lettered" means, and the alert is the one that is wrong.

### Proposed

Read the **dead subscription's backlog** instead of the source
subscription's forward count:

```
max(pubsub_googleapis_com:subscription_num_undelivered_messages{
  monitored_resource="pubsub_subscription",
  subscription_id="<service>-dead"}) >= n
```

That is the same queue `tako dlq` reads, so the alert and the CLI agree
by construction, and both paths land in it.

### What changes for the user

The doc comment, and the meaning:

```go
// BEFORE: fires when at least n messages have given up — exhausted
//         their retries and landed on the dead-letter topic — within
//         the window (default: 5 minutes).

// AFTER:  fires when at least n messages are WAITING in the dead-letter
//         queue — however they got there: retries exhausted, or a
//         handler returning tako.Permanent.
```

Three consequences, stated plainly because they are the reason this
needs approval and not just a commit:

- **It becomes a level, not a rate.** A dead letter that nobody drains
  keeps the alert firing until it is drained. That is arguably the point
  — "there is unhandled poison in the queue" is a condition, not an
  event — but it is a real change: today's version goes quiet on its own
  after the window, the proposed one does not until you run
  `tako dlq … --ack` or `--replay`.
- **`Window.Every` stops meaning anything** for this preset, because a
  gauge has no counting window. `Window.For` still works and becomes the
  useful knob: "at least n waiting, sustained for 10 minutes" ignores a
  message that is about to be replayed.
- **`Backlogged` and `DeadLettered` become the same SHAPE** — both
  gauges on a subscription, one on age and one on depth. That reads as a
  simplification rather than a coincidence.

### Alternative considered and rejected

Count publishes to the dead TOPIC (`send_message_operation_count`).
It catches both paths and stays a rate, so nothing above changes. But it
counts *arrivals* and cannot see a backlog that is already there: an
alert that goes quiet while a hundred dead letters sit unread is the
failure mode this preset exists to prevent.

### Not in this proposal

`Backlogged` is unaffected — it already reads a gauge on the source
subscription, and oldest-unacked-age is unrelated to how a message dies.

## Z2 — `DeadLettered` observed firing, and the off-by-one that would have hidden it (2026-08-03)

The preset whose transition had never been seen, and whose query was
wrong for the whole of Y1.

### Defect 16, found by reading the doc against the operator

Three presets document **"at least n"** and compiled to **`> n`**:
`DeadLettered`, `CronFailed`, `JobFailed`. So
`DeadLettered(thumbs, 1)` — the way anyone says "tell me when anything
gives up" — needed TWO dead letters.

The error is in the quiet direction, which is the direction nobody
notices: the alert simply does not arrive, and a queue that is not
alerting looks like a queue that is fine.

Fixed with an `atleast` op compiling to `>=`. The thresholds that
document "exceeds" (`Erroring`, `SlowerThan`, `Backlogged`) keep strict
`>`. Two tests: one pins the operator per preset, one reads the SDK's
own doc comment and fails if a preset says "at least" while compiling
with anything but `atleast`.

**The fix was load-bearing for this very walk.** The alert fired at
`value=1`. Under `> 1` it would have stayed silent, and the walk would
have gone looking for a metrics bug that was not there.

### Defect 17: the command in the mail did not work

The `DeadLettered` body printed:

```
tako dlq thumbnails --project … --env x1
```

Pasted, that answers:

```
tako: no async service named "thumbnails" — async services: gallery-onuploaded, gallery-makethumb, …
```

Dead letters are held **per consumer** — one dead topic per async
service, none per queue — so the queue name, which is exactly what a
reader of the alert reaches for, is the wrong argument. The cron body's
command was cross-checked against the deploy plan and was right; this
one was never checked. Same gap, one command over. Fixed, and
`TestDlqCommandNamesAnAsyncService` now checks it against the graph's
async services.

### The transition, live

```
=== the alert must be QUIET before the poison lands
quiet (no series)
  t+5m  raw=FIRING  value=0      ← the metric series appears
  t+6m  raw=FIRING  value=1      ← the forward is counted
=== the alert is FIRING at t+6m: FIRING  value=1
```

Produced by uploading a `text/plain` payload, which fails transiently
five times until Pub/Sub gives up and forwards. Confirmed in the DLQ
itself, not inferred:

```
$ tako dlq gallery-makethumb --project tako-test-01 --env x1
  20878337811428352  2026-08-03 07:58:53  decode image: image: unknown format
    {"photoID":"srwl4u5rdnmdn5ta"}
```

**Z2's goal is met: `DeadLettered` goes from quiet to firing on a real
dead letter.** With one large caveat, which is the next entry up as a
DX proposal awaiting approval: it fires for retry exhaustion and is
structurally blind to `tako.Permanent`, which is the commoner path. The
first poison in this walk — an undecodable image — took that path, and
the alert stayed quiet for twelve minutes while a dead letter sat in the
queue. The metric was not broken; it was measuring the wrong thing.

### Discovered work

- Pub/Sub dead-letter metrics took ~5 minutes to surface, and the series
  does not exist at all until the first forward. Any walk that asserts
  "quiet" on this preset must distinguish *no series* from *series at
  zero*; the gate script prints both.

## Z2b — DX proposal: the alerting namespace, and examples/siren (2026-08-03) — AWAITING APPROVAL

Owner-directed. Supersedes the earlier "DeadLettered is blind to
tako.Permanent" proposal, which fixed one metric; this fixes the family.

### The rule the names broke

Spec Part 1 rule 6 — *no primitive owns a general concept*. Alerting
claimed `Count`, `Rate`, `Sum`, `Mean`, `Percentile`, `Above`, `Below`,
`Absent`, `Platform`, `For`, `Email`, `PubSub`. Three are actively
misleading rather than merely broad:

- `tako.PubSub` is an alert channel, while tako's own pub/sub primitive
  is `tako.Topic`. The name points at the wrong concept in its own API.
- `tako.Count` reads a counter; `tako.Counter` declares one.
- `tako.For` is a bare preposition holding a top-level slot.

Rule 6 also states its own remedy: general concepts become **methods
scoped to an instance**. That is the lever here, not a prefix.

### Naming scheme

**1. Readings become methods on the handle they read.** Five general
words leave and none arrive:

```go
uploads.Rate()            // was tako.Rate(uploads)
uploads.Count()           // was tako.Count(uploads)
thumbMs.Sum()             // was tako.Sum(thumbMs)
thumbMs.Mean()            // was tako.Mean(thumbMs)
thumbMs.Percentile(0.99)  // was tako.Percentile(thumbMs, 0.99)
```

**2. Comparisons take a `When` prefix** — specific to conditions, and
unclaimable by anything else:

```go
tako.WhenAbove(…)  tako.WhenBelow(…)  tako.WhenAbsent(…)
```

**3. Channels take an `Alert` prefix**, and `PubSub` is renamed for what
it actually takes:

```go
tako.AlertEmail("ops@example.com")
tako.AlertWebhook("https://…")
tako.AlertTopic(incidents)          // was tako.PubSub — it takes a tako.Topic
```

`ChatSpace` is dropped: it needs a real Google Chat space, so no walk can
prove it works, and an unverifiable channel is worse than an absent one.
It returns additively when something can test it.

**4. Every condition is `<Subject><Situation>`** — the pattern the good
names already follow (`CronMissed`, `CronFailed`, `JobFailed`,
`SingletonDown`) and the bad ones broke:

| was | becomes | watches |
|---|---|---|
| `DeadLettered(c, n)` | **gone** | — |
| `Backlogged(c, age)` | `QueueStalled(c, age, w)` | oldest message's age |
| — | `QueueBacklogged(c, n, w)` | messages waiting — **new** |
| `Erroring(t, frac)` | `RequestsErroring(t, frac, w)` | 5xx share |
| `SlowerThan(t, d, q)` | `RequestsSlow(t, d, q, w)` | latency quantile |
| `AtCapacity(t)` | `ServiceAtCapacity(t, w)` | instance ceiling |
| `Platform(type, …)` | `PlatformMetric(type, …)` | a Google metric |
| `For(d)` | **gone** — use `Window{For: d}` | — |

`RequestsErroring`/`RequestsSlow` name what is measured rather than the
target, because the target is polymorphic (service, handler, route,
site) and any primitive-shaped prefix would be a lie for three of the
four.

**5. The dead-letter queue stops being a symbol.** It becomes a field on
a queue-scoped config struct, which is rule 2's shape and removes the
last general word:

```go
type QueueWatch struct {
    DeadLetters bool          // watch the dead-letter queue, not the live one
    For         time.Duration // REQUIRED — how long the level must hold
}
```

So the 2×2 is two constructors and one boolean:

```go
tako.QueueBacklogged(thumbs, 500, tako.QueueWatch{For: 10 * time.Minute})
tako.QueueBacklogged(thumbs, 1, tako.QueueWatch{DeadLetters: true, For: 10 * time.Minute})
tako.QueueStalled(thumbs, 30*time.Minute, tako.QueueWatch{For: 5 * time.Minute})
tako.QueueStalled(thumbs, 24*time.Hour, tako.QueueWatch{DeadLetters: true, For: time.Hour})
```

Underneath, all four are the same two GAUGES on a subscription
(`num_undelivered_messages`, `oldest_unacked_message_age`) — measured on
the live subscription or the `-dead` one. `dead_letter_message_count`
goes unused: it counts arrivals and cannot see a backlog already
sitting there.

**6. `For` is required on both queue conditions.** A level crosses
transiently every time a burst arrives, so "500 deep" means nothing
without "for how long". Precedent: `Count` refuses to default its
period, because the period IS the count. Omitting it is an analysis
error, not a guess:

```
app.go:31:44: tako.QueueBacklogged needs a sustain window — write tako.QueueWatch{For: …}; a queue crosses any depth for an instant, so how long it stays there IS the alert (tako/window)
```

### What this costs

`DeadLettered` and `For` are deleted, not deprecated, and eight
constructors are renamed. Every one is a compile error at the call site,
never a silent reinterpretation — except `Backlogged(q, 5)` with an
untyped constant, which would move from 5ns to 5 messages. Nobody writes
that; it is the one honest gap.

This reverses surface previously approved (the nine-preset catalogue and
the raw vocabulary). Doing it now is the cheap direction: **anything cut
can return additively; anything tagged cannot be removed without
breaking.**

### examples/siren — the app that exists to fail

The existing examples are too busy to double as an alerting testbed:
gallery's pipeline has to *work* for its own walk, so breaking it on
purpose fights the walk. `siren` is the sixth example and its entire job
is to break on command, one failure mode per switch:

```
GET  /boom?rate=0.5     a route that 5xxs a set fraction   → RequestsErroring
GET  /slow?ms=3000      a route that sleeps                → RequestsSlow
POST /flood?n=1000      floods the queue, worker sleeps    → QueueBacklogged
POST /poison?kind=hard  Permanent → dead-letters at once   → QueueBacklogged{DeadLetters}
POST /poison?kind=soft  transient → dead-letters after 5   → QueueStalled{DeadLetters}
POST /pause             worker stops acking                → QueueStalled
```

plus a cron that can be paused (`CronMissed`), one that always fails
(`CronFailed`), a job that exits non-zero (`JobFailed`), a singleton
that can be told to die (`SingletonDown`), a counter and a histogram for
the reading methods, and three channels — `AlertEmail`, `AlertWebhook`
pointed at siren's own `/incidents` route, and `AlertTopic` over a
declared `Incident` topic so the copy-pasteable struct in
`reference/alerts.md` is code that runs.

`ServiceAtCapacity` gets `MaxInstances: 1` and a `/slow` flood.

Its test suite asserts the GENERATED QUERY per condition — the live
walk (`validate/stage10.md`) is what proves they fire.

### Sequencing

This lands **before Z3**. Z3 adds `Latency`/`Instances`/`Saturation` —
three more general words — and they should be born under the scheme as
`ServiceLatency`/`ServiceInstances`/`ServiceSaturation` rather than
renamed a week later.

## Z2b — the rename and examples/siren, built (2026-08-03)

Owner-approved. Three commits: the namespace, the example, the docs.

### What the namespace became

Readings are methods on the handle they read — `uploads.Rate()`,
`thumbMs.Percentile(0.99)`. That is spec rule 6's OWN remedy for a
general concept ("methods scoped to a store instance"), and it removed
five top-level words without adding one. The `Recording` interface went
with them.

`When` on comparisons, `Alert` on channels, `<Subject><Situation>` on
conditions, and the dead-letter queue demoted from a symbol to a
`QueueWatch` field — because the two queues are the same two gauges on
the same kind of subscription and nothing else differs. `PubSub` became
`AlertTopic`: it takes a `tako.Topic`, and the old name pointed at the
wrong concept inside tako's own API.

`Platform` could not express its common case AT ALL. Filters are pasted
verbatim, so scoping to your own service needed an env-prefixed physical
name that does not exist at declaration time — the bare name matches
nothing, and omitting the filter matches every service in the project.
`ServiceMetric`/`QueueMetric` take the object and write the matcher.

### Six defects the query golden found

`examples/siren` declares 22 alerts across all 15 readings. Rendering
every generated query beside its notification sentence, and READING IT,
found:

1. **The latency query was malformed.** The range selector was appended
   to the label list, so it landed inside the braces —
   `{…,route="GET /slow"[60s]}` — which Cloud Monitoring answers with a
   400. Every latency alert had been broken since Y1.
2. **`SingletonDown` compared instance count `< 0`.** The preset takes
   no threshold argument, so the analyzer read zero. The only check a
   singleton has could not fire.
3. **`JobFailed` matched `job_name` on the job's name.** A Cloud Run job
   deploys under its RUNNER SERVICE's name. Third matcher in this
   feature to name something no deploy creates.
4. **`WhenAbsent` on a recording compiled to a threshold comparison**,
   which cannot express absence: an empty result set compares to nothing.
5. **Histogram queries carried no namespace**, so a preview env's series
   moved prod's p99.
6. **Raw readings had no notification sentence**, so their subject line
   was the Go variable name — defect 8 again, surviving in the half that
   had no example.

Three more surfaced while wiring the analyzer, none caused by the
rename: `Counter`/`Histogram` vars were never registered in
`resourceVars` (so a raw reading resolved to no subject and would have
generated `custom.googleapis.com/tako/` with an empty name); channel
constructors were dispatched from a hand-copied case list; and
`checkQuantiles` matched constructors only, so a `Percentile` written as
a method would have gone unchecked.

### The pattern, stated once more

Nine of these were invisible to every test that existed, and visible in
about four minutes of reading a rendered artifact. The through-line of
this whole feature is that **a query is not checkable by looking at the
code that builds it** — only by looking at what it built. The golden is
now that artifact, and three tests stand where the reading did: matchers
must name objects the plan creates, queries must be structurally
well-formed, and a comparison must be reachable.

### Not done

siren has never been deployed. Everything above is offline: goldens,
structure, analysis. Whether 22 policies actually create, and whether
each goes quiet then fires, is the Z2b live walk in
`validate/stage10.md`, and it is the only thing that can close it.

## Z2b — the live walk: 22 policies, 8 fired, three defects (2026-08-04)

siren deployed to `tako-test-01/z2`. Everything below is measured.

### Defect 22 — the notification agent does not exist

The deploy died after creating 56 objects:

```
create iam grants topic z2-incidents: Service account
service-119764066724@gcp-sa-monitoring-notification.iam.gserviceaccount.com
does not exist. (HTTP 400 from pubsub.googleapis.com)
```

`grants.go` carried a comment saying the opposite — "granting to an agent
that does not exist yet is accepted" — an assumption nothing had tested,
because no example declared a Pub/Sub channel until siren. Pub/Sub
refuses the binding outright.

Fixed by provisioning the identity rather than waiting for Google to
create it lazily: `EnsureServiceIdentity` calls serviceusage's
`generateServiceIdentity`, which is what `gcloud beta services identity
create` calls, is idempotent, and returns the same address every time.
The deploy calls it before any grant when the graph declares a Pub/Sub
channel.

### Defect 23 — a policy can only name a metric that already has data

The next deploy died on:

```
The following PromQL metric(s) are invalid:
  run_googleapis_com:container_memory_utilizations (HTTP 400)
```

The descriptor existed. Two series existed. The name still did not
resolve — because `container/memory/utilizations` is a **DISTRIBUTION**,
and Cloud Monitoring publishes a distribution only as `_bucket`,
`_count` and `_sum`, never under its bare name.

Checking the project's whole PromQL name list found the general case:
**four more metrics tako's own presets name did not resolve either**
(`subscription_oldest_unacked_message_age`,
`subscription_push_request_count`, `topic_byte_cost`,
`job_completed_execution_count`) — not because they are wrong, but
because nothing had written them in that project yet. tako seeds its own
metrics for exactly this reason and cannot seed Google's.

So the deploy now asks first, and says which alerts cannot be created
and why, instead of failing partway through an apply with Google's
sentence:

```
tako: these alerts name metrics Cloud Monitoring cannot resolve yet:
  Fat — run_googleapis_com:container_memory_utilizations
      it is a DISTRIBUTION, published only in parts: …_bucket, …_count, …_sum
      alert on one of those instead of the bare name

tako seeds its own metrics for this reason and cannot seed Google's.
Deploy without the alert, let the resource run once, then deploy again.
```

### The walk

All **22 policies and 3 channels** created — every policy carrying
documentation, severity WARNING, and no malformed query. The Pub/Sub
channel is the one defect 22 was blocking.

Baseline, running each policy's OWN query: **21 quiet, 1 firing.** The
one was `Failing`, and it was right — `Flaky` had run at 00:40 and
answered 500 (scheduler status.code 13). A true positive nobody staged.

Then the switches, and eight conditions went from quiet to firing:

| alert | reading | fired at |
|---|---|---|
| `Erroring` | error_rate | `1` — every `/boom` 500ing |
| `Slowing` | latency p99 | `4075.52` ms |
| `Sluggish` | histogram mean | `2499.99` ms |
| `Piling` | live queue depth | `274` messages |
| `Poisoned` | dead queue depth | `198` messages |
| `Maxed` | at instance ceiling | `1` |
| `JobBad` | job executions failed | `1` |
| `Failing` | cron failures | `3` |

**No condition ever fired falsely.** The fourteen that stayed quiet were
right to: their subjects were healthy, or their windows had not elapsed.

One thing worth knowing before the next walk: **a burst does not fire a
rate alert.** Forty deliberate 500s left `Erroring` quiet, because it
reads a rate over a rolling 60-second window and the burst was six
minutes gone. It fired within two minutes of SUSTAINED load. The data
was never missing — `route='GET /boom' status='5xx'` sat at 41 the whole
time. The alert was measuring what it says it measures.

### Not proven, and honestly so

`Missed` (10m of cron silence — proven twice on gallery), `Stalling`
(15m oldest-message age), `Untriaged` (24h), `Quiet`/`Idle`/`Dark`/
`NoThroughput` (30m–6h windows), `LoopDown` (needs the singleton
broken), `SubPoisoned` (needs the incident subscriber to fail),
`Grinding`, `Fat`, `Pushback`, `Billing` (thresholds not crossed).

Fourteen of twenty-two were created, verified quiet-while-healthy, and
left unfired. That is the honest number: **22/22 created and correct at
rest, 8/22 observed transitioning.**
