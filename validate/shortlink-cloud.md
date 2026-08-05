# Validate C3 — gcp Bucket + Entity (+ minimal Pub/Sub); shortlink graduates

The first full cycle on an app with state: Datastore entities, a derived
composite index, a Pub/Sub queue with an OIDC push worker, and derived
IAM. Uses the dedicated test project. Prereqs: gcloud ADC, and a
Datastore-mode database (preflight prints the create command if absent).

## 1. Conformance(gcp) — the internal bar

```
go test ./pkg/backends/conformance -backend=gcp -project <test-project> -run 'TestEntity|TestBucket'
```

**Judge:** both pass (Entity 18 chapters, Bucket 7) against real
Datastore/GCS. First run creates a conformance composite index and waits
for it; index serving can lag READY by ~1 minute — a retry is honest.
Note: entity chapters leave tiny conf-<hex> namespaces behind.

## 2. Deploy, plan first

```
cd examples/shortlink
tako deploy --plan --project <test-project>
tako deploy --project <test-project>
```

**Judge, line by line:** 2 SAs · datastore index `links (owner,
-created)` (marshaled property names, not Go field names) · topic
clicks · 2 run services · per-target iam grants (project datastore.user
for both SAs; pubsub.publisher on clicks for the api SA; run.invoker on
the worker for its own SA; tokenCreator for the Pub/Sub agent on the
worker SA) · push subscription. First deploy waits on the index build
(minutes). The security summary must trace every grant to a code edge,
and the worker must say "OIDC push from its own SA only".

## 3. The app works — including the async spine

```
URL=$(… the ✓ shortlink line)
curl -X POST $URL/api/links -d '{"slug":"gh","url":"https://github.com","owner":"you"}'
curl $URL/api/links/gh                  # 200; publishes a click
curl -X POST $URL/api/links -d '{"slug":"gh","url":"https://x.com","owner":"you"}'   # 409, Datastore tx
curl $URL/api/links/nope                # 404
curl "$URL/api/links?owner=you"         # composite-index query
# a few seconds later:
curl $URL/api/links/gh                  # hits incremented — real Pub/Sub push
```

**Judge:** hits counts exactly one per resolve (idempotency ledger over
at-least-once delivery). The worker URL must reject a plain
`curl -X POST <worker-url>/_tako/api/push` (403: IAM, not app code).
Caveat: right after first deploy, IAM propagation can 403 entity ops
for a minute or two — the error text says it's a deploy bug (it is not,
in this one window); settle before judging.

## 4. Idempotence and drift

```
tako deploy --project <test-project>       # all skips, ✓ no changes
```

## 5. Destroy — §4.6a with real data

```
tako destroy --project <test-project>
```

**Judge:** the listing shows a separate `DATA — deleted with the app,
gone forever` section naming the datastore kinds and namespace; typing
anything but the app name aborts; the run revokes grants, deletes in
reverse order, purges the datastore namespace (entity count printed),
and removes the framework bucket. Re-running destroy right away may
still list the datastore index (deletion is async) and grant targets
(policies are always readable) — both converge.
