# Code review 2 — health, duplication, and the newcomer's path (2026-08-06)

Owner-requested review: code structure and simplicity, duplication, dead
documents, and readability for a human discovering the codebase.

**This task names no review section**, so the request itself is the whole
brief. It is vague by nature ("is the codebase healthy"), so it was
sharpened before starting, per the handoff rules:

> **Acceptance.** (1) Every duplication claim is a measured pair of
> locations, not an impression. (2) Every "dead document" is dead by a
> stated test, not by age. (3) Readability findings name the file a
> newcomer opens and what happens to them there. (4) Any code change is
> **hardening**: behaviour provably unchanged, existing suite untouched.

**Not in scope:** splitting large files for their own sake, renaming for
taste, restructuring packages, and anything that changes behaviour. Those
become backlog entries, not this diff.

## The numbers

Measured today, against `caff4f6`:

| | |
|---|---|
| Go outside `examples/` | 45,260 lines |
| of which tests | 12,116 (27%) |
| `TODO`/`FIXME`/`XXX` markers | **0** |
| analyzer rules | 52 |
| exported SDK symbols | 195 |
| docs (`docs/*.md`, excluding served trees) | 22,008 lines |
| of which `task-log.md` | **18,401 (84%)** |

`code-review-1` (2026-08-01) measured 29.5k lines of Go and 38 analyzer
rules. Five days later: **45.3k and 52**. The tree grew by half again,
and the zero-marker discipline held through all of it — that is the
health headline, and it is not a guess.

## Verdict

**The code is healthy. The documentation is where the rot is.** Three
findings below are real and small; the fourth is the one that actually
costs a newcomer, and it is not about code at all.

---

## 1. Duplication — two measured pairs, one of them mine

Scanned every non-test `.go` file outside `examples/` for repeated
7-line statement blocks (imports and comments excluded). The scan found
**one** cross-file duplicate. Two more were found by name.

**1a. `urlEscape` is written twice, byte for byte.**

- `pkg/provision/alertdoc.go:355`
- `pkg/provision/gcp/descriptorsearch.go:135` (as `urlQueryEscape`)

Identical bodies: RFC 3986 unreserved-set percent-encoding, hand-rolled
because `url.QueryEscape` turns a space into `+` and the Logs Explorer
reads its query from a **path** segment. The reason is real and is
documented — in one of the two copies.

`pkg/provision/gcp` already imports `pkg/provision`, so one home is
available without a new package or a cycle.

**1b. Four functions take the tail after a separator.**

| | |
|---|---|
| `analyzer.ShortVar(h Handler)` | splits `Var` on `.` |
| `codegen.shortVarName(v string)` | splits on `.` — **identical body** |
| `provision.shortNames([]string)` | maps the same split |
| `gcp.lastSegment(name)` | splits on `/` |

`codegen.shortVarName` is **mine, added five commits ago in FL11**, and
it duplicates `analyzer.ShortVar`'s body exactly. I wrote it because
`ShortVar` takes a `Handler` and I had a `Resource`. The honest fix is a
string-shaped `ShortVar` that the `Handler` form calls.

`lastSegment` splits a different separator for a different domain (GCP
resource paths) and is not the same function; it stays.

**1c. The security-header set is written in three places.**

`pkg/runtime/page.go:361`, `site.go:178`, `status.go:129` each `Set`
the same CSP / nosniff / Referrer-Policy trio. FL11 unified the four
*status* writers into one and left the policy itself in three copies —
which is how a header set drifts, and that drift is exactly the defect
FL11 was fixing (a declared 404 got CSP as a template and not as a
static file).

This is the one worth doing for a reason beyond tidiness: it has already
failed once.

## 2. Dead documents — three, by a stated test

A document is dead here if **its stated purpose is to instruct work that
has since shipped**. That is a stronger test than "nothing links to it":
a review nobody links to is still a dated record of why the code looks
the way it does, and deleting it destroys reasoning that comments point
at.

