# validate/sharding.md — T4: parallel jobs shard for real

The claim: `WithParallel(n)` is n USEFUL tasks — each knows its shard
via `ctx.Task()`, locally and on Cloud Run, and shop's settle proves
the pattern: disjoint shard subtotals, an idempotent merge, no
coordinator.

**Setup:** run the CLI as `go run ../../cmd/tako …` from
examples/shop (needs the usual shop cloud setup — see
validate/shop.md §6 for auth; the email walker account and jar).

## 1. The pattern, in the source

examples/shop/app.go `runSettle`: `idx, count := ctx.Task()`; each
order belongs to exactly one shard (`fnv(ID) % count`); each task
Puts its own ShardReport (idempotent under retries); every task then
ATTEMPTS the merge and whoever sees all shards present writes the
identical final report. No leader, no lock, retry-safe.

## 2. Local: the fan-out is real

`go run ../../cmd/tako test` — TestSettleShardsAreDisjointAndMergeOnce:
9 orders through `WithParallel(4)` → all four shard subtotals exist,
sum exactly to the report (disjoint cover), re-run doesn't double-
count; and `Task()` is `(0,1)` outside a job.

## 3. Cloud: four tasks, one truth

Deploy, stock, place ~6 orders (validate/shop.md §6 recipe), then:

```
curl -s -b /tmp/shop.jar -X POST $B/api/settle -H 'Content-Type: application/json' -d '{"day":"t4"}'
gcloud run jobs executions describe $(gcloud run jobs executions list --job shop-settle \
  --project <test-project> --region us-central1 --format 'value(name)' --limit 1) \
  --project <test-project> --region us-central1 --format 'value(spec.taskCount,status.succeededCount)'
```

→ `4 4` — four tasks, all succeeded. `tako logs shop-settle` shows
four `task=<i>` streams; the `reports/t4` entity carries the exact
order count and total (identical to a serial run); `settle-shards`
holds 4 disjoint subtotals summing to it.

Destroy per validate/shop.md. Record the verdict in
docs/validation-log.md.
