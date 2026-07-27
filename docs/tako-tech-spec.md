# Tako — Tech Spec: A High-Level Framework over Google Cloud Run

Name: **Tako**. CLI: `tako`. Module: `github.com/gsandoval/tako`. Handlers and resources are both declared as code (no comment directives).
Users write pure business logic in one Go package; `tako dev` runs it locally, `tako deploy` provisions and ships it.
Companion document: `tako-implementation-plan.md` (phased build order — local development and testing first, cloud via example graduation).

---

## Part 1 — Interface Design Principles

Every part of the SDK follows six rules. When adding a primitive later, it must fit all six or it doesn't ship.

1. **Everything is a declared noun.** Infrastructure *and* behavior are package-level vars via constructors: resources (`tako.Bucket("photos")`) and handlers (`var MakeThumb = tako.Worker(thumbs, makeThumb)`). Binding is a compile-checked constructor argument, never a comment or a naming convention — payload mismatches are compile errors with autocomplete, not analysis errors. Nothing is ever configured in YAML that could be declared in code. Corollary: constructors are side-effect-free records (they register intent, never touch I/O), so package initialization order can never matter observably.

2. **One constructor shape.** `tako.Kind("name", ...Option)` or `tako.Kind[T]("name", ...Option)`. The name is the resource's stable identity and must be a string constant (analyzer-enforced). Options all live in one namespace: `tako.WithTTL(...)`, `tako.WithGoogle()`.

3. **One method shape.** Every operation takes `Ctx` first and returns `(T, error)` or `error`. Every synchronous handler is `func(Ctx, Req) (Res, error)` where `Req` is a struct (possibly empty) whose fields carry `path:`/`query:`/`form:` tags or bind from the body. No naked `context.Context`, no variadic surprises, no method that can't be cancelled or traced.

4. **One verb vocabulary.** Keyed stores — Bucket, Entity, Cache — share `Get / Put / Delete / Exists`. Message carriers — Queue, Topic — share `Publish`. Learning one primitive teaches the others.

5. **One async handler shape.** Every asynchronous handler — worker, subscriber, job — is `func(Ctx, T) error`. Return `nil` to ack, an error to retry, `tako.Permanent(err)` to give up and dead-letter. One retry mental model everywhere.

6. **No primitive owns a general concept.** The entity store is *an* entity store, not "the database": there is no `tako.DB`, no global `tako.Tx`, no top-level `Query`. Transactions, queries, and migrations are methods scoped to a store instance, so `tako.Postgres("main")` can arrive later with its own idioms and zero breaking changes. (Details in Part 5.)

---

## Part 2 — The SDK Surface (v0/v1)

### 2.1 Resource catalog

```go
var (
    // Storage & data
    photos = tako.Bucket("photos")
    photoStore = tako.Entity[Photo]("photos")          // typed entity store (Datastore)
    feed   = tako.Cache[FeedPage]("feed", tako.WithTTL(time.Minute))

    // Messaging
    thumbs = tako.Queue[ThumbJob]("thumbnails")     // work queue: competing consumers, ONE worker
    ready  = tako.Topic[PhotoReady]("photo-ready")  // event topic: fan-out, MANY subscribers

    // Coordination & config
    relock = tako.Lock("reindex")
    stripe = tako.Secret("stripe-key")

    // Web
    auth   = tako.Auth(tako.WithGoogle(), tako.WithEmail())
    site   = tako.Static("./web", tako.SPA())
    tmpl   = tako.Templates("./templates")

    // Long-running compute
    reidx  = tako.Job[ReindexParams]("reindex")

    // Observability
    ups    = tako.Counter("uploads")
    lat    = tako.Histogram("thumb_ms")
)
```

The variable names (`photos`, `photoStore`, `thumbs`, ...) are entirely the user's choice — the framework identity is the constant string passed to the constructor, and handler constructors bind by taking the resource var as an argument (`tako.Worker(thumbs, makeThumb)`). Nothing in the SDK reserves or interprets variable names.

### 2.2 Bucket

```go
photos.Get(ctx, key)            (tako.Blob, error)
photos.Put(ctx, key, blob)      error
photos.Delete(ctx, key)         error
photos.Exists(ctx, key)         (bool, error)
photos.List(ctx, prefix)        tako.Iter[tako.ObjectInfo]

// Direct client transfer — bypasses Cloud Run's 32MB request cap:
photos.SignedPut(ctx, key, 15*time.Minute, tako.WithMaxSize(20<<20), tako.WithContentType("image/*")) (string, error)
photos.SignedGet(ctx, key, 15*time.Minute) (string, error)

// Event source, usable as a Subscribe binding:
//   var OnUploaded = tako.Subscribe(photos.Uploaded, onUploaded)
// handler: func(Ctx, tako.ObjectInfo) error
```

`Blob` wraps a reader + content type + size; small blobs can be received/returned directly by `api` handlers, large ones go through signed URLs.

### 2.3 Entity — *an* entity store, not *the* database

```go
photoStore.Get(ctx, id)               (Photo, error)    // tako.IsNotFound(err)
photoStore.Put(ctx, photo)            error
photoStore.Delete(ctx, id)            error
photoStore.Exists(ctx, id)            (bool, error)

photoStore.Query(ctx).
     Where("Owner", "=", uid).
     Where("Ready", "=", true).
     OrderBy("-Created").
     Limit(100).All()           ([]Photo, error)   // also .First(), .Iter()

// Read-modify-write, transactional:
photoStore.Update(ctx, id, func(p *Photo) { p.Ready = true }) error

// Multi-entity transaction — scoped to THIS store:
photoStore.Tx(ctx, func(tx tako.EntityTx[Photo]) error {
    a, _ := tx.Get(id1)
    b, _ := tx.Get(id2)
    a.Rank, b.Rank = b.Rank, a.Rank
    tx.Put(a); tx.Put(b)
    return nil
})
```

Schema comes from the struct: `entity:"id"` marks the key field, `entity:"index"` marks queryable fields (the analyzer generates Datastore composite indexes from actual `Query` call sites, so unindexed-field queries are *analysis-time* errors, not runtime 500s).

