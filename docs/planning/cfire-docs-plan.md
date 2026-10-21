# Tako × cfire User-Facing Documentation Plan

**Status:** Approved for Implementation  
**Target Repository:** `/Users/memo/Programming/tako`  
**Scope:** Public & User-Facing Documentation Surface (`README.md`, `docs/guide/`, `docs/reference/`, `docs/planning/`)

---

## 1. Executive Summary & Objectives

`tako` has evolved from a single-language framework into a **polyglot cloud framework** that natively supports both **Go** and **cfire** (`.cf`), a modern C++23 systems programming language.

While internal architecture specifications ([`docs/tako-cfire-plan.md`](../tako-cfire-plan.md)) and operational verification playbooks ([`validate/cfire.md`](../../validate/cfire.md)) are complete, the **public, user-facing surface** does not yet reflect these capabilities:
1. **The Front Door (`README.md`)** presents Tako exclusively as *"Write Go. Get a cloud."* Developers discovering Tako have no indication that `.cf` files are first-class citizens.
2. **The Tutorial Story (`docs/guide/`)** walks through six Go chapters (`01-hello` through `06-blog`), but lacks a dedicated end-to-end tutorial chapter showing how to write, run, test, and deploy a native cfire application.
3. **The Feature Reference (`docs/reference/`)** documents Go types and interfaces, but lacks a dedicated cfire reference chapter detailing `import "core:tako"`, struct tags (`entity:"id"`, `entity:"index"`, `path:`, `query:`, `json:`, `header:`), the C-archive ABI bridge (`libtako.a`), and distroless container runtime images (`gcr.io/distroless/cc-debian12:nonroot`).

### Strategic Objectives
- Elevate `cfire` to a first-class language alongside Go in Tako's public story without regressing the Go developer experience.
- Maintain Tako's signature concise, opinionated, prose-first documentation aesthetic.
- Ensure 100% compliance with `pkg/docserver` automated link, member, and symbol verification tests.

---

## 2. Documentation Architecture & Touchpoints

```
tako/
├── README.md                          <-- Update: Polyglot hero, dual-language 5-minute quickstart, cfire overview
├── docs/
│   ├── embed.go                       <-- Invariant: Embeds only guide/, reference/, api/
│   ├── guide/
│   │   ├── README.md                  <-- Update: Index listing cfire tutorial chapter
│   │   └── 07-cfire.md                <-- NEW: Complete cfire worked example (Hello -> Shortlink -> Deploy)
│   ├── reference/
│   │   ├── README.md                  <-- Update: Index listing cfire reference
│   │   └── cfire.md                   <-- NEW: Complete cfire SDK reference (syntax, tags, ABI, builds)
│   └── planning/                      <-- (Excluded from embed.go)
│       ├── cfire-docs-plan.md         <-- This comprehensive plan
│       ├── backlog.md                 <-- Granular, prioritized backlog with DoD
│       └── active.md                  <-- Active execution tracking board
```

### 2.1 Front Door (`README.md`)
- **Headline & Hero:** Expand tagline to encompass systems programming: *"Write Go or cfire. Get a cloud."*
- **Dual-Language Code Showcase:** Side-by-side or sequential worked example showing the exact same endpoint (`GET /greet/{name}`) in both Go and cfire.
- **Five Minutes to a URL:** Provide the quickstart steps for cfire:
  1. `mkdir hello-cf && cd hello-cf`
  2. Write `hello.cf` (`import "core:tako"`)
  3. Run `tako dev` (automatic `.cf` detection, hot reload)
  4. Run `tako deploy --project <id>` (automatic cross-compilation with `libtako.a` on `distroless/cc-debian12`)
- **Worked Examples Index:** Add row for `examples/cfire-hello`, `examples/cfire-shortlink`, and `examples/cfire-gallery`.

### 2.2 The Tutorial Guide (`docs/guide/07-cfire.md`)
Create chapter `07-cfire.md` following the exact chapter format of `01-hello.md` through `06-blog.md`:
- **The Concept:** Why write a cloud service in a high-performance C++23 systems language with Tako.
- **The Source:** Complete, runnable `.cf` application with request binding, entity persistence, and background queue workers:
  ```cfire
  package shortlink
  import "core:tako"
  
  type Link struct {
      code:     str `entity:"id" json:"code"`
      url:      str `json:"url"`
      clicks:   i64 `entity:"index" json:"clicks"`
  }
  
  Links := tako.entity[Link]("links")
  Clicks := tako.queue[Link]("clicks")
  GetLink := tako.api("GET /links/{code}", handle_get)
  ...
  ```
- **The Local Loop:** What happens when typing `tako dev` in a cfire directory:
  - Generates `.tako/gen/<service>/main.cf`
  - Compiles `libtako.a` via Go `c-archive`
  - Links via `cfire build main.cf -o .tako/bin/<service> --link-lib .tako/lib/libtako.a --include <sdk_dir>`
  - Runs in-process with HTTP server and BoltDB storage.
- **Testing:** How to write and run unit tests with `tako test` using `*_test.cf`.
- **Deploying to Cloud Run:** The single-command deploy and OCI layer construction.

### 2.3 The Reference Manual (`docs/reference/cfire.md`)
Create `cfire.md` inside `docs/reference/` matching the canonical 7-section structure:
1. **Declare:** Top-level package declarations (`tako.entity[T]`, `tako.bucket`, `tako.queue[T]`, `tako.topic[T]`, `tako.cache[T]`, `tako.lock`, `tako.secret`, `tako.api`, `tako.worker`, `tako.cron`).
2. **Use:** Method calls on declared handles (`Links.get(ctx, id)`, `Links.put(ctx, entity)`, `Clicks.publish(ctx, item)`).
3. **Local:** In-process C-ABI mechanics, thread safety, and memory management (`tako_buf_t`, `tako_c_free`).
4. **Cloud:** Container packaging on `gcr.io/distroless/cc-debian12:nonroot`, glibc/libstdc++ dynamic linkage, IAM least-privilege projection.
5. **Test it:** In-package tests with `*_test.cf`.
6. **Errors you'll meet:** Compiler and analyzer static diagnostic table (`tako/entity-id`, `tako/route-params`, `tako/const-name`).
7. **Not yet:** Current limits (e.g. server-side HTML template engine in cfire vs Go html/template).

---

## 3. Strict Verification & Acceptance Rubric

Every user-facing documentation change MUST pass the following automated gates:
1. **Zero Docserver Regressions:**
   ```bash
   go test -v ./pkg/docserver/...
   ```
   Ensures:
   - `TestEveryLinkBetweenDocsResolves`: Every Markdown link resolves to an existing file and valid anchor.
   - `TestNavCoversEveryPage`: Every page in `guide/` and `reference/` appears in the navigation index.
   - `TestEveryAnalyzerRuleIsInTheErrorsIndex`: All static analysis rules are documented in `reference/errors.md`.
   - `TestExportGeneratesCompleteStaticSite`: Static documentation generator (`tako docs --export`) renders without error.
2. **Zero Doc Surface Regressions:**
   ```bash
   go test -v ./docs/...
   ```
   Ensures `docs/embed.go` and `api/surface.txt` remain byte-for-byte compliant.
3. **Full Conformance & Example Verification:**
   ```bash
   go test -v ./cmd/tako/... ./pkg/analyzer -run TestCfire ./pkg/codegen -run TestCfire
   ```
   Ensures all CLI commands and cfire analyzers continue to function flawlessly.
