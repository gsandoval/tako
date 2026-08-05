# Validate C4 — Pub/Sub push + dead-letter; gallery async graduates

The failure path, live: fan-out with independent fates, bounded retries
with backoff, dead-letter topics you can inspect, Permanent as one
attempt. Uses the dedicated test project. Prereqs: gcloud ADC, the
Datastore-mode database, and the `notifier-key` secret (preflight
prints the exact create command if absent — let it fail once to see
the UX, then create).

## 1. Conformance(gcp) — the internal bar

```
go test ./pkg/backends/conformance -backend=gcp -project <test-project> \
  -run 'TestEntity|TestBucket$|TestTopicGCP|TestBucketEventEmitGCP'
```

**Judge:** Entity 18 + Bucket 7 chapters still green; TestTopicGCP
proves publish fan-out (two pull subscriptions each see every message)
against real Pub/Sub; TestBucketEventEmitGCP proves a through-service
Put announces ObjectMeta on the bucket's events topic. Push DELIVERY is
infrastructure — §3 is its validation.

## 2. Deploy, plan first

```
cd examples/gallery
tako deploy --plan --project <test-project>
tako deploy --project <test-project>
```

**Judge, line by line:** 5 SAs and 5 run services (partition: every
worker/subscriber its own service) · bucket · datastore index
`photos (owner, ready)` · topics thumbnails, photo-ready,
photos-uploaded (bucket events), and FOUR `<service>-dead` · four pull
(dead-letter keep) subscriptions · grants across seven target types ·
four push subscriptions. The ⚠ line must say the cron is not scheduled
in the cloud. The security summary must trace every grant to a code
edge — including publisher-on-own-dead-topic (Permanent), the Pub/Sub
agent's dead-letter pair (publisher on dead topics, subscriber on
source subscriptions), secretAccessor for every SA (package-wide
resolution, approved call (a)), and publisher on photos-uploaded for
exactly the bucket-writing services. Async services must say
"redelivery: 5 attempts with backoff, then dead-letter".

## 3. The pipeline, end to end — then the failure paths

```
URL=$(… the ✓ gallery line)
curl -F caption=sunset -F img=@some.jpg $URL/api/photos      # returns {id}
sleep 20; curl -s $URL/api/photos/<id>/thumb -o /dev/null -w '%{http_code}\n'   # 200: real resized thumb
curl -s "$URL/api/photos"                                    # ready feed lists it
```

**Judge:** upload → runtime-emitted bucket event → onUploaded → queue →
makeThumb (private, batch profile) → photo-ready → BOTH subscribers,
with zero gallery code changes.

**Independent fates + retry narration:**
```
curl -F caption=boom -F img=@some.jpg $URL/api/photos
tako logs gallery-notifyowner        # attempt n/5 lines, backoff-spaced
tako logs gallery-warmfeed           # acks every time — unaffected
```
NotifyOwner rejects `boom` captions; watch `attempt 2/5 … attempt 5/5`
land over ~2–5 minutes (10s minimum backoff, exponential), then:
```
tako dlq                             # gallery-notifyowner  1 message(s)
tako dlq gallery-notifyowner         # payload + "delivery attempts exhausted"
```
WarmFeed must ack every delivery of the same events — one subscriber
failing never touches the other. The thumb still exists; the photo is
still in the feed.

**Permanent = one attempt:**
```
: > empty.jpg && curl -F caption=x -F img=@empty.jpg $URL/api/photos
tako dlq gallery-makethumb           # within seconds: "empty upload" reason — NO retries
```

**Kill mid-job / redelivery:** while a `boom` retry cycle is pending,
the nacked deliveries ARE redeliveries — confirm via the attempt
counter climbing in logs. For a harder kill: upload, then immediately
`gcloud run services update gallery-makethumb --min-instances=0
--max-instances=0 --project <p> --region <r>` before the thumb lands;
restore max and watch the delivery arrive late and complete —
exactly-once effect via the idempotent pipeline (thumb Put + Ready
flip are idempotent).

**Push endpoint is IAM-gated:** `curl -X POST <gallery-makethumb
URL>/_tako/api/push` → 403 (Cloud Run IAM, not app code).

## 4. Idempotence and drain

```
tako deploy --project <test-project>       # all skips, ✓ no changes
tako dlq gallery-notifyowner --ack         # drains; second dlq shows empty
```

## 5. Destroy — §4.6a with dead-letter data

```
tako destroy --project <test-project>
```

**Judge:** the DATA section lists the bucket, the datastore kinds, AND
the dead-letter keep subscriptions (retained messages are data); typed
app name required; reverse-order deletion (push subs → grants → runs →
pull subs → topics → …), namespace purge with entity count, clean
project after (conf-* namespaces and the shared composite indexes may
remain — known cosmetics).
