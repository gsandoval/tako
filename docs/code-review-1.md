# Code review 1 — the release pass (2026-08-01)

User-requested pre-release review of the framework's own code (not the
SDK surface, which `api-review-2.md` covers, and not the CLI's shape,
which `cli-review-1.md` covers): is it standardized, is it free of
duplication, does it follow good engineering practice? Reviewed against
the whole tree — 29.5k lines of Go outside `examples/`, 38 analyzer
rules, 12 CLI commands.

**Verdict: the code is in good shape and the discipline shows.** Zero
`TODO`/`FIXME`/`XXX` markers in the tree. One `do()` and one error
taxonomy behind every cloud call. One `analyzer.Error` type formatting
every diagnostic. Every panic carries a `tako:` prefix and a
remediation. Doc comments explain *why*, and several of them name the
gate that found the bug they prevent. The findings below are real but
none of them is structural.

Ranked by consequence. Five were fixed in this pass; the rest are
proposals, because they change user-visible text, are their own task,
or are a decision rather than a defect.

---

## Fixed in this pass

**F1. `docs/C.md` was a 3075-line accidental duplicate.** Byte-identical
to the first 3075 lines of `docs/task-log.md`, added by a stray redirect
in `5542f67` (C2) and carried for a week. Deleted; nothing referenced it
except one task-log sentence listing it as a historical record.

**F2. The naming rule was implemented twice.** `provision.Config.Name`
(`pkg/provision/desired.go`) and `gcpb.Prefixed`
(`pkg/backends/gcp/naming.go`) both applied the env prefix, with the
default env spelled as a constant in one and as a bare `"prod"` literal
in the other — while `naming.go`'s own doc comment claimed the rule was
"shared verbatim by the runtime and the provisioner **so the names
services read are the names deploys create**". They agreed by luck.
A change to either would have had deploys create resources under names
the runtime resolves differently — a failure that shows up only in the
cloud, only at run time, and reads as a missing resource. Now one
function and one constant, with all 35 `Config.Name` call sites going
through it.

**F3. `err.(*apiError)` instead of `errors.As`, at 10 sites.** The
four-way permission taxonomy (`Classify`), the SERVICE_DISABLED
convergence (`disabledAPI`), and every "already gone / not yet created"
404 check used a bare type assertion. They work *today* because
`Classify` is always called at the leaf, on the raw error — but
`provision.Apply` wraps with `%w` two frames up, and 34 sites in the tree
wrap that way, so the invariant holding was a convention nobody had
written down.
The first refactor that moved a `Classify` call up one frame would have
silently demoted every 403 into a generic message with no remediation.
Now `asAPIError` uses `errors.As`.

**F4. Six copies of the 404 predicate**, collapsed into one `notFound`
helper — the same shape observers read as "not created yet" and deletes
read as "already gone".

**F5. `gofmt` and `go vet` are clean.** One unformatted file
(`internal/schedule/schedule_test.go`) and one real vet finding (a
leaked `context.WithDeadline` cancel in `pkg/runtime/call_test.go`).
Both fixed. Full suite green, all six examples green.

---

## P1 — Logging never became cloud-shaped — **SHIPPED 2026-08-01**

> Fixed in the slice recorded in the task log: `BootGCP` installs a
> JSON handler with the severity rename, request logs became
> `httpRequest` records, and handler lines carry the trace that nests
> them under their request. `validate/logging.md` is its gate script.
> The description below is what was wrong.

`pkg/runtime/http.go:178`:

```go
// logRequests prints one terse line per request in dev; structured JSON
// request logs become the default when the cloud runtime lands (Phase E).
```

**Phase E landed in Stage 2.** The comment is a promise that expired
five stages ago, and the code under it still does
`fmt.Printf("%s %s → %d (%.1fms)\n", …)` — the dev-console format, in
production, on stdout.

The larger half: **no `slog` handler is installed anywhere in the
framework.** `ctx.Log()` hands back `slog.Default()`, so a deployed
service writes Go's default *text* handler to stderr. Cloud Logging
files everything a container prints as severity `DEFAULT` unless the
line is JSON carrying a `severity` field. So:

- `ctx.Log().Error(…)` and `ctx.Log().Info(…)` are indistinguishable
  once they land;
- `tako logs` prints a `severity` column that is always the same value;
- observability.md's "no severity filter" limitation, which reads like
  a CLI gap, is really *"nothing ever emits a severity"*.

