# validate/stage10.md — the human walks, collected

One document, growing per slice, so Stage 10's human testing is a single
accumulating checklist rather than six scattered walks. Each section is
added when its slice lands agent-side and is signed when you have run it.

| Slice | What you test | Ready for you |
|---|---|---|
| Z1 | alerting works and the mail is usable | **yes — run it now** |
| Z2 | a dead letter actually raises an alert | **done agent-side**; folded into Z1's walk |
| Z3 | `Latency`/`Instances`/`Saturation` | not built |
| Z4 | `tako metrics <search>` | not built |
| Z5 | housekeeping (one error text) | not built |
| Z6 | the published module | not built |

---

## Scope: what belongs to Z1, and what does not

You asked whether the Z1 test is really Z1's or actually lands later.
Checked against the slices rather than assumed:

**In Z1** — everything about whether an alert *works*: channels and
policies created, `CronMissed` going quiet while the cron is alive and
firing after it stops, the notification's subject and body, the console
links, the paste-able command, and a clean destroy.

**In Z2, and the reason it was worth waiting for** — `DeadLettered`.
Gallery declares `Stuck`, so it appears in your walk either way, and
until Z2 it had never been observed firing. Z2 has now landed: it fires,
and two defects came out of it that your walk would otherwise have hit
(an off-by-one that made `DeadLettered(thumbs, 1)` need two dead
letters, and a `tako dlq` command in the mail that errored when pasted).
**The walk below is now the whole of Z1 and Z2 in one pass.**

**Not in Z1 — and not needed for it** — Z3 and Z4 are about *authoring*
alerts against platform metrics: nicer constructors, and a way to find
metric names. Neither changes whether the alerting you would test today
works. If Z1 felt like it needed them, that would be a scope error; it
does not.

**Not in Z5/Z6 either.** Z5 is one error text unrelated to alerting; Z6
is the published module path.

So: one walk, after Z2, and it is the whole of Z1 and Z2.

---

## Z1 + Z2 — the alerting walk

Run `validate/alerts.md` top to bottom. It is written to be pasted; what
follows is only what to look at hardest, and why each item is there.

**Substitute your own address** in `examples/gallery/app.go` — it ships
`ops@example.com` deliberately, and the walk needs mail to arrive.

### The four things that have each been wrong once

1. **§5, the quiet half.** With the cron alive, the policy's own query
   must answer `QUIET (data present)`. A gate that skipped this recorded
   a pass on an alert that was firing permanently.
2. **§5, the series labels.** Not the count — the labels. There must be
   a series with `route='/_tako/cron/Cleanup'` and a `job` matching what
   the query filters. A run that checked the descriptor's *schema*
   instead found 88 series all carrying `route=''`.
3. **§7b, the links.** Open every one. Two of four shapes were wrong,
   and chasing one of them uncovered an alert that could never fire.
4. **§3, the cron actually landing.** `gcloud scheduler jobs run` exits
   0 when the trigger is accepted, not when the run succeeds. Check
   `status.code` and a 200 in the logs.

### What you should see

Subject, with no `on __missing__` tail:

```
[ALERT - Warning] gallery/x1: cron Cleanup has not run
```

Body, under **Policy documentation** (Google's own
`A PromQL query was observed at 1` stays above it — that line cannot be
suppressed):

```
No run of cron Cleanup has reached x1-gallery in the last 5m.

Either the schedule never fired, or it fired and the service never
answered. A run that did arrive and then failed is a different alert
(CronFailed) — this one means nothing arrived at all.

Schedule: every 24h. Run it by hand:

    gcloud scheduler jobs run x1-gallery-cron-cleanup --project … --location us-central1
```

and a second mail, for `Stuck`, once the Z2 step below has run.

### Z2's step — a dead letter raises an alert

Transcript in `docs/task-log.md`. **Use a `text/plain` payload, not a
corrupt image**, and the reason matters:

```
URL=$(gcloud run services describe x1-gallery --project $PROJECT \
  --region us-central1 --format 'value(status.url)')
echo "not an image at all" > /tmp/doc.txt
curl -s -o /dev/null -w "%{http_code}\n" \
  -F caption=transient -F "img=@/tmp/doc.txt;type=text/plain" "$URL/api/photos"
```

That fails **transiently** five times, so Pub/Sub gives up and forwards
it — which is the path `DeadLettered` can see. A corrupt *image* fails
via `tako.Permanent`, which tako dead-letters itself while answering 204,
so Pub/Sub records a success and the alert stays quiet. That gap is
real, it is written up as a DX proposal awaiting approval, and if you
poison with an image you will be looking at it rather than at a bug.

`Stuck` then goes quiet → firing, at **value=1**: `DeadLettered(thumbs, 1)`
fires on the FIRST dead letter. It compiled to `> 1` until Z2 and needed
two — an off-by-one in the quiet direction.

Allow ~5 minutes, and note the metric has **no series at all** until the
first forward; *no series* and *series at zero* mean different things.

Confirm it in the queue rather than inferring it — and note the argument
is the async SERVICE, not the queue:

```
tako dlq gallery-makethumb --project $PROJECT --env x1     # peek
```

### Sign-off

Two rows in `docs/validation-log.md` are waiting on this walk: **Y1**
(2026-08-02) and **Y2** (2026-08-03), both at *pass (agent-side;
awaiting human verdict)*.
