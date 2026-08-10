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
  cloudscheduler.googleapis.com storage.googleapis.com \
  monitoring.googleapis.com --project <project>
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
| `roles/monitoring.alertPolicyEditor` | `tako.Alert` policies (Y1) |
| `roles/monitoring.notificationChannelEditor` | `tako.AlertEmail`/`AlertWebhook`/`AlertTopic` channels (Y1) |
| `roles/serviceusage.serviceUsageConsumer` | bill tako's API calls to the target project, not to gcloud's shared client (FL19) |
| `roles/identitytoolkit.admin` | `tako users` against a project: create the first admin, hand out roles (SC1) |

**On the accounts row.** It appears in Layer 2 as well, derived per
service, and that is not a duplicate: there it is a property of a
service whose code calls `auth.Users()`, and here it is a property of
the human. `tako users --project` is the only bootstrap path to a first
admin — before it existed the answer was "write a program, deploy it,
run it once, delete it" — and a deployer who can `tako deploy` can
already deploy code that mints accounts. The grant adds no power the
principal lacks; it removes a detour. It is the only Layer 1 role that
is not needed for a deploy, so a CI principal that never runs
`tako users` can leave it out.

**On the quota one.** Every call tako makes names `--project` as the
quota project (`X-Goog-User-Project`). Without it, Application Default
Credentials from `gcloud auth application-default login` carry no quota
project at all, so Google meters the call against the project behind
gcloud's own shared OAuth client — shared with every gcloud user on
earth, and rate-limited accordingly. The first `tako logs` after a
deploy met HTTP 429 for a consumer that was not the target project,
while `gcloud logging read` against the same project worked, because
gcloud bills its own reads correctly.

Most deployers already have this: `serviceusage.services.use` is
included in `serviceUsageViewer` (the preflight's enabled-API check)
and in `serviceUsageAdmin` (service agents). It is listed separately
because a deployer holding only the table's other roles would meet a
403 naming a service nobody asked tako to touch; `Classify` gives that
one its own text and the grant to run.

Each C-task's preflight checks only the subset that task touches; the
full bundle exists so a user can grant once and never think about it
again. (A `tako.deployer` custom role bundling these is a later nicety,
not a requirement — predefined roles keep the setup explainable.)

## Layer 2 — the runtime (derived, minimal, per service)

One service account per service: `<service>-sa@<project>` (env-prefixed
for non-prod envs, clamped to IAM's 6–30 chars with a stable hash
suffix — `Config.SAName` is the source of truth). Grants
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
| service READS accounts (`auth.Users().Get/ByEmail/List`) | `roles/identitytoolkit.viewer` on the project |
| service WRITES accounts (`Create/Grant/Revoke/Disable/Delete`) | `roles/identitytoolkit.admin` on the project |
| every service | `roles/monitoring.metricWriter` on the project |

The two account rows are split deliberately: reading the list of people
is not the same power as minting an account and handing out a role, and
Identity Platform has a separate predefined role for each. A service
whose package never calls `auth.Users()` produces no such edge and gets
neither — "this service can mint accounts" stays a property of the
service that asked for it. Neither role is project-scopable further:
Identity Platform's IAM is project-level, the way Datastore's is.

The API these need — `identitytoolkit.googleapis.com` — is already
enabled by the Identity Platform section below, because declaring
`tako.Auth` is what turns sign-in on in the first place. A runtime 403
naming it therefore means the service is running against a project tako
did not provision, and the error text says exactly that.

The last row is the one exception to edge-derivation, and it is stated
rather than hidden: counters and histograms produce no usage edge (a
`tako.Counter` is recorded into, not wired to a service), and every
service exports per-endpoint metrics whether or not it declares one. So
the grant is package-wide, like secrets. Cloud Monitoring has no
per-metric IAM to scope it further.

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

## Identity Platform (tako.Auth) — automated to the honest maximum

Declaring `tako.Auth(...)` makes sign-in part of the app, and the
deploy automates EVERYTHING mechanical, narrated as it goes:

- **Initialization**: absent Identity Platform config is initialized
  by the deploy (`auth initialized Identity Platform`). (Admin calls
  need the `x-goog-user-project` quota header on user ADC — handled.)
- **Email provider**: declaring `WithEmail` IS the decision — the
  deploy enables it (`auth email/password provider enabled`).
- **Browser client config**: the auto-provisioned public API key and
  auth domain are discovered and injected as env for the framework's
  sign-in page.
- **Authorized domains**: the app's own service hosts — BOTH Cloud Run
  URL forms — are registered for sign-in popups (`auth <host>
  authorized for sign-in`).

**The one genuinely manual step** is the Google provider, because
Google exposes no public API for creating external OAuth clients. The
deploy BLOCKS with this instruction until it is done (and rejects
placeholder client ids — the console accepts anything; Google then
answers `invalid_client` at the first popup):

> enable the Google provider:
> `https://console.firebase.google.com/project/<p>/authentication/providers`
> → Google → Enable (pick a support email) → Save — the Firebase
> console auto-provisions the OAuth client. Manual alternative (org-
> restricted consoles): consent screen + an OAuth web client with
> redirect URI `https://<p>.firebaseapp.com/__/auth/handler`, pasted
> into Identity Platform → Providers → Google.

`tako deploy --plan` performs all checks without mutating (would-do
narration). **The user-facing developer documentation must carry this
section** — until that documentation exists as its own deliverable,
this file and the deploy's blocking error text are its homes.

## Non-goals

- No VPC/org-policy management (out of scope by doctrine).
- No support for shared multi-tenant projects in v0 — detected (foreign
  unlabeled resources at reconcile) and warned, never managed.
- No automatic role granting by tako itself: tako PRINTS grant commands,
  the human runs them — the tool that deploys should not also be the
  tool that escalates its own permissions.