This is the one finding I would not ship without a decision on. The fix
is small — a JSON handler at boot in the cloud, a `ReplaceAttr` mapping
`level`→`severity`, and `logRequests` emitting through it — and it is
worth a slice of its own because it is user-visible behavior.

**Documented honestly in the meantime**: observability.md now has a
Logging section and states the severity limitation under "Not yet"
rather than leaving users to discover it in an incident.

## P2 — No injectable HTTP host, and 130 hand-built URLs — **SHIPPED 2026-08-02**

> Client.url with named host constants in both gcp packages; Client.Base
> redirects every host at one fake server. Proved by reintroducing the
> U2 container-port bug and watching the new test catch it. The
> description below is what was wrong.

Already on the discovered-work list from U2; this review adds the scale
and the duplication angle. Every cloud call builds its URL inline:

```go
url := fmt.Sprintf("https://run.googleapis.com/v2/%s/services?pageSize=1000", r.parent())
u := "https://datastore.googleapis.com/v1/projects/" + d.C.Project + "/indexes"
```

130 literal `https://*.googleapis.com` URLs across 28 files in
`pkg/provision/gcp` and `pkg/backends/gcp`, in two styles (`Sprintf`
and `+` concatenation), with the `projects/%s/locations/%s` shape
rebuilt over and over. `preflight.go` alone has 21.

The duplication is the smaller cost. The larger one is that **nothing
in `pkg/provision/gcp` or `pkg/backends/gcp` can be tested against a
fake server**, which is why four failures this stage were only findable
live: the named container port with no number, the
`cloud_run_revision` metric refusal, and two more. One change fixes
both problems:

```go
func (c *Client) url(host, format string, args ...any) string  // base overridable in tests
```

Sized as its own task; it touches every adapter file.

## P3 — `pkg/runtime` is an unversioned public API

Generated `.tako/gen/<service>/main.go` — which lives in the *user's*
repo — imports 33 distinct symbols from `pkg/runtime`, out of ~87
exported. Example test suites import it directly too.

So `pkg/runtime`'s surface is a compatibility contract in practice,
while the root package (148 locked entries, two written reviews, a
`sdk_lock.golden` regenerated deliberately) is the only one treated as
one. Renaming `runtime.JSONHandler` breaks every checked-in generated
file until the next `tako test`.

