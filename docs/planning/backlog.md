# Tako × cfire Documentation Backlog

This backlog tracks the tasks required to bring user-facing documentation in Tako up to 100% parity with its native `cfire` systems programming capabilities.

---

## Backlog Items

### `DOC-CF-01`: Update Top-Level `README.md` for Polyglot Go & cfire
- **Scope:** Modify `/Users/memo/Programming/tako/README.md`.
- **Requirements:**
  1. Update headline/tagline: *"Write Go or cfire. Get a cloud."*
  2. Add dual-language code tabs or showcase for the `GET /greet/{name}` API in both Go and cfire.
  3. Add a dedicated **Five minutes to a cfire URL** section (`mkdir hello-cf`, `tako dev`, `tako deploy`).
  4. Update the **Where to next** table to include `examples/cfire-hello`, `examples/cfire-shortlink`, and `examples/cfire-gallery`.
- **Definition of Done:**
  - `README.md` contains accurate, working cfire code snippets matching `examples/cfire-hello/hello.cf`.
  - All Markdown links resolve to real files in the repository.
- **Verification Command:**
  ```bash
  git diff README.md
  ```

---

### `DOC-CF-02`: Author Tutorial Guide Chapter `docs/guide/07-cfire.md`
- **Scope:** Create `/Users/memo/Programming/tako/docs/guide/07-cfire.md`.
- **Requirements:**
  1. Follow the canonical structure of chapters `01-hello.md` through `06-blog.md`.
  2. Detail how to build a complete service with endpoints, entities, queue workers, and cron schedules in cfire.
  3. Explain the local dev loop (`tako dev` with `.cf` hot reload and `libtako.a` C-archive compilation).
  4. Explain testing with `tako test` using `*_test.cf`.
  5. Explain single-binary distroless container builds on `gcr.io/distroless/cc-debian12:nonroot`.
- **Definition of Done:**
  - Written in Tako's prose style.
  - Passes `docserver` rendering and link validation without errors.
- **Verification Command:**
  ```bash
  go test -v ./pkg/docserver -run TestEveryEmbeddedPageRenders
  ```

---

### `DOC-CF-03`: Author Reference Manual Page `docs/reference/cfire.md`
- **Scope:** Create `/Users/memo/Programming/tako/docs/reference/cfire.md`.
- **Requirements:**
  1. Follow the canonical 7-section reference shape:
     - **Declare**: `tako.entity[T]`, `tako.bucket`, `tako.queue[T]`, `tako.topic[T]`, `tako.cache[T]`, `tako.lock`, `tako.secret`, `tako.api`, `tako.worker`, `tako.cron`.
     - **Use**: Method APIs on declared handles (`get`, `put`, `delete`, `query`, `publish`, `inc`, `observe`).
     - **Local**: C-ABI boundary (`libtako.a`), memory management (`tako_buf_t`, `tako_c_free`), and thread safety.
     - **Cloud**: Distroless runtime image, glibc/libstdc++ dynamic linkage, IAM least-privilege projection.
     - **Test it**: Writing test blocks with `tako test`.
     - **Errors you'll meet**: Rule explanations for `tako/entity-id`, `tako/route-params`, `tako/const-name`.
     - **Not yet**: Current feature boundaries (e.g. server-side HTML template engine).
- **Definition of Done:**
  - Adheres strictly to the 7-section reference structure.
  - All links to other reference pages resolve cleanly.
- **Verification Command:**
  ```bash
  go test -v ./pkg/docserver -run TestEveryLinkBetweenDocsResolves
  ```

---

### `DOC-CF-04`: Update Guide and Reference Indexes (`docs/guide/README.md` & `docs/reference/README.md`)
- **Scope:** Modify `/Users/memo/Programming/tako/docs/guide/README.md` and `/Users/memo/Programming/tako/docs/reference/README.md`.
- **Requirements:**
  1. Add chapter `07-cfire.md` to `docs/guide/README.md` under the worked examples list.
  2. Add `cfire.md` to `docs/reference/README.md` under a new or appropriate section (e.g. "Languages & Runtimes" or "Systems Programming").
- **Definition of Done:**
  - `docserver` navigation covers all pages (`TestNavCoversEveryPage` passes).
  - All intra-doc links resolve cleanly.
- **Verification Command:**
  ```bash
  go test -v ./pkg/docserver -run TestNavCoversEveryPage
  ```

---

### `DOC-CF-05`: Automated Verification & Docserver Conformance Run
- **Scope:** Full validation of the embedded documentation engine.
- **Requirements:**
  1. `go test -v ./pkg/docserver/...` passes 100%.
  2. `go test -v ./docs/...` passes 100%.
  3. `tako docs --export <dir>` builds the static documentation site with the new cfire pages included.
- **Definition of Done:**
  - Zero broken links, zero invalid anchors, zero missing pages in nav.
  - Conformance test exit code 0.
- **Verification Command:**
  ```bash
  go test -v ./pkg/docserver/... && go test -v ./docs/...
  ```
