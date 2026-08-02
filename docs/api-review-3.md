# API review 3 — options, scoping, and the namespace (2026-08-02)

Baseline: the 148-entry `testdata/sdk_lock.golden` as of `58d566b`.

**Scope is narrower than review 1 and different from review 2.** Review 1
(F10) asked "does every symbol satisfy the six rules"; review 2 (R3)
asked "did the freeze hold, and is this publishable". This review asks
one question the first two never put to the surface as a whole:

> When a user types `tako.` — can they tell which options apply where,
> and can two symbols be confused for one another?

The answer is no, and the reasons are structural rather than
cosmetic. Nothing here re-litigates a settled deviation from review 1;
those are listed in §9 as explicitly out of scope.

## 0. Why now

`git tag` is empty. R3's verdict says it plainly: *"Push + v0.1.0 tag
remain owner actions to execute."*

Every rename in this document is free today and a breaking change the
day after the tag exists. This is the last moment the namespace can be
reshaped without a v2. It is also the moment *before* alerting adds
~12 symbols and at least one new option family, which would otherwise
be designed against a surface about to move.

---

## 1. `Option` is three types wearing one name

`Option` is a single struct (`options.go:12`) accepted at 23 call sites
by 23 producers. Its doc comment makes a specific promise:

```go
// ... which options are valid for which constructor is enforced by the
// analyzer at build time, so an invalid combination fails analysis, not
// production.
```

That promise holds for most declarations and **is false for every
operation option**. There are three enforcement tiers, and the type
system distinguishes none of them:

| Tier | Where | What an illegal option does | Sinks |
|---|---|---|---|
| **Analysis** | most declarations | `tako/invalid-option` at build | 18 constructors |
| **Silence** | `Bucket`, `Entity` | nothing — dropped, forever | 2 constructors |
| **Runtime** | `Publish`, `SignedPut`, `UploadHeaders` | an error string, at request time | 3 operations |

The runtime tier is by design (`api-review-1.md` row 51 records
"bad options error at call time"), but "call time" in a framework whose
entire thesis is *static analysis, never reflection in the request path*
means a class of mistakes reaches production that nothing else in tako
would allow.

### The probe

Four illegal option uses in one file, analyzed with the real CLI:

```go
var (
    photos = tako.Bucket("photos", tako.WithTTL(time.Minute))          // cache option
    album  = tako.Entity[Photo]("album", tako.WithSingleflight())      // cron option
    jobs   = tako.Queue[string]("jobs")
)

var Hello = tako.API("GET /hello", func(ctx tako.Ctx, _ struct{}) (string, error) {
    jobs.Publish(ctx, "x", tako.WithTTL(time.Minute))                  // cache option
    photos.SignedPut(ctx, "k", time.Minute, tako.WithSingleflight())   // cron option
    return "hi", nil
})
```

```
$ tako gen graph
optprobe: 2 services
  service optprobe (package optprobe, profile default)
    GET /hello → Hello (app.go:19)
    bucket photos
    entity album
    queue jobs
  service optprobe-do (package optprobe, profile default)
    ...
```

**Zero errors.** Two options vanish silently; two become 500s the first
time that route is hit in production.

## 2. Defect — `Bucket` and `Entity` swallow options

Not a design opinion; a bug, and it regresses a rule this project
already wrote down. `resources.go:328` states it:

> Declaration options are validated per kind — an option that is
> accepted and silently dropped is worse than an error (the S6 audit
> found `WithRateLimit` doing exactly that).

`extractBucket` (`resources.go:88`) and `extractEntity`
(`resources.go:43`) both read `call.Args[0]` and never look at
`call.Args[1:]`. The probe above proves it end to end.

**Root cause is the same class as the code-review findings**: the rule
is held by convention across 14 hand-written enforcement sites in 5
files, with nothing asserting that every constructor declares an option
set. A table keyed by constructor — one entry per entry in the lock —
with a test that fails when a constructor is missing from it, would make
this unrepresentable. That test is worth writing **whichever remedy in
§6 is chosen**.

Fix is small and independent of everything else in this review. It
should not wait for the redesign.

## 3. The matrix

Extracted from the analyzer's own error texts, which enumerate the legal
sets. This table does not exist anywhere else in the repo or the docs —
assembling it was most of this review.

| Sink | Kind | Accepts |
|---|---|---|
| `API`, `Page` | route | `WithAuth`, `WithPrivate` |
| `Service` | service | `WithDomain`, `WithProfile`, `WithFootprint`, `WithTraffic` |
| `Worker`, `Subscribe` | compute | `WithProfile`, `WithFootprint`, `WithTraffic` |
| `Cron` | compute | `WithSingleflight`, `WithTimezone`, `WithDeadline`, `WithProfile`, `WithFootprint` |
| `JobHandler` | compute | `WithParallel`, `WithRetries`, `WithProfile`, `WithFootprint` |
| `Singleton` | compute | `WithFootprint` |
| `Site` | site | `SPA`, `WithBuild`, `WithDevProxy`, `WithRedirect`, `WithTrustedHTML` |
| `Auth` | auth | `WithGoogle`, `WithEmail` |
| `Cache` | carrier | `WithTTL` |
| `Queue`, `Topic` | carrier | `WithDelivery` |
| `Bucket`, `Entity` | resource | **nothing — and nothing checks** (§2) |
| `Job`, `Secret`, `Counter`, `Histogram` | resource | nothing |
| `Lock` | resource | nothing (framework-owned lease policy, deliberate) |
| `QueueHandle.Publish` | operation | `WithDelay` |
| `BucketHandle.SignedPut` | operation | `WithMaxSize`, `WithContentType` |
| `UploadHeaders` | operation | `WithMaxSize`, `WithContentType` |

