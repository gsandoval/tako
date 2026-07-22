# API Review 1 — the pre-cloud freeze (Gate F10)

**Purpose.** Every exported symbol of package `tako` (the normative
surface, snapshotted in `testdata/sdk_lock.golden`, 132 entries) is
assessed against the six interface rules (spec Part 1). This review is
human-led: the assessments and recommendations below are the agent's
input; the **Verdict** column is yours. Gate F10 closes with your
explicit written approval at the bottom, after which the lock is frozen —
post-F10 changes to the exported surface require reopening this document.

This is the last cheap moment to rename anything.

**How to read the tables.** *Rules* lists which of the six rules bear on
the symbol (1 declared-noun · 2 constructor-shape · 3 method-shape ·
4 verb-vocabulary · 5 async-shape · 6 no-general-concept). *Status*:
**live** (implemented + validated through a gate), **stub** (declared,
panics, arrives Phase G/H). Verdicts for stubs can be `keep (provisional)`
— they freeze the *intent*, and their implementing phase may reopen them
with a documented reason.

---

## 1. Keyed stores — Bucket, Entity, Cache (rules 2, 3, 4, 6)

All three share `Get / Put / Delete / Exists` with `Ctx` first — rule 4
holds exactly. Names are string constants (analyzer-enforced).

| Symbol | Status | Assessment | Recommendation | Verdict |
|---|---|---|---|---|
| `Bucket(name, ...Option) *BucketHandle` | live | Rules 1–2 clean. | keep | |
| `BucketHandle.Get/Put/Delete/Exists` | live | Rule 4 exactly. | keep | |
| `BucketHandle.List(ctx, prefix) Iter[ObjectInfo]` | live | Only keyed store with `List`. Justified asymmetry: buckets list by key **prefix**; entities query by **indexed field** (`Query`). A shared name would lie about the semantics. | keep | |
| `BucketHandle.SignedPut(ctx, key, ttl, ...Option)` / `SignedGet(ctx, key, ttl)` | live | `ttl` positional (mandatory), constraints as options — deliberate: no signed URL without an expiry. F6-validated. | keep | |
| `BucketHandle.Uploaded` (field, `BucketEvent`) | live | The bucket-event binding (`tako.Subscribe(photos.Uploaded, …)`). Compile-checked, reads naturally. Only `Uploaded` exists; `Deleted` etc. would be additive. | keep | |
| `Entity[T](name, ...Option) *EntityHandle[T]` | live | Rule 6's showcase: *an* entity store, never "the database". | keep | |
| `EntityHandle.Get/Put/Delete/Exists` | live | Rule 4. Key is `id string` (from the `entity:"id"` tag) vs bucket's `key` — parameter names differ where semantics differ. | keep | |
| `EntityHandle.Update(ctx, id, mutate func(*T))` | live | Read-modify-write sugar over `Tx`. Not part of the rule-4 vocabulary but earns its place (shortlink, gallery both need it). | keep | |
| `EntityHandle.Tx(ctx, fn func(EntityTx[T]) error)` | live | Rule 6: transactions scoped to the store instance. | keep | |
| `EntityTx[T]` — `Get/Put/Delete(id)` **without Ctx** | live | Deliberate rule-3 deviation: the tx closure is already ctx-scoped by `Tx`; re-passing Ctx would invite smuggling the tx across contexts. | keep (confirm deviation) | |
| `EntityHandle.Query(ctx) *EntityQuery[T]` + `Where/OrderBy/Limit/All/First/Iter` | live | Builder chain; `Where(field, op, value)` fields are analyzer-checked against `entity:"index"` tags (const-query-field, unindexed-query rules). `All/First/Iter` close the chain without re-taking Ctx (captured at `Query`) — same tx-style deviation. | keep (confirm deviation) | |
| `Iter[T]` — `Next() (T, bool)` / `Err()` | live | Standard Go cursor idiom; no Ctx on `Next` (captured at creation). | keep (confirm deviation) | |
| `Cache[T](name, ...Option) *CacheHandle[T]` | live | Per-instance in-memory, honestly documented. | keep | |
| `CacheHandle.Get/Put/Delete/Exists` | live | Rule 4; absent-or-expired ⇒ `IsNotFound`. | keep | |
| `CacheHandle.GetOrFill(ctx, key, fill)` | live | Name candidates were `GetOrSet` (wrong: fill computes) and `Fetch` (opaque). `GetOrFill` says what happens. Concurrent misses may double-fill (documented; singleflight is a compatible later upgrade). | keep | |

