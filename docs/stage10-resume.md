# Stage 10 — resume brief

Written 2026-08-04 before a context clear. Everything here is verified
state, not recollection; where something is unverified it says so.

**Status: Z1–Z4 are done agent-side and await the owner's verdicts.
Z5 is unstarted. Z6 is removed. Z7 is proposed, unapproved.**

Verify that claim before trusting it:

```
go test ./...                    # green as of 69be83e
git log --oneline -1             # 69be83e
```

`git status` should show only `logaster-2013-06.jpg`, which is
deliberately never staged.

---

## 0. Facts about the environment

- **The repo has NO git remote.** `git remote -v` is empty. Earlier
  summaries in this session said "N commits unpushed", which was wrong —
  there is nothing to push to yet. A remote has to be added before any
  push, and that is the owner's to do.
- **tako-test-01 is clean**: zero alert policies, zero Cloud Run
  services, no `x1-`/`z2-`/`z3-` anything. Metric descriptors and time
  series remain, by design (§4.6a: data is never auto-deleted).
- The scratch project for walks is `tako-test-01`. The owner's address
  for live alert delivery is **darchitect@gmail.com** — substitute it
  into the example being walked and revert to `ops@example.com` before
  committing.

---

## 1. The five verdicts waiting

`docs/validation-log.md` has five rows at *pass (agent-side; awaiting
human verdict)*: **Y1, Y2, Z2b, Z3, Z4**. The owner said they will
validate these. The agent's job is to support that, not to re-run it.

`validate/stage10.md` is the collected walk document — one file, growing
per slice, which is how the owner asked for it. It already answers the
scope question ("is this Z1's test or does it land later?") for Z1/Z2.

**What each needs, honestly:**

| row | how it is checked | needs a deploy? |
|---|---|---|
| Y1 + Y2 | `validate/alerts.md` on gallery, both halves | yes |
| Z2b | `validate/stage10.md` §Z2b on siren | yes |
| Z3 | read `pkg/provision/testdata/siren_queries.golden.txt` — `Hot`, `Crowded` | no |
| Z4 | `tako metrics <search>` against any project | no |

Z3 and Z4 are inspectable without spending a deploy cycle. Say so rather
than implying every row needs one.

**Do not re-run these walks unprompted.** They have been run; the
transcripts are in `docs/task-log.md`. What is missing is the owner's
signature, which no amount of agent work produces.

---

## 2. Z5 — housekeeping, the next thing to build

From the plan. Three items, one commit, no human gate — **except** the
error text, whose wording needs approval like any other user-facing
string.

1. **Cloud Tasks' 7-day name-reuse block.** Redeploying after a destroy
   fails with Google's raw `a queue with this name existed too recently`.
   tako should name the wait and suggest `--env`. Discovered during the
   T3 walk (2026-08-01, task log) and never scheduled. **Write the
   proposed text into the task log and stop for approval before
   shipping it** — error texts are product UX and golden-tested.
2. **`pkg/analyzer/analyzer.go` and `edges.go` are not gofmt-clean.**
   Pre-existing, unrelated to any feature. `gofmt -l .` shows them and
   nothing else.
3. **The plan's stage table** — already brought current in Z2b; verify
   rather than redo.

---

## 3. Z7 — `tako docs`, proposed and unapproved

Full DX proposal in `docs/task-log.md` (2026-08-04). Summary: serve the
guide, the reference, and the SDK's exported signatures on
`localhost:8090`, from a `go:embed`ed copy so it works without a
checkout. The api section generates from `testdata/sdk_lock.golden`, so
the API-freeze test and the docs page share one source.

**Do not build it without approval.**

One test in that proposal is worth keeping whatever else changes: a link
check across the embedded pages. `reference/alerts.md` gained links to
`observability.md` and `footprint.md` during Y2 and nothing verifies
they resolve.

---

## 4. THE OPEN QUESTION (owner, to answer after the resume)

> Can every alert built in Z1–Z4 be written using plain tako alert
> constructs?

That is: are the presets sugar over the raw vocabulary, or do they reach
things the raw vocabulary cannot express? If the latter, the raw
vocabulary has a capability gap and the presets are load-bearing rather
than convenient.

**The surface, as it stands today** (verified by reading `alert.go`):

