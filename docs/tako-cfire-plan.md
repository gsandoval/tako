# Tako × cfire Compatibility Specification & Agent Execution Plan (Option A)

**Status:** Draft · Architecture & Execution Blueprint  
**Target Repositories:**
- **tako** (`/Users/memo/Programming/tako`) — Go control plane, local/GCP runtime backends, and CLI (`tako`)
- **cfire** (`/Users/memo/Programming/cfire`) — C++23 systems programming language & compiler (`cfire`)

> **Note on `docs/embed.go` Isolation:**  
> `docs/embed.go` embeds only `guide/`, `reference/`, and `api/`. Like `tako-tech-spec.md`, `tako-implementation-plan.md`, and `tako-agent-plan.md`, this top-level planning document in `docs/tako-cfire-plan.md` is intentionally excluded from the embedded doc surface so `docs/surface_test.go` remains unaffected.

---

## 1. Executive Summary & Architectural Thesis

### 1.1 Why Option A (First-Class Language & Runtime Shim) Wins

`tako` is a `58,159 LOC` declarative cloud framework and control plane built around **two narrow architectural waists**:

1. **The Compile-Time Waist (`pkg/analyzer.AppGraph` — `387 LOC`):**  
   Every downstream control-plane subsystem in `tako`—infrastructure reconciliation (`pkg/provision/gcp`, `8,692 LOC`), Mermaid topology rendering (`pkg/analyzer/mermaid.go`), IAM least-privilege derivation, Cloud Monitoring MQL alert synthesis, and browser client generation (`pkg/clientgen`)—consumes a single, language-agnostic JSON value: `analyzer.AppGraph`. None of those packages inspect `go/ast` or `go/types` directly (except `clientgen` for nested TypeScript DTO fields, which can also be fed from a type schema map).
2. **The Runtime Waist (`pkg/backends` — `590 LOC` interfaces, `2,193 LOC` conformance tests):**  
   Every stateful primitive in `tako` (`Entity`, `Bucket`, `Queue`, `Topic`, `Cache`, `Lease`, `Users`) operates over opaque `[]byte` JSON payloads and `string` keys at the backend boundary (`pkg/backends/interfaces.go`), backed either by in-process local storage (`pkg/backends/local` on `bbolt`) or production GCP services (`pkg/backends/gcp` on Firestore/Datastore, GCS, Pub/Sub, Cloud Tasks, Memorystore, Secret Manager).

Rewriting `tako` in `cfire` (Option B) would require re-implementing `58k+ LOC` of battle-tested GCP REST/gRPC control-plane reconcilers, IAM policy diffing, Cloud Build/Artifact Registry OCI layer assembly, and local `bbolt` chaos engines.  
By contrast, **Option A (First-Class Framework Shim)** bridges `cfire` into `tako` at its two narrow waists in **~2,800 LOC total across both repositories**, giving `.cf` applications:
- 100% of `tako`'s local development loop (`tako dev`, `.tako/data` persistence, dev bar, chaos delivery `--chaos`, `tako test` in-memory `TestApp`),
- 100% of `tako`'s static verification (`tako analyze`, `tako graph --mermaid`, `tako/rule` diagnostics), and
- 100% of `tako`'s zero-config GCP deployment (`tako preview`, `tako deploy`, `tako destroy`, `tako logs`, `tako alerts`).

```
                      ┌─────────────────────────────────────────────┐
                      │         Developer's cfire App (*.cf)        │
                      │   import "core:tako"                        │
                      │   Links := tako.entity[Link]("links")       │
                      │   Create := tako.api("POST /links", create) │
                      └───────────┬─────────────────────┬───────────┘
                                  │                     │
             Compile-Time Waist   │                     │   Runtime Waist
     (cfire outline --tako-graph) │                     │   (core:tako -> libtako.a)
                                  ▼                     ▼
                      ┌───────────────────────┐ ┌───────────────────────────┐
                      │  analyzer.AppGraph    │ │   tako/cmd/libtako        │
                      │  (JSON Contract)      │ │   (c-archive C-ABI)       │
                      └───────────┬───────────┘ └─────────────┬─────────────┘
                                  │                           │
            ┌─────────────────────┼─────────────────┐         ▼
            ▼                     ▼                 ▼   ┌───────────────────┐
   ┌─────────────────┐  ┌──────────────────┐ ┌────────┐ │ tako/pkg/runtime  │
   │pkg/codegen      │  │pkg/provision/gcp │ │mermaid │ │ tako/pkg/backends │
   │(.tako/gen/*.cf) │  │(Cloud Run/IAM/…) │ │& alerts│ │ (local & gcp)     │
   └─────────────────┘  └──────────────────┘ └────────┘ └───────────────────┘
```

### 1.2 Non-Negotiable Design Invariants

1. **Zero Regression to Go Apps:** Existing Go `tako` apps and all existing `go test ./...` packages continue to work untouched. Language detection in `tako` is automatic based on module contents (`go.mod` vs `build.cf` / `*.cf`).
2. **Identical Six Interface Rules:** A `cfire` Tako app obeys the exact same 6 structural rules as a Go Tako app (package-level declarations, constant string names, two-arg handlers `(ctx: tako.Ctx, req: Req) -> Res!`, no raw cloud SDK imports, no ambient env/goroutines in init, indexed entity queries).
3. **Identical Diagnostic Format:** Every static violation in a `.cf` file emits `path:line:col: message (tako/rule-id)` matching `pkg/analyzer.Error` byte-for-byte.
4. **Readable Generated Code:** Just as `pkg/codegen` writes human-readable `.tako/gen/<service>/main.go`, `pkg/codegen` for `cfire` writes human-readable, `cfire fmt`-clean `.tako/gen/<service>/main.cf`.
5. **Single-Binary Container Images:** `pkg/build` compiles `.tako/gen/<service>/main.cf` linked with `libtako.a` into a self-contained `linux/amd64` binary layered onto `gcr.io/distroless/cc-debian12:nonroot` with deterministic timestamps (`time.Unix(0, 0)`).

---

## 2. Normative `core:tako` Surface in `cfire`

### 2.1 The Six Interface Rules in `cfire`

Every `cfire` application targeting `tako` imports `"core:tako"` and follows the six normative rules from `tako-tech-spec.md` §1.2:

1. **Rule 1 — Package-Level Declarations Only:**  
   Resources (`tako.entity`, `tako.bucket`, `tako.queue`, `tako.topic`, `tako.cache`, `tako.lock`, `tako.secret`, `tako.auth`, `tako.site`, `tako.service`, `tako.counter`, `tako.histogram`, `tako.alert`) and handlers (`tako.api`, `tako.page`, `tako.worker`, `tako.subscribe`, `tako.cron`, `tako.job`, `tako.singleton`) MUST be declared as top-level package bindings (`Name := tako.<ctor>(...)`). Calling a declaration constructor inside a `fn` body is rejected with `(tako/decl-in-func)`.
2. **Rule 2 — Compile-Time String Literal Names:**  
   The resource name, route pattern, or cron schedule passed as the first argument to any `tako.*` declaration constructor MUST be a string literal (e.g., `"links"`, `"POST /links"`, `"GET /links/{code}"`). Computed strings or variables are rejected with `(tako/non-const-name)`.
3. **Rule 3 — Uniform Handler Signatures:**  
   - **API / Page Handlers:** `fn(ctx: tako.Ctx, req: ReqStruct) -> ResType!` where `ReqStruct` is a declared `struct` (or `tako.Empty`) and `ResType` is a declared `struct`, slice `[]T`, primitive, `tako.Empty`, `tako.Blob`, or `tako.HTML`. Violations emit `(tako/bad-handler-sig)`.
   - **Worker / Topic Subscriber / Job Handlers:** `fn(ctx: tako.Ctx, msg: MsgStruct) -> void!`
   - **Bucket Subscriber Handlers:** `fn(ctx: tako.Ctx, ev: tako.BucketEvent) -> void!`
   - **Cron / Singleton Handlers:** `fn(ctx: tako.Ctx) -> void!`
4. **Rule 4 — Struct Tags Drive Binding & Schema:**  
   - Request structs use field tags: `` `path:"code"` ``, `` `query:"limit"` ``, `` `form:"title"` ``, `` `blob:"file"` ``, `` `json:"url"` ``. Every `{param}` in a route pattern MUST match a `` `path:"param"` `` field on `ReqStruct`, and vice versa (`tako/route-param-mismatch`).
   - Entity structs MUST have exactly one `` `entity:"id"` `` field of type `str` (`tako/entity-missing-id` / `tako/entity-bad-id`). Fields queried via `.where("field", ...)` or `.order_by("field", ...)` MUST carry `` `entity:"index"` `` (`tako/unindexed-query`).