## 2. Message carriers — Queue, Topic (rules 2, 3, 4, 5)

| Symbol | Status | Assessment | Recommendation | Verdict |
|---|---|---|---|---|
| `Queue[T](name, ...Option)` / `Topic[T](name, ...Option)` | live | Rule 4: both `Publish`. Queue=work (one Worker), Topic=events (N Subscribers) — the distinction is doctrine (settled decision 6). | keep | |
| `QueueHandle.Publish(ctx, msg, ...Option)` | live | Options: `WithDelay` (F8). Bad options error at call time with the standard text. | keep | |
| `TopicHandle.Publish(ctx, event)` | live | **No opts param** — delay is a queue power, enforced by the compiler. Asymmetric signatures are the honest choice; adding `...Option` "for symmetry" would create a runtime error where a compile error now lives. | keep (confirm asymmetry) | |
| `EventSource[T]` (sealed interface) | live | Unexported methods seal it: only `TopicHandle[T]` and `BucketEvent` can be subscription sources. Sealing is the feature. | keep | |

## 3. Handler constructors (rules 1, 2, 3, 5)

| Symbol | Status | Assessment | Recommendation | Verdict |
|---|---|---|---|---|
| `API[Req, Res](route, fn, ...Option) Endpoint[Req, Res]` | live | Rule 3's showcase. Route is const (analyzer-enforced). | keep | |
| `Endpoint.Call(ctx, req)` | live | The var IS the callable surface; cross-package Call is the RPC (spec §2.12, Phase H exercises it remotely). | keep | |
| `Worker(queue, fn, ...Option)` / `Subscribe(source, fn, ...Option)` | live | Rule 5 exactly; binding is the compile-checked first argument. | keep | |
| `Cron(schedule, fn, ...Option)` | live | Rule-2 deviation, documented in spec §2.8: no payload ⇒ first arg is the schedule, not a resource. v0 schedule syntax `"every <duration>"` (tako/cron-schedule). | keep (confirm deviation) | |
| `Handler` (opaque struct) | live | Return type of async/scheduled constructors. Opaque on purpose — the var is registration + docs, not an API. | keep | |
| `Page[Req](route, fn, ...Option) Endpoint[Req, HTML]` | stub | Phase G. Shape mirrors `API` with `HTML` result — consistent. | keep (provisional) | |
| `JobHandler(job, fn, ...Option)` / `Job[T](name, ...Option)` / `JobHandle.Run(ctx, params)` | stub | Phase H. `Run` (not `Publish`): a job trigger isn't a message carrier — rule-4 boundary respected. | keep (provisional) | |
| `Singleton(name, fn, ...Option)` | stub | Phase H. Rule-2 deviation like Cron (name, no payload) — spec-documented. | keep (provisional) | |

## 4. Coordination, config, observability

