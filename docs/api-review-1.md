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
| `Bucket(name, ...Option) *BucketHandle` | live | Rules 1–2 clean. | keep | **yes** — clean fit, nothing to add |
| `BucketHandle.Get/Put/Delete/Exists` | live | Rule 4 exactly. | keep | **yes** |
| `BucketHandle.List(ctx, prefix) Iter[ObjectInfo]` | live | Only keyed store with `List`. Justified asymmetry: buckets list by key **prefix**; entities query by **indexed field** (`Query`). A shared name would lie about the semantics. | keep | **yes** — the asymmetry is real; a unified `List` would promise semantics buckets can't deliver |
| `BucketHandle.SignedPut(ctx, key, ttl, ...Option)` / `SignedGet(ctx, key, ttl)` | live | `ttl` positional (mandatory), constraints as options — deliberate: no signed URL without an expiry. F6-validated. | keep | **yes** — mandatory-as-positional is the right forcing function; an optional TTL would ship unbounded URLs |
| `BucketHandle.Uploaded` (field, `BucketEvent`) | live | The bucket-event binding (`tako.Subscribe(photos.Uploaded, …)`). Compile-checked, reads naturally. Only `Uploaded` exists; `Deleted` etc. would be additive. | keep | **yes** — field-as-event-source is the best-reading binding in the SDK; growth is additive |
| `Entity[T](name, ...Option) *EntityHandle[T]` | live | Rule 6's showcase: *an* entity store, never "the database". | keep | **yes** |
| `EntityHandle.Get/Put/Delete/Exists` | live | Rule 4. Key is `id string` (from the `entity:"id"` tag) vs bucket's `key` — parameter names differ where semantics differ. | keep | **yes** — param names carrying semantics is right |
| `EntityHandle.Update(ctx, id, mutate func(*T))` | live | Read-modify-write sugar over `Tx`. Not part of the rule-4 vocabulary but earns its place (shortlink, gallery both need it). | keep | **yes** — two examples demand it, which is the bar |
| `EntityHandle.Tx(ctx, fn func(EntityTx[T]) error)` | live | Rule 6: transactions scoped to the store instance. | keep | **yes** |
| `EntityTx[T]` — `Get/Put/Delete(id)` **without Ctx** | live | Deliberate rule-3 deviation: the tx closure is already ctx-scoped by `Tx`; re-passing Ctx would invite smuggling the tx across contexts. | keep (confirm deviation) | **yes, deviation confirmed** — closure-scoped tx is idiomatic Go (cf. `sql.Tx` callbacks); re-passing Ctx adds a wrong-ctx failure mode with zero benefit |
| `EntityHandle.Query(ctx) *EntityQuery[T]` + `Where/OrderBy/Limit/All/First/Iter` | live | Builder chain; `Where(field, op, value)` fields are analyzer-checked against `entity:"index"` tags (const-query-field, unindexed-query rules). `All/First/Iter` close the chain without re-taking Ctx (captured at `Query`) — same tx-style deviation. | keep (confirm deviation) | **yes, deviation confirmed** — same reasoning as Tx; stringly `Where` is acceptable only because the analyzer checks fields, which it does |
| `Iter[T]` — `Next() (T, bool)` / `Err()` | live | Standard Go cursor idiom; no Ctx on `Next` (captured at creation). | keep (confirm deviation) | **yes, deviation confirmed** — matches `bufio.Scanner`/`sql.Rows` idiom every Go user knows |
| `Cache[T](name, ...Option) *CacheHandle[T]` | live | Per-instance in-memory, honestly documented. | keep | **yes** — honesty about per-instance scope is the load-bearing part; keep it loud in docs |
| `CacheHandle.Get/Put/Delete/Exists` | live | Rule 4; absent-or-expired ⇒ `IsNotFound`. | keep | **yes** — collapsing absent/expired is correct for a cache; callers must not care which |
| `CacheHandle.GetOrFill(ctx, key, fill)` | live | Name candidates were `GetOrSet` (wrong: fill computes) and `Fetch` (opaque). `GetOrFill` says what happens. Concurrent misses may double-fill (documented; singleflight is a compatible later upgrade). | keep | **yes** — best of the three names; double-fill documented and upgrade-compatible |