**23 producers × 23 sinks = 529 combinations. 39 are legal — 7.4%.**
The other 92.6% compile.

Six families fall out, and they are close to disjoint: **route**,
**service/compute**, **site**, **auth**, **carrier**, **operation**.
That clustering is the design; the surface just doesn't express it.

## 4. The namespace — collisions and near-collisions

| Pair | Problem | Severity |
|---|---|---|
| `Redirect(target)` / `WithRedirect(from, to)` | unrelated concepts: a page handler's return value vs a static rewrite rule on `Site`. Same word, different arity, different family | **high** |
| `Auth()` / `WithAuth()` | declare the app's providers vs require sign-in on one route. Adjacent enough to be guessed wrong | medium |
| `WithEmail()` / a future `Email(addr)` | `WithEmail` enables password sign-in; the alerting design wants `Email` for a notification channel. Found by accident while designing an unrelated feature — which is the evidence that nobody has audited the namespace | medium (blocks alerting) |
| `Handler` (type) / `JobHandler` (func) | `JobHandler` reads like a type. It is the only handler constructor named `<Noun>Handler` — `Worker`, `Subscribe`, `Cron`, `Singleton`, `API`, `Page` are all roles. It is named around the collision with `Job` | medium |
| `On`, `Off`, `Inherit`, `Default`, `Batch`, `Latency` | six single generic words in the package namespace, from two unrelated enums (`Toggle`, `Profile`). `tako.Default` and `tako.On` are the kind of name a later feature will want and cannot have | medium |
| `SPA()` | the **only** option not prefixed `With`. There is no reason; it predates the convention | low, trivial to fix |
| `UploadHeaders(opts...)` | the only free function taking options. Pairs with `SignedPut`, so it has a reason, but it is shaped like nothing else | low |

## 5. One concept, two spellings

Review 2 established the principle when it retired `WithRateLimit`:
*"one knob with two spellings is worse than a breaking change."* The
surface still contains three instances.