5. **Rule 5 — Explicit Resource Access in Handler Call Trees:**  
   IAM permissions and service topology edges are derived statically from the call graph rooted at each declared handler (`Edges`).
6. **Rule 6 — No Raw Listeners or Unmanaged Background Threads:**  
   Calling `net.tcp_listen`, `http.listen_and_serve`, or spawning detached threads outside `tako` primitives in a `core:tako` package is rejected (`tako/forbidden-call`).

### 2.2 Complete `.cf` API Surface (`import "core:tako"`)

Below is the canonical `cfire` API surface that `cfire/runtime/core/tako.hpp` and `cfire/src/docgen/catalog.hpp` expose when a user writes `import "core:tako"`:

```cfire
package myapp

import "core:tako"

// ── 1. Request / Entity Types & Struct Tags ─────────────────────────────────

type Link struct {
    code:       str   `entity:"id" json:"code"`
    url:        str   `json:"url"`
    owner_id:   str   `entity:"index" json:"owner_id"`
    clicks:     i64   `entity:"index" json:"clicks"`
    created_at: i64   `entity:"index" json:"created_at"`
}

type CreateLinkReq struct {
    url: str `json:"url"`
}

type GetLinkReq struct {
    code: str `path:"code"`
}

type ListLinksReq struct {
    owner_id: str `query:"owner_id"`
    limit:    i32 `query:"limit"`
}

type ClickEvent struct {
    code:    str `json:"code"`
    ts_unix: i64 `json:"ts_unix"`
}

// ── 2. Resource Declarations (Top-Level Only) ───────────────────────────────

// Service scaling & domain options (optional per package)
Svc := tako.service("shortlink", tako.ServiceOpts{
    profile: "latency",
    domain:  "links.example.com",
})

// Stateful storage
Links   := tako.entity[Link]("links")
Assets  := tako.bucket("assets")
Clicks  := tako.queue[ClickEvent]("clicks")
Events  := tako.topic[ClickEvent]("events")
HotUrls := tako.cache[str]("hot-urls")
Migrate := tako.lock("migrate")
ApiKey  := tako.secret("external-api-key")

// Auth & Roles
AdminRole := tako.role("admin")
AdminsOnly := tako.require_role(AdminRole)
Auth := tako.auth("google", "email")

// Observability
ClickCounter := tako.counter("clicks_total")
LatencyHist  := tako.histogram("redirect_ms")

// ── 3. Handler Declarations (Top-Level Only) ────────────────────────────────

CreateLink := tako.api("POST /links", create_link, tako.RouteOpts{
    sign_in: true,
})

GetLink := tako.api("GET /links/{code}", get_link)

DeleteLink := tako.api("DELETE /links/{code}", delete_link, tako.RouteOpts{
    allow: AdminsOnly,
})

ProcessClick := tako.worker(Clicks, process_click)

AuditClick := tako.subscribe(Events, audit_click)

OnAssetUpload := tako.subscribe_bucket(Assets, on_asset_upload)

PruneExpired := tako.cron("0 * * * *", prune_expired, tako.CronOpts{
    timezone:     "America/New_York",
    singleflight: true,
})

// ── 4. Handler Implementations ──────────────────────────────────────────────

fn create_link(ctx: tako.Ctx, req: CreateLinkReq) -> Link! {
    if req.url == "" {
        return Err(tako.bad_request("url is required"))
    }
    user := ctx.user() or {
        return Err(tako.unauthorized("sign-in required"))
    }
    link := Link{
        code:       "abc123",
        url:        req.url,
        owner_id:   user.id,
        clicks:     0,
        created_at: ctx.now_unix(),
    }
    try Links.put(ctx, link)
    try HotUrls.set(ctx, link.code, link.url, 300)
    try Clicks.publish(ctx, ClickEvent{code: link.code, ts_unix: link.created_at})
    ClickCounter.inc(ctx, 1)
    return link
}

fn get_link(ctx: tako.Ctx, req: GetLinkReq) -> Link! {
    if cached := HotUrls.get(ctx, req.code); cached != None {
        // cache hit path
    }
    link := try Links.get(ctx, req.code)
    return link
}

fn delete_link(ctx: tako.Ctx, req: GetLinkReq) -> tako.Empty! {
    try Links.delete(ctx, req.code)
    return tako.Empty{}
}

fn process_click(ctx: tako.Ctx, ev: ClickEvent) -> void! {
    try Links.tx(ctx, |tx: tako.Tx[Link]| -> void! {
        mut link := try tx.get(ev.code)
        link.clicks += 1
        try tx.put(link)
        return
    })
}

fn audit_click(ctx: tako.Ctx, ev: ClickEvent) -> void! {
    ctx.log_info("click audited", "code", ev.code)
}

fn on_asset_upload(ctx: tako.Ctx, ev: tako.BucketEvent) -> void! {
    data := try Assets.get(ctx, ev.key)
    ctx.log_info("asset uploaded", "key", ev.key, "bytes", str(len(data)))
}

fn prune_expired(ctx: tako.Ctx) -> void! {
    rows := try Links.query()
        .where("owner_id", "==", "guest")
        .order_by("-created_at")
        .limit(100)
        .all(ctx)
    for row in rows {
        try Links.delete(ctx, row.code)
    }
}
```

### 2.3 Complete Method Table of `core:tako` Types

| Type | Methods & Signatures | Maps to `tako` Go Runtime / Backend |
|---|---|---|
| `tako.Ctx` | `ctx.user() -> tako.User?`<br>`ctx.now_unix() -> i64`<br>`ctx.trace_id() -> str`<br>`ctx.log_info(msg: str, kvs: ...str)`<br>`ctx.log_warn(msg: str, kvs: ...str)`<br>`ctx.log_error(msg: str, kvs: ...str)` | `tako.Ctx.User()`, `slog.InfoContext`, trace propagation |
| `tako.Entity[T]` | `.get(ctx: tako.Ctx, id: str) -> T!`<br>`.put(ctx: tako.Ctx, val: T) -> void!`<br>`.delete(ctx: tako.Ctx, id: str) -> void!`<br>`.tx(ctx: tako.Ctx, fn: \|tako.Tx[T]\| -> void!) -> void!`<br>`.query() -> tako.Query[T]` | `backends.EntityStore` (`Get`, `Put`, `Delete`, `Tx`, `Query`) |
| `tako.Query[T]` | `.where(field: str, op: str, val: Any) -> tako.Query[T]`<br>`.order_by(field: str) -> tako.Query[T]`<br>`.limit(n: i32) -> tako.Query[T]`<br>`.cursor(tok: str) -> tako.Query[T]`<br>`.all(ctx: tako.Ctx) -> []T!`<br>`.page(ctx: tako.Ctx) -> tako.Page[T]!` | `backends.Query` (`Filters`, `OrderField`, `OrderDesc`, `Limit`, `Cursor`) |
| `tako.Bucket` | `.put(ctx: tako.Ctx, key: str, data: bytes, content_type: str) -> void!`<br>`.get(ctx: tako.Ctx, key: str) -> bytes!`<br>`.delete(ctx: tako.Ctx, key: str) -> void!`<br>`.signed_put(ctx: tako.Ctx, key: str, content_type: str, ttl_sec: i32) -> str!`<br>`.signed_get(ctx: tako.Ctx, key: str, ttl_sec: i32) -> str!` | `backends.BucketStore` (`Put`, `Get`, `Delete`, `SignedPutURL`, `SignedGetURL`) |
| `tako.Queue[T]` | `.publish(ctx: tako.Ctx, msg: T) -> void!`<br>`.publish_delayed(ctx: tako.Ctx, msg: T, delay_sec: i32) -> void!` | `backends.QueueBroker.Publish` |
| `tako.Topic[T]` | `.publish(ctx: tako.Ctx, msg: T) -> void!` | `backends.TopicBroker.Publish` |
| `tako.Cache[T]` | `.get(ctx: tako.Ctx, key: str) -> T?`<br>`.set(ctx: tako.Ctx, key: str, val: T, ttl_sec: i32) -> void!`<br>`.delete(ctx: tako.Ctx, key: str) -> void!` | `backends.CacheStore` (`Get`, `Set`, `Delete`) |
| `tako.Lock` | `.with_lease(ctx: tako.Ctx, ttl_sec: i32, fn: \|\| -> void!) -> void!` | `backends.LeaseStore.Acquire` / `Release` |
| `tako.Secret` | `.get(ctx: tako.Ctx) -> str!` | `backends.SecretStore.Access` |
| `tako.Endpoint[Req, Res]` | `.call(ctx: tako.Ctx, req: Req) -> Res!` | Direct in-process call or `runtime.RemoteJSON` when `TAKO_CALLEE_<SVC>` is set |
| `tako.Job[Param]` | `.run(ctx: tako.Ctx, param: Param) -> void!` | `runtime.TriggerJob` |
| `tako.Site` | `.render(ctx: tako.Ctx, tmpl: str, data: Any) -> tako.HTML!`<br>`.markdown(md: str) -> str` | `runtime.RenderTemplate` / `runtime.RenderMarkdown` |
| HTTP Error Constructors | `tako.bad_request(msg: str) -> str`<br>`tako.unauthorized(msg: str) -> str`<br>`tako.forbidden(msg: str) -> str`<br>`tako.not_found(msg: str) -> str`<br>`tako.conflict(msg: str) -> str` | Encodes structured status prefix (`"__tako_status:400:..."`) mapped to `tako.ErrorStatus` in `libtako` |
| `tako.TestApp` | `tako.new_test_app() -> tako.TestApp`<br>`app.request(method: str, path: str, body: str) -> tako.TestResponse`<br>`app.as_user(uid: str, email: str, roles: []str) -> tako.TestApp`<br>`app.drain_async() -> void!` | `tako.NewTestApp` in-memory `bbolt` + synchronous queue/topic drain |