| Symbol | Status | Assessment | Recommendation | Verdict |
|---|---|---|---|---|
| `Lock(name, ...Option)` / `LockHandle.Do/TryDo(ctx, fn)` | stub | Phase H (lease). `TryDo` returning `(bool, error)` — held-elsewhere is data, not an error. Most users never touch it (singleflight/Singleton are the faces). | keep (provisional) | |
| `Secret(name, ...Option)` / `SecretHandle.Value() string` | live | **The one no-Ctx, no-error read in the SDK** — deliberate rule-3 deviation, load-bearing: boot refuses to start with a declared secret unset, so `Value` *cannot* fail at request time. Consequence (documented): secrets are process-wide, the one backend test apps share. | keep (confirm deviation) | |
| `Counter(name)` / `CounterHandle.Add(ctx, delta)` — no return | live | Rule-3 deviation: fire-and-forget. Metrics that can fail teach users to ignore errors — worse than no error. | keep (confirm deviation) | |
| `Histogram(name)` / `HistogramHandle.Observe(ctx, value)` | live | Same. | keep (confirm deviation) | |

## 5. Ctx, identity, errors, helpers

| Symbol | Status | Assessment | Recommendation | Verdict |
|---|---|---|---|---|
| `Ctx` (interface: `context.Context` + `User/MaybeUser/Log/IsHTMX`) | live | Rule 3's first parameter everywhere. | keep | |
| `Ctx.User() User` (panics unauthenticated) / `MaybeUser() (User, bool)` | live* | Panic-on-unauthenticated is safe only behind `WithAuth` — which is Phase G. Until then every example uses `MaybeUser`. The *shape* is right; the guarantee arrives with auth. | keep | |
| `Ctx.IsHTMX() bool` | live* | **DISCUSS.** A web-specific probe on the universal Ctx. Doctrine says hypermedia is core (settled decision 13), which argues for it; the counterargument is workers asking `IsHTMX()` (always false, meaningless). Alternative: move to a Phase-G page-scoped ctx. Freezing it now means workers keep the method forever. | your call — lean keep, per decision 13 | |
| `Ctx.Log() *slog.Logger` | live | Structured, handler-tagged. | keep | |
| `User{ID, Email, Name}` | live | Minimal by design; Phase G may add fields (additive). | keep | |
| `Errorf(status, format, ...)` / `NotFound()` / `IsNotFound(err)` | live | HTTP-status errors are the API error model; `IsNotFound` spans Entity/Cache/Bucket (spec §2.9). | keep | |
| `Permanent(err)` | live | Rule 5's third outcome. | keep | |
| `NewID() string` | live | 16-char sortable id. One global helper, no config — fine. | keep | |
| `Blob{Reader, ContentType, Size}` | live | The streaming type both directions (upload field + response). `Size: -1` = unknown (documented). | keep | |
| `ObjectInfo{Key, Size, ContentType, Updated}` | live | Bucket listing + event payload (wire shape stable since F5). | keep | |
| `HTML` / `Redirect(target) (HTML, error)` | stub | Phase G. `Redirect` returns `(HTML, error)` so page handlers `return tako.Redirect("/")` in one line — shape chosen for the call site. | keep (provisional) | |

## 6. Web & auth declarations (all Phase G)

| Symbol | Status | Assessment | Recommendation | Verdict |
|---|---|---|---|---|
| `Site(dir, ...Option)` + `Render/Fragment/Markdown` | stub | Spec §2.11 (the polished `Site` primitive). **Note:** spec Part 3's example still shows the older `Static`/`Templates` split — the spec fix-up is a standing Phase-G task; the SDK (normative) has only `Site`. | keep (provisional) | |
| `Auth(opts ...Option)` — **no name arg** | stub | Rule-2 deviation: an app has one auth config, a name would be noise. Worth confirming now since it's the only nameless resource constructor. | keep (confirm deviation) | |
| `SPA()` | stub | Only optionless-noun Option without a `With` prefix. Deliberate: `WithSPA()` reads worse; it's a mode, not a parameter. Confirm the naming exception. | keep (confirm) | |

## 7. Options (rule 2 — one namespace)

All 18 `With*` options + `SPA()` live in one namespace; validity per
constructor is enforced (analyzer at declaration sites, call-time errors
at operation sites — exact texts under golden test).