By that test, three of fifteen:

| File | Lines | Says |
|---|---|---|
| `stage7-handoff.md` | 315 | "A self-contained brief for whoever implements Stage 7" |
| `stage7-resume.md` | 119 | "Resume here — U2, U3, U4" |
| `stage10-resume.md` | 380 | "FL1–FL4 are next and still unapproved. Z6 is removed. Z7 is proposed, unapproved." |

Stages 5–9 are closed (`tako-agent-plan.md:299`). FL1–FL14 have shipped.
Z7 shipped. Z6 is unblocked, not removed. **`stage10-resume.md` is the
most actively misleading file in the repository**: five status claims,
all false, written in the confident present tense of a briefing.

814 lines. Git keeps them; nothing else needs to.

**Everything else stays, and the distinction matters.** The review
records — `api-review-1/2/3`, `cli-review-1`, `code-review-1`,
`domain-options` — are cited *from the shipped docs*
(`reference/errors.md` cites `api-review-3`, `reference/cli.md` cites
`cli-review-1`) and carry the reasoning behind decisions the code still
obeys. `code-review-1` is referenced by nothing but the task log and is
still not dead: it is the baseline this review measures against.

## 3. Structure — five files over 700 lines, and none of them is wrong

`deploy.go` (900), `provision/alerts.go` (855), `codegen.go` (786),
`analyzer/alerts.go` (758), `analyzer/analyzer.go` (750).

Read rather than counted, each is one cohesive thing: a deploy is a
sequence, `Desired` is a fold over the graph, the analyzer's dispatch is
one switch. Splitting them would move code without reducing what a
reader must hold. **No action** — recorded so the next reviewer does not
re-derive it.

`pkg/backends`, `internal/hooks` and `internal/boot` have no test files.
All three are interface/hook declarations exercised entirely through the
conformance suite and the runtime. **No action**, same reason.

## 4. The newcomer's path — the finding that actually costs

`README.md` is good: 142 lines, a working example above the fold, five
minutes to a URL. A newcomer's first step is solid.

Their second step is `docs/`, and there it collapses. Fifteen loose
`.md` files with no index, beside three subdirectories (`guide/`,
`reference/`, `api/`) that *are* the real documentation and are served
by `tako docs`. Nothing at that level says which is which. The largest
file by an order of magnitude — 84% of all words in `docs/` — is
`task-log.md`, an append-only log of 226 entries whose header says
"append-only, one entry per task" and offers no other navigation.

A newcomer opening `docs/` cannot tell that `guide/` is for them and
`task-log.md` is not. `CLAUDE.md` has that map, but it is addressed to
an agent and says "read the section you're pointed at" — instructions
for someone already being pointed.

**The fix is one file**: `docs/README.md`, saying what each thing is and
who it is for. Not a rewrite, not a reorganisation — a signpost at the
junction where a person currently guesses.

---

## Proposed changes

Split by risk, because they are not the same kind of edit.

**A. Hardening (mechanical, behaviour unchanged, suite untouched):**

1. One `urlEscape` in `pkg/provision`, called from both sites.
2. `analyzer.ShortVar` gains a string form; `codegen.shortVarName` and
   `provision.shortNames` call it. `gcp.lastSegment` stays.
3. One `securityHeaders(h http.Header)` in `pkg/runtime`, called from
   all three writers.

Each is provable: the existing suite passes untouched, and (3) gets a
test asserting all three paths emit the same set — the property whose
absence FL11 was cleaning up after.

**B. Deletions (destructive, judgment):** the three stage briefs, 814
lines.

**C. Readability (additive):** `docs/README.md`.

**Discovered, not done** (backlog, per the fence): `internal/hooks` is a
mutable-global bridge between the SDK and the runtime — six package
vars assigned at boot. It is the one structure in the tree that a
newcomer will find surprising, and unpicking it is a design task, not a
cleanup.