---

## 3. Pillar 2: The `libtako` C-Archive Runtime Bridge

### 3.1 Why `go build -buildmode=c-archive`

Rather than re-implementing `bbolt` local persistence, devbar HTML injection, chaos delivery, OIDC session verification, Firestore/Datastore transactions, GCS V4 signed URLs, and Cloud Monitoring OTLP exporters in C++, `tako` exposes a small `cmd/libtako` package compiled as a C static archive (`libtako.a` + `libtako.h`).
- Every backend operation in `tako/pkg/backends` already takes `(ctx, resourceName, key, []byte)`.
- `cfire` already has compile-time struct reflection (`cfire::reflect_struct<T>`) and zero-boilerplate JSON serialization (`cfire::core::json::marshal(val)` / `unmarshal<T>(str)`).
- Therefore, `cfire` serializes `T` to a JSON string, passes `(const char* ptr, int32_t len)` across the C ABI to `libtako.a`, and unmarshals the returned JSON buffer into `T`.

### 3.2 Exact File Path & Build Command

- **Go Source:** `/Users/memo/Programming/tako/cmd/libtako/main.go`
- **C++ Runtime Header:** `/Users/memo/Programming/cfire/runtime/core/tako.hpp`
- **Host Archive Build (macOS / Linux dev):**
  ```bash
  go build -buildmode=c-archive -o .tako/lib/libtako.a github.com/gsandoval/tako/cmd/libtako
  ```
- **Linux Container Cross-Build (`pkg/build`):**
  ```bash
  CGO_ENABLED=1 GOOS=linux GOARCH=amd64 CC=x86_64-linux-gnu-gcc \
    go build -trimpath -buildmode=c-archive -o .tako/lib/libtako-linux-amd64.a github.com/gsandoval/tako/cmd/libtako
  ```
  *(Fallback if cross-gcc is absent on macOS host: `pkg/build` builds inside a lightweight builder or uses `zig cc -target x86_64-linux-gnu` which ships as a single static binary without Docker).*

### 3.3 Exact C-ABI Specification (`libtako.h` / `cmd/libtako/main.go`)

Every function exported by `cmd/libtako/main.go` uses `//export` and follows strict ownership rules:
1. **Inputs (`const char*`, `int32_t`):** Borrowed for the duration of the synchronous C call; Go copies them if retained.
2. **Outputs (`tako_buf_t`):** Allocated with `C.malloc` on the Go side and freed by the C++ caller via `tako_c_free(buf)` immediately after copying into `std::string` / `std::vector<uint8_t>`.
3. **Context Handle (`uint64_t ctx_id`):** An integer handle into a concurrency-safe Go registry (`sync.Map` of `uint64 -> context.Context`) live for the duration of a request, worker invocation, or transaction callback.

```c
// Conceptual C ABI exposed by cmd/libtako/main.go
#include <stdint.h>

typedef struct {
    char*   data;
    int32_t len;
    int32_t status; // 0 = OK, 404 = ErrNotFound, 409 = ErrConflict, 400..599 = HTTP error
    char*   err;    // non-null when status != 0; freed by tako_c_free
} tako_buf_t;

typedef struct {
    const char* method;
    const char* path;
    const char* raw_query;
    const char* body;
    int32_t     body_len;
    uint64_t    ctx_id;
} tako_http_req_t;

// Callback typedefs invoked from Go into C++
typedef tako_buf_t (*tako_http_cb)(tako_http_req_t req, void* user_data);
typedef tako_buf_t (*tako_async_cb)(uint64_t ctx_id, const char* payload, int32_t payload_len, void* user_data);
typedef tako_buf_t (*tako_cron_cb)(uint64_t ctx_id, void* user_data);
typedef tako_buf_t (*tako_tx_cb)(uint64_t tx_id, void* user_data);

// Lifecycle & Server
void       tako_c_free(tako_buf_t buf);
void       tako_c_boot(void);
void       tako_c_set_queue_rate(const char* queue_name, int32_t rate_limit);
void       tako_c_register_roles(const char* roles_csv);
void       tako_c_mount_json(const char* pattern, tako_http_cb cb, void* user_data, int32_t require_auth, const char* policy_name, int32_t cache_sec);
void       tako_c_mount_blob(const char* pattern, tako_http_cb cb, void* user_data, int32_t require_auth, const char* policy_name, int32_t cache_sec);
void       tako_c_mount_page(const char* pattern, tako_http_cb cb, void* user_data, int32_t require_auth, const char* policy_name, int32_t cache_sec);
void       tako_c_register_worker(const char* var_name, const char* queue_name, tako_async_cb cb, void* user_data);
void       tako_c_register_subscriber(const char* var_name, const char* topic_or_bucket, const char* binding_kind, tako_async_cb cb, void* user_data);
void       tako_c_register_cron(const char* var_name, const char* schedule, int32_t singleflight, tako_cron_cb cb, void* user_data);
void       tako_c_register_job(const char* var_name, tako_async_cb cb, void* user_data);
void       tako_c_register_singleton(const char* var_name, tako_cron_cb cb, void* user_data);
int32_t    tako_c_serve(int32_t has_auth, const char* auth_providers_csv, int32_t has_bucket, int32_t has_cron, const char* site_dir, const char* site_opts_json);

// Request binding helpers (backed by *http.Request in ctx_id)
tako_buf_t tako_c_req_param(uint64_t ctx_id, const char* kind, const char* name);
tako_buf_t tako_c_ctx_user_json(uint64_t ctx_id);
int64_t    tako_c_ctx_now_unix(uint64_t ctx_id);
void       tako_c_ctx_log(uint64_t ctx_id, const char* level, const char* msg, const char* kvs_json);

// Entity Store
void       tako_c_entity_register(const char* name, const char* id_field, const char* indexes_csv, const char* composites_json);
tako_buf_t tako_c_entity_get(uint64_t ctx_id, const char* entity_name, const char* id);
tako_buf_t tako_c_entity_put(uint64_t ctx_id, const char* entity_name, const char* id, const char* json_data, int32_t json_len);
tako_buf_t tako_c_entity_delete(uint64_t ctx_id, const char* entity_name, const char* id);
tako_buf_t tako_c_entity_query(uint64_t ctx_id, const char* entity_name, const char* query_spec_json);
tako_buf_t tako_c_entity_tx(uint64_t ctx_id, const char* entity_name, tako_tx_cb cb, void* user_data);
tako_buf_t tako_c_tx_get(uint64_t tx_id, const char* entity_name, const char* id);
tako_buf_t tako_c_tx_put(uint64_t tx_id, const char* entity_name, const char* id, const char* json_data, int32_t json_len);
tako_buf_t tako_c_tx_delete(uint64_t tx_id, const char* entity_name, const char* id);

// Bucket Store
tako_buf_t tako_c_bucket_put(uint64_t ctx_id, const char* bucket_name, const char* key, const char* data, int32_t len, const char* content_type);
tako_buf_t tako_c_bucket_get(uint64_t ctx_id, const char* bucket_name, const char* key);
tako_buf_t tako_c_bucket_delete(uint64_t ctx_id, const char* bucket_name, const char* key);
tako_buf_t tako_c_bucket_signed_url(uint64_t ctx_id, const char* bucket_name, const char* key, const char* method, const char* content_type, int32_t ttl_sec);

// Queue & Topic Brokers
tako_buf_t tako_c_queue_publish(uint64_t ctx_id, const char* queue_name, const char* json_data, int32_t len, int32_t delay_sec);
tako_buf_t tako_c_topic_publish(uint64_t ctx_id, const char* topic_name, const char* json_data, int32_t len);

// Cache, Lock, Secret, Metrics
tako_buf_t tako_c_cache_get(uint64_t ctx_id, const char* cache_name, const char* key);
tako_buf_t tako_c_cache_set(uint64_t ctx_id, const char* cache_name, const char* key, const char* val_json, int32_t len, int32_t ttl_sec);
tako_buf_t tako_c_cache_delete(uint64_t ctx_id, const char* cache_name, const char* key);
tako_buf_t tako_c_lock_with_lease(uint64_t ctx_id, const char* lock_name, int32_t ttl_sec, tako_cron_cb cb, void* user_data);
tako_buf_t tako_c_secret_get(uint64_t ctx_id, const char* secret_name);
void       tako_c_metric_inc(uint64_t ctx_id, const char* counter_name, int64_t delta);
void       tako_c_metric_observe(uint64_t ctx_id, const char* hist_name, double val);

// TestApp Harness (for `tako test`)
uint64_t   tako_c_testapp_new(void);
void       tako_c_testapp_close(uint64_t app_id);
tako_buf_t tako_c_testapp_request(uint64_t app_id, const char* method, const char* path, const char* body, int32_t body_len, const char* user_json);
tako_buf_t tako_c_testapp_drain(uint64_t app_id);
```