Deliberate constraints that keep the SQL door open:

- `Tx` lives on the store, not the framework — no promise of cross-store transactions that SQL couldn't honor.
- `Where` takes explicit `(field, op, value)` — no string query language that would collide with SQL.
- Struct-tag schema and index derivation are Entity-specific; the future Postgres primitive gets its own migration story rather than inheriting one that doesn't fit.

### 2.4 Queue vs. Topic — the one distinction users must learn

|                  | `Queue[T]`                         | `Topic[T]`                            |
|------------------|------------------------------------|---------------------------------------|
| Semantics        | work distribution                  | event broadcast                       |
| Handlers         | exactly **one** `tako.Worker`        | **any number** of `tako.Subscribe`      |
| Two handlers =   | analysis error                     | independent fan-out, independent retries |
| Extra powers     | delay, rate limiting               | external topics (later)               |

```go
thumbs.Publish(ctx, job) error
thumbs.Publish(ctx, job, tako.WithDelay(4*time.Hour)) error   // provisions Cloud Tasks under the hood

ready.Publish(ctx, event) error
```

Queue options `WithDelay` / `WithRateLimit(n)` silently select Cloud Tasks instead of Pub/Sub as the backend — same user API, provisioner's choice.

### 2.5 Coordination

```go
relock.Do(ctx, func(ctx tako.Ctx) error { ... })    // blocks until held
held, err := relock.TryDo(ctx, fn)                        // false if someone else holds it
```

Most users never touch `Lock` directly — they use the constructors built on it (`tako.Singleton`, `tako.Cron(..., tako.WithSingleflight())`, §2.8).

### 2.6 Secret, Cache, Metrics

```go
stripe.Value() string                      // resolved once at startup; no ctx, cannot fail at request time

feed.Get(ctx, key)  (FeedPage, error)      // IsNotFound when absent/expired
feed.Put(ctx, key, page) error             // TTL from the declaration
feed.GetOrFill(ctx, key, func(ctx) (FeedPage, error)) (FeedPage, error)

ups.Add(ctx, 1)
lat.Observe(ctx, ms)
```

v0 `Cache` is per-instance in-memory (documented honestly — no cross-instance invalidation); a shared Redis backend becomes a `tako.WithShared()` opt-in later without changing call sites.

### 2.7 Jobs — long-running compute

```go
var reidx = tako.Job[ReindexParams]("reindex")

var Reindex = tako.JobHandler(reidx, reindex, tako.WithParallel(10))

func reindex(ctx tako.Ctx, p ReindexParams) error { ... }   // same async shape as everything else

// trigger from any handler:
reidx.Run(ctx, ReindexParams{Since: t})
```

Runs as a Cloud Run **Job** — no request timeout, `tako.WithParallel(n)` maps to task-count fan-out.

### 2.8 Handler constructors — the complete set

Handlers are declared exactly like resources: a package-level var, a constructor, options. The handler body is an ordinary (usually unexported) function; the var is the registration, the documentation, and — for `API`/`Page` — the callable endpoint.

| Constructor | Handler shape | Returns | Runs as |
|---|---|---|---|
| `tako.API(route, fn, ...opts)` | `func(Ctx, Req) (Res, error)` | `Endpoint[Req, Res]` | app service |
| `tako.Page(route, fn, ...opts)` | `func(Ctx, Req) (HTML, error)` | `Endpoint[Req, HTML]` | app service |
| `tako.Worker(queue, fn, ...opts)` | `func(Ctx, T) error` (T from `Queue[T]`) | handler ref | worker service |
| `tako.Subscribe(topic, fn, ...opts)` | `func(Ctx, T) error` (T from `Topic[T]`) | handler ref | subscriber service |
| `tako.Subscribe(bucket.Uploaded, fn)` | `func(Ctx, ObjectInfo) error` | handler ref | subscriber service |
| `tako.JobHandler(job, fn, ...opts)` | `func(Ctx, T) error` (T from `Job[T]`) | handler ref | Cloud Run Job |
| `tako.Cron(schedule, fn, ...opts)` | `func(Ctx) error` | handler ref | app service |
| `tako.Singleton(name, fn)` | `func(Ctx) error` (long loop) | handler ref | min=1 service + lease |

Handler options share the resource-option namespace: `tako.WithAuth()`, `tako.WithProfile(tako.Batch)`, `tako.WithPrivate()`, `tako.WithSingleflight()`, `tako.WithParallel(10)`.

**Request structs.** Every `API`/`Page` handler takes a `Req` struct (empty is fine). Fields bind by tag: `path:"id"` from the route pattern, `query:"limit"` from the query string, `form:"caption"` from form posts; untagged fields bind from the JSON body (multipart when a `tako.Blob` field is present). Binding code is generated statically — no reflection at request time.

```go
type UploadReq struct {
    Caption string    `form:"caption"`
    Img     tako.Blob `form:"img"`
}
var Upload = tako.API("POST /api/photos", upload, tako.WithAuth())
func upload(ctx tako.Ctx, req UploadReq) (Photo, error) { ... }
```

**What the compiler now checks vs. what the analyzer still checks.** Payload types are compile-time: `tako.Worker(thumbs, fn)` requires `fn` to match the queue's `T` or it doesn't build, with autocomplete showing the expected shape. The analyzer retains what the type system can't express: constructor arguments (routes, schedules, names) must be constants; route path params must have matching `path:`-tagged `Req` fields; at most one `Worker` per queue; option validity per constructor; handler vars must be package-level.

Uniformity to notice: the three payload-carrying async kinds (worker, subscribe, job) share one signature, one retry model, and one binding pattern — the bound resource is the constructor's first argument. `cron` and `singleton` carry no payload, so their constructors take a schedule/name instead. `WithSingleflight` and `Singleton` are the user-facing faces of the internal lease (§4.4) — raw leader election is intentionally not exposed.

### 2.9 Ctx, errors, auth

