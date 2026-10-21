# Tako × cfire Documentation & Examples Backlog

---

## Milestone 1: Initial cfire Surface (COMPLETED)
- **`DOC-CF-01`**: Update Top-Level `README.md` for Polyglot Go & cfire (**DONE**)
- **`DOC-CF-02`**: Author Tutorial Guide Chapter `docs/guide/07-cfire.md` (**DONE**)
- **`DOC-CF-03`**: Author Reference Manual Page `docs/reference/cfire.md` (**DONE**)
- **`DOC-CF-04`**: Update Guide and Reference Indexes (**DONE**)
- **`DOC-CF-05`**: Automated Verification & Docserver Conformance Run (**DONE**)

---

## Milestone 2: 100% Dual-Language Examples, Documentation & Visual Architecture Graphs

### `EX-CF-01`: Complete Dual-Language Examples Parity (`examples/cfire-*`)
- **Scope:**
  - Create `examples/cfire-journal/journal.cf` (posts, moved_urls, site, auth, roles, status page, HTMX fragments).
  - Create `examples/cfire-shop/shop.cf` (products, orders, reports, sequences, settle job/runner, invoicer singleton, auth subscriber).
  - Create `examples/cfire-blog/blog.cf` (comments entity, Hugo static site build, HTMX comment island endpoints).
  - Create `examples/cfire-siren/siren.cf` (work queue, incidents topic, hits counter, work_ms histogram, failure switches, alert declarations).
  - Update `examples/README.md` with the side-by-side table of all 7 Go and `cfire` examples.
  - Extend `pkg/analyzer/cfire.go`, `pkg/analyzer/cfire_test.go`, and `pkg/codegen/cfire_test.go` to support and verify all 7 `cfire-*` examples (`job`, `runner`, `singleton`, `auth`, `role`, `alert`, `channel`, `service` constructors).
- **Definition of Done:**
  - All 7 `examples/cfire-*` directories exist and pass `TestCfireAnalyzeExamples` and `TestCfireCodegenExamples`.
- **Verification Command:**
  ```bash
  go test -v ./pkg/analyzer -run TestCfire && go test -v ./pkg/codegen -run TestCfire
  ```

---

### `GRAPH-01`: Mermaid Graph Rendering in `pkg/docserver/layout.go` & Polyglot Landing Page
- **Scope:**
  - Update `pkg/docserver/layout.go` landing page hero and cards to reflect Go + `cfire` parity.
  - Add Mermaid diagram container styles and progressive client-side rendering in `pkg/docserver/layout.go` for `<code class="language-mermaid">` blocks.
- **Definition of Done:**
  - `tako docs` and `tako docs --export` render Mermaid blocks cleanly and pass `go test -v ./pkg/docserver/...`.

---

### `GUIDE-DUAL-01`: Mental Model (`docs/guide/00-mental-model.md`) Dual-Language + 3 Architecture Graphs
- **Scope:**
  - Update `docs/guide/00-mental-model.md` to show both Go and `cfire` syntax, constructors, and `.tako/` artifacts across all 6 sections.
  - Add 3 Mermaid graphs:
    1. **The Two-Waist Compilation & Reconciliation Pipeline** (`flowchart LR`).
    2. **Package-to-Service Partitioning** (`flowchart LR`).
    3. **Static Call-Graph to Least-Privilege IAM & Composite Index Derivation** (`flowchart LR`).
- **Definition of Done:**
  - Contains 3 valid Mermaid graphs and dual-language Go + `cfire` coverage across every section.

---

### `GUIDE-DUAL-02`: All Tutorial Chapters (`01-hello.md` – `07-cfire.md`, `local.md`, `cloud.md`) Dual-Language + Topology Graphs
- **Scope:**
  - Update `01-hello.md`, `02-shortlink.md`, `03-gallery.md`, `04-journal.md`, `05-shop.md`, `06-blog.md`, `07-cfire.md`, `local.md`, and `cloud.md`.
  - Every tutorial chapter links to both `examples/<name>` and `examples/cfire-<name>`, shows both Go and `cfire` code side-by-side, and includes a Mermaid service/resource topology graph.
  - `local.md` and `cloud.md` explain both Go and `cfire` execution/deployment and include Mermaid architecture graphs.
- **Definition of Done:**
  - Every guide file has Go + `cfire` examples and at least one Mermaid graph.

---

### `REF-DUAL-01`: HTTP, Data & Async Reference Pages Dual-Language + Graphs
- **Scope:**
  - Update `api.md`, `pages.md`, `site.md`, `service.md`, `domains.md`, `entity.md`, `cache.md`, `observability.md`, `alerts.md`, `queue.md`, `topic.md`, `bucket.md`, `cron.md`, `lock.md`, `handler.md`.
  - Add Go + `cfire` code blocks for *Declare* and *Use*.
  - Add Mermaid diagrams to `entity.md` (index derivation), `alerts.md` (signal-to-notification pipeline), `queue.md` (delivery/retry/DLQ flow), `topic.md` (fan-out topology), and `bucket.md` (signed URL + event flow).
- **Definition of Done:**
  - All 15 pages include Go + `cfire` syntax and pass `pkg/docserver` member/mention/unwired-panic checks.

---

### `REF-DUAL-02`: Compute & Edges Reference Pages Dual-Language + Graphs
- **Scope:**
  - Update `job.md`, `singleton.md`, `calls.md`, `footprint.md`, `auth.md`, `testing.md`, `client-ts.md`, `cli.md`, `errors.md`.
  - Add Go + `cfire` code blocks for *Declare* and *Use*.
  - Add Mermaid diagrams to `calls.md` (cross-service OIDC RPC), `auth.md` (session & policy gate flow), `job.md` (parallel task sharding), and `singleton.md` (distributed lease loop).
- **Definition of Done:**
  - All 9 pages include Go + `cfire` coverage and pass `pkg/docserver` golden/rule/CLI checks.

---

### `VERIFY-DUAL-01`: Full Conformance Verification Gate (No Git Commit)
- **Scope:**
  - Run `go test -v ./pkg/docserver/... ./docs/... ./pkg/analyzer -run TestCfire ./pkg/codegen -run TestCfire ./cmd/tako/...`
  - Verify `git status` leaves all changes in the working tree without committing (respecting the 6-hour no-commit hold).