### 3.4 How C++ Closures Cross the C-ABI (`void* user_data` Trampoline)

In `cfire/runtime/core/tako.hpp`, C++ lambdas and `fn` pointers are bridged to `tako_http_cb`, `tako_async_cb`, and `tako_tx_cb` without global mutable state by passing the address of a stack-allocated or heap-registered `std::function` as `void* user_data`:

```cpp
// Inside cfire::core::tako::Entity<T>::tx
template <typename Fn>
std::expected<void, std::string> tx(Ctx ctx, Fn&& fn) const {
    auto wrapper = std::function<std::expected<void, std::string>(Tx<T>)>(std::forward<Fn>(fn));
    auto trampoline = +[](uint64_t tx_id, void* user_data) -> tako_buf_t {
        auto* fp = static_cast<std::function<std::expected<void, std::string>(Tx<T> )>*>(user_data);
        Tx<T> tx_handle{tx_id};
        auto res = (*fp)(tx_handle);
        if (!res.has_value()) {
            return tako_make_err_buf(500, res.error());
        }
        return tako_buf_t{nullptr, 0, 0, nullptr};
    };
    tako_buf_t res = tako_c_entity_tx(ctx.id, name_.c_str(), trampoline, &wrapper);
    return tako_unwrap_void(res);
}
```

---

## 4. Pillar 3: Static Analyzer & `AppGraph` JSON Contract

### 4.1 CLI Contract Between `tako` and `cfire`

When `tako` runs `analyzer.Analyze(cfg)`:
1. `tako/pkg/analyzer/cfire.go` checks whether `cfg.Dir` is a `cfire` project via `IsCfireProject(dir string) bool`:
   - Returns `true` if `dir` contains no `.go` files at top-level AND contains one or more `.cf` files (excluding `.tako/`).
2. If `IsCfireProject(cfg.Dir)` is `true`, `Analyze` executes:
   ```bash
   cfire outline --tako-graph <cfg.Dir>
   ```
3. **Exit Code & Output Contract of `cfire outline --tako-graph <dir>`:**
   - **Exit `0` (Valid App):** Writes a single JSON object to `stdout` conforming strictly to `analyzer.AppGraph` (`pkg/analyzer/appgraph.go`), plus an optional `"diagnostics": []` empty array.
   - **Exit `1` (Rule Violations):** Writes structured diagnostics to `stderr` (one line per error in `path:line:col: message (tako/rule)` format) AND/OR a JSON envelope with `"errors": [{"pos": "...", "msg": "...", "rule": "..."}]`, which `tako/pkg/analyzer/cfire.go` parses directly into `analyzer.ErrorList`.
   - **Exit `2` (Syntax / Type Sema Error):** Writes compiler syntax/type errors to `stderr`; `tako/pkg/analyzer/cfire.go` returns a regular `error` (just as `packages.Load` does when Go code fails to compile).

### 4.2 AST Walk Passes in `cfire/src/tako_analyzer.hpp`

`cfire` already parses `.cf` files into `ParsedFile` and type-checks them in `sema.hpp` (`CheckedFile`). We add a self-contained header `cfire/src/tako_analyzer.hpp` invoked by `cfire outline --tako-graph <dir>` that runs **five deterministic passes** over all `.cf` packages in `<dir>` (skipping `.tako/`, `.cfire/`, and `*_test.cf` files for production graph declarations):

#### Pass 1: Module & Package Discovery
- **Module Name (`graph.Module`):**
  - If `build.cf` declares `b.set_name("my-app")`, use `"my-app"`.
  - Otherwise, use `std::filesystem::path(dir).filename().string()`.
- **Package Path:**
  - Root directory `.cf` files have package path `graph.Module` and short package name from `package <name>`.
  - Subdirectory `sub/foo.cf` files have package path `graph.Module + "/sub"`.

#### Pass 2: Entity Struct Tag Inspection
For every `type <Name> struct` referenced as type argument `T` in `tako.entity[T](...)`:
1. Scan `StructDecl::fields` and their `tag` strings (using existing tag parser logic from `codegen.hpp`).
2. Locate fields with `entity:"id"` and `entity:"index"`:
   - If zero fields have `entity:"id"`, emit diagnostic:  
     `file.cf:line:col: entity <Name> must have an entity:"id" string field (tako/entity-missing-id)`
   - If `entity:"id"` field is not `str`, emit:  
     `file.cf:line:col: entity <Name> id field "<field>" must be str, got <type> (tako/entity-bad-id)`
   - Record all `entity:"index"` field JSON names (from `json:"..."` tag or field name if untagged) in sorted order into `Resource.Indexes`.

#### Pass 3: Top-Level Resource & Handler Extraction
Walk top-level `StmtKind::VarDecl` (`Var := tako.<ctor>(...)`) and function bodies (to enforce `tako/decl-in-func`):
1. **Rule Check (`tako/decl-in-func`):** Any call to `tako.entity`, `tako.bucket`, `tako.queue`, `tako.topic`, `tako.cache`, `tako.lock`, `tako.secret`, `tako.api`, `tako.page`, `tako.worker`, `tako.subscribe`, `tako.cron`, `tako.job`, `tako.singleton`, `tako.auth`, `tako.site`, `tako.service`, `tako.counter`, `tako.histogram`, or `tako.alert` inside a `FuncDecl` body emits:  
   `file.cf:line:col: tako.<ctor> must be declared at package level (tako/decl-in-func)`
2. **Rule Check (`tako/non-const-name`):** Verify the first string argument (name, route, or schedule) is an `ExprKind::StringLit` and non-empty; otherwise emit:  
   `file.cf:line:col: tako.<ctor> name must be a non-empty string literal (tako/non-const-name)`