Two honest options, both cheap: state the policy ("`pkg/…` is internal;
regenerate after upgrading"), or extend the lock-test pattern to
`pkg/runtime`. Doing neither means finding out from a user.

## P4 — "Not wired" has two mechanisms and three phrasings

One condition — a resource used with no app booted — answers three
ways:

| Resource | Mechanism | Text |
|---|---|---|
| Bucket, Cache, Entity, Queue, Topic, Metrics, signer | **panic** | `tako: no entity backend wired — run via tako dev, a generated service, or tako.NewTestApp` |
| Lock, Job | **returned error** | `tako: locks are not wired — Do works in booted services (tako dev / deploy) and NewTestApp tests, not bare tests` |
| Site | **returned error** | `tako: site templates are not wired — Render works in generated services (tako dev / deploy), not bare tests` |

The mechanism split is not forced by signatures: `Entity.Get` returns an
error and panics anyway; `Lock.Do` returns an error and uses it. Each
text is good on its own — the Site one is more accurate than the others,
since templates genuinely need a *generated* service — but a user
meeting two of them learns that tako answers the same mistake in two
moods.

There is probably a defensible rule ("panic where it can only be a
programmer error; return where a bare test is a legitimate place to
be"), but the current split does not follow it. Since this is product
text, it wants a decision, not a refactor.

## P5 — `ctx.User()` panics with two different texts

`pkg/runtime/ctx.go:29` and `testapp.go:221` handle the identical
mistake:

```
tako: Ctx.User on a request with no authenticated user — auth is declared with tako.Auth and required per-route with tako.WithAuth
tako: ctx.User on an unauthenticated context — pass a user to app.Ctx, or use MaybeUser
```

Different remediation is *correct* here — the fix genuinely differs
between a request and a test. What is not deliberate is the method
being spelled `Ctx.User` in one and `ctx.User` in the other. Small, and
worth aligning while the texts are still cheap to touch.

## P6 — The CLI exits two different ways

`cmd/tako/main.go` dispatches twelve commands. Six route errors through
`fail(err)`, which prints the error bare. Four inline the same three
lines instead:

```go
fmt.Fprintf(os.Stderr, "tako: %v\n", err)
os.Exit(1)
```

So `tako deploy` failing prints the message, and `tako prune` failing
prints `tako: ` first — a prefix that appears or not depending on which
command you ran. Four copies of one block, and one user-visible
inconsistency. `cli-review-1` audited uniformity across the command
surface and did not reach into the dispatcher.

Also here: `gen` is the only command implemented inline in `main()`
rather than delegating to a `runX`, which is why its ~40 lines of
mode-parsing sit in the switch.

## P7 — `pkg/devserver` has no tests

447 lines implementing the whole `tako dev` loop — watch, analyze,
generate, run, restart, the resource banner — with zero `_test.go`
files. It is covered by manual gate scripts (`validate/skeleton.md`
onward), which is real coverage but the slowest kind, and it is the
component a new user meets first. `internal/boot`, `internal/hooks` and
`internal/secrets` are likewise untested except through their callers.

Everything else is well covered: analyzer goldens, the conformance
suite across both backends, runtime tests including the shutdown drain,
provision plan tests, and six example suites.

## P8 — Small duplication, listed for completeness

- **`plural(n)` exists four times** — `cmd/tako/genclient.go:59`,
  `pkg/runtime/shutdown.go:190`, `pkg/devserver/devserver.go:442`,
  `pkg/analyzer/summary.go:106`. Four unexported copies of a two-line
  function in four packages is idiomatic Go, not a defect; noting it
  because a shared `internal/text` would also be reasonable.
- **`pickDead` and `pickLocalDead`** (`cmd/tako/dlq.go:164`,
  `dlq_local.go:177`) are structurally identical prefix resolvers
  differing only in element type and one error string. A generic
  `pickByPrefix[T]` would collapse them; the two error texts should
  stay distinct.

## P9 — `cmd/tako/deploy.go` is 1070 lines

It holds `runDeploy`, `runLogs` and `runDestroy` — three separate
commands — plus about twenty helpers. `logs` and `destroy` living in
`deploy.go` is the only place in the CLI where the file name does not
predict what is in it. Mechanical to split (`logs.go`, `destroy.go`)
and worth doing before more lands on top of it.

---

## Documentation review

Covered in the same pass, since "can someone use this without reading
the code" is a release question.

**The corpus is disciplined.** 22 reference pages all following the same
seven-section shape, 9 guide pages, a root README that gets to a URL in
five minutes. Nothing in `docs/reference` or `docs/guide` mentions a
retired symbol — `WithCDN`, `WithRateLimit` and `Footprint.Drain` appear
only in historical records, correctly. All 28 `validate/*.md` scripts
are cited by a signed verdict in `validation-log.md`; none is dead
weight. Of the 148 locked SDK symbols, all but the `*Handle` type names
(which docs correctly reference through their constructors) appear in
the docs.

**Three gaps, all now closed:**

1. **No page for the test surface.** `NewTestApp`, `DrainQueues`,
   `AdvanceCron`, `Chaos`, `SetSecret`, `SignIn` were scattered across
   eight feature pages' "Test it" sections; `Advance` (the fake clock)
   and `HTMX` appeared nowhere at all. Added
   `docs/reference/testing.md`.
2. **No index of analysis errors.** 38 rules, of which 10 appeared in
   no doc — `auth-provider`, `deadline`, `job-binding`, `mixed-traffic`,
   `package-level`, `page-binding`, `subscribe-binding`, `timezone`,
   `unexported-req`, `worker-binding`. Added
   `docs/reference/errors.md`, indexing every rule with its verbatim
   text and what it protects.
3. **`ctx.Log()` was undocumented** — the only `Ctx` method with no
   mention anywhere, despite being how a user emits a log line. Added
   to observability.md, along with the severity limitation from P1.

**One stale claim, fixed:** observability.md's "Not yet" still described
an `instance` metric label that U2 retired when metrics moved to
`generic_task` (the label is `revision` now, with instance identity
carried by the resource's `task_id`).

## The pattern behind the findings

F2, F3 and P1 are the same shape as the defects the last two gates
caught: **an invariant that holds by convention with nothing asserting
it.** The naming rule agreed by luck. The taxonomy survives wrapping by
luck. The Phase-E logging promise expired with no test to notice.

Where this codebase does assert its invariants — golden error texts, the
conformance suite, `sdk_lock.golden` — it is genuinely hard to break. The
proposals above are mostly about extending that habit to the three or
four places it has not reached yet.
