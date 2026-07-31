# validate/tasks.md — T3: Cloud Tasks (delay + rate limit)

The claim: `WithDelay` delivers in the cloud at its scheduled time,
`WithRateLimit` caps a queue's dispatch everywhere, and both ride ONE
declaration — a Cloud Tasks queue in front of the worker, derived,
granted, and destroyed like everything else.

**Setup:** run the CLI as `go run ../../cmd/tako …` from
examples/shortlink (the demanding example: TTL'd links publish their
own expiry `WithDelay`; the archive queue declares
`WithRateLimit(2)`).

## 1. The declarations carry the whole story

`expiries` is a plain queue whose PUBLISH SITE uses WithDelay — the
analyzer detects the option at the call site (`gen graph --json`
shows `"delayed": true`); `archive` declares WithRateLimit(2)
(`"rate_limit": 2`). Break it: `WithRateLimit(0)` → the 1–500 rule;
any option on a Topic → invalid-option. All golden-tested.

## 2. Local: the same semantics, the test clock owns time

`go run ../../cmd/tako test` — read
TestExpiryDeletesTheLinkItWasPublishedFor as documentation: not due →
alive; due → gone AND archived through the paced queue; a re-claimed
slug survives a stale expiry (expiry is a claim about one link, not
the slug). Under `tako dev` the dispatcher paces archive delivery at
2/s (generated main: `runtime.SetQueueRate("archive", 2)`).

## 3. Cloud

```
go run ../../cmd/tako deploy --project <test-project>
```

**Judge the plan:** `+ create tasks queue expiries-tako` and
`archive-tako`; grants show the app service holding
`cloudtasks.enqueuer` on each queue and `iam.serviceAccountUser` on
the WORKER's SA (the task's OIDC identity is the worker's own — the
same trust edge as a Pub/Sub push). Preflight verified cloudtasks.

Then, live ($B = the ✓ shortlink URL):

```
curl -s -X POST $B/api/links -H 'Content-Type: application/json' \
  -d '{"slug":"fleeting","url":"https://example.com","owner":"walker","ttlSeconds":120}'
gcloud tasks queues describe archive-tako --project <test-project> --location us-central1 \
  --format 'value(rateLimits.maxDispatchesPerSecond)'          # → 2.0
gcloud tasks list --queue expiries-tako --project <test-project> --location us-central1  # 1 task, scheduled +2m
```

Wait ~2½ minutes:

```
curl -s $B/api/links/fleeting      # → 404: the delayed expiry fired
```

and `tako logs` shows the expire worker's delivery ~120s after
create — Cloud Tasks held the message, not the service.

## 4. Teardown

```
go run ../../cmd/tako destroy --project <test-project>
```

**Judge:** both `✗ tasks queue …` lines; clean project. Record the
verdict in docs/validation-log.md.