3. **Resource Construction:** Populate `analyzer.Resource`:
   - `Kind`: `"entity"`, `"bucket"`, `"queue"`, `"topic"`, `"cache"`, `"lock"`, `"secret"`, `"auth"`, `"site"`, `"service"`, `"status"`, `"counter"`, `"histogram"`, `"channel"`, `"alert"`
   - `Name`: literal string name
   - `Var`: `"<pkg_name>.<VarName>"`
   - `Package`: package import path
   - `Type`: `"<pkg_name>.<StructName>"`
   - `Pos`: `"<rel_path>:<line>:<col>"`
   - Extract optional config struct literals (`tako.DeliveryOpts`, `tako.SiteOpts`, `tako.ServiceOpts`) into `RateLimit`, `MaxAttempts`, `MinBackoffSeconds`, `MaxBackoffSeconds`, `Dir`, `Build`, `BuildOut`, `DevProxy`, `SPA`, `TrustedHTML`, `NotFound`, `Domain`, `Profile`, `Footprint`, `Traffic`.
4. **Handler Construction & Signature Validation (`tako/bad-handler-sig`):**
   - Look up the handler `FuncDecl` passed to `tako.api` / `tako.worker` / `tako.subscribe` / `tako.cron` / `tako.job` / `tako.singleton`.
   - Verify parameter 0 is `tako.Ctx`.
   - For `api` / `page`, verify parameter 1 is a `struct` type, and return type is fallible `Res!`. Extract `ReqType`, `ReqRef`, `ResRef`, `ResBlob` (`Res == "tako.Blob"`), `Bindings` (`path`, `query`, `form`, `blob` tagged fields mapped to Go scalar names: `str` $\to$ `"string"`, `[]str` $\to$ `"[]string"`, `bool` $\to$ `"bool"`, `i32` $\to$ `"int"`, `i64` $\to$ `"int64"`, `f64` $\to$ `"float64"`), and `HasBody` (true if `ReqStruct` has untagged or `json:"..."` fields and HTTP method is `POST`/`PUT`/`PATCH`).
   - Verify route path parameters `{param}` against `Bindings` (`Kind == "path"`); on mismatch emit `(tako/route-param-mismatch)`.
   - Verify single worker per queue (`tako/duplicate-worker`).

#### Pass 4: Transitive Call Graph & Resource `Edges` + Query Index Scan
For each declared `Handler`, perform a depth-first traversal of its `FuncDecl` body and any helper `FuncDecl`s it calls within the package/module:
1. **Resource Access Edges (`analyzer.Edge`):**
   - `Entity.get` / `Entity.query` $\to$ `Edge{From: h.Var, To: r.Name, ToKind: "entity", Kind: "resource", Verb: "read"}`
   - `Entity.put` / `Entity.delete` / `Entity.tx` $\to$ `Verb: "write"` (plus `"read"` for `tx` if `tx.get` is called)
   - `Bucket.get` $\to$ `ToKind: "bucket", Verb: "read"`
   - `Bucket.put` / `Bucket.delete` $\to$ `ToKind: "bucket", Verb: "write"`
   - `Bucket.signed_put` / `Bucket.signed_get` $\to$ `ToKind: "bucket", Verb: "sign"`
   - `Queue.publish` / `Queue.publish_delayed` $\to$ `ToKind: "queue", Verb: "publish"` (if `publish_delayed`, set `Resource.Delayed = true`)
   - `Topic.publish` $\to$ `ToKind: "topic", Verb: "publish"`
   - `Cache.get` $\to$ `ToKind: "cache", Verb: "read"`
   - `Cache.set` / `Cache.delete` $\to$ `ToKind: "cache", Verb: "write"`
   - `Lock.with_lease` $\to$ `ToKind: "lock", Verb: "write"`
   - `Secret.get` $\to$ `ToKind: "secret", Verb: "read"`
   - `Job.run` $\to$ `ToKind: "job", Verb: "run"`
   - `Endpoint.call` $\to$ `Edge{From: h.Var, To: targetHandler.Var, Kind: "call", Verb: "call"}`
2. **Query Index & Composite Index Derivation (`tako/unindexed-query`):**
   - Inspect method chains rooted at `<EntityVar>.query()`:
     - Collect `.where("<field>", "<op>", ...)` and `.order_by("<field>")`.
     - Verify every `<field>` is in `Resource.Indexes`; if not, emit:  
       `file.cf:line:col: query on <Entity>.<field> requires entity:"index" tag (tako/unindexed-query)`
     - If a query chain combines multiple fields (or equality filter + inequality/order on another field), synthesize the `Resource.Composites` entry (`[]string` of field names, with `"-field"` for descending order) and `Resource.CompositeSources` (`"file.cf:line"`), sorted identically to `tako/pkg/analyzer/analyzer.go` (`TestEachCompositeKeepsItsOwnSource`).

#### Pass 5: Service Grouping & Deterministic Sorting
Apply the exact service-partitioning rules from `tako/pkg/analyzer/analyzer.go`:
- Every package with at least one `api`, `page`, `cron`, or `site` produces an app `Service` (named from `tako.service(...)` if declared, else the package name, or module name for root package).
- Every `worker` or `subscribe` in a package *without* synchronous routes produces a dedicated async `Service` (`<pkg>-<handler>`), or attaches to the package's service according to `tako`'s service grouping rules.
- Every `job` produces a `Service` with `Job: true`.
- Every `singleton` produces a `Service` with `Singleton: true`.
- Sort `Services`, `Resources`, `Handlers`, and `Edges` deterministically so `AppGraph` JSON output is byte-identical across runs.

---

## 5. Pillar 4: Code Generation, Dev Loop, Test Harness & Container Build

### 5.1 `.tako/gen/<service>/main.cf` Code Generation (`tako/pkg/codegen/cfire.go`)

When `codegen.Generate(g, dir)` runs on a `cfire` project (`analyzer.IsCfireProject(dir)` is `true`), it delegates to `GenerateCfire(g *analyzer.AppGraph, dir string) ([]string, error)` in `tako/pkg/codegen/cfire.go`.

For each service `<svc>`, `GenerateCfire` writes `.tako/gen/<svc>/main.cf`:

```cfire
// Code generated by tako. DO NOT EDIT.
//
// Service shortlink, generated from the app's declarations. This file is meant
// to be read; to change it, change the app (or the generator), never this output.
package main

import "core:tako"
import "core:json"
import shortlink "../.."

fn main() -> i32 {
    tako.boot()

    // Register entity schemas for local bbolt index enforcement
    tako.register_entity("links", "code", "clicks,created_at,owner_id", "[]")

    // POST /links -> shortlink.CreateLink
    tako.mount_json("POST /links", |req: tako.HttpReq| -> tako.HttpRes! {
        decoded := try decode_CreateLink(req)
        ctx := tako.ctx_from_req(req)
        res := try shortlink.create_link(ctx, decoded)
        return tako.json_res(200, json.marshal(res))
    }, tako.MountOpts{require_auth: true})

    // GET /links/{code} -> shortlink.GetLink
    tako.mount_json("GET /links/{code}", |req: tako.HttpReq| -> tako.HttpRes! {
        decoded := try decode_GetLink(req)
        ctx := tako.ctx_from_req(req)
        res := try shortlink.get_link(ctx, decoded)
        return tako.json_res(200, json.marshal(res))
    }, tako.MountOpts{})

    // worker shortlink.ProcessClick (queue "clicks")
    tako.register_worker("ProcessClick", "clicks", |ctx: tako.Ctx, payload: str| -> void! {
        msg := try json.unmarshal[shortlink.ClickEvent](payload)
        try shortlink.process_click(ctx, msg)
    })

    // cron shortlink.PruneExpired ("0 * * * *")
    tako.register_cron("PruneExpired", "0 * * * *", true, |ctx: tako.Ctx| -> void! {
        try shortlink.prune_expired(ctx)
    })

    return tako.serve(tako.ServeOpts{
        has_auth:       true,
        auth_providers: "google,email",
        has_bucket:      true,
        has_cron:       true,
    })
}

fn decode_CreateLink(req: tako.HttpReq) -> shortlink.CreateLinkReq! {
    mut out := try json.unmarshal[shortlink.CreateLinkReq](req.body)
    return out
}

fn decode_GetLink(req: tako.HttpReq) -> shortlink.GetLinkReq! {
    mut out := shortlink.GetLinkReq{}
    out.code = req.path_string("code")
    return out
}
```

### 5.2 Test Harness Generation & `tako test` (`cmd/tako/main.go` & `pkg/codegen/cfire.go`)

Just as Go apps get `.tako/gen/<svc>/harness/tako_harness_test.go`, `GenerateCfire` emits `.tako/gen/<svc>/harness/tako_harness.cf` which registers all routes, workers, subscribers, and entity schemas onto `tako.new_test_app()` via an `@init` hook (`tako.register_test_harness(...)`).