**Retries.** `WithRetries(n)` on `JobHandler` (range **0–10**) and
`Delivery.MaxAttempts` on `Queue`/`Topic` (range **5–100**) are the same
question — how many times before giving up — with two names, two ranges,
and two homes. Both ranges are individually defensible (jobs have no
dead letter; Pub/Sub's policy dictates 5–100), which is exactly why the
divergence is invisible until you hit it.

**Time.** Five knobs, five unrelated names, no shared vocabulary:
`WithTTL` (cache entry), the `ttl` parameter on `SignedGet`/`SignedPut`
(URL validity), `WithDeadline` (cron run limit), `Traffic.Timeout`
(request limit), `WithDelay` (publish deferral). Rule 4 gives methods
one verb vocabulary; nothing imposes the same discipline on options,
and it shows.

**Concurrency.** `WithParallel(n)` (job shards), `Footprint.Concurrency`
(requests per instance), `Delivery.RateLimit` (deliveries per second) —
three different "how much at once", correctly distinct, but nothing in
the names says which axis each one moves.

## 6. Three remedies

### (A) Family-typed options

`func Cron(schedule string, fn func(Ctx) error, opts ...ComputeOption)`,
with per-family marker interfaces.

Requires one distinct type per option (a single `Option` struct cannot
selectively implement markers), so ~23 small types, and exported
constructors returning unexported types — a known Go wart.

Catches the *family* mistake at compile time (`WithTTL` on a `Cron`),
but **not** the sub-family mistake (`WithParallel` on a `Cron`, both
compute), so `invalid-option` survives in reduced form. Partial win for
real cost.

### (B) Config structs — recommended for declarations

```go
var Cleanup = tako.Cron("every 24h", cleanupOrphans, tako.CronConfig{
    Singleflight: true,
    Timezone:     "America/Mexico_City",
})
```

Fully compile-checked, discoverable in godoc and autocomplete, no marker
methods, and it **deletes `invalid-option` and `service-option`
entirely**. Field-level range validation (retries 0–10, deadline
15s–30m) stays in the analyzer, where it belongs — that is domain
validation, not "does this option belong here".

The strongest argument for (B) is that **tako already chose it three
times**: `Delivery`, `Footprint`, and `Traffic` are config structs
passed through a `With…` wrapper that exists only to satisfy a variadic.
The three most complex option payloads independently grew into structs.
That is the flat model reporting its own limits.

Costs: rule 2's "options all live in one namespace" is rewritten; every
example, doc snippet, and golden moves; single-flag cases get longer
(`tako.SiteConfig{SPA: true}` vs `tako.SPA()`) — though arguably
clearer.

### (C) Prefix-only rename

`WithCacheTTL`, `WithSiteSPA`, `WithAuthEmail`, `WithCronDeadline`.

Cheapest by an order of magnitude — a pure rename, no restructuring.
Fixes every collision in §4 and makes autocomplete useful (`tako.WithCron`
narrows). Zero type safety; the analyzer and both rules stay.

### Recommendation — the hybrid

1. **Declarations → (B) config structs.** The one remedy that is both
   fully compile-checked and discoverable, and the direction the surface
   already drifted.
2. **Operations → delete the option tier.** `Publish` has one option and
   `SignedPut` has two. Make them explicit parameters or a small struct;
   the runtime-error tier in §1 disappears rather than being documented.
3. **Names → (C) applied regardless**, since collisions are orthogonal
   to how options are typed and cost almost nothing to fix now.
4. **Ship §2's defect fix immediately**, independent of 1–3, plus the
   completeness test that would have caught it.

## 7. Per-symbol recommendations

Only symbols with a recommendation are listed. **Every other entry in
the 148 is "no change"** — including all eight review-1 deviations (§9).

| Symbol | Recommendation | Why |
|---|---|---|
| `SPA()` | → `WithSPA()`, or a `SiteConfig` field | only unprefixed option |
| `WithRedirect(from, to)` | → `WithRewrite`, or `SiteConfig.Rewrites` | frees `Redirect` from the collision |
| `WithEmail()` | → `WithPassword()` or `AuthConfig.Password` | it enables email+**password** sign-in; the current name describes the identifier, not the method, and blocks `Email` for alerting |
| `WithAuth()` | → `WithSignIn()` | says what it requires; ends the `Auth`/`WithAuth` ambiguity |
| `WithRetries(n)` | → `WithAttempts(n)`, aligned with `Delivery.MaxAttempts` | one concept, one word (ranges stay different, documented) |
| `JobHandler(job, fn, …)` | → `Runner(job, fn, …)` or `Task(job, fn, …)` | matches `Worker`/`Subscribe`/`Singleton`; stops reading as a type |
| `On`, `Off`, `Inherit` | → `Toggle` methods or `ToggleOn`/`ToggleOff` | reclaims three generic words |
| `Default`, `Latency`, `Batch` | → `ProfileDefault`, `ProfileLatency`, `ProfileBatch` | reclaims three more; `tako.Default` is indefensible in a package namespace |
| `UploadHeaders(opts…)` | → method on `BucketHandle`, or fold into `SignedPut`'s return | it exists to mirror `SignedPut`'s options; if those stop being options, so does this |
| `WithDelay(d)` on `Publish` | → `PublishAfter(ctx, msg, d)` | deletes a runtime-error path |
| `WithMaxSize`, `WithContentType` on `SignedPut` | → an explicit params struct | same |
| `Bucket`, `Entity` | reject unknown options (§2) | defect, ship now |

## 8. Cost

| Slice | Shape |
|---|---|
| §2 defect + completeness test | small; no surface change; ship independently |
| Names (C) | mechanical rename + lock regen + docs/goldens sweep |
| Declarations (B) | the large one: every constructor, every example, every reference page, ~40 goldens |
| Operations | small; 3 sinks |

The middle two are one API task each by the standing rule — API changes
are their own task, never a side effect — and each needs the lock
regenerated and `docs/reference/errors.md` updated, since two rules die.

## 9. Explicitly out of scope

Not reopened here; all confirmed in review 1 and re-confirmed in review 2:

- `EntityTx` / `EntityQuery` / `Iter` operating without `Ctx`
- `SecretHandle.Value()` — no Ctx, no error (the boot-time guarantee)
- `Counter.Add` / `Histogram.Observe` returning nothing
- `Cron(schedule, …)` and `Auth()` / `Service()` deviating from rule 2's
  name-first shape
- `TopicHandle.Publish` having no `...Option` where `QueueHandle` does
- `Ctx.IsHTMX` as the only request-shape probe
- `Handler` being opaque

## 10. Decisions for the owner

1. **Remedy**: the hybrid in §6, (C) alone, or leave the option system
   as it is and fix only §2?
2. **Rule 2 rewrite**: (B) requires editing spec Part 1's "options all
   live in one namespace". Approve the spec change, or reject (B) on
   that basis?
3. **Which renames**: §7 as a set, or symbol by symbol? `Default` →
   `ProfileDefault` and `JobHandler` → `Runner` are the two most
   disruptive to existing docs.
4. **Ordering vs alerting**: this review's changes first, or alerting
   first against the current surface and renamed after? (Recommendation:
   this first — alerting adds a family and a `Email`-shaped collision.)
5. **Tag timing**: everything here assumes v0.1.0 is not yet tagged. If
   the tag ships first, all of §7 becomes a v2 discussion.

---

**Status: REVIEW COMPLETE — awaiting owner decisions (§10).** No code
changed in this pass.