## 2. Message carriers — Queue, Topic (rules 2, 3, 4, 5)

| Symbol | Status | Assessment | Recommendation | Verdict |
|---|---|---|---|---|
| `Queue[T](name, ...Option)` / `Topic[T](name, ...Option)` | live | Rule 4: both `Publish`. Queue=work (one Worker), Topic=events (N Subscribers) — the distinction is doctrine (settled decision 6). | keep | **yes** — the one distinction users must learn, and the API teaches it |
| `QueueHandle.Publish(ctx, msg, ...Option)` | live | Options: `WithDelay` (F8). Bad options error at call time with the standard text. | keep | **yes** |
| `TopicHandle.Publish(ctx, event)` | live | **No opts param** — delay is a queue power, enforced by the compiler. Asymmetric signatures are the honest choice; adding `...Option` "for symmetry" would create a runtime error where a compile error now lives. | keep (confirm asymmetry) | **yes, asymmetry confirmed** — trading a compile error for cosmetic symmetry would be strictly worse; if topics ever earn a real option, adding `...Option` then is source-compatible |
| `EventSource[T]` (sealed interface) | live | Unexported methods seal it: only `TopicHandle[T]` and `BucketEvent` can be subscription sources. Sealing is the feature. | keep | **yes** — sealing keeps `Subscribe` sources enumerable for the analyzer; that's the contract |

## 3. Handler constructors (rules 1, 2, 3, 5)

| Symbol | Status | Assessment | Recommendation | Verdict |
|---|---|---|---|---|
| `API[Req, Res](route, fn, ...Option) Endpoint[Req, Res]` | live | Rule 3's showcase. Route is const (analyzer-enforced). | keep | **yes** |
| `Endpoint.Call(ctx, req)` | live | The var IS the callable surface; cross-package Call is the RPC (spec §2.12, Phase H exercises it remotely). | keep | **yes** — one callable surface for local, test, and remote is the framework's core bet; right shape |
| `Worker(queue, fn, ...Option)` / `Subscribe(source, fn, ...Option)` | live | Rule 5 exactly; binding is the compile-checked first argument. | keep | **yes** |
| `Cron(schedule, fn, ...Option)` | live | Rule-2 deviation, documented in spec §2.8: no payload ⇒ first arg is the schedule, not a resource. v0 schedule syntax `"every <duration>"` (tako/cron-schedule). | keep (confirm deviation) | **yes, deviation confirmed** — inventing a `CronHandle` resource just to satisfy rule 2 would be ceremony; schedule-first reads right |
| `Handler` (opaque struct) | live | Return type of async/scheduled constructors. Opaque on purpose — the var is registration + docs, not an API. | keep | **yes** — opacity prevents an accidental second API growing on registration vars |
| `Page[Req](route, fn, ...Option) Endpoint[Req, HTML]` | stub | Phase G. Shape mirrors `API` with `HTML` result — consistent. | keep (provisional) | **yes (provisional)** — `Endpoint[Req, HTML]` reuse means pages get `Call` in tests for free; good |
| `JobHandler(job, fn, ...Option)` / `Job[T](name, ...Option)` / `JobHandle.Run(ctx, params)` | stub | Phase H. `Run` (not `Publish`): a job trigger isn't a message carrier — rule-4 boundary respected. | keep (provisional) | **yes (provisional)** — `Run` vs `Publish` keeps rule 4's vocabulary honest |
| `Singleton(name, fn, ...Option)` | stub | Phase H. Rule-2 deviation like Cron (name, no payload) — spec-documented. | keep (provisional) | **yes (provisional)** — same reasoning as Cron |

## 4. Coordination, config, observability

