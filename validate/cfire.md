# Validation: cfire Apps on Tako

This playbook validates that native cfire applications (`examples/cfire-*`) are first-class Tako applications with zero behavioral compromises.

## 1. Static Verification & Topology Graph

### `examples/cfire-hello`
```bash
cd /Users/memo/Programming/tako/examples/cfire-hello
go run ../../cmd/tako gen graph
go run ../../cmd/tako gen graph --mermaid
go run ../../cmd/tako gen graph --json
```

**Expected Result:**
- Output lists service `cfire-hello` with routes `GET /hello`, `GET /greet/{name}`, and `POST /echo`.
- Mermaid diagram renders cleanly without errors.
- JSON output matches `analyzer.AppGraph`.

### `examples/cfire-shortlink`
```bash
cd /Users/memo/Programming/tako/examples/cfire-shortlink
go run ../../cmd/tako gen graph
go run ../../cmd/tako gen graph --mermaid
```

**Expected Result:**
- Output identifies:
  - `entity links` with indexed fields `clicks`, `owner_id`
  - `queue clicks`
  - `cache hot-urls`
  - handlers: `CreateLink`, `GetLink`, `ProcessClick`, `PruneExpired`
- Mermaid diagram correctly renders queue wire: `r_queue_Clicks --> h_shortlink_ProcessClick`.

### `examples/cfire-gallery`
```bash
cd /Users/memo/Programming/tako/examples/cfire-gallery
go run ../../cmd/tako gen graph
go run ../../cmd/tako gen graph --mermaid
```

**Expected Result:**
- Output identifies `entity photos`, `bucket assets`, `UploadPhoto`, and `GetPhoto`.

## 2. Code Generation & Artifacts

Run generation across any of the cfire examples:
```bash
cd /Users/memo/Programming/tako/examples/cfire-hello
go run ../../cmd/tako gen code
```

**Expected Result:**
- Emits human-readable `.tako/gen/cfire-hello/main.cf`.
- Emits in-package test harness hook `.tako/gen/cfire-hello/harness/tako_harness.cf`.

## 3. Static Diagnostics & Rule Verification

Run the Tako analyzer against a broken Tako app:
```bash
go run /Users/memo/Programming/tako/cmd/tako gen graph /path/to/badapp
```

**Expected Result:**
- Emits standardized diagnostic format `file.cf:line:col: message (tako/rule-id)`.
- Exit code 1 when rules like `entity-missing-id`, `entity-bad-id`, `non-const-name`, `decl-in-func`, or `route-param-mismatch` are violated.