```go
ctx.User()      tako.User    // guaranteed on WithAuth() routes
ctx.MaybeUser() (tako.User, bool)
ctx.Log().Info("resized", "photo", id)     // structured, trace-correlated

tako.IsNotFound(err)     // Entity.Get, Cache.Get, Bucket.Get
tako.NotFound()          // from a page/api handler → 404
tako.Permanent(err)      // async handlers: don't retry, dead-letter
tako.Errorf(409, "...")  // api handlers: explicit status
```

Auth is one declaration (`tako.Auth(...)`) plus the `tako.WithAuth()` handler option. The framework injects a `/api/_session` endpoint that exchanges a Firebase ID token for an HttpOnly session cookie, so browser fetches and SSR pages share one validation path in the shim.

### 2.10 Testing

```go
func TestUpload(t *testing.T) {
    app := tako.NewTestApp(t)                    // all backends in-memory
    u := app.SignIn("alice")
    p, err := Upload.Call(app.Ctx(u), UploadReq{Caption: "cat", Img: testBlob()})
    app.DrainQueues()                                 // runs workers synchronously
    got, _ := photoStore.Get(app.Ctx(u), p.ID)
    if !got.Ready { t.Fatal("thumbnail never marked ready") }
}
```

`Endpoint.Call` in tests invokes the handler directly against the test backends (bypassing HTTP middleware, same as calling the function). The local backends double as the test harness — handler-to-worker flows are testable in-process with breakpoints, which hand-rolled Cloud Run code can never offer.

### 2.11 Websites — one primitive, not three bolted parts

`Static` + `Templates` + loose pages was assembly required. The polished primitive is `Site`, which owns the whole web-serving story:

```go
var site = tako.Site("./web",
    tako.WithBuild("npm run build", "dist"),      // run at deploy time, serve the output dir
    tako.SPA(),                                    // unknown paths → index.html (omit for plain static)
    tako.WithDomain("gallery.example.com"),        // domain mapping + managed TLS
    tako.WithRedirect("/old-gallery", "/"),
    tako.WithCDN(),                                // opt-in: LB + Cloud CDN (deploy cost stated honestly)
)
```

What `Site` does without being asked:

- **Frontend builds are part of `tako deploy` and `tako dev`** — `WithBuild` runs the command with the app's env available, serves the output, and in dev mode re-runs it on file changes (or proxies to a dev server via `WithDevProxy("http://localhost:5173")` for Vite-style HMR). No separate frontend pipeline to wire.
- **Correct caching by default**: fingerprinted assets (`app.3f2a1c.js`) get `Cache-Control: immutable`, HTML gets `no-cache`, ETags everywhere. Nobody configures this by hand correctly.
- **Sensible security headers by default** (CSP in report-only to start, HSTS on custom domains), overridable per-site.
- **v0 serving embeds the site in the app service's binary** (Go `embed`) — one service, one URL, no LB; `WithCDN` upgrades to bucket + Cloud CDN + load balancer when scale justifies the provisioning cost and latency.

Server-rendered pages become part of the same story rather than a parallel one:

```go
var PhotoPage = tako.Page("GET /p/{id}", photoPage)

func photoPage(ctx tako.Ctx, req PhotoPageReq) (tako.HTML, error) {
    p, err := photoStore.Get(ctx, req.ID)
    if tako.IsNotFound(err) { return tako.HTML{}, tako.NotFound() }
    return site.Render(ctx, "photo.html", p)            // templates live in ./web/templates, layout support built in
}
type PhotoPageReq struct{ ID string `path:"id"` }

var UpdateCaption = tako.Page("POST /p/{id}/caption", updateCaption, tako.WithAuth())

func updateCaption(ctx tako.Ctx, req CaptionReq) (tako.HTML, error) { ... }
type CaptionReq struct {
    ID      string `path:"id"`
    Caption string `form:"caption"`
}
```

`Page` POST handlers receive form fields as tagged `Req` struct fields, and — because session-cookie auth plus browser forms means CSRF is *your* problem, not the user's — the shim enforces CSRF tokens on all unsafe-method page routes automatically (token exposed as a template function and a meta tag for fetch callers). Returning `tako.Redirect("/p/"+id)` covers the POST-redirect-GET pattern. This makes htmx/Alpine-style server-driven apps a first-class path, not just SPAs.

**Hypermedia (htmx) as a first-class SSR pattern.** htmx needs almost nothing server-side, which is why Tako can support it completely: `site.Fragment(ctx, name, data)` renders a template without the layout; `ctx.IsHTMX()` lets one route serve full pages and fragments; `tako.Redirect(...)` automatically emits `HX-Redirect` instead of a 302 on htmx requests (same handler code for both worlds); and the built-in CSRF token is wired for the `hx-headers` meta-tag pattern so htmx POSTs pass CSRF with zero configuration. Combined with tagged-Req form binding, this makes server-driven interactive sites — the "website with an API" default case — a fully supported path with no SPA and no Node.

**Markdown, sanitized by default.** For static markdown content, use your generator via `WithBuild` — Tako does not rebuild Hugo. Tako's contribution is *dynamic* markdown: user- or admin-authored content from Entity or Bucket rendered at request time via a `{{ markdown .Body }}` template function and `site.Markdown(ctx, src)`. Rendering is goldmark; output is sanitized by default (bluemonday, strict policy) because user-supplied markdown is an XSS vector — trusted-content opt-out is explicit (`tako.WithTrustedHTML()`), never the default. Both are pure-Go dependencies; no infrastructure is involved.

Last piece: `tako gen client ts` emits a typed TypeScript client for every `api` endpoint (param/response types from the Go signatures, session auth pre-wired), so the frontend talking to the backend is generated, not hand-maintained.

**Generator compatibility and the rendering-model boundary.** `WithBuild` runs on the deploying machine (or CI) with whatever toolchain is installed there — consistent with the local-build philosophy — so any static-output generator works without tako knowing it exists: Hugo (`WithBuild("hugo --minify", "public")`), Astro/Vite/Next in static-export mode (`WithBuild("npm run build", "dist")`), Jekyll, Zola, plain files. In dev, generators with their own dev servers use `WithDevProxy` (e.g. `http://localhost:1313` for Hugo, `:4321` for Astro) instead of rebuild-on-change. Go-template SSR via `tako.Page` is the native dynamic path.

