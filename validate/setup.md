# validate/setup.md — SC2 (`tako setup`, `tako datastore create`)

**Read step 6 first if you are deciding whether this is done.** Every
other step ran; the live path did not, because there is no project to
run it against.

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

## 6. NOT DONE: the live path

Everything above runs against a fake project. **No part of SC2 has
touched a real GCP project**, which means these are unproven:

- that `services:batchEnable` behaves as assumed (it is classified as
  idempotent-mutating and will be retried);
- that `:getIamPolicy` / `:setIamPolicy` round-trip preserves bindings
  the fake does not model (conditions, `deleted:` principals);
- that the hand-over block is correct — a fake cannot tell me the
  commands I print actually work;
- the billing check, `firestore databases create`, and every real
  refusal text.

A fake server can prove the calls tako makes. Only a project can prove
they are the right ones. **This is the outstanding gate for SC2**, and
it needs a throwaway project to point at.

## 7. The suite is unaffected

```
go test ./... && for d in examples/*/; do (cd $d && tako test -count=1 ./...); done
```

**Expect** the root suite and all seven examples green. The one
framework change outside the new files is `Requirements()` — extracted
from `Preflight` so setup and preflight cannot derive different answers,
with preflight's behaviour unchanged.