- raw comparisons: `WhenAbove`, `WhenBelow`, `WhenAbsent`
- readings on your own recordings: `.Rate()`, `.Count()`, `.Sum()`,
  `.Mean()`, `.Percentile(q)` on Counter/Histogram handles
- Google's metrics: `ServiceMetric`, `ServicePercentile`,
  `ServiceInstances`, `QueueMetric`, `PlatformMetric`
- presets: `QueueBacklogged`, `QueueStalled`, `CronMissed`,
  `CronFailed`, `JobFailed`, `RequestsErroring`, `RequestsSlow`,
  `ServiceAtCapacity`, `SingletonDown`

**One finding already in hand, and it likely decides the answer.**
Grepped, not guessed: **there is no raw reader for tako's OWN
per-endpoint metrics.** `custom.googleapis.com/tako/requests` and
`.../request_ms` are written by every service (see
`pkg/runtime/http.go`) and read by four presets, but nothing in the SDK
surface can name them. `ServiceMetric` reads Google's metrics; the
reading methods read *declared* Counters and Histograms; `requests` is
neither.

So the following are **preliminarily** not expressible in raw
constructs, and each should be confirmed against
`pkg/provision/testdata/siren_queries.golden.txt` rather than taken from
this brief:

- `CronMissed`, `CronFailed` — need `requests` filtered by the cron's
  route
- `RequestsErroring`, `RequestsSlow` — need `requests`/`request_ms`
  filtered by route and status
- `QueueBacklogged`/`QueueStalled` with `DeadLetters: true` — need the
  `<service>-dead` subscription, and `QueueMetric` targets only the live
  one
- `JobFailed` — needs `cloud_run_job` scoping, which no constructor
  offers

and these **are** expressible today:

- `ServiceAtCapacity` ≈ `WhenAbove(ServiceInstances(svc), max-0.5, w)`
- `SingletonDown` ≈ `WhenBelow(ServiceInstances(loop), 1, w)`
- live-queue `QueueBacklogged`/`QueueStalled` ≈ `QueueMetric(q, "…/num_undelivered_messages" | "…/oldest_unacked_message_age")`

**If that holds, the interesting follow-up is what to do about it** —
and the answer is not obviously "add constructors". Options worth
weighing rather than assuming: a raw reader for tako's own request
metrics (`RouteMetric`? `EndpointMetric`?); a `DeadLetters(c)` selector
for `QueueMetric`; a `JobMetric`; or the position that presets are
*meant* to be the only way to reach framework-internal metrics, since
their names and labels are tako's implementation detail and exposing
them freezes them as API.

That last one is a real argument and should not be dismissed to make the
surface look tidier.

---

## 5. Standing constraints

Never `git push`, never tag — commit on a feature branch, ff-merge to
main, delete the branch; the owner pushes. Exclude
`logaster-2013-06.jpg` from staging. Commit trailer
`Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>`.
Full suite green before every commit. Error texts in
`docs/reference/*.md` are golden-verbatim — copy them from
`pkg/analyzer/testdata/errors/*/expect.golden.txt`, never hand-edit.
DX proposals stop for approval; API changes are their own task.

Publishing is blocked on the flautas friction logs — see the plan's Z6
note. Do not offer it as a next step.

---

## 6. What this feature taught, worth not relearning

Written down because it was rediscovered four times.

**An empty result set satisfies no comparison.** `increase(x[26h]) == 0`
never fires; `WhenBelow` could not detect a stop; `SingletonDown`
resolved itself while the singleton was gone. Every condition meaning
"less than expected" needs a missing-data arm — `or vector(0)` is the
idiom, and every `below` now carries it.

**Reading a rendered artifact finds what tests do not.** Nine defects
came out of printing every generated query beside its notification
sentence and reading it; two more came from opening a console link; one
from reading the CLI's own output. The golden at
`pkg/provision/testdata/siren_queries.golden.txt` exists to be read as
prose, not diffed.

**Verifying a shape is not verifying a value.** A `route` label declared
in a descriptor and empty on all 88 series; a `job` label spelled three
ways; a `subscription_id` naming a topic; `response_class="2xx"` where
Pub/Sub uses `ack`. In each case the code was well-formed and the query
matched nothing — which reads exactly like a healthy system.