What is explicitly **not** supported in v0: server-rendering frameworks that require their own runtime — Astro SSR, Next.js, Nuxt, SvelteKit in server mode — because tako services are compiled Go binaries. The two supported rendering models are *static output from any generator* and *Go SSR*. The future answer is a bring-your-own-runtime primitive (sketch: `tako.App("./frontend", tako.WithRuntime("node"))`) deployed as a routed service with the same derived IAM and env injection — a deliberate later chapter (it reopens buildpacks and needs a non-Go resource-client story), documented so users with an Astro SSR app know the boundary up front rather than discovering it mid-migration.

### 2.12 Services and typed service-to-service calls

The unit of service partitioning is the **Go package**. One package = one Cloud Run service; a single-package app deploys exactly as before, and growing to multiple services is `mkdir`, not YAML:

```go
// billing/billing.go
package billing

// Exported Endpoint var = callable from other service packages.
var Charge = tako.API("POST /charge", charge, tako.WithPrivate())  // private: IAM-authenticated callers only

func charge(ctx tako.Ctx, req ChargeReq) (Receipt, error) { ... }
```

```go
// gallery/app.go
import "example.com/shop/billing"

var Checkout = tako.API("POST /api/checkout", checkout, tako.WithAuth())

func checkout(ctx tako.Ctx, cart Cart) (Order, error) {
    receipt, err := billing.Charge.Call(ctx, ChargeReq{...})   // typed, autocomplete on Req/Res
    ...
}
```

The `Endpoint[Req, Res]` value *is* the RPC surface, and `.Call` is deliberately one word louder than a plain function call — the hop is visible at the call site, which §"realities" below demands. There is **no call-site rewriting anywhere in codegen**: the endpoint value carries a transport that the generated boot hook wires per environment:

- **`tako dev` and tests**: direct in-process invocation — breakpoints step straight from `checkout` into `charge`.
- **Same service (co-located packages)**: direct in-process invocation, wired at boot.
- **Remote service**: an HTTP/JSON stub, authenticated with an OIDC ID token minted from the caller's service identity (metadata server, audience = callee URL, which Cloud Run validates natively — no shared secrets), with trace context, the deadline budget, and the caller's SESSION ID propagated automatically — the callee resolves it against the shared session store, so `ctx.User()` crosses the hop as the same verified, revocable identity, and a worker's anonymity stays honest. *(Fix-up, W9: an earlier draft said "optionally, off by default, a signed user-identity header" — that caution priced a crypto-signed header design; Datastore-backed sessions made verification a record lookup, so propagation is automatic and mints no new authority: presenting a session id is equivalent to presenting the session cookie.)*

Go's own visibility rules carry the access model: only **exported** `Endpoint` vars are callable from other service packages (unexported handlers stay service-internal; calling raw handler funcs across packages is an analysis error — it would hide the RPC boundary). `tako.WithPrivate()` is the orthogonal *ingress* option: no unauthenticated invocations from outside.

The derived-security story extends naturally: the analyzer finds `billing.Charge` references with the same usage scan that finds resource references, each edge grants the caller's SA `run.invoker` on the callee, and the deploy summary prints the service call graph. Remaining honesty rules: cross-service calls are never silently retried in v0 (a typed call must not invent duplicate charges — `tako.WithIdempotencyKey` later); call cycles between services need no analyzer rule — calling `b.Step` requires importing `b`, and package = service makes any service cycle an import cycle Go itself refuses *(fix-up, W9: previously "the analyzer rejects call cycles"; the compiler already does)*.

**What typed calls must not hide — Cloud Run realities.** The ergonomics above are real, but a cross-service call is a network hop with Cloud Run's specific costs, and the framework surfaces them rather than absorbing them:

- *Transport honesty.* `private` means IAM-only (`ingress=all`, no unauthenticated invocations, OIDC-validated at the Google front end) — **not** network-internal. True `ingress=internal` requires the caller to route via VPC (Direct VPC egress or a connector), the exact configuration tax tako avoids elsewhere; it becomes an explicit `WithVPC` opt-in later with its costs stated, like Redis and Cloud SQL.
- *Cold starts are loud, not silent.* A sync call edge into a scale-to-zero (`default`-profile) service triggers a deploy-time analyzer warning: give the callee `tako.WithProfile(tako.Latency)` (min-instances, with its cost) or make the interaction async. Same philosophy as the unindexed-query error — distributed-systems costs fail at analysis time, not in production.
- *Deadline budgets, not stacked timeouts.* Generated stubs propagate the caller's remaining deadline (minus a margin) with each call, so chains fail fast instead of multiplying timeouts. The analyzer additionally warns on sync chains deeper than one hop, since waiting callers hold their own request slots and couple the services' autoscaling.
- *Cost.* Each sync hop bills instance time on both sides simultaneously; documented next to the feature, not in a footnote.

The framework's stated opinion follows from these: **cross-service communication defaults to events** (`Topic`/`Queue`), which Cloud Run's scaling model and economics favor; synchronous calls are for genuinely request/response interactions, kept — with their dev-mode, breakpoint, and topology-merge benefits — but never presented as free.

Because topology is derived rather than declared, it can be *re*-declared: a future `topology:` block in `tako.yaml` can co-locate several packages into one Cloud Run service (the boot hook simply wires those endpoints in-process) without touching code — the monolith/microservice decision becomes deployment config, which is the endgame this architecture buys.

---

## Part 3 — First Example: the Gallery (async + storage)

```
gallery/
├── go.mod
├── app.go
├── templates/photo.html
├── web/            # index.html, app.js (Firebase Auth widget)
└── tako.yaml         # app, gcp_project, region — nothing else
```

