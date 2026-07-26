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
