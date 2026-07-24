# Tako cloud permissions — the whole-system design

Written before C2 so every cloud slice implements ONE permission model
instead of accreting per-step patches. Normative for Stage 2: every
C-phase DX proposal carries a "Cloud errors & permissions" section
designed against this document.

## Principles

1. **Two layers, opposite biases.** The *deployer* (a human or CI
   running `tako`) gets a simple, one-time, documented setup — friction
   here is felt on day one and drives people away. The *runtime* (the
   deployed services) gets strict least privilege derived per service
   from the AppGraph — permanence lives here, so minimality matters
   most here. Never trade one for the other.
2. **A dedicated project per app.** Tako assumes it owns the project
   (settled: reconcile-by-label, destroy-by-label). Sharing a project
   with non-tako infrastructure multiplies both blast radius and
   permission ceremony; a dedicated project makes the deployer bundle
   small and the runtime derivation sound.
3. **Preflight as UX.** Every cloud command checks what it needs up
   front (`testIamPermissions`, API-enabled probes) and fails with the
   *specific* missing grant and the copy-pasteable command that fixes
   it — the analyzer-error philosophy applied to IAM. No stack traces,
   no "permission denied" without a noun.
4. **Name the grant, never guess broader.** Remediation texts name the
   narrowest predefined role that unblocks the step (e.g.
   `roles/artifactregistry.writer` on one repository — never
   `roles/editor`, never project-wide when resource-scoped works).
5. **Errors are taxonomy, not strings.** Cloud interactions fail in
   four distinguishable ways and the texts must not conflate them:
   - **401 unauthenticated** → "no Google credentials; run gcloud auth login"
   - **403 unauthorized** → "authenticated but lacks X; grant: <command>"
   - **403 API disabled** → "enable it: gcloud services enable <api>"
   - **404 missing resource** → "does not exist; create: <command>"
   (C1's push errors implement this; it is the template.)

## Layer 1 — the deployer

One-time setup for a fresh dedicated project (later `tako deploy` will
print exactly this when preflight finds gaps; until then it's the
documented block):

```
gcloud services enable artifactregistry.googleapis.com run.googleapis.com \
  iam.googleapis.com datastore.googleapis.com pubsub.googleapis.com \
  cloudtasks.googleapis.com secretmanager.googleapis.com \
  cloudscheduler.googleapis.com storage.googleapis.com --project <project>
```

Deployer role bundle (granted to the human/CI principal, project-scoped
— acceptable because the project is dedicated):

| Role | Why |
|---|---|
| `roles/artifactregistry.writer` | push images (C1) |
| `roles/run.admin` | create/update Cloud Run services (C2) |
| `roles/iam.serviceAccountAdmin` | create per-service SAs (C2) |
| `roles/iam.serviceAccountUser` | attach SAs to services (C2) |
| `roles/storage.admin` | buckets (C3+) |
| `roles/datastore.owner` | entity stores + indexes (C3+) |
| `roles/pubsub.admin` | topics/subscriptions (C-async) |
| `roles/cloudtasks.admin` | delayed queues (C-async) |
| `roles/secretmanager.admin` | secret references (C-config) |
| `roles/cloudscheduler.admin` | crons (C-async) |

Each C-task's preflight checks only the subset that task touches; the
full bundle exists so a user can grant once and never think about it
again. (A `tako.deployer` custom role bundling these is a later nicety,
not a requirement — predefined roles keep the setup explainable.)

## Layer 2 — the runtime (derived, minimal, per service)

One service account per service: `<app>-<service>@<project>`. Grants
derive from AppGraph edges only — nothing project-wide, nothing manual
(spec §4.6's table is the source of truth):

| Edge in the graph | Grant (resource-scoped) |
|---|---|
| service reads/writes bucket B | `roles/storage.objectAdmin` on B only |
| service uses entity store | `roles/datastore.user` |
| service publishes to queue/topic T | `roles/pubsub.publisher` on T only |
| worker/subscriber service | its push endpoint grants `roles/run.invoker` to the Pub/Sub push SA only |
| service reads secret S | `roles/secretmanager.secretAccessor` on S only |
| service A calls service B (spec §2.12) | `roles/run.invoker` on B for A's SA only |

`tako deploy` prints this derivation as the security summary — the user
sees every grant and why, every deploy. Removing a declaration removes
the grant on the next reconcile (grants are not data; the
never-auto-delete rule protects data, not IAM).

## What this means per remaining C-task

- **C2 (reconciler + deploy)**: preflight run.admin + SA roles; errors
  distinguish API-disabled vs missing-role vs org-policy denials;
  security summary ships here.
- **C3+ (backends)**: each backend's provision step preflights its own
  role; runtime code NEVER handles 403 by escalating — a runtime 403 is
  a reconciler bug and says so.
- **Async (Pub/Sub/Tasks)**: push auth is OIDC from the push SA;
  verification failure texts distinguish "no token" (misconfigured
  subscription) from "wrong audience" (stale URL).

## Non-goals

- No VPC/org-policy management (out of scope by doctrine).
- No support for shared multi-tenant projects in v0 — detected (foreign
  unlabeled resources at reconcile) and warned, never managed.
- No automatic role granting by tako itself: tako PRINTS grant commands,
  the human runs them — the tool that deploys should not also be the
  tool that escalates its own permissions.