```go
package gallery

import "github.com/gsandoval/tako"

var (
    photos = tako.Bucket("photos")
    photoStore = tako.Entity[Photo]("photos")
    thumbs = tako.Queue[ThumbJob]("thumbnails")
    ready  = tako.Topic[PhotoReady]("photo-ready")
    auth   = tako.Auth(tako.WithGoogle(), tako.WithEmail())
    site   = tako.Static("./web", tako.SPA())
    tmpl   = tako.Templates("./templates")
    ups    = tako.Counter("uploads")
)

type Photo struct {
    ID      string `entity:"id"`
    Owner   string `entity:"index"`
    Caption string
    Ready   bool   `entity:"index"`
}
type ThumbJob   struct{ PhotoID string }
type PhotoReady struct{ PhotoID, Owner string }

// ── Handler declarations (registration + docs + callable endpoints) ──

var (
    StartUpload = tako.API("POST /api/photos", startUpload, tako.WithAuth())
    List        = tako.API("GET /api/photos", list)
    Image       = tako.API("GET /api/photos/{id}/image", image)
    PhotoPage   = tako.Page("GET /p/{id}", photoPage)

    OnUploaded  = tako.Subscribe(photos.Uploaded, onUploaded)
    MakeThumb   = tako.Worker(thumbs, makeThumb, tako.WithProfile(tako.Batch))
    NotifyOwner = tako.Subscribe(ready, notifyOwner)
    WarmFeed    = tako.Subscribe(ready, warmFeedCache)
    Cleanup     = tako.Cron("every 24h", cleanupOrphans, tako.WithSingleflight())
)

// 1. Client asks to upload → gets a signed URL (no 32MB cap, no proxying bytes)
type StartUploadReq struct{ Caption string `json:"caption"` }

func startUpload(ctx tako.Ctx, req StartUploadReq) (UploadTicket, error) {
    p := Photo{ID: tako.NewID(), Owner: ctx.User().ID, Caption: req.Caption}
    if err := photoStore.Put(ctx, p); err != nil {
        return UploadTicket{}, err
    }
    url, err := photos.SignedPut(ctx, p.ID, 15*time.Minute,
        tako.WithContentType("image/*"), tako.WithMaxSize(20<<20))
    return UploadTicket{Photo: p, UploadURL: url}, err
}

// 2. GCS notifies on upload completion → enqueue thumbnail work
func onUploaded(ctx tako.Ctx, obj tako.ObjectInfo) error {
    if strings.HasSuffix(obj.Key, "_thumb") { return nil }
    ups.Add(ctx, 1)
    return thumbs.Publish(ctx, ThumbJob{PhotoID: obj.Key})
}

// 3. Worker resizes, flips Ready, announces the event
//    (compile-checked: makeThumb must take ThumbJob because thumbs is Queue[ThumbJob])
func makeThumb(ctx tako.Ctx, job ThumbJob) error {
    img, err := photos.Get(ctx, job.PhotoID)
    if err != nil { return err }
    small, err := resize(img, 256)
    if err != nil { return tako.Permanent(err) }        // bad image: don't retry
    if err := photos.Put(ctx, job.PhotoID+"_thumb", small); err != nil { return err }
    if err := photoStore.Update(ctx, job.PhotoID, func(p *Photo) { p.Ready = true }); err != nil { return err }
    p, _ := photoStore.Get(ctx, job.PhotoID)
    return ready.Publish(ctx, PhotoReady{PhotoID: p.ID, Owner: p.Owner})
}

// 4. Fan-out: two independent subscribers to one event
func notifyOwner(ctx tako.Ctx, e PhotoReady) error { /* email/push */ return nil }
func warmFeedCache(ctx tako.Ctx, e PhotoReady) error { /* ... */ return nil }

func list(ctx tako.Ctx, _ struct{}) ([]Photo, error) {
    q := photoStore.Query(ctx).Where("Ready", "=", true)
    if u, ok := ctx.MaybeUser(); ok { q = q.Where("Owner", "=", u.ID) }
    return q.Limit(100).All()
}

type ImageReq struct{ ID string `path:"id"` }

func image(ctx tako.Ctx, req ImageReq) (tako.Blob, error) {
    return photos.Get(ctx, req.ID+"_thumb")
}

type PhotoPageReq struct{ ID string `path:"id"` }

func photoPage(ctx tako.Ctx, req PhotoPageReq) (tako.HTML, error) {
    p, err := photoStore.Get(ctx, req.ID)
    if tako.IsNotFound(err) { return tako.HTML{}, tako.NotFound() }
    return tmpl.Render("photo.html", p)
}

func cleanupOrphans(ctx tako.Ctx) error { /* delete never-completed uploads */ return nil }
```

This exercises: signed-URL direct upload, bucket events, work queue, event fan-out, entity queries + transactional update, auth-gated and public routes, SSR, static hosting, metrics, and a lease-protected cron — the whole primitive set in ~110 lines of business logic, with every queue/topic/route binding compile-checked.

`tako deploy` prints the derived security summary:

```
Derived IAM:
  app service:        photos [objectAdmin + signBlob], datastore [user], thumbnails [publisher]
  thumbs worker:      photos [objectAdmin], datastore [user], photo-ready [publisher]
  ready subscribers:  (NotifyOwner) datastore [user]   (WarmFeedCache) datastore [user]
Endpoints:  2 authed, 3 public, 1 SSR page, static /*
Async:      1 queue (dead-letter on), 2 topics, 1 cron (singleflight)
```

---

## Part 3b — Second Example: the Journal (the website-with-an-API pattern)

Where the gallery showcases async + storage, the journal showcases the default
web shape: a server-rendered site with interactive UI and no SPA, no JSON
round-trips for the UI, and no client framework beyond htmx as a static asset.
This is `examples/journal` from the agent plan.