| Symbol | Status | Notes | Verdict |
|---|---|---|---|
| `WithTTL, WithDelay, WithMaxSize, WithContentType, WithProfile, WithSingleflight` | live | Validated through F4–F9 gates. | |
| `WithRateLimit` | stub | Phase F/H (Cloud Tasks). | |
| `WithAuth, WithPrivate, WithGoogle, WithEmail` | stub | Phase G / Phase E (IAM). | |
| `WithBuild, WithCDN, WithDevProxy, WithDomain, WithRedirect, WithTrustedHTML, SPA` | stub | Phase G (Site). `WithRedirect(from, to)` (site-level) vs `Redirect(target)` (handler return) — adjacent names, different layers; acceptable, flag if it reads badly. | |
| `Profile` + `Default/Latency/Batch` | live | Consts, not strings — typo-proof. | |

## 8. Testing surface (spec §2.10)

| Symbol | Status | Assessment | Recommendation | Verdict |
|---|---|---|---|---|
| `NewTestApp(tb) *TestApp` | live | Boots isolated in-process backends; auto-wires via the F7-approved registry carve-out (the one sanctioned rule-1 side effect, test-only). | keep | |
| `TestApp.SignIn(name) User` / `Ctx(as ...User)` | live | Identity injection; `Ctx()`'s variadic user is the one variadic in the SDK besides options — reads well (`app.Ctx()`, `app.Ctx(u)`). | keep | |
| `TestApp.DrainQueues()` | live | Settles async incl. chaos duplicates; parked (delayed) messages are idle. | keep | |
| `TestApp.Advance(d)` / `AdvanceCron(name)` | live | **DISCUSS.** Two time verbs: `Advance` moves the fake clock (releases `WithDelay`); `AdvanceCron` fires a *named* cron once. Unifying (`Advance(25h)` fires due crons) sounds elegant but is worse: cron tests want "this cron ran once", not clock math that fires unrelated crons too. Recommend keeping both, and wiring crons *additionally* onto the fake clock only if a real need appears. | keep both | |
| `TestApp.Chaos()` | live | Seed from `TAKO_CHAOS_SEED` env or time; prints reproduction line. A `Chaos(seed)` overload was noted as discovered work — env replay has covered every real use so far, and adding a param later is impossible (signature frozen). **If ever, now.** | your call — lean keep as-is | |
| `TestApp.SetSecret(name, value)` | live | Process-wide by necessity (`Value` has no Ctx) — documented, cleanup-scoped. | keep | |

## 9. Deliberate rule deviations — the complete list

Rule discipline means deviations are named, not discovered. These seven
are the only ones; each is load-bearing:

1. `SecretHandle.Value()` — no Ctx, no error (boot guarantees it).
2. `Counter.Add` / `Histogram.Observe` — no error (fire-and-forget).
3. `EntityTx` / `EntityQuery` finishers / `Iter` — no Ctx re-pass
   (captured at creation; prevents cross-context smuggling).
4. `Cron` / `Singleton` — first arg is schedule/name, no bound resource
   (no payload exists; spec §2.8).
5. `TopicHandle.Publish` — no options param (delay is a queue power;
   compile-enforced).
6. `Auth()` — nameless constructor (one auth config per app).
7. Async constructors registering in the test registry — rule-1
   carve-out, F7-approved, test-only, order-independent.

## 10. Open items that are NOT SDK surface

For completeness (they came up during gates, none block the freeze):
example-level `serveImage` fn name and dual `Upload`+`StartUpload` flow
(spec Part 3 fix-ups, Phase G); dead-letter replay CLI; `tako gen graph
--json/--mermaid`; dev orphan hardening; `tako cron run <name>` for dev.

---

## Verdicts

*(to be filled by the reviewer — per-row above, plus any renames here)*

**Renames requested:**

**Gate F10 approval** — I approve freezing the exported API of package
`tako` as snapshotted in `testdata/sdk_lock.golden` at commit `____`:

Signed: ____________  Date: ____________
