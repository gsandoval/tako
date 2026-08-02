# validate/dlq-replay.md — U3: a dead letter gets a second chance

The claim: `tako dlq --replay` puts a dead-lettered message back into
at-least-once delivery and removes it from the DLQ — a **move**. It
rides the real broker, so a replay that fails again comes back; and a
replay from a **topic** reaches only the subscriber that failed, never
the ones that already succeeded.

**Setup:** run the CLI as `go run ../../cmd/tako …` from
`examples/gallery`. `<test-project>` is the scratch project
(tako-test-02).

## 1. Unit truth first

```
go test ./... 
go test ./pkg/backends/local -run TestReplay -v
go test ./cmd/tako -run 'TestPickDead|TestAck|TestReplayNeeds'
```

→ green, and specifically:

- `TestReplayReachesOnlyTheFailedSubscriber` — the whole topic story:
  the failed subscriber runs once, the healthy one **zero** times.
- `TestReplayThatFailsAgainReturnsToTheDLQ` — the reason replay goes
  through the broker instead of being a one-shot call.
- `TestReplayKeepsTheFileWhenTheCarrierIsGone` — a record with nowhere
  to go is kept, because the file is the only copy.
- `TestPickDeadByPrefix` — unique prefixes resolve; ambiguous ones
  refuse and say how many matched.

## 2. Local: make a dead letter, replay it, watch it come back

`tako dev` in one terminal. gallery's `MakeThumb` fails permanently on
an undecodable image, which is the easiest dead letter to produce:

```
head -c 4096 /dev/urandom > /tmp/bad.jpg
curl -s -o /dev/null -w "%{http_code}\n" \
  -F caption=dlq -F img=@/tmp/bad.jpg http://localhost:8080/api/photos
```

→ `200`, then in the dev console:

```
✗ dead-letter thumbnails: MakeThumb permanent failure: decode image: image: unknown format
  payload → .tako/data/deadletter/thumbnails/
```

List it — note the id at the start of the line:

```
go run ../../cmd/tako dlq --local
```

Replay it by a **prefix**:

```
go run ../../cmd/tako dlq --local thumbnails --replay <first-4-digits>
```

```
↩ replayed 1699… → thumbnails
  the running tako dev picks these up within a couple of seconds; if dev is down they wait for its next boot
```

Within ~2s the dev console shows the redelivery — and, because the
image is still undecodable, **the message dead-letters again**:

```
↩ replayed 1 dead letter(s) back into delivery
✗ dead-letter thumbnails: MakeThumb permanent failure: decode image: image: unknown format
```

**That is the pass, not a failure.** Replay is a second chance under
the same rules, not a promise of success. Confirm the move happened:
`tako dlq --local` lists ONE dead letter (the new one), not two.

Then fix and replay for the success path — upload a real PNG (§2a of
`validate/metrics.md` builds one), and replay that message instead.

## 3. Local: `--ack` and `--replay` refuse each other

```
go run ../../cmd/tako dlq --local --ack --replay 1699
```

→ `--ack and --replay do the opposite things to the same message — pick one`

And an ambiguous prefix:

```
go run ../../cmd/tako dlq --local thumbnails --replay 1
```

→ `"1" matches N dead letters — use more characters`

## 4. Cloud: deploy and produce a topic dead letter

```
go run ../../cmd/tako deploy --project <test-project> --env dlq
```

**Keep `--env dlq` on every command below.** It prefixes every resource,
so the Cloud Run services are `dlq-gallery`, `dlq-gallery-notifyowner`
and so on. Deploy without it and the names are bare (`gallery`) — drop
the `dlq-` prefix everywhere if you do. Note `tako dlq` takes the
LOGICAL service name (`gallery-notifyowner`) and applies the env
itself; only the raw `gcloud` reads need the prefix spelled out.

gallery's `photo-ready` topic has **two** subscribers — `NotifyOwner`
and `WarmFeed` — which is what makes it the interesting case. Produce a
failure in ONE of them (the `boom` caption path; see gallery's
`notifyOwner`), upload, and wait for the dead-letter policy to give up:

```
URL=$(gcloud run services describe dlq-gallery --project <test-project> \
  --region us-central1 --format 'value(status.url)')
curl -s -o /dev/null -w "%{http_code}\n" -F caption=boom -F img=@/tmp/big.png "$URL/api/photos"
```

```
go run ../../cmd/tako dlq --project <test-project> --env dlq
go run ../../cmd/tako dlq gallery-notifyowner --project <test-project> --env dlq
```

→ the message, with its Pub/Sub `messageId` first on the line.

## 5. The replay reaches one subscriber, not both

**Note WarmFeed's current state before replaying** (its log lines, or
whatever it wrote), so you can prove it did not re-run.

```
go run ../../cmd/tako dlq gallery-notifyowner --replay <prefix> \
  --project <test-project> --env dlq
```

```
↩ replayed 14672041893 → photo-ready (topic)
```

Then:

- `tako dlq gallery-notifyowner …` → the message is **gone** from the
  DLQ (it moved), or back on it if it failed again.
- `tako logs gallery-notifyowner` → the handler ran again.
- **`tako logs gallery-warmfeed` → one line reading
  `skipping replay addressed to another service`, and NO re-run of its
  handler.** This is the claim: a topic replay does not re-charge the
  card that was already charged.

## 6. Destroy clean

```
go run ../../cmd/tako destroy --plan --project <test-project> --env dlq
go run ../../cmd/tako destroy --project <test-project> --env dlq
```

The `--plan` line is V5 riding along: the same preview destroy prints,
without arming the prompt.

Confirm the project is pristine — no services, topics, subscriptions,
service accounts or scheduler jobs left.

## 7. Put gallery back

Revert any edit made to produce the failure. `go test ./...` green.

## Verdict

Record in `docs/validation-log.md`: U3, plus V5 if §6's `--plan` was
exercised.