When the user runs `tako test` in a `cfire` project:
1. `tako` runs `analyzer.Analyze` + `codegen.Generate`.
2. `tako` ensures `.tako/lib/libtako.a` is built and up to date.
3. `tako` invokes `cfire test --include .tako/gen/<svc>/harness/tako_harness.cf --link-lib .tako/lib/libtako.a`.
4. Tests written in `*_test.cf` call `app := tako.new_test_app()` and `res := app.request("POST", "/links", ...)`, executing against in-memory `bbolt` via `libtako.a`.

### 5.3 Dev Loop (`tako/pkg/devserver/devserver.go`)

In `pkg/devserver/devserver.go`:
1. `watch(dir)` already watches all non-hidden directories recursively; ensure `.cf` file modifications trigger `s.cycle(false)` and `.cfire/` (like `.tako/`) is ignored in `hiddenPath` so `cfire`'s cache writes do not re-trigger the watcher.
2. In `s.cycle(first)`:
   - When `analyzer.IsCfireProject(s.dir)` is `true`, replace:
     ```go
     exec.Command("go", "build", "-o", bin, "./"+filepath.Join(".tako", "gen", svc.Name))
     ```
     with `buildCfireLocal(s.dir, svc.Name, bin)` which:
     1. Ensures `.tako/lib/libtako.a` exists (built once and cached under `.tako/lib/`).
     2. Invokes `cfire build .tako/gen/<svc.Name>/main.cf -o <bin> --link-lib .tako/lib/libtako.a`.
   - Everything else in `devserver` (stdin leash `guard`, `TAKO_DEV_NONCE` readiness probe, `TAKO_DATA_DIR=.tako/data`, `TAKO_CHAOS_SEED`, hot swap on port `8080`) works **without modification** because `tako_c_serve` in `libtako.a` runs the exact same `pkg/runtime.Serve` loop!

### 5.4 Container Image Build (`tako/pkg/build/build.go`)

In `pkg/build/build.go`:
1. Add `const DefaultCfireBase = "gcr.io/distroless/cc-debian12:nonroot"` (provides `glibc` + `libstdc++` + `libgcc_s` needed by C++23 binaries while remaining distroless and `nonroot`).
2. In `compile(dir, service)`:
   - Detect if `.tako/gen/<service>/main.cf` exists.
   - If so:
     1. Ensure `.tako/lib/libtako-linux-amd64.a` is built (`GOOS=linux GOARCH=amd64 CGO_ENABLED=1`).
     2. Invoke `cfire build .tako/gen/<service>/main.cf -o .tako/bin/<service>-linux-amd64 --target linux-amd64 --link-lib .tako/lib/libtako-linux-amd64.a`.
   - Select `DefaultCfireBase` when `opts.Base == ""` for `cfire` services.
   - `assemble()` and `binaryLayer()` remain 100% unchanged.

---

## 6. Step-by-Step Agent Execution Plan (Vertical Slices `CF0.1` – `CF5.2`)

> **Instructions for the Executing Coding Agent:**
> - Execute slices in strict sequential order: `CF0.1` $\to$ `CF0.2` $\to$ `CF1.1` $\to$ `CF1.2` $\to$ `CF1.3` $\to$ `CF2.1` $\to$ `CF2.2` $\to$ `CF3.1` $\to$ `CF3.2` $\to$ `CF4.1` $\to$ `CF5.1` $\to$ `CF5.2`.
> - Every slice ends with a **Verification Gate**. Do NOT proceed to the next slice until all commands in the current slice's Verification Gate exit `0`.
> - Never weaken or delete an existing test in `/Users/memo/Programming/tako` or `/Users/memo/Programming/cfire`.

---

### Milestone CF0: `libtako` C-Archive & `core:tako` Runtime Foundation

#### Slice `CF0.1` — Create `cmd/libtako` C-Archive in `tako`

- **Repository:** `/Users/memo/Programming/tako`
- **Goal:** Expose `tako/pkg/runtime` and `tako/pkg/backends` over the C ABI defined in Section 3.3 and verify it compiles to `libtako.a` + `libtako.h` and passes round-trip CGO tests.
- **Files to Create / Modify:**
  1. **Create** `/Users/memo/Programming/tako/cmd/libtako/main.go`:
     - Package `main` with `import "C"`.
     - Implement context handle registry (`var ctxMap sync.Map`, `var nextCtxID atomic.Uint64`).
     - Implement `tako_c_boot`, `tako_c_free`, `tako_c_mount_json`, `tako_c_mount_blob`, `tako_c_mount_page`, `tako_c_register_worker`, `tako_c_register_subscriber`, `tako_c_register_cron`, `tako_c_register_job`, `tako_c_register_singleton`, `tako_c_serve`.
     - Implement `tako_c_entity_register`, `tako_c_entity_get`, `tako_c_entity_put`, `tako_c_entity_delete`, `tako_c_entity_query`, `tako_c_entity_tx`, `tako_c_tx_get`, `tako_c_tx_put`, `tako_c_tx_delete` delegating to `runtime.EntityStore()`.
     - Implement `tako_c_bucket_put`, `tako_c_bucket_get`, `tako_c_bucket_delete`, `tako_c_bucket_signed_url` delegating to `runtime.BucketStore()`.
     - Implement `tako_c_queue_publish`, `tako_c_topic_publish`, `tako_c_cache_get`, `tako_c_cache_set`, `tako_c_cache_delete`, `tako_c_lock_with_lease`, `tako_c_secret_get`, `tako_c_metric_inc`, `tako_c_metric_observe`.
     - Implement `tako_c_testapp_new`, `tako_c_testapp_close`, `tako_c_testapp_request`, `tako_c_testapp_drain`.
  2. **Create** `/Users/memo/Programming/tako/cmd/libtako/libtako_test.go`:
     - Unit test that boots a `TestApp` through the `tako_c_*` functions, registers an entity `"links"`, puts a JSON document, queries it with a filter, runs a transaction (`tako_c_entity_tx`), publishes to a queue, drains async workers, and asserts 0 memory leaks / 100% expected responses.
  3. **Create** `/Users/memo/Programming/tako/pkg/build/libtako.go`:
     - Helper `EnsureLibTako(dir string, goos, goarch string) (archivePath string, headerDir string, err error)` that builds `.tako/lib/libtako-<goos>-<goarch>.a` when missing or stale relative to the `tako` binary timestamp.
- **Verification Gate (`CF0.1`):**
  ```bash
  cd /Users/memo/Programming/tako && go test -v ./cmd/libtako/...
  cd /Users/memo/Programming/tako && go build -buildmode=c-archive -o /tmp/libtako_test.a ./cmd/libtako && rm -f /tmp/libtako_test.a /tmp/libtako_test.h
  ```

---

#### Slice `CF0.2` — Add `core:tako` Normative SDK & Link Flags to `cfire`

