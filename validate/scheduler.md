# validate/scheduler.md — T2: crons fire in the cloud

The claim: a Cron declaration is a REAL schedule everywhere — ticker
locally, Cloud Scheduler in the cloud, one trigger identity the route
verifies itself, singleflight collapsing overlap across instances.

**Setup:** run the CLI as `go run ../../cmd/tako …` from
examples/gallery (the demanding example: Cleanup, every 24h,
singleflight). The fast-cron observation (§4) uses a scratch app.

## 1. The declaration is the whole story

`gallery.Cleanup = tako.Cron("every 24h", cleanupOrphans,
tako.CronConfig{Singleflight: true})` — nothing else. Schedules also take 5-field
cron expressions ("0 3 * * *"); intervals unix-cron can't express are
refused at analysis time (break it: "every 7m" → the grammar rule,
"0 25 * * *" → the field judgment; golden-tested).

## 2. Deploy: the trigger exists, named and OIDC-guarded

```
cd examples/gallery && go run ../../cmd/tako deploy --project <test-project>
```

**Judge the plan:** `+ create scheduler job gallery-cron-cleanup` —
and NO ⚠ inert-cron warning anywhere (that text is dead). Then:

```
gcloud scheduler jobs describe gallery-cron-cleanup \
  --project <test-project> --location us-central1
```

→ schedule `0 0 * * *` (the 24h mapping), UTC, POST to
`…/_tako/cron/Cleanup`, oidcToken as gallery-sa. The route trusts
nobody: `curl -X POST <gallery-url>/_tako/cron/Cleanup` → 401;
with a bad bearer → 403.

## 3. Force a run (operator path)

```
gcloud scheduler jobs run gallery-cron-cleanup --project <test-project> --location us-central1
go run ../../cmd/tako logs gallery --since 5m
```

→ `cron ok cron=Cleanup took=…` — Scheduler → OIDC → dispatch →
handler, on demand.

## 4. Observation: a fast cron fires ITSELF, and overlap collapses

Scratch app (`tako.Cron("every 1m", tick, tako.CronConfig{Singleflight: true})`,
handler writes a tick entity then sleeps 90s), deployed to the test
project. Wait ~4 minutes:

- tick entities accumulate — nobody curled anything; the SCHEDULE did
  it (the W-gate criterion for "crons run in the cloud").
- because each run outlives the next tick, the logs show the
  singleflight narration (`↻ … skipped — an overlapping run holds the
  lease`) — collapse across triggers, live.

```
go run ../../cmd/tako destroy --project <test-project>
```

**Judge:** `✗ scheduler job …` in the teardown; clean project.
Record the verdict in docs/validation-log.md.
