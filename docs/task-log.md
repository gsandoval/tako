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