```go
package journal

import "github.com/gsandoval/tako"

var (
    posts = tako.Entity[Post]("posts")
    site  = tako.Site("./web")                       // templates in ./web/templates; htmx vendored in ./web
    auth  = tako.Auth(tako.WithGoogle())
)

type Post struct {
    ID        string `entity:"id"`
    Title     string
    Body      string // author-supplied markdown
    Published bool   `entity:"index"`
}

var (
    Home    = tako.Page("GET /", home)
    Read    = tako.Page("GET /p/{id}", read)
    Create  = tako.Page("POST /posts", create, tako.WithAuth())
    Publish = tako.Page("POST /p/{id}/publish", publish, tako.WithAuth())
)

func home(ctx tako.Ctx, _ struct{}) (tako.HTML, error) {
    ps, err := posts.Query(ctx).Where("Published", "=", true).Limit(50).All()
    if err != nil { return tako.HTML{}, err }
    return site.Render(ctx, "home.html", ps)
}

type ReadReq struct{ ID string `path:"id"` }

func read(ctx tako.Ctx, req ReadReq) (tako.HTML, error) {
    p, err := posts.Get(ctx, req.ID)
    if tako.IsNotFound(err) { return tako.HTML{}, tako.NotFound() }
    return site.Render(ctx, "post.html", p)          // post.html uses {{ markdown .Body }}
}

type CreateReq struct {
    Title string `form:"title"`
    Body  string `form:"body"`
}

func create(ctx tako.Ctx, req CreateReq) (tako.HTML, error) {
    p := Post{ID: tako.NewID(), Title: req.Title, Body: req.Body}
    if err := posts.Put(ctx, p); err != nil { return tako.HTML{}, err }
    if ctx.IsHTMX() {
        return site.Fragment(ctx, "post-card.html", p)   // swapped into the list by hx-target
    }
    return tako.Redirect("/p/" + p.ID)               // no-JS fallback; emits HX-Redirect under htmx
}

type PublishReq struct{ ID string `path:"id"` }

func publish(ctx tako.Ctx, req PublishReq) (tako.HTML, error) {
    var out Post
    err := posts.Update(ctx, req.ID, func(p *Post) { p.Published = !p.Published; out = *p })
    if err != nil { return tako.HTML{}, err }
    return site.Fragment(ctx, "publish-toggle.html", out)  // htmx swaps just the button
}
```

The templates carry the other half — and nothing else, because the framework
already wired the sharp edges:

```html
<!-- web/templates/post.html -->
<article>{{ markdown .Body }}</article>          <!-- goldmark, sanitized by default -->

<!-- web/templates/home.html (excerpt) -->
<form hx-post="/posts" hx-target="#posts" hx-swap="afterbegin">
  <input name="title"> <textarea name="body"></textarea>
  <button>Save</button>                          <!-- CSRF via hx-headers meta wiring: automatic -->
</form>
<div id="posts">{{ range . }}{{ template "post-card.html" . }}{{ end }}</div>
```

What this example demonstrates, deliberately: one handler serving both htmx
and plain-form clients (`create` — fragment vs. redirect, with `Redirect`
choosing 302 or `HX-Redirect` itself); partial updates without any JSON
endpoint (`publish` returns just the button); user-authored markdown rendered
safely with zero configuration; auth-gated writes with public reads; and form
binding through tagged `Req` structs. The entire interactive site is four
handlers and three templates — no API layer exists because none is needed,
which is the point of the pattern. When an API *is* also wanted (mobile app,
integrations), it's added as `tako.API` endpoints beside these pages, sharing
the same store, auth, and session — that combination is the "website with an
API" default this framework optimizes for.

---

## Part 4 — Under the Hood

Pipeline: **analyze → generate → build → provision → deploy**. `tako dev` reuses the first two stages with local backends.

### 4.1 Analyzer

- Load the module via `golang.org/x/tools/go/packages` (syntax + types).
- **Resources**: package-level vars initialized by known SDK constructors; names must be constants; `T` in generics resolved via `go/types` to extract schemas (drives Datastore indexes, JSON codecs, later generated clients).
- **Handlers**: extracted by the *same* mechanism as resources — package-level vars initialized by known handler constructors (`tako.API`, `tako.Worker`, ...). No comment-parsing subsystem exists. Payload-type correctness is the compiler's job (generics bind handler shapes to resource `T`s); the analyzer enforces what types can't: constant constructor args (routes, schedules, names), route path params ↔ `path:`-tagged `Req` fields, option validity per constructor, package-level placement.
- **Bindings**: the bound resource is the constructor's first argument, so binding existence is compile-checked; the analyzer adds cardinality and boundary rules — at most one `Worker` per queue; cross-package calls only through exported `Endpoint` vars (raw handler funcs crossing service packages is an error).
- **Usage edges**: which handlers touch which resources, with read/write verbs from the SDK methods called. v0: per-function AST reference scan, transitive through same-module calls (slightly over-grants); upgrade path: SSA + RTA call-graph for precision. Powers derived IAM.
- **Index derivation**: `Query` call sites with their `Where`/`OrderBy` combinations → generated `index.yaml`; querying an untagged field fails analysis.

Output: an `AppGraph` (resources, handlers, edges, service partitioning). Partitioning rule: **package = service**. Within a package, api/page/cron handlers deploy together as that package's service; each worker/subscriber still gets its own service (independent scaling + IAM); jobs → Cloud Run Jobs. A scaling profile is physically a service property, so conflicting `WithProfile` values among a package's api/page handlers are an analysis error ("split into separate packages or unify the profile") — never silently resolved, never auto-split. Cross-package handler calls become call edges (§2.12): direct calls in dev, generated OIDC-authenticated stubs in the cloud, `run.invoker` grants derived per edge, cycles rejected at analysis time, warnings emitted for sync calls into scale-to-zero callees and for chains deeper than one hop.

### 4.2 Codegen

Per service, a generated `main` package under `.tako/gen/<service>/` — readable by users, which *is* the transparency story:

- Route registration on Go 1.22+ `net/http` (native method+wildcard patterns, no router dep).
- Static param decoding per endpoint (path/query/JSON/multipart) — no reflection at request time.
- Push-endpoint plumbing for workers/subscribers: verify the Pub/Sub OIDC push token (audience = service URL), decode envelope → typed payload, map return values: `nil` → 204 ack; `Permanent` → 204 + log (no retry); error → 500 → redelivery with backoff → dead-letter after N attempts.
- Resource wiring at startup from env (`TAKO_RES_PHOTOS=gallery-photos-4f2k`, `TAKO_BACKEND=gcp|local`); handles are injected into the user's package-level vars via an SDK boot hook. The same boot hook wires each `Endpoint`'s transport: direct invocation for local/dev/tests and co-located packages, HTTP+OIDC stub for remote services — there is no call-site rewriting anywhere in codegen.
- Injected `/api/_session` endpoint when `Auth` is declared.

