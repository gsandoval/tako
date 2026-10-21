# Active Planning & Execution Board: Tako × cfire Dual-Language Examples, Docs & Graphs

**Current Milestone:** Milestone 2 — 100% Dual-Language Examples, Documentation & Visual Architecture Graphs (**COMPLETED**)  
**Directory:** `/Users/memo/Programming/tako/docs/planning`  
**Git Commit Policy:** **NO COMMITS** until after 2026-10-21T19:08:36-04:00 (all changes remain unstaged in the working tree).

---

## 1. Active Task Matrix (Milestone 2)

| ID | Title | Status | Target Files | Verification Gate |
| :--- | :--- | :---: | :--- | :--- |
| **`EX-CF-01`** | Complete Dual-Language Examples Parity (`examples/cfire-*`) | **DONE** | `examples/cfire-{journal,shop,blog,siren}`<br>`examples/README.md`<br>`pkg/analyzer/cfire*.go`<br>`pkg/codegen/cfire_test.go` | `PASS` (`TestCfireAnalyzeExamples` & `TestCfireCodegenExamples` on all 7 examples) |
| **`GRAPH-01`** | Mermaid Graph Rendering in `pkg/docserver/layout.go` | **DONE** | `pkg/docserver/layout.go` | `PASS` (`go test -v ./pkg/docserver/...`) |
| **`GUIDE-DUAL-01`** | Mental Model (`00-mental-model.md`) Dual-Language + 3 Graphs | **DONE** | `docs/guide/00-mental-model.md` | `PASS` (`go test -v ./pkg/docserver/...`) |
| **`GUIDE-DUAL-02`** | Tutorial Chapters (`01`–`07`, `local.md`, `cloud.md`) Dual-Language + Graphs | **DONE** | `docs/guide/01-hello.md` .. `07-cfire.md`<br>`docs/guide/local.md`<br>`docs/guide/cloud.md` | `PASS` (`go test -v ./pkg/docserver/...`) |
| **`REF-DUAL-01`** | HTTP, Data & Async Reference Pages Dual-Language + Graphs | **DONE** | `docs/reference/{api,pages,site,service,domains,entity,cache,observability,alerts,queue,topic,bucket,cron,lock,handler}.md` | `PASS` (`go test -v ./pkg/docserver/...`) |
| **`REF-DUAL-02`** | Compute & Edges Reference Pages Dual-Language + Graphs | **DONE** | `docs/reference/{job,singleton,calls,footprint,auth,testing,client-ts,cli,errors,cfire}.md` | `PASS` (`go test -v ./pkg/docserver/...`) |
| **`VERIFY-DUAL-01`** | Full Conformance Verification Gate (Working Tree Only) | **DONE** | All Modified Packages | `PASS` (`pkg/docserver`, `docs`, `pkg/analyzer`, `pkg/codegen`, `cmd/tako`) |