- **Repository:** `/Users/memo/Programming/cfire`
- **Goal:** Implement `import "core:tako"` in `cfire` (`runtime/core/tako.hpp`), register it in `sema.hpp`, `codegen.hpp`, and `docgen/catalog.hpp`, and support `--link-lib <path.a>` and `--include <file.cf>` on `cfire build` / `cfire test`.
- **Files to Create / Modify:**
  1. **Create** `/Users/memo/Programming/cfire/runtime/core/tako.hpp`:
     - Declare `extern "C"` prototypes matching `libtako.h` (Section 3.3) guarded by `#ifdef CFIRE_HAS_LIBTAKO`, with a self-contained in-memory fallback stub when `CFIRE_HAS_LIBTAKO` is not defined (so `cfire check` and standalone compilation without `libtako.a` still type-check and compile cleanly).
     - Implement `namespace cfire::core::tako` containing C++23 classes/functions for:
       - `Ctx`, `Empty`, `Blob`, `HTML`, `User`, `BucketEvent`, `HttpReq`, `HttpRes`
       - `ServiceOpts`, `RouteOpts`, `CronOpts`, `DeliveryOpts`, `SiteOpts`, `MountOpts`, `ServeOpts`
       - `Entity<T>`, `Tx<T>`, `Query<T>`, `Page<T>` (using `cfire::core::json::marshal` and `unmarshal<T>`)
       - `Bucket`, `Queue<T>`, `Topic<T>`, `Cache<T>`, `Lock`, `Secret`, `Counter`, `Histogram`, `Role`, `Policy`
       - `Endpoint<Req, Res>`, `Job<Param>`, `Site`, `TestApp`, `TestResponse`
       - Top-level constructors: `api`, `page`, `worker`, `subscribe`, `subscribe_bucket`, `cron`, `job`, `singleton`, `entity`, `bucket`, `queue`, `topic`, `cache`, `lock`, `secret`, `auth`, `role`, `require_role`, `site`, `service`, `counter`, `histogram`, `boot`, `serve`, `mount_json`, `mount_blob`, `mount_page`, `register_worker`, `register_subscriber`, `register_cron`, `register_entity`, `new_test_app`, `bad_request`, `unauthorized`, `forbidden`, `not_found`, `conflict`.
  2. **Modify** `/Users/memo/Programming/cfire/runtime/cfire_core_stdlib.hpp`:
     - Include `"core/tako.hpp"`.
  3. **Modify** `/Users/memo/Programming/cfire/src/sema.hpp` & `/Users/memo/Programming/cfire/src/codegen.hpp`:
     - Register `"core:tako"` in the known core module table so `import "core:tako"` resolves namespace `tako` to `cfire::core::tako`.
     - Map generic constructors `tako.entity[T](...)`, `tako.queue[T](...)`, `tako.topic[T](...)`, `tako.cache[T](...)` to `cfire::core::tako::entity<T>(...)`, etc.
  4. **Modify** `/Users/memo/Programming/cfire/src/main.cpp`:
     - Add CLI flags `--link-lib <path>` (appends `<path>` and `-lpthread -ldl -lm` to the C++ compiler invocation and defines `-DCFIRE_HAS_LIBTAKO=1`) and `--include <file.cf>` (includes an extra `.cf` file in the compilation unit, used by `tako test` harness).
  5. **Modify** `/Users/memo/Programming/cfire/src/docgen/catalog.hpp`:
     - Add `"core:tako"` module documentation entry and regenerate `docs/STDLIB_REFERENCE.md` via `./build/cfire doc --stdlib > docs/STDLIB_REFERENCE.md`.
- **Verification Gate (`CF0.2`):**
  ```bash
  cd /Users/memo/Programming/cfire && make test && make review-check
  ```

---

### Milestone CF1: Static Analyzer (`cfire outline --tako-graph` & `tako/pkg/analyzer`)

#### Slice `CF1.1` — Implement `cfire outline --tako-graph` in `cfire`

- **Repository:** `/Users/memo/Programming/cfire`
- **Goal:** Implement the five-pass Tako static analyzer in `cfire/src/tako_analyzer.hpp` and expose `cfire outline --tako-graph <dir>`.
- **Files to Create / Modify:**
  1. **Create** `/Users/memo/Programming/cfire/src/tako_analyzer.hpp`:
     - Implement Pass 1 through Pass 5 exactly as specified in Section 4.2.
     - Enforce rule IDs:
       - `tako/decl-in-func`
       - `tako/non-const-name`
       - `tako/bad-handler-sig`
       - `tako/route-param-mismatch`
       - `tako/entity-missing-id`
       - `tako/entity-bad-id`
       - `tako/unindexed-query`
       - `tako/duplicate-worker`
       - `tako/forbidden-call`
       - `tako/mixed-profile`
       - `tako/mixed-footprint`
     - Serialize the resulting `AppGraph` JSON to `stdout` matching `pkg/analyzer/appgraph.go` field names and `omitempty` semantics.
     - When rule violations occur, print each diagnostic in `rel/path.cf:line:col: message (tako/rule-id)` format to `stderr` and exit `1`.
  2. **Modify** `/Users/memo/Programming/cfire/src/main.cpp`:
     - Wire `--tako-graph` into `cmd_outline(argc, argv)`.
  3. **Create** `/Users/memo/Programming/cfire/tests/tako_analyzer_test.sh` (or integrate into `tests/run_tests.sh`):
     - Test valid `.cf` apps (`hello`, `shortlink`) emit valid `AppGraph` JSON with expected `services`, `resources`, `handlers`, `edges`, `indexes`, and `composites`.
     - Test invalid `.cf` apps trigger each `tako/*` rule ID with exact `file:line:col` formatting.
- **Verification Gate (`CF1.1`):**
  ```bash
  cd /Users/memo/Programming/cfire && make test
  ```

---

#### Slice `CF1.2` — Wire `tako/pkg/analyzer/cfire.go` into `tako`

- **Repository:** `/Users/memo/Programming/tako`
- **Goal:** Teach `tako/pkg/analyzer` to automatically detect `cfire` projects, invoke `cfire outline --tako-graph`, and return `*analyzer.AppGraph` and `analyzer.ErrorList`.
- **Files to Create / Modify:**
  1. **Create** `/Users/memo/Programming/tako/pkg/analyzer/cfire.go`:
     - `func IsCfireProject(dir string) bool`: returns `true` when `dir` has `.cf` files (outside `.tako/` and `.cfire/`) and no `go.mod`.
     - `func analyzeCfire(cfg Config) (*TypedGraph, error)`:
       - Resolves `cfire` binary via `os.Getenv("CFIRE_BIN")`, `exec.LookPath("cfire")`, or `/Users/memo/Programming/cfire/build/cfire`.
       - Runs `cfire outline --tako-graph <cfg.Dir>`.
       - If exit code is `1`, parses `stderr` lines matching `^([^:]+:\d+:\d+): (.+) \(tako/([a-z0-9-]+)\)$` into `analyzer.ErrorList` and returns `nil, errs`.
       - If exit code is `0`, unmarshals `stdout` JSON into `*AppGraph` and returns `&TypedGraph{Graph: graph, Dir: cfg.Dir}, nil`.
  2. **Modify** `/Users/memo/Programming/tako/pkg/analyzer/analyzer.go`:
     - At the top of `AnalyzeTyped(cfg Config)`:
       ```go
       if IsCfireProject(cfg.Dir) {
           return analyzeCfire(cfg)
       }
       ```
  3. **Create** `/Users/memo/Programming/tako/pkg/analyzer/cfire_test.go`:
     - Golden tests verifying `analyzer.Analyze` on `testdata/cfire_hello`, `testdata/cfire_shortlink`, and negative rule fixtures (`tako/non-const-name`, `tako/unindexed-query`, `tako/route-param-mismatch`).
     - Verify `analyzer.Mermaid(graph)` and `gcp.Plan(graph, ...)` work unmodified on the `AppGraph` produced from `.cf` sources!
- **Verification Gate (`CF1.2`):**
  ```bash
  cd /Users/memo/Programming/tako && go test -v ./pkg/analyzer/...
  ```

---

### Milestone CF2: Code Generation, `tako dev`, and `tako test`

#### Slice `CF2.1` — Implement `.tako/gen/<service>/main.cf` & Harness Generator in `tako/pkg/codegen`

- **Repository:** `/Users/memo/Programming/tako`
- **Goal:** Generate human-readable `.tako/gen/<service>/main.cf` and `.tako/gen/<service>/harness/tako_harness.cf` from `*analyzer.AppGraph`.
- **Files to Create / Modify:**
  1. **Create** `/Users/memo/Programming/tako/pkg/codegen/cfire.go`:
     - `func GenerateCfire(g *analyzer.AppGraph, dir string) ([]string, error)`
     - Emit `.tako/gen/<svc>/main.cf` for HTTP services, async worker/subscriber services, jobs, and singletons following the template in Section 5.1.
     - Emit request decoder functions `decode_<HandlerVar>(req: tako.HttpReq) -> <Pkg>.<ReqType>!` handling `HasBody` (`json.unmarshal`) and `Bindings` (`req.path_string`, `req.path_int64`, `req.query_string`, `req.query_int`, `req.form_string`, `req.form_blob`).
     - Emit `.tako/gen/<svc>/harness/tako_harness.cf` for `tako test`.
  2. **Modify** `/Users/memo/Programming/tako/pkg/codegen/codegen.go`:
     - In `Generate(g *analyzer.AppGraph, dir string)`:
       ```go
       if analyzer.IsCfireProject(dir) {
           return GenerateCfire(g, dir)
       }
       ```
     - In `Mains(written []string)`: include both `"main.go"` and `"main.cf"`.
  3. **Create** `/Users/memo/Programming/tako/pkg/codegen/cfire_test.go`:
     - Test that `codegen.Generate` on `cfire` fixtures emits valid `.cf` code that passes `cfire check` and `cfire fmt --check`.