| Symbol | Status | Assessment | Recommendation | Verdict |
|---|---|---|---|---|
| `Lock(name, ...Option)` / `LockHandle.Do/TryDo(ctx, fn)` | stub | Phase H (lease). `TryDo` returning `(bool, error)` — held-elsewhere is data, not an error. Most users never touch it (singleflight/Singleton are the faces). | keep (provisional) | **yes (provisional)** — held-elsewhere as `(bool, error)` is exactly right; contention is an expected outcome, not a failure |
| `Secret(name, ...Option)` / `SecretHandle.Value() string` | live | **The one no-Ctx, no-error read in the SDK** — deliberate rule-3 deviation, load-bearing: boot refuses to start with a declared secret unset, so `Value` *cannot* fail at request time. Consequence (documented): secrets are process-wide, the one backend test apps share. | keep (confirm deviation) | **yes, deviation confirmed** — the boot-time guarantee is what makes it sound; verified the panic path only fires on test misuse with an instructive message (secret.go). Process-wide consequence is contained by `SetSecret`'s cleanup |
| `Counter(name)` / `CounterHandle.Add(ctx, delta)` — no return | live | Rule-3 deviation: fire-and-forget. Metrics that can fail teach users to ignore errors — worse than no error. | keep (confirm deviation) | **yes, deviation confirmed** — an error nobody will check is API noise; fire-and-forget is the industry norm for metrics (expvar, statsd, otel counters) |
| `Histogram(name)` / `HistogramHandle.Observe(ctx, value)` | live | Same. | keep (confirm deviation) | **yes, deviation confirmed** — same. (Doc nit: both constructors actually take `...Option` per the lock — rows above elide it; keep the uniform rule-2 shape) |

## 5. Ctx, identity, errors, helpers

