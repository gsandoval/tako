# Validate C6 — lifecycle: change classes, canary/promote/rollback, prune, --env

Stage 2's closer: the deploy's whole life story, walked in one sitting
on the dedicated test project with gallery. Prereqs: gcloud ADC, the
Datastore database, `notifier-key` (prod) — and step 8 will demand the
pr-42 copy.

## 1. Deploy, and read the classified plan

```
cd examples/gallery
tako deploy --project <test-project>
```

**Judge:** every create wears `+`, updates `~`, skips are quiet. A
plain first deploy has nothing beyond additive/in-place, so NO prompt
appears — the ceremony is reserved for plans that earn it.

## 2. Drift-inject, and get told

```
gcloud run services update gallery --memory 1Gi --region <r> --project <p>
tako deploy --plan --project <test-project>
```

**Judge:** the gallery line reads `~ update … ← DRIFT: modified
outside tako; tako-owned fields will be overwritten`. Run a real
deploy; the next `--plan` is clean (tako-owned fields restored).

## 3. Canary — the new code auditions

Make a visible change (e.g. bump a caption string or just trust the
digest change from any edit), then:

```
tako deploy --canary=10 --project <test-project>
for i in $(seq 1 30); do curl -s -o /dev/null -w '%{http_code} ' $URL/api/photos; done
```

**Judge:** the canary block prints each service's split — gallery
`10% <new revision> / 90% <old>`, every async service `100% <old>`
(staged at 0%, the line says so). The curl loop keeps succeeding; in
`tako logs gallery` both revisions serve. The async spine still runs
OLD code: the whole app is "old + a 10% taste", never mixed.

## 4. Promote

```
tako promote --project <test-project>
```

**Judge:** every service → `100% latest (sha <git sha>)`; a second
curl loop is all-new; async services now run the new revision.

## 5. Rollback

```
tako rollback --project <test-project>
```

**Judge:** each service prints `<serving> ← <previous> (sha …)` —
instant, no build step ran. Any single-revision service says `stays —
only one revision exists`. `tako rollback` again returns... nothing
older: the error text. Re-`tako promote` (or deploy) before moving on.

## 6. Orphans, rename suspects, prune

Add a scratch resource, deploy, rename it, read the plan, remove it,
prune:

```
# in app.go:  var scratch = tako.Topic[PhotoReady]("scratch")
tako deploy --project <p>                    # + create topic scratch
# rename in code: "scratch" → "scratch2"
tako deploy --plan --project <p>
```

**Judge:** the plan shows `+ create topic scratch2` AND `? orphaned
(kept) topic scratch` with the `↳ rename suspect` line saying data
does not follow automatically; a real deploy PROMPTS (`continue?
[y/N]` — replaces/suspects only) and `--yes` skips it. Then delete the
scratch var entirely, deploy (orphan remains), and:

```
tako prune --project <p>                     # ✗ topic scratch2 (stateless)
tako prune --project <p>                     # "no orphans — nothing to prune"
```

For the data path: `tako prune --data` when an orphaned bucket or an
undeclared datastore kind exists lists counts and requires the typed
app name (exercise it in step 8's env teardown if not here).

## 7. Replace class (observed, not forced)

A queue/topic rebind (changing a Worker's queue) renders as
`! update push subscription … ← REPLACE: topic is immutable … in-flight
messages … lost` and prompts. Judging the rendered text from the unit
test (TestReplaceClassAndRenameSuspects) is acceptable — forcing a
live rebind on gallery is app surgery.

## 8. --env previews are whole, disjoint apps

```
tako deploy --env=pr-42 --project <p>        # preflight demands pr-42-notifier-key
printf 'pr' | gcloud secrets create pr-42-notifier-key --data-file=- --project <p>
tako deploy --env=pr-42 --project <p>
curl -s <pr-42 gallery URL>/api/photos      # empty feed: nothing shared
tako destroy --env=pr-42 --project <p>      # typed confirmation; prod untouched
```

**Judge:** every name is pr-42-prefixed, the Datastore namespace is
pr-42, the secret is its own, and destroying it leaves prod serving.

## 9. Destroy

```
tako destroy --project <test-project>
```

Clean project (the known cosmetics — conf-* namespaces, shared
composite indexes, conf-signer SA — stand). Record the verdict in
docs/validation-log.md.