- **Verification Gate (`CF2.1`):**
  ```bash
  cd /Users/memo/Programming/tako && go test -v ./pkg/codegen/...
  ```

---

#### Slice `CF2.2` — Wire `tako dev` and `tako test` for `cfire` Apps

- **Repository:** `/Users/memo/Programming/tako`
- **Goal:** Enable `tako dev` (live reload, `.tako/data` local `bbolt`, dev bar, `--chaos`) and `tako test` (in-memory `TestApp` harness) on `cfire` projects.
- **Files to Create / Modify:**
  1. **Modify** `/Users/memo/Programming/tako/pkg/devserver/devserver.go`:
     - In `s.cycle(first)`: if `analyzer.IsCfireProject(s.dir)`, call `build.EnsureLibTako(s.dir, runtime.GOOS, runtime.GOARCH)` and compile `.tako/gen/<svc.Name>/main.cf` to `.tako/bin/<svc.Name>` using `cfire build`.
     - Ensure `hiddenPath` ignores `.cfire/` cache directory events so `cfire build` does not retrigger `fsnotify`.
  2. **Modify** `/Users/memo/Programming/tako/cmd/tako/main.go` (around `cmdTest`):
     - When `analyzer.IsCfireProject(dir)` is `true`:
       - Run `analyzer.Analyze` and `codegen.Generate`.
       - Call `build.EnsureLibTako(dir, runtime.GOOS, runtime.GOARCH)`.
       - Execute `cfire test` passing `--include .tako/gen/<svc>/harness/tako_harness.cf --link-lib <libtako.a>`.
  3. **Create** `/Users/memo/Programming/tako/pkg/devserver/cfire_dev_test.go`:
     - Integration test that boots a `cfire` app under `devserver.Run`, performs HTTP requests, modifies the `.cf` file, waits for hot-reload, and verifies the updated response and persisted `.tako/data` state.
- **Verification Gate (`CF2.2`):**
  ```bash
  cd /Users/memo/Programming/tako && go test -v ./pkg/devserver/...
  ```

---

### Milestone CF3: Container Image Build (`pkg/build`) & Cloud Provisioning

#### Slice `CF3.1` — Linux Cross-Compilation & Distroless Image Assembly for `cfire`

- **Repository:** `/Users/memo/Programming/cfire` & `/Users/memo/Programming/tako`
- **Goal:** Enable `pkg/build.Service` to build deterministic `linux/amd64` container images for `cfire` services layered on `gcr.io/distroless/cc-debian12:nonroot`.
- **Files to Create / Modify:**
  1. **Modify** `/Users/memo/Programming/cfire/src/main.cpp`:
     - Add `--target <triple>` flag to `cfire build` (e.g., `--target linux-amd64`), which selects `x86_64-linux-gnu-g++` or `zig c++ -target x86_64-linux-gnu` (configurable via `CFIRE_CXX_LINUX_AMD64`) and passes `-static-libstdc++ -static-libgcc`.
  2. **Modify** `/Users/memo/Programming/tako/pkg/build/build.go`:
     - Add `const DefaultCfireBase = "gcr.io/distroless/cc-debian12:nonroot"`.
     - In `Service(opts Options, service string)`: detect if `.tako/gen/<service>/main.cf` exists; if so, use `compileCfire(opts.Dir, service)` and default base `DefaultCfireBase`.
  3. **Create** `/Users/memo/Programming/tako/pkg/build/cfire_build_test.go`:
     - Verify OCI image assembly, layer structure (`/app/<service>`), deterministic `sha256` digest across repeated builds, and labels (`tako.app`, `tako.service`).
- **Verification Gate (`CF3.1`):**
  ```bash
  cd /Users/memo/Programming/tako && go test -v ./pkg/build/...
  ```

---

### Milestone CF4: Reference Example Graduation (`hello`, `shortlink`, `gallery`)

#### Slice `CF4.1` — Author & Graduate `examples/cfire-hello`, `examples/cfire-shortlink`, and `examples/cfire-gallery`

- **Repository:** `/Users/memo/Programming/tako`
- **Goal:** Prove full framework parity by building and testing `cfire` ports of `tako`'s flagship reference applications:
  1. `/Users/memo/Programming/tako/examples/cfire-hello/` (`hello.cf`, `hello_test.cf`, `build.cf`) — stateless API route + path/query bindings.
  2. `/Users/memo/Programming/tako/examples/cfire-shortlink/` (`shortlink.cf`, `shortlink_test.cf`, `build.cf`) — `Entity[Link]`, `Cache[str]`, `Queue[ClickEvent]`, `Worker`, `Cron`, transactional click counter (`Entity.tx`), and indexed queries.
  3. `/Users/memo/Programming/tako/examples/cfire-gallery/` (`gallery.cf`, `gallery_test.cf`, `build.cf`) — `Bucket` (`signed_put`, `signed_get`), `subscribe_bucket` event handler, `Topic`, `Auth` (`SignIn`, `RequireRole`), and `Counter`/`Histogram` metrics.
- **Verification Gate (`CF4.1`):**
  ```bash
  cd /Users/memo/Programming/tako && go run ./cmd/tako analyze examples/cfire-hello
  cd /Users/memo/Programming/tako && go run ./cmd/tako analyze examples/cfire-shortlink
  cd /Users/memo/Programming/tako && go run ./cmd/tako analyze examples/cfire-gallery
  cd /Users/memo/Programming/tako && go run ./cmd/tako test examples/cfire-hello
  cd /Users/memo/Programming/tako && go run ./cmd/tako test examples/cfire-shortlink
  cd /Users/memo/Programming/tako && go run ./cmd/tako test examples/cfire-gallery
  ```

---

### Milestone CF5: Documentation, Validation Scripts & Conformance Suite

#### Slice `CF5.1` — Validation Playbook (`validate/cfire.md`) & Full Conformance

- **Repository:** `/Users/memo/Programming/tako` & `/Users/memo/Programming/cfire`
- **Goal:** Add an end-to-end human/agent validation playbook `validate/cfire.md` (matching `validate/m1-hello.md` .. `validate/m5-polish.md`) and ensure both repositories pass 100% of their test suites.
- **Files to Create / Modify:**
  1. **Create** `/Users/memo/Programming/tako/validate/cfire.md`:
     - Step-by-step reproducible commands verifying `tako analyze`, `tako graph --mermaid`, `tako test`, and `tako dev` on `examples/cfire-hello`, `examples/cfire-shortlink`, and `examples/cfire-gallery`.
  2. **Modify** `/Users/memo/Programming/cfire/docs/GUIDE.md`:
     - Add a chapter section on building cloud-native applications with `import "core:tako"` and the `tako` CLI.
- **Verification Gate (`CF5.1`):**
  ```bash
  cd /Users/memo/Programming/cfire && make review-check
  cd /Users/memo/Programming/tako && go test ./...
  ```

---

## 7. Quick Reference Checklist for Agent Execution

| Slice | Repo | Primary Deliverable | Gate Command |
|---|---|---|---|
| `CF0.1` | `tako` | `cmd/libtako/main.go` + `pkg/build/libtako.go` | `go test ./cmd/libtako/...` |
| `CF0.2` | `cfire` | `runtime/core/tako.hpp` + `--link-lib` + `--include` | `make test && make review-check` |
| `CF1.1` | `cfire` | `src/tako_analyzer.hpp` (`cfire outline --tako-graph`) | `make test` |
| `CF1.2` | `tako` | `pkg/analyzer/cfire.go` + golden tests | `go test ./pkg/analyzer/...` |
| `CF2.1` | `tako` | `pkg/codegen/cfire.go` (`main.cf` + `tako_harness.cf`) | `go test ./pkg/codegen/...` |
| `CF2.2` | `tako` | `pkg/devserver` + `cmd/tako` (`tako dev` & `tako test`) | `go test ./pkg/devserver/...` |
| `CF3.1` | `both` | `pkg/build` Linux cross-compile + `cc-debian12:nonroot` | `go test ./pkg/build/...` |
| `CF4.1` | `tako` | `examples/cfire-{hello,shortlink,gallery}` | `tako analyze` & `tako test` on all 3 |
| `CF5.1` | `both` | `validate/cfire.md` + `cfire/docs/GUIDE.md` | `go test ./...` & `make review-check` |
