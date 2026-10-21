# Active Planning & Execution Board: Tako × cfire User-Facing Documentation

**Current Milestone:** Milestone 1 — User-Facing Documentation Surface (COMPLETED)  
**Directory:** `/Users/memo/Programming/tako/docs/planning`  
**Last Updated:** 2026-10-21  

---

## 1. Active Task Matrix

| ID | Title | Status | Assignee | Target Files | Verification Gate |
| :--- | :--- | :---: | :---: | :--- | :--- |
| **`DOC-CF-01`** | Update `README.md` for Polyglot Go & cfire | **DONE** | Coding Agent | `README.md` | Verified dual-language hero, 5-min cfire quickstart, and chapter index |
| **`DOC-CF-02`** | Author Guide `docs/guide/07-cfire.md` | **DONE** | Coding Agent | `docs/guide/07-cfire.md` | `PASS: TestEveryEmbeddedPageRenders` |
| **`DOC-CF-03`** | Author Reference `docs/reference/cfire.md` | **DONE** | Coding Agent | `docs/reference/cfire.md` | `PASS: TestEveryLinkBetweenDocsResolves` (193 internal links, 66 repo links) |
| **`DOC-CF-04`** | Update Guide & Reference Indexes | **DONE** | Coding Agent | `docs/guide/README.md`<br>`docs/reference/README.md` | `PASS: TestNavCoversEveryPage` & `PASS: TestSectionsComeFromTheReferenceIndex` |
| **`DOC-CF-05`** | End-to-End Docserver Conformance Verification | **DONE** | Coding Agent | All Doc Packages & `pkg/docserver/export_test.go` | `PASS: go test -v ./pkg/docserver/... ./docs/...` |

---

## 2. Definition of Done (DoD) Evaluation

All 5 evaluation criteria satisfied:
1. **Fidelity:** Documentation matches actual behavior (`cfire outline --json`, `EnsureLibTako`, `distroless/cc-debian12:nonroot`, struct tags).
2. **Style & Tone:** Adheres to Tako's concise, opinionated prose and 7-section reference format.
3. **Automated Link Resolution:** `TestEveryLinkBetweenDocsResolves` verified 193 internal links, 2 anchors, and 66 repository file links with zero broken targets.
4. **Nav & Static Export Coverage:** `TestNavCoversEveryPage` and `TestExportGeneratesCompleteStaticSite` verify that `/guide/07-cfire` and `/reference/cfire` appear in navigation and export cleanly to HTML.
5. **Zero Doc Surface Regressions:** `TestEmbeddedSurfaceMatchesTheSDKLock` in `docs/surface_test.go` and `TestEveryAnalyzerRuleIsInTheErrorsIndex` in `pkg/docserver/rules_test.go` pass 100%.
