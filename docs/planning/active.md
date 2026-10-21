# Active Planning & Execution Board: Tako × cfire User-Facing Documentation

**Current Milestone:** Milestone 1 — User-Facing Documentation Surface  
**Directory:** `/Users/memo/Programming/tako/docs/planning`  
**Last Updated:** 2026-10-21  

---

## 1. Active Task Matrix

| ID | Title | Status | Assignee | Target Files | Verification Gate |
| :--- | :--- | :---: | :---: | :--- | :--- |
| **`DOC-CF-01`** | Update `README.md` for Polyglot Go & cfire | **TODO** | Coding Agent | `README.md` | `git diff README.md` |
| **`DOC-CF-02`** | Author Guide `docs/guide/07-cfire.md` | **TODO** | Coding Agent | `docs/guide/07-cfire.md` | `go test -v ./pkg/docserver -run TestEveryEmbeddedPageRenders` |
| **`DOC-CF-03`** | Author Reference `docs/reference/cfire.md` | **TODO** | Coding Agent | `docs/reference/cfire.md` | `go test -v ./pkg/docserver -run TestEveryLinkBetweenDocsResolves` |
| **`DOC-CF-04`** | Update Guide & Reference Indexes | **TODO** | Coding Agent | `docs/guide/README.md`<br>`docs/reference/README.md` | `go test -v ./pkg/docserver -run TestNavCoversEveryPage` |
| **`DOC-CF-05`** | End-to-End Docserver Conformance Verification | **TODO** | Coding Agent | All Doc Packages | `go test -v ./pkg/docserver/... ./docs/...` |

---

## 2. Definition of Done (DoD) Criteria

Before any task is marked **DONE**, it must satisfy:
1. **Fidelity:** The documentation reflects actual, tested behavior in the codebase (e.g. `cfire outline --json`, `EnsureLibTako`, `distroless/cc-debian12`, struct tags).
2. **Style & Tone:** Matches Tako's concise, opinionated, prose-first aesthetic.
3. **Automated Link Resolution:** `TestEveryLinkBetweenDocsResolves` in `pkg/docserver` passes with 0 broken links and 0 broken anchors.
4. **Nav Coverage:** `TestNavCoversEveryPage` in `pkg/docserver` confirms that every new page is reachable from the navigation index.
5. **Zero Doc Surface Regressions:** `TestEmbeddedSurfaceMatchesTheSDKLock` in `docs/surface_test.go` passes without changes to `api/surface.txt`.

---

## 3. Immediate Next Steps

1. Execute **`DOC-CF-01`**: Update top-level `README.md`.
2. Execute **`DOC-CF-02`**: Draft and review `docs/guide/07-cfire.md`.
3. Execute **`DOC-CF-03`**: Draft and review `docs/reference/cfire.md`.
4. Execute **`DOC-CF-04`**: Link pages into `docs/guide/README.md` and `docs/reference/README.md`.
5. Execute **`DOC-CF-05`**: Run full test suite across `pkg/docserver` and `docs`.
