# Stage 10 — resume brief

Written 2026-08-04 before a context clear. Everything here is verified
state, not recollection; where something is unverified it says so.

**Status: Z1–Z4 are done agent-side and await the owner's verdicts.
Z5 is DONE (§2). D1/D2 are fixed; FA1–FA4 are proposed, unapproved. Z6 is removed. Z7 is proposed, unapproved.
FL1–FL4 (§4) are proposed, unapproved, and are the work Z6 now waits
on. §5's open question is ANSWERED (see §5).**

Verify that claim before trusting it:

```
go test ./...                    # green as of a7daf1a
git log --oneline -1             # a7daf1a
```

*Corrected 2026-08-04: this brief claimed the suite was green at
`69be83e`. It was not — `TestEveryCustomMethodPostIsClassified` had been
failing since `fc2e889` (Z2b). Fixed in Z5; the lesson is that "green as
of <sha>" in a handoff is a claim to re-run, not to inherit.*

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

## 2. Z5 — housekeeping (mostly done, 2026-08-04)

From the plan. Three items, one commit, no human gate — **except** the
error text, whose wording needs approval like any other user-facing
string.

1. **Cloud Tasks' 7-day name-reuse block — DONE.** The deploy names the
   wait and suggests `--env`, and `tako destroy` warns in its preview
   ahead of the confirmation prompt (both approved 2026-08-04). The
   error alone arrives after the clock has started, which is why the
   warning was the more important half.
2. **gofmt — DONE (by verification).** `gofmt -l .` prints nothing;
   `analyzer.go` and `edges.go` were formatted at some point after the
   item was logged.
3. **The plan's stage table — DONE (by verification).** Current, as Z2b
   left it.
4. **Not on the list: the suite was red.** `:generateServiceIdentity`
   shipped in Z2b unclassified, so the custom-method retry guard had
   been failing for three commits. Classified as a third category —
   `idempotentMutatingVerbs`, a mutation Google defines as idempotent —
   and pinned by a behaviour test. Fixed in `0c87c48`.

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

## 4. FL1–FL4 — the flautas gaps, which are now the release gate

Proposed 2026-08-04, unapproved. **This is the work Z6 was removed in
favour of**: publishing is blocked on the flautas friction logs, and
these four slices are those logs answered. Nothing here is optional to
a v0.1.0 tag; the ordering below is the only open question.

Source: flauta's friction log (sibling repo, `docs/friction.md`,
entries dated 2026-08-02), kept under its ADR 0008 — an app-side
workaround is the default, promotion to a tako task is the exception.
Four entries: two promoted by flauta, one reopened here on evidence
flauta did not have, one closed with no task.

*A note on the record: this plan was first written into
`tako-agent-plan.md` as "Stage 11" and was dropped from that file by
69be83e while uncommitted (two agents, one worktree). The parked-list
line at the bottom of that file still points at "FL2, Stage 11" and now
points here instead. If Stage 11 is wanted as a durable stage rather
than a section of a resume brief, it should be restored there — this
brief is by nature ephemeral.*

**The standing rules are unchanged.** One feature, one gate; DX
proposal first; **no feature without an example demanding it** — each
slice below names the example that will demand it, and nothing ships on
an outside consumer's word alone.

**FL1 — `tako.RedirectPermanent`.** A Page handler can only redirect
303 (hard-wired in `pkg/runtime/page.go`), and the SDK's only permanent
redirect is `SiteConfig.Redirects` — a map literal read at analysis
time, so it cannot carry data decided per request. Verified, and there
is no clean workaround: the three flauta found are build-time-only
308s, an honest-but-temporary 303, or both mechanisms at once. Add a
`status` to `HTML` (widening the `hooks.HTMLParts` tuple), keep
`Redirect` at 303, serve 301 from the new constructor, leave the htmx
path identical — `HX-Redirect` + 200 either way, because permanence
means nothing to a client that is not a crawler. Example: journal grows
a slug rename that mints a moved-URL record and serves 301 from the
store — a redirect decided per request from entity data, which is the
whole of the gap. Gate FL1: rename a post, watch the old URL answer
301, and the same route answer `HX-Redirect` under htmx.

**FL2 — `SiteConfig{NotFound: "404.html"}`.** Unparks the styled error
page. Two paths 404 today and both answer in plain text: `pageError`
for a handler's `tako.NotFound()`, and the site handler for a static
miss — which is why a handler-side result type cannot close this alone
and a config field can. Cheaper than it looks: `RegisterSiteTemplates`
already receives `SiteOptions` and installs `renderSite` as package
state, so both paths can render the site's own layout with no codegen
restructuring. A missing or broken template falls back to today's plain
text — a 404 never becomes a 500. Example: journal has both paths
already. Three calls for the proposal: whether `NotFound` resolves as a
template first and falls back to a static file in the site dir, so
generators that already emit `404.html` just work (recommended);
whether 500s are in scope (recommended: no); whether htmx requests get
the rendered document like everyone else (recommended: yes — one
behavior, no mode switch). Gate FL2: a mistyped URL and a missing post
both answer 404 wearing the site.