| Symbol | Status | Assessment | Recommendation | Verdict |
|---|---|---|---|---|
| `Ctx` (interface: `context.Context` + `User/MaybeUser/Log/IsHTMX`) | live | Rule 3's first parameter everywhere. | keep | **yes** |
| `Ctx.User() User` (panics unauthenticated) / `MaybeUser() (User, bool)` | live* | Panic-on-unauthenticated is safe only behind `WithAuth` — which is Phase G. Until then every example uses `MaybeUser`. The *shape* is right; the guarantee arrives with auth. | keep | **yes** — the pair is right; panic message is instructive (points at `MaybeUser`). Phase G must make `WithAuth`⇒`User` a tested guarantee, not just docs |
| `Ctx.IsHTMX() bool` | live* | **DISCUSS.** A web-specific probe on the universal Ctx. Doctrine says hypermedia is core (settled decision 13), which argues for it; the counterargument is workers asking `IsHTMX()` (always false, meaningless). Alternative: move to a Phase-G page-scoped ctx. Freezing it now means workers keep the method forever. | your call — lean keep, per decision 13 | **yes, keep** — a second page-scoped ctx type would fracture "one Ctx everywhere" (rule 3's real value: one signature, one test helper) to save workers a harmless always-false method. `context.Context` already pays this cost (`Deadline` on ctxs that never expire). Condition: record that `IsHTMX` is the *only* request-shape probe Ctx gets — the next web probe (`IsJSON`? `Accepts`?) reopens this row rather than riding the precedent |
| `Ctx.Log() *slog.Logger` | live | Structured, handler-tagged. | keep | **yes** |
| `User{ID, Email, Name}` | live | Minimal by design; Phase G may add fields (additive). | keep | **yes** |
| `Errorf(status, format, ...)` / `NotFound()` / `IsNotFound(err)` | live | HTTP-status errors are the API error model; `IsNotFound` spans Entity/Cache/Bucket (spec §2.9). | keep | **yes** — one not-found predicate across all keyed stores is rule 4 paying off |
| `Permanent(err)` | live | Rule 5's third outcome. | keep | **yes** |
| `NewID() string` | live | 16-char sortable id. One global helper, no config — fine. | keep | **yes** |
| `Blob{Reader, ContentType, Size}` | live | The streaming type both directions (upload field + response). `Size: -1` = unknown (documented). | keep | **yes** |
| `ObjectInfo{Key, Size, ContentType, Updated}` | live | Bucket listing + event payload (wire shape stable since F5). | keep | **yes** — wire-stable since F5; freezing is overdue |
| `HTML` / `Redirect(target) (HTML, error)` | stub | Phase G. `Redirect` returns `(HTML, error)` so page handlers `return tako.Redirect("/")` in one line — shape chosen for the call site. | keep (provisional) | **yes (provisional)** — shape-for-the-call-site is the right bias; htmx-aware redirect semantics (decision 13) land behind it without surface change |

## 6. Web & auth declarations (all Phase G)

| Symbol | Status | Assessment | Recommendation | Verdict |
|---|---|---|---|---|
| `Site(dir, ...Option)` + `Render/Fragment/Markdown` | stub | Spec §2.11 (the polished `Site` primitive). **Note:** spec Part 3's example still shows the older `Static`/`Templates` split — the spec fix-up is a standing Phase-G task; the SDK (normative) has only `Site`. | keep (provisional) | **yes (provisional)** — one primitive beats the `Static`/`Templates` split (spec §2.1 also still shows the old split — same fix-up). `Render`/`Fragment`/`Markdown` names are clear |
| `Auth(opts ...Option)` — **no name arg** | stub | Rule-2 deviation: an app has one auth config, a name would be noise. Worth confirming now since it's the only nameless resource constructor. | keep (confirm deviation) | **yes, deviation confirmed** — a name implies you can have several; you can't. A mandatory-but-meaningless name would be the actual rule violation (false generality) |
| `SPA()` | stub | Only optionless-noun Option without a `With` prefix. Deliberate: `WithSPA()` reads worse; it's a mode, not a parameter. Confirm the naming exception. | keep (confirm) | **yes, exception confirmed** — `WithSPA()` reads like a parameter it isn't; low precedent risk while it's documented as the sole exception |

## 7. Options (rule 2 — one namespace)

All 18 `With*` options + `SPA()` live in one namespace; validity per
constructor is enforced (analyzer at declaration sites, call-time errors
at operation sites — exact texts under golden test).

| Symbol | Status | Notes | Verdict |
|---|---|---|---|
| `WithTTL, WithDelay, WithMaxSize, WithContentType, WithProfile, WithSingleflight` | live | Validated through F4–F9 gates. | **yes** — gate-validated, no concerns |
| `WithRateLimit` | stub | Phase F/H (Cloud Tasks). | **yes (provisional)** |
| `WithAuth, WithPrivate, WithGoogle, WithEmail` | stub | Phase G / Phase E (IAM). | **yes (provisional)** |
| `WithBuild, WithCDN, WithDevProxy, WithDomain, WithRedirect, WithTrustedHTML, SPA` | stub | Phase G (Site). `WithRedirect(from, to)` (site-level) vs `Redirect(target)` (handler return) — adjacent names, different layers; acceptable, flag if it reads badly. | **yes (provisional)** — `WithRedirect`/`Redirect` adjacency is fine: different arity, different position (option vs return), hard to confuse in practice |
| `WithParallel(n)` | stub | **Added in review** — in the lock (Job fan-out, Phase H; `tako.JobHandler(reidx, reindex, tako.WithParallel(10))`) but omitted from this table. No verdict would have meant freezing it unreviewed. | **yes (provisional)** — shape and name consistent with the family; Phase H may reopen with Jobs |
| `Profile` + `Default/Latency/Batch` | live | Consts, not strings — typo-proof. | **yes** — typed consts over strings is right; note `Default`/`Latency`/`Batch` are broad top-level names, acceptable cost of typo-proofing |

## 8. Testing surface (spec §2.10)

| Symbol | Status | Assessment | Recommendation | Verdict |
|---|---|---|---|---|
| `NewTestApp(tb) *TestApp` | live | Boots isolated in-process backends; auto-wires via the F7-approved registry carve-out (the one sanctioned rule-1 side effect, test-only). | keep | **yes** |
| `TestApp.SignIn(name) User` / `Ctx(as ...User)` | live | Identity injection; `Ctx()`'s variadic user is the one variadic in the SDK besides options — reads well (`app.Ctx()`, `app.Ctx(u)`). | keep | **yes** — the variadic-user reads well; arguably an eighth deviation (rule 3's "no variadic surprises") — add it to §9 so the list stays complete |
| `TestApp.DrainQueues()` | live | Settles async incl. chaos duplicates; parked (delayed) messages are idle. | keep | **yes** — parked-is-idle is the subtle right call; without it every `WithDelay` test would hang |
| `TestApp.Advance(d)` / `AdvanceCron(name)` | live | **DISCUSS.** Two time verbs: `Advance` moves the fake clock (releases `WithDelay`); `AdvanceCron` fires a *named* cron once. Unifying (`Advance(25h)` fires due crons) sounds elegant but is worse: cron tests want "this cron ran once", not clock math that fires unrelated crons too. Recommend keeping both, and wiring crons *additionally* onto the fake clock only if a real need appears. | keep both | **yes, keep both** — verified: `Advance` only releases parked queue messages; crons fire only by name. Two verbs match the two test intents ("time passed" vs "this cron ran"), and a unified verb firing unrelated crons would make tests order-fragile. Fix-up: comment at testapp.go:110 still says "Cron joins this clock in F9" — it didn't; F9 shipped `AdvanceCron` instead |
| `TestApp.Chaos()` | live | Seed from `TAKO_CHAOS_SEED` env or time; prints reproduction line. A `Chaos(seed)` overload was noted as discovered work — env replay has covered every real use so far, and adding a param later is impossible (signature frozen). **If ever, now.** | your call — lean keep as-is | **yes, keep as-is** — env replay is the better reproduction channel (rerun from CI without editing source). A seed pinned in test code is a trap: the seed's meaning depends on dispatcher internals, so any refactor silently changes what it reproduces — the durable regression artifact is a deterministic test of the interleaving, not a magic number. Also "impossible later" is overstated: `Chaos(seed ...int64)` keeps every existing call site compiling if a real need ever appears, so the cost of deciding "no" today is low |
| `TestApp.SetSecret(name, value)` | live | Process-wide by necessity (`Value` has no Ctx) — documented, cleanup-scoped. | keep | **yes** — the LIFO-cleanup drain (settle deliveries before unsetting) shows the sharp edge is handled |

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
8. `TestApp.Ctx(as ...User)` — the one non-Option variadic (rule 3's
   "no variadic surprises"); test-only, added in this review so the
   list stays complete. `app.Ctx()` / `app.Ctx(u)` is the whole use.

