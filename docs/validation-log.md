# Validation log

Human gate verdicts, one entry per feature (see `tako-agent-plan.md`).
A feature is DONE only when its entry here records a pass.

| Feature | Gate | Date | Verdict | Notes |
|---|---|---|---|---|
| F5 Topic + Subscribe + bucket events | F5 | 2026-07-20 | **pass** | Verdict recorded by memo (darchitect). Validated via validate/topic.md: event-driven pipeline (bucket event → queue → worker → topic), one event fanning out to two subscribers with independent fates (WarmFeed acks while NotifyOwner retries into its own dead letter), per-subscriber dead-letter JSON with subscriber field, subscribe-binding error text, compile-checked payloads, conformance(local) 40 cases incl. Topic + BucketEvents chapters. Devserver idle-reload feedback loop found and fixed during validation. |
| F4 Queue + Worker | F4 | 2026-07-19 | **pass** | Verdict recorded by memo (darchitect). Validated via validate/queue.md: publish → in-process worker, retry narration (attempt n/5 with reason + delay) ending in dead-letter, Permanent short-circuit, dead-letter JSON inspectable on disk, one-worker error text, compile-checked payload, conformance(local) entity+bucket+queue. |
| F3 Bucket + Blob | F3 | 2026-07-19 | **pass** | Verdict recorded by memo (darchitect). Validated via validate/bucket.md: multipart upload, streamed download (Content-Type/Length, byte-identical), object listing, on-disk layout under .tako/data/buckets, blob-binding error texts, missing-file 400, conformance(local) entity+bucket. |
| F2 Entity + shortlink | F2 | 2026-07-19 | **pass** | Verdict recorded by memo (darchitect). Validated via validate/entity.md: shortlink end-to-end (409 tx claim, hits, 404, filtered list), kill/restart persistence via .tako/data/entity.db, unindexed-query + entity-id + const-query-field error texts, conformance(local) 17/17. |
| F1 API endpoints | F1 | 2026-07-19 | **pass** | Verdict recorded by memo (darchitect). Validated via validate/api.md: good/bad requests, all six analyzer error texts (incl. the two added rules unexported-req and bind-type — kept), HTTP error bodies, generated decode readability, compile-error fixture behavior, log correlation. |
| S0 walking skeleton | G0 | 2026-07-19 | **pass** | Verdict recorded by memo (darchitect). Validated: dev loop feel, curl, live reload, const-route error text, generated-main readability, clean shutdown. Workflow fix during validation: committed go.work so `go run ../../cmd/tako dev` works from example dirs. Follow-up noted (not blocking): `gen graph` to grow jq-able `--json` and `--mermaid` output modes. |