### 4.3 Runtime shim

Middleware, outermost first: recovery + request ID + structured JSON logs (trace-correlated via `X-Cloud-Trace-Context`) → auth (session cookie or bearer; JWKS cached, refreshed on `kid` miss; 401 before handler on `WithAuth()` endpoints) → decode → handler → encode (JSON / HTML / streamed Blob). `Ctx` wraps `context.Context` + user + logger + trace; every resource call takes it, so storage ops are traced and cancellable for free.

### 4.4 The lease — one mechanism, three features

A lease = a GCS object write guarded by `ifGenerationMatch` (atomic compare-and-swap), holding `{owner, expiry}`, heartbeated at TTL/3. Zero extra infrastructure to provision — it lives in a framework-owned bucket.

- **`Lock`**: `Do` campaigns for the lease, runs `fn` while heartbeating, releases on return; context is cancelled if the lease is lost mid-flight.
- **`cron ... singleflight`**: the generated cron handler wraps the user function in `TryDo` on a lease named after the cron — double-fires and overlapping slow runs collapse to one execution.
- **`tako.Singleton`**: provisioned as `min=1, max=1, cpu-always-on`; but Cloud Run runs old+new revisions concurrently during deploys, so max=1 alone isn't a guarantee — the generated loop only executes while holding the lease, giving clean handoff between revisions.

Raw leader election is deliberately not in the SDK: the use cases are covered, and the failure modes stay the framework's problem.

### 4.5 Build

`ko`-style: `CGO_ENABLED=0 go build` per service → append binary as a layer on distroless via `go-containerregistry` → push straight to Artifact Registry. No Docker daemon, no buildpacks, seconds not minutes. (Buildpacks return if non-Go languages ever do.)

### 4.6 Provisioner — stateless reconciler

Direct GCP API calls, no Terraform state. Deterministic names + labels (`tako-app`, `tako-resource`); each deploy lists-by-label, diffs against the AppGraph, creates/updates, warns on orphans (never auto-deletes); `tako destroy` deletes by label. ~10 resource kinds you fully control makes idempotent reconcile cheaper than the state-file problem; if the surface grows, `pkg/provision` swaps for generated Terraform behind the same AppGraph interface.

Reconcile order:

```
1. Service accounts (one per service)
2. Buckets, Datastore indexes, topics (+ dead-letter topics), framework lease bucket
3. IAM bindings from graph edges
4. Secret Manager references
5. Push images
6. Cloud Run services & Jobs (env carries resource names)
7. Push subscriptions (need service URLs): one per worker; one PER HANDLER per topic (independent fan-out); GCS notification → topic for bucket.Uploaded
8. Cloud Scheduler jobs
9. Identity Platform providers
```

Derived IAM mapping:

| Edge | Grant |
|---|---|
| Bucket read / write | `objectViewer` / `objectAdmin` on that bucket |
| SignedPut/SignedGet used | `iam.serviceAccountTokenCreator` on the service's own SA (signBlob) |
| Queue/Topic publish | `pubsub.publisher` on that topic |
| Entity any | `datastore.user` on the project *(honest limitation: GCP has no per-kind Datastore IAM — say so in the summary)* |
| Push delivery | push SA gets `run.invoker` on the receiving service |
| Cross-service call (§2.12) | caller's SA gets `run.invoker` on callee; `private` = IAM-only, no unauthenticated access (network-internal ingress is a later `WithVPC` opt-in) |
| Job.Run used | `run.jobsExecutor` on that job |

Queue backend selection: plain queues → Pub/Sub push; queues using `WithDelay`/`WithRateLimit` → Cloud Tasks (same user API, same push endpoint).

Scaling profiles → Cloud Run knobs:

| Profile | min | max | conc | cpu/mem | cpu throttle |
|---|---|---|---|---|---|
| default | 0 | 20 | 80 | 1 / 512Mi | on |
| latency | 1 | 100 | 40 | 2 / 1Gi | off |
| batch | 0 | 10 | 10 | 2 / 2Gi | on |

Escape hatches: `overrides:` block in `tako.yaml` patching raw service specs; `handle.Raw()` returning the underlying GCP client; readable generated code in `.tako/gen/`.

### 4.6a Deployment lifecycle — diffs, updates, rollback, destruction

**Desired vs. observed state.** Every deploy computes desired state from scratch (the AppGraph from the current code) and observed state by listing GCP objects with the `tako-app=<app>, tako-env=<env>` labels. There is no state file to corrupt or drift; the cloud *is* the state.