## 10. Open items that are NOT SDK surface

For completeness (they came up during gates, none block the freeze):
example-level `serveImage` fn name and dual `Upload`+`StartUpload` flow
(spec Part 3 fix-ups, Phase G); dead-letter replay CLI; `tako gen graph
--json/--mermaid`; dev orphan hardening; `tako cron run <name>` for dev.

---

## Verdicts

*(per-row verdicts above are the senior-review recommendation, written
2026-07-23 after checking every claim against the code — the lock file,
`ctx.go`, `testapp.go`, `secret.go`, `options.go`, the local dispatcher,
and example usage. Final authority and the signature below remain the
human reviewer's.)*

**Summary: approve the freeze — every row is a yes.** All 132 lock
entries reviewed; no renames. The seven claimed deviations are each
load-bearing and verified in code. On the four contested rows: keep
`IsHTMX` on Ctx (with the "only request-shape probe" fence recorded in
its row), keep `Advance`/`AdvanceCron` as two verbs, keep `Chaos()`
env-seeded, keep `Auth()` nameless.

**Conditions to resolve before signing (none change the API surface):**

1. `WithParallel(n)` was in the lock but absent from §7 — row added
   during this review; it needs your explicit verdict like every other
   symbol.
2. Stale comment `testapp.go:110` ("Cron joins this clock in F9") —
   crons never joined the fake clock; `AdvanceCron` shipped instead.
   Fix the comment so the frozen doc and code agree.
3. §9 lists seven deviations; `TestApp.Ctx(as ...User)` is arguably an
   eighth (rule 3's "no variadic surprises", test-only). Add it or
   record why it doesn't count — the list's value is being complete.

**Renames requested:** none.

**Conditions resolved (2026-07-23):** (1) `WithParallel` row stands with
its provisional yes; (2) the stale `testapp.go` comment is fixed —
crons fire by name via `AdvanceCron`, never via the fake clock; (3)
`TestApp.Ctx(as ...User)` recorded as deviation #8 in §9.

**Gate F10 approval** — I approve freezing the exported API of package
`tako` as snapshotted in `testdata/sdk_lock.golden` at commit `384b9cf`:

Signed: memo (darchitect) — approval given via gate instruction
"mark F9 and F10 as validated"  Date: 2026-07-23
