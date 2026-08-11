# validate/setup.md — SC2 (`tako setup`, `tako datastore create`)

**Read 6a first if you are deciding whether this is done.** The live
path ran against `tako-test-01` on 2026-08-10, including the IAM write.
Three things remain unproven, each needing a condition this project
does not have.

## 1. What SC2 is, and is not

Preflight already derived exactly what an app's graph needs, checked
every API and permission, and printed a copy-pasteable fix for each gap.
It never applied one. SC2 is that half, and nothing else — the earlier
claim that tako "cannot tell you what is missing" was wrong and is
corrected in the task log.

## 2. The region that is never defaulted

```
cd examples/journal && tako datastore create
```

```
tako: tako datastore create needs --region, and will not pick one for you
      a Datastore database's location is permanent: it cannot be moved,
      and every other --region in this CLI defaults because it can be
      changed by deploying again. Pick the region your services run in.
```

**Expect** this refusal BEFORE any project resolution — it needs
nothing from the cloud, and meeting "no project set" first would teach
the wrong lesson. `tako datastore` with no subcommand prints the same
reasoning as usage.

This is the whole reason the database is its own command rather than a
step inside setup (D4, owner 2026-08-10): every other thing tako
provisions is derivable, replaceable or revocable, and this one is not.

## 3. Setup grants the tighter set

```
go test ./pkg/provision/gcp -run 'Plan|SecondRun|Granting|Applying' -count=1
```

**Expect** green, including:

- `TestThePlanIsWhatThisGraphNeedsAndNoMore` — an app with no queues is
  never granted `pubsub.admin`, and one with no crons never gets
  `cloudscheduler.admin`. This is the owner's ruling made testable.
- `TestASecondRunHasNothingToDo` — the tighter set makes re-running
  setup the NORMAL way an app grows into a permission, so idempotence
  is a property rather than a nicety.
- `TestPlanningMutatesNothing` — the fake fails the test on any
  mutating call during a plan.
- `TestGrantingToAnotherPrincipalReadsThePolicyNotTheCaller` — the CI
  case. `testIamPermissions` answers for the CALLER, so using it for a
  service account would report every role as already held (the human
  holds them), grant the service account nothing, and report success.
- `TestApplyingKeepsBindingsItDidNotComeFor` — an IAM policy is a
  document written whole; a careless write removes other people.

## 4. The guard that found real bugs

```
go test ./pkg/provision/gcp -run 'PermissionsDoc|PreflightGaps' -count=1
```

`docs/tako-cloud-permissions.md`'s Layer 1 table has been the source of
truth since before any code knew these roles — it is what a user copied
by hand, and it is what an operator reads when setup CANNOT grant. Now
that code derives them too, the two are held together: every role tako
can grant must appear in the table (the table may list more, being the
"grant once and stop thinking" maximum).

It failed on first run and found three genuine defects:

- **`artifactregistry.writer` was not enough.** The deploy CREATES the
  repository and only `.admin` grants
  `artifactregistry.repositories.create`. Anyone granted exactly what
  the table said failed on their first deploy.
- **`resourcemanager.projectIamAdmin` was missing entirely**, though
  preflight has required `resourcemanager.projects.setIamPolicy` all
  along — every derived runtime grant is a policy write. Same failure,
  same first deploy.
- **`serviceusage.serviceUsageAdmin`** is newly needed, because setup
  enables APIs rather than printing the command.

The doc is corrected. The guard is why they were found at all.

## 5. Preflight now points at the command

`TestPreflightGapsPointAtTheCommandThatFixesThem` pins that a gap says
`tako setup --project …` rather than a raw `gcloud` line, and that the
database gap does **not** print `gcloud firestore databases create` —
that would pick the region for the user, which is the one choice that
cannot be taken back.

## 6. The live path — run against tako-test-01 (2026-08-10)

Owner supplied the project. What ran, and what it proved:

**The tighter set, in real data.** Each example planned against the same
project:

```
hello      nothing to do — 4 API(s) on, 6 role(s) held
shortlink  nothing to do — 7 API(s) on, 9 role(s) held
gallery    nothing to do — 10 API(s) on, 11 role(s) held
blog       nothing to do — 5 API(s) on, 7 role(s) held
```

One project, one principal, different answers per graph. That is the
owner's ruling working against something real rather than a fake.

**A gap, and who can see it.** `cloudscheduler.googleapis.com` was
disabled (no scheduler jobs existed, so nothing was lost, and the
command under test puts it back). gallery, which declares a cron:

```
  APIs — 1 to enable, 9 already on
    + cloudscheduler.googleapis.com
```

hello, in the same project, at the same moment: `nothing to do`. The
gap is invisible to a graph that does not need it.

**Apply, and idempotence.**

```
  apply? [y/N] y
  ✓ enabled  1 API(s): cloudscheduler
```

`gcloud services list --enabled` confirms it landed;
re-running immediately reports `nothing to do — 10 API(s) on`.
`services:batchEnable` behaves as assumed, and its
idempotent-mutating classification holds.

**The database.** gallery declares entities and the remainder said
nothing about a database — correct: the project has one, and
`DatastoreExists` saw it. Creating it again exercises the conflict
path:

```
tako datastore · tako-test-01
  creating a datastore-mode database in us-central1 — this location is permanent
  create? [y/N] y
tako: tako-test-01 already has a database — its location cannot be changed,
      so there is nothing to do here
```

**The CI path, which is the one that could have failed silently.** A
throwaway service account, planned against a project where the CALLER
holds everything:

```
  Roles for serviceAccount:tako-sc2-probe@… — 6 to grant, 0 already held
    + roles/run.admin                          run.services.create
    + roles/iam.serviceAccountAdmin            iam.serviceAccounts.create
    + roles/iam.serviceAccountUser             iam.serviceAccounts.actAs
    + roles/artifactregistry.admin             artifactregistry.repositories.create
    + roles/storage.admin                      storage.buckets.create
    + roles/resourcemanager.projectIamAdmin    resourcemanager.projects.setIamPolicy
```

`0 already held` is the proof. Had this used `testIamPermissions` — which
answers for the caller — it would have said "6 already held, nothing to
do", granted the service account nothing, and printed success.

**The write itself**, authorized by the owner and run against the same
account:

```
  apply? [y/N] y
  ✓ granted  6 role(s) to serviceAccount:tako-sc2-probe@tako-test-01.iam.gserviceaccount.com
```

Verified against `gcloud projects get-iam-policy`, before and after:

```
roles the probe now holds:
    roles/artifactregistry.admin
    roles/iam.serviceAccountAdmin
    roles/iam.serviceAccountUser
    roles/resourcemanager.projectIamAdmin
    roles/run.admin
    roles/storage.admin

changes to anything NOT the probe:
    none

bindings before/after: 13 / 19
```

`changes to anything NOT the probe: none` is the important line. An IAM
policy is a document written whole, so the read-modify-write either
preserves every other principal or quietly removes people;
`TestApplyingKeepsBindingsItDidNotComeFor` asserts it against a fake and
this proves it against a real policy with 13 existing bindings.

**Re-running granted nothing** (`nothing to do — 4 API(s) on, 6 role(s)
held`), so the grant path is idempotent too.

**And the ruling's consequence, live.** The same principal, planned
against gallery instead of hello:

```
  Roles for serviceAccount:tako-sc2-probe@… — 5 to grant, 6 already held
    + roles/datastore.indexAdmin               datastore.indexes.create
    + roles/cloudscheduler.admin               cloudscheduler.jobs.create
    + roles/pubsub.admin                       pubsub.topics.create
    + roles/secretmanager.viewer               secretmanager.secrets.get
    + roles/secretmanager.admin                secretmanager.secrets.setIamPolicy
```

Six held from the smaller app, five more for the larger one. That is
"setup is a command you re-run as the app grows" doing exactly what the
tighter-set ruling intended, incrementally, against a real policy.

**Cleanup.** All six bindings removed, the service account deleted, and
the project's policy diffed back to byte-identical with where it
started: `policy restored: True`, 13 bindings before and after.

This run also found a cosmetic defect (a doubled blank line before
`--plan: nothing was changed`), now fixed.

## 6a. STILL NOT PROVEN

- **The hand-over block.** It needs a principal without
  `setIamPolicy`, which the owner's account is not.
- **The billing refusal**, which needs a project without billing.
- **Item 44** (`waitForAPIs` is never called): the one API enabled here
  was not immediately followed by a grant, so the propagation window was
  never exercised.

## 7. The suite is unaffected

```
go test ./... && for d in examples/*/; do (cd $d && tako test -count=1 ./...); done
```

**Expect** the root suite and all seven examples green. The one
framework change outside the new files is `Requirements()` — extracted
from `Preflight` so setup and preflight cannot derive different answers,
with preflight's behaviour unchanged.
