# validate/entity.md — Gate F2: Entity + shortlink (local only)

What this gate approves: the Entity surface (CRUD, Query, Update, Tx),
bbolt persistence across restarts, and the unindexed-query error text.

Prerequisite: `PORT=8090` if 8080 is busy (Firestore emulator).

## 1. Start

```
cd examples/shortlink
PORT=8090 go run ../../cmd/tako dev
```

**Observe:** the banner lists three endpoints *and* the resource line
`entity links (indexes: Owner, Created)` — the index set is derived from
the app's actual query call sites.

## 2. Play with shortlink end-to-end

```
curl -s -X POST localhost:8090/api/links \
  -d '{"slug":"gh","url":"https://github.com","owner":"memo"}'
curl -s -X POST localhost:8090/api/links \
  -d '{"slug":"hn","url":"https://news.ycombinator.com","owner":"memo"}'

curl -i -X POST localhost:8090/api/links -d '{"slug":"gh","url":"https://x.com"}'
# 409 {"error":"slug \"gh\" is taken"}    ← Errorf inside a transaction

curl -s localhost:8090/api/links/gh       # hits increments per resolve
curl -i localhost:8090/api/links/nope     # 404 via Update-miss → IsNotFound
curl -s "localhost:8090/api/links?owner=memo"   # filtered, newest first
```

## 3. Kill and restart — persistence

Ctrl-C the dev loop, start it again, `curl localhost:8090/api/links/gh`.

**Observe:** links and hit counts survive (bbolt at
`.tako/data/entity.db`). Judge: is it obvious where dev data lives and
how to reset it (`rm -rf .tako`)?

## 4. The unindexed-query error (the gate's centerpiece)

In `app.go`'s `list`, change `q.Where("Owner", …)` to
`q.Where("URL", …)`. Save.

**Observe:**

```
✗ analysis failed — still serving last good build
  app.go:…: query filters on Link.URL, but URL is not indexed — tag it
  entity:"index" to make it queryable (tako/unindexed-query)
```

Judge the message: does it teach the schema model in one line? Also try
`OrderBy("-Hits")` (…"to make it orderable"). Revert both.

## 5. More analyzer errors (optional, quick)

- Remove the `entity:"id"` tag from `Slug` → `tako/entity-id`.
- Change `Slug string` to `Slug int` (keep the tag) → id-must-be-string.
- Replace the query field with a variable → `tako/const-query-field`.
Revert all.

## 6. Conformance suite — the local/gcp contract

```
cd ../.. && go test ./pkg/backends/conformance -backend=local -v 2>&1 | tail -25
```

**Observe:** the entity contract cases (CRUD, upsert, query ops, time
ordering across zones, tx commit/rollback). This suite is what the gcp
backend must pass unchanged in Phase E.

## 7. Tests

```
go test ./...
cd examples/shortlink && go test ./...
```

Record the verdict in `docs/validation-log.md` (feature F2, gate F2).