**Change detection without slow diffs.** Comparing full live specs field-by-field is slow and fragile (GCP backfills defaults). Instead, every managed object carries an annotation `tako-spec-hash: <sha256 of the spec tako intends>`. The diff per resource is: absent → **create**; hash differs → **update** (server-side apply of only tako-owned fields); hash matches → **skip**. This makes no-op deploys near-instant and gives drift detection for free: if the live spec was hand-edited in the console, its content no longer matches its recorded hash, and `tako deploy` prints a drift warning before overwriting tako-owned fields (fields set via the `overrides:` escape hatch are part of the intended spec, so they're preserved, not fought).

**Update classes.** Not all diffs are equal; the reconciler classifies each change and treats them differently:

| Class | Examples | Behavior |
|---|---|---|
| Additive | new endpoint, new resource, new index, new subscriber | apply silently |
| In-place mutable | scaling profile, env, image, IAM, schedule | apply silently |
| Replace-required | changing a queue's backend (Pub/Sub↔Tasks), renaming a service | create-new → shift → delete-old, announced in the plan |
| Destructive on data | resource var deleted, bucket/entity store renamed | **never automatic** — see below |

**Deletion is explicit, always.** Removing `tako.Bucket("photos")` from code does *not* delete the bucket. The deploy output marks it `orphaned (kept)`, and stateless (compute/messaging) orphans are cleaned by `tako prune`, while data-bearing orphans (buckets, entity kinds) require `tako prune --data` with an interactive confirmation listing object counts. A rename is indistinguishable from delete+create, so the plan flags the pair as a rename suspect and states plainly that data does not follow a rename automatically — physical names embed the logical name (GCS bucket names are immutable identities; a Datastore kind rename means copying every entity), so a true rename is a data MIGRATION, not a relabel. A migrating `tako rename old new` (object rewrite + entity copy + delete-old behind confirmations) is a planned feature; until it lands, the honest path is: declare both, copy, drop the old. `tako destroy` tears down everything by label, same data confirmation.

**Versioned rollouts.** Cloud Run revisions are the version mechanism — tako doesn't reinvent one:

- Images are pushed and deployed **by digest**, never by tag, and each revision is annotated with the git SHA and the AppGraph hash. A "version" of the app is therefore a set of pinned revisions across services.
- Default rollout: deploy new revisions with 100% traffic (Cloud Run itself guarantees zero-downtime cutover — new revision must pass startup/health before receiving traffic).
- `tako deploy --canary=10` splits traffic 10/90 between new and prior revision on the `app` service; `tako promote` completes it. (Workers/subscribers don't canary in v0 — Pub/Sub push targets one URL; splitting async traffic meaningfully needs subscription juggling that isn't worth the complexity yet.)
- `tako rollback` shifts traffic back to the previous revision set — instant, no rebuild, because the old images and revisions still exist. Infra-shape changes roll back via `git revert` + `tako deploy` (the reconciler makes the cloud match the old code again); the additive-first ordering below means the old revisions' resources still exist during the window.

**Ordering and mid-rollout safety.** Applies are additive-first (new resources, IAM, images, then services, then subscriptions, then pruning of replaced compute), so at every instant during a deploy, both the old and new revision can run correctly — which matters because Cloud Run *will* run both concurrently for a short window. Two consequences the docs must state: (1) queue/topic payload structs should evolve additively (new optional fields), since an old worker may receive a new publisher's message during rollout; (2) deploys take a lease (`tako-deploy` in the framework bucket — the same lease mechanism from §4.4), so two concurrent `tako deploy` runs from CI and a laptop can't interleave half-applied graphs; the second blocks with a "deploy in progress by <who> since <when>" message.

**Plan before apply.** `tako deploy --plan` (and the default interactive prompt when the plan contains anything beyond additive/in-place changes) prints the classified diff — exactly the Terraform habit users trust, minus the state file.

**Environments.** `tako deploy --env=pr-42` prefixes every resource name and label with the env — a completely disjoint resource set from the same code, which is what makes preview environments nearly free. `tako destroy --env=pr-42` removes it wholesale; CI wires that to PR close.

### 4.7 Local dev

One process, in-memory/on-disk backends, no Docker or emulators: Bucket → `.tako/data/` directory; Entity → bbolt-backed store with in-process index scans; Queue/Topic → channels invoking handlers directly (breakpoints across publish→consume — impossible against real Pub/Sub); Lock/lease → in-process mutex; signed URLs → local upload endpoints; Auth → dev-minted tokens + injected "sign in as test user" bar; Jobs → goroutines. File watcher re-runs analyze→generate→`go run`, target <1s.

The same generated mains run in both modes — only `TAKO_BACKEND` differs — and a shared conformance test suite runs against `local` and `gcp` backends so they can't silently diverge. Fidelity gaps are printed, not hidden ("queue: in-process, no retry backoff locally").

---

## Part 5 — Deliberate Omissions & the SQL Door

**SQL (the planned second database).** Deferred because Cloud SQL drags in VPC connectors, connection pooling, and migration tooling — a large, separable chapter. The interface guardrails already in place so it lands cleanly:

```go
// Future — sketch only:
var db = tako.Postgres("main", tako.WithMigrations("./migrations"))
db.Query / db.Exec / db.Tx(ctx, func(tx tako.SQLTx) error { ... })
```

- No global `tako.DB`, `tako.Tx`, or `tako.Query` today → nothing to un-claim.
- Entity's transactions, query builder, and struct-tag schema are all scoped to Entity → Postgres brings its own idioms (SQL migrations, likely sqlc-style typed queries) without inheriting document-store assumptions.
- Derived IAM and the reconciler treat it as just another resource kind.
- Docs language matters: Entity is "a typed entity store," never "the database."

**Shared cache (Redis).** Same VPC tax as Cloud SQL. `Cache` ships per-instance in-memory; `WithShared()` later swaps the backend without touching call sites. Distributed rate limiting waits on the same decision; per-instance approximate limiting covers abuse protection meanwhile.

**Websockets/realtime.** Cloud Run technically supports them, but instance affinity + scale-to-zero makes correct realtime a research project. Explicitly out of scope; document SSE + polling patterns instead.

**Raw leader election.** Covered by singleton/singleflight/Lock (§4.4); intentionally never exposed as a campaign/observe API.

---

## Part 6 — Build Sequence

1. SDK types + analyzer for `api` + `Bucket`; `tako gen` prints the AppGraph.
2. Codegen + local backends + `tako dev` → already a pleasant local web framework.
3. `tako deploy`: reconciler for bucket + Cloud Run + derived IAM.
4. `Entity` (queries, Update, Tx, index derivation).
5. `Queue` + worker (Pub/Sub push, dead-letter, `Permanent`); then `Topic` + subscribe + `bucket.Uploaded` (≈90% shared plumbing).
6. Signed URLs (unlocks the real demo upload flow).
7. `Auth` + `_session` + SSR pages + `Static`.
8. Lease → `Lock`, `singleflight`, `singleton`; `cron`.
9. `Secret`, `Cache`, metrics, `Job`.
10. Polish: `tako logs`, `NewTestApp`, preview envs (`tako deploy --env=pr-42` — a name prefix, nearly free), security summary.

Every step ends in something demoable; the gallery in Part 3 exercises all of it by step 9.