**FL3 — `Query.Count()`.** flauta rated this workaround-fine and it was
headed for the not-scheduled list. Reopened on a fact flauta could not
see from outside, and the owner asked for it to be reconsidered only if
Datastore made it simple. It does. The gcp backend is a hand-rolled
Datastore v1 REST client, not the Go library (`go.mod` has no
`cloud.google.com/go/datastore`), and `runAggregationQuery` is a
sibling endpoint of the `runQuery` it already speaks: the same query
object built today, wrapped in `{aggregations: [{count: {}}]}`. One new
verb and one response field — server-side, billed per index scan rather
than per matched entity, no new dependency. Local counts what it
already filters. One call for the proposal: `Count` ignores `Limit`
(recommended — `Limit` shapes a page, `Count` answers the total, which
is the question a numbered pager asks). Example: journal's home listing
renders "page N of M" instead of a bare first-50. Gate FL3: the total
is right on both backends, including with filters.

**FL4 — `Query.Page(cursor)`.** Called out by the owner as definitely
important, and the slice with the sharp edge. `Page(after string)
(EntityPage[T], error)` joins `All`/`First`/`Iter` as an executor,
returning items, an opaque cursor and whether more remain; the empty
string starts at the beginning. On gcp the cursor is nearly in hand
already — `Query` reads `endCursor` on every batch and uses it
internally to drain them, then throws it away at the API boundary.

The edge is that it cannot always be handed back honestly. The backend
deliberately falls back to a client-side sort when the order is the
default id order, or cannot push down beside an inequality filter
(`pkg/backends/gcp/entity.go`, the comment above the order block); in
that case the server's cursor is not a position in the order the caller
asked for, and returning it would silently skip or repeat rows. So the
rule is uniform across both backends: **`Page` requires an explicit
`OrderBy`**, refused with a runtime error rather than guessed — never a
query that pages locally and lies in cloud.

This is a backend-interface change (`backends.EntityQuery` gains a
start cursor; `Query` must return an end cursor), so local implements a
**keyset** cursor — the last row's `(sort value, id)` resumed as a
compound predicate — not an offset, because only keyset matches
Datastore's behaviour when rows are written between pages. That
agreement is the conformance case, written first: insert and delete
between page 1 and page 2, both backends observe the same thing.
Example: gallery's `GET /api/photos` takes `?cursor=` and returns the
next one. Gate FL4: page through gallery on both backends, with a write
landing mid-walk.

**Closed with no task — the site domain through the config seam.**
flauta asked for nothing and is right: `tako/config-literal` refusing a
variable is what lets the deploy plan be computed without running the
app. One duplicated string held by a test is the smaller cost.
Recorded so the question is not reopened from outside later.

**Not promoted.** flauta's log also carries four gaps known from
planning and not yet hit in code — no full-text query, no image
processing, no roles in auth, no configurable markdown pipeline. Two
are standing non-goals (admin primitives; anything that turns Entity
into "the database"), and all four are unhit. They stay in the
consumer's log until an example here demands them.

**Ordering — the open call.** FL1 first: it is the only one blocking an
outside project's design as written (flauta's ADR 0014 needs superseding
with an honest-303 story if tako declines it), and it is the smallest.
FL3 before FL4: it is a single endpoint and warms the aggregation path,
while FL4 changes the backend interface and needs a cloud gate. FL2 is
independent of all three and can land anywhere. Suggested: **FL1 → FL3
→ FL4 → FL2**, against Z5 and Z7 which are unrelated and can interleave.
Not yet decided, and worth an owner's minute — the alternative reading
is that FL2 goes first because it is the only one a reader of the site
would ever notice.

**Verdicts travel back.** When a slice lands, its entry in
`flautas/docs/friction.md` needs closing on the flauta side under its
ADR 0008. That is a flauta-session job — tako never patches flauta, and
the flauta agent appears to hold read-only access here.

---

## 5. THE OPEN QUESTION — ANSWERED 2026-08-04

> Can every alert built in Z1–Z4 be written using plain tako alert
> constructs?

**No.** Full evidence in `docs/task-log.md` under "Stage 10 — the open
question answered, by rendering both halves". Headline, because the rest
of this section was written before the measurement and got it wrong:

- **The "cannot name them" claim below is FALSE.** `PlatformMetric` can
  name `custom.googleapis.com/tako/requests`, and `platformResource`
  already resolves it to `generic_task`.
- **The real barriers are two others.** `namespace` is `App+"-"+Env`, so
  no matcher over tako's own metrics can be written correctly at
  declaration time; and the raw Google-metric path has exactly one
  reduction, `max()`, while every preset over `requests` needs a rate,
  an increase, a ratio, an absence or a quantile. **The gap is
  reduction, not naming** — which means adding a naming constructor
  would not close it.
- **Two defects fell out of rendering both halves**, both in shipped
  surface, both logged as discovered work and unfixed: `WhenAbsent` over
  any Google-metric constructor compiles to `> 0` (its inverse), and a
  user matcher on a label tako already supplies is concatenated rather
  than overridden, giving a contradictory selector.
- **Verdict on the follow-up:** the presets are load-bearing for the
  nine conditions measured on tako's own metrics, and the "presets are
  deliberately the only door to framework-internal metrics" position is
  the strongest one — those names have already been changed three times
  this stage. If anything is added, it should be the two things about
  GOOGLE's metrics: a `DeadLetters(c)` selector for `QueueMetric`, and
  an `atleast`-shaped comparison.

*Everything below this line is the pre-measurement analysis, kept for
the record. Where it disagrees with the above, the above wins.*

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

## 6. Standing constraints

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

## 7. What this feature taught, worth not relearning

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
