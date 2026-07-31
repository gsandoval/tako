# Stage 7 handoff — domains/CDN, metrics export, dlq replay, shared-cache plan

A self-contained brief for whoever implements Stage 7. Read this WITH
the repo; it points at precedents instead of restating them. The
stage was proposed and approved 2026-07-31 (task-log "Stage 7
proposal" + the user's priority order); slice order U1 → U2 → U3 →
U4, U4 may interleave.

## Ground rules (non-negotiable, from CLAUDE.md + standing practice)

- **Workflow per slice:** DX proposal already approved at stage level,
  but each slice still ends in: implement → agent-side scripted+live
  validation (transcript into docs/task-log.md) → a gate script in
  validate/<name>.md → the user's verdict row in
  docs/validation-log.md. STOP between slices only if something
  contradicts the approved proposal.
- **NEVER `git push`, never tag.** Commit via feature branch →
  ff-merge to main → delete branch, message prefixed with the task ID
  (U1:/U2:/U3:/U4:), trailer
  `Co-Authored-By: <your model name> <noreply@anthropic.com>`.
  Exclude the stray `logaster-2013-06.jpg` from staging.
- **Error texts are product**, golden-tested: analyzer errors are
  `path:line:col: message (tako/rule-id)` with fixtures in
  pkg/analyzer/testdata/errors/<name>/ (app.go + go.mod/go.sum copied
  from a sibling + expect.golden.txt via `go test ./pkg/analyzer -run
  TestErrors/<name> -update`).
- **The SDK surface is reviewed.** New exported symbols (WithShared —
  do NOT add it in U4; U1/U2/U3 need NO new symbols: WithDomain,
  WithCDN, Counter, Histogram already exist) must regenerate
  testdata/sdk_lock.golden deliberately (`go test . -run TestSDKLock
  -update`) and be called out in the task log.
- **Costs visible:** standing money always gets a ⚠ at PLAN time
  (precedent: warnFootprints in cmd/tako/deploy.go).
- **Test project:** tako-test-02 (dedicated; leave it PRISTINE after
  every walk — destroy, delete scratch secrets/accounts). ADC is
  logged in. Expect IAM propagation windows (~1–5min) on fresh
  grants/SAs: poll, don't fail. tako-test-01 also exists.
- **Full suite green before every commit:** `go test ./...` from the
  repo root (workspace covers all examples).

## Provisioner architecture you must follow

- Desired state: pkg/provision/desired.go builds []Object per Kind;
  kinds + reconcile order in pkg/provision/provision.go
  (reconcileOrder is additive-first; destroy walks it backwards).
- **Apply ORDER matters and bit us twice:** infrastructure a grant
  targets must be APPLIED BEFORE the grants pass (T3 postmortem:
  tasks queues applied after grants → enqueuer grants converged
  against a missing target). deploy.go's explicit Apply chain: put
  new infra kinds into the first Apply filter list (with
  KindTasksQueue) unless they depend on run services.
- Appliers live in pkg/provision/gcp/<kind>.go with
  List/Create/Update/Delete + Kind(). Labels are the managed marker
  where the API supports them; where it doesn't, use the established
  fallbacks: description marker `tako:<app>:<env> spec-hash=<h>`
  (scheduler.go) or a name suffix (tasks.go's `-tako`). LiveHash must
  reproduce EXACTLY the desired SpecHash map shape.
- **Observation must tolerate disabled APIs** — use disabledAPI(err)
  in every new List (pkg/provision/gcp/gcp.go): API off ⇒ empty
  inventory, or destroy of unrelated apps breaks.
- Grants: derivation in pkg/provision/grants.go (targets ensured even
  when empty, so revocation converges); target-type URL routing in
  pkg/provision/gcp/grants.go urls(). grants.apply retries the
  fresh-member 400 window and only converges when the target truly
  reads back gone — do not weaken that.
- Preflight: pkg/provision/gcp/preflight.go Needs struct + api list;
  needsOf() in cmd/tako/deploy.go derives Needs from the graph. Every
  new API must appear there (one gap = one copy-pasteable fix line).
- Classify(err, project, verb, role) wraps every applier error.

## U1 — WithDomain + WithCDN (one slice: the load balancer IS both)

Goal: `tako.Site(..., tako.WithDomain("blog.example.com"))` serves
the app on that domain with managed TLS; adding `tako.WithCDN()`
turns on Cloud CDN on the same balancer. Blog
(examples/blog) is the demanding example.

Current state: both options exist in options.go and are REFUSED by
the analyzer via the siteNotYet map in pkg/analyzer/site.go with
tako/not-yet texts, golden-pinned in testdata/errors/notyet/ (and
referenced in docs/reference/site.md + api-review docs). Removing
them from siteNotYet flips them on — then:

1. **Analyzer:** record Domain (string, constant — reuse constString)
   and CDN (bool) on the site Resource (appgraph.go has Dir/Build/...
   siblings). New rules: WithCDN without WithDomain → positioned
   error (suggested text: "tako.WithCDN rides the domain's load
   balancer — declare tako.WithDomain first (tako/cdn-needs-domain)");
   invalid domain string (must be a bare FQDN, no scheme/slash/port).
   Update notyet fixture (repurpose like cronschedule was — the OLD
   not-yet texts become valid declarations, so the fixture needs new
   bad inputs), site option goldens, gallery/blog graph goldens.
2. **Provision kinds** (Compute Engine APIs, all global unless noted):
   - KindGlobalAddress (compute.googleapis.com/compute/v1/global/addresses)
   - KindManagedCert (compute .../sslCertificates, type MANAGED, the
     domain in managed.domains)
   - KindServerlessNEG (regional:
     .../regions/<region>/networkEndpointGroups, networkEndpointType
     SERVERLESS, cloudRun.service=<service>)
   - KindBackendService (global, protocol HTTPS/HTTP2 n/a for
     serverless — use protocol HTTPS, loadBalancingScheme
     EXTERNAL_MANAGED, backends=[{group: NEG}]; enableCDN=<WithCDN>,
     cdnPolicy cacheMode CACHE_ORIGIN_HEADERS or USE_ORIGIN_HEADERS —
     pick USE_ORIGIN_HEADERS so the runtime's existing immutable-asset
     headers drive caching; document the choice)
   - KindURLMap (defaultService=backendService)
   - KindTargetHTTPSProxy (urlMap + sslCertificates)
   - KindForwardingRule (global, 443, IPAddress=the address,
     loadBalancingScheme EXTERNAL_MANAGED)
   - Plus the HTTP→HTTPS redirect: a second url-map with
     defaultUrlRedirect(httpsRedirect:true) + targetHttpProxy + :80
     forwarding rule.
   Compute resources support labels on some kinds only — addresses
   yes, certs no: use the name-prefix marker pattern
   (`<app>-<env>-…`, e.g. cfg.Name(svc)+"-lb-…") and document it in
   each applier's type comment. All are long-running compute
   OPERATIONS: poll like waitOperation in run.go (compute has its own
   global/region operations endpoints — write a small waiter).
   Order: address/cert/NEG/backend/urlmap/proxy/rule BEFORE grants in
   the deploy chain is fine (nothing grants onto them), but they need
   the RUN SERVICE to exist for the NEG → apply after run services,
   before the summary. reconcileOrder: append after
   KindSchedulerJob; destroy reverses (forwarding rule first).
3. **Cloud Run ingress:** a domain-fronted service should ideally
   restrict ingress to the LB
   (metadata run.googleapis.com/ingress: internal-and-cloud-load-balancing)
   — propose as part of the slice but make it conditional: only when
   WithDomain is declared AND keep the run.app URL working is
   sacrificed; simpler v1: leave ingress all (both URLs work), note
   the hardening as discovered work. DECIDE in the DX transcript and
   record it.
4. **Auth:** the domain must join the authorized-domains set —
   precedent: EnsureAuthClient + the host registration in
   cmd/tako/deploy.go (grep authHosts). Also inject it so
   TAKO_AUTH_DOMAIN/session cookies work on the custom host (check
   pkg/runtime/session.go cookie domain assumptions).
5. **Preflight:** compute.googleapis.com when a domain is declared.
   Deployer needs roles/compute.loadBalancerAdmin (+
   compute.globalAddresses etc. — verify the minimal set live and
   record it in docs/tako-cloud-permissions.md).
6. **DNS is the manual step, preflight-as-UX:** after creating the
   address, the plan/summary prints EXACTLY:
   `create an A record: blog.example.com → <ip>` and narrates cert
   provisioning status (FAILED_NOT_VISIBLE etc. mapped to "waiting
   for DNS"; ACTIVE = done). Deploys are stateless/resumable — a
   re-run picks up where the cert is.
7. **Cost ⚠ at plan time:** `⚠ the load balancer for
   blog.example.com is standing infrastructure (~$18/mo + egress)
   whether traffic comes or not` — mirror warnFootprints.
8. **Docs:** reference/site.md's WithDomain/WithCDN sections flip
   from Not-yet to real; guide 06-blog gains the domain walk;
   cloud.md's manual-steps section gains DNS.
9. **Gate:** validate/domain.md. Agent-side: goldens, plan rendering
   with all new kinds, refusal texts, cost ⚠, destroy order.
   LIVE gate needs a real domain — **OPEN QUESTION for the user:
   which domain? (asked at proposal time, unanswered).** Script the
   walk parameterized on <your-domain>; the user runs the DNS step.
   Everything except cert-ACTIVE can be validated agent-side with a
   placeholder domain (cert will sit PROVISIONING — show it, destroy
   cleanly; document that a never-verified cert still deletes).

## U2 — Metrics export (Cloud Monitoring)

Goal: Counter.Add / Histogram.Observe reach Cloud Monitoring as
custom metrics; gallery's `uploads` counter and `thumb_ms` histogram
(examples/gallery/app.go, declared since F8) become visible in
Metrics Explorer.

Current state: the ONLY backends.Metrics implementation is
pkg/backends/local/metrics.go (slog lines + in-process totals); even
BootGCP wires it (pkg/runtime/boot.go:98 area). The metrics
CONTRACT is pkg/backends/ (grep `Metrics` interface).

1. **pkg/backends/gcp/metrics.go:** implement the interface with an
   in-process batcher: accumulate deltas/observations, flush every
   60s AND on Serve shutdown (the stdin-leash/SIGTERM path — grep
   runtime.Serve shutdown hooks) via monitoring v3
   projects.timeSeries.create:
   - counters → metric type `custom.googleapis.com/tako/<name>`,
     CUMULATIVE Int64 with a stable start time per process.
   - histograms → same prefix, GAUGE or CUMULATIVE DISTRIBUTION with
     explicit bucket bounds (pick exponential 2x from 1ms..~100s for
     duration-like metrics; document; keep bounds in one const).
   - resource: use the canonical `cloud_run_revision` monitored
     resource (labels: service_name/revision_name/location from env
     K_SERVICE/K_REVISION + region) — REQUIRED for sensible grouping.
   - rate limits: one create per flush with all series batched (max
     200 series/call); drop-and-log on 4xx, retry-once on 5xx. NEVER
     block a request path on a flush.
2. **Wire:** BootGCP switches hooks.Metrics to the new store (keep
   local for dev/test). TAKO_* env already carries project/service.
3. **Grants:** every service whose handlers reach a counter/histogram
   edge (edges exist? check pkg/analyzer — metrics are carriers; if
   no usage edges are derived for them, grant it to EVERY service the
   way secrets are package-wide, and note why) gets
   roles/monitoring.metricWriter on the project
   (pkg/provision/grants.go, project target precedent: datastore.user).
4. **Preflight:** monitoring.googleapis.com when any metric declared.
5. **Conformance:** the metrics contract chapter — local is
   log-based; gcp flush can be unit-tested against a fake HTTP server
   (the applier-style c.do is in backends/gcp — metrics live in
   pkg/backends/gcp (the BACKEND client, not provision/gcp) — reuse
   its client/auth (metadata token) like lease.go does.
6. **Docs:** reference/observability.md flips the "no export" Not-yet;
   states metric naming, the ~60s flush latency, and the Metrics
   Explorer path. Cost note: custom metrics bill per series/point at
   moderate volume — one line.
7. **Gate:** validate/metrics.md — deploy gallery, upload a photo,
   wait ≤2 flushes, `gcloud monitoring time-series list` (or the API)
   showing custom.googleapis.com/tako/uploads > 0 and thumb_ms
   distribution; destroy clean.

## U3 — dlq replay

Goal: `tako dlq <service> --replay <id>` and `--replay --all`:
re-deliver dead-lettered messages, then remove them from the DLQ —
a MOVE back into at-least-once delivery.

Current state: cmd/tako/dlq.go lists and `--ack`s (deletes);
pkg/provision/gcp/dlq.go pulls from the keep-subscriptions on the
dead topics (`<svc>-dead` topics, `<x>-dead-keep` pulls). Local dead
letters are JSON files under .tako/data/deadletter/... (shape in
pkg/backends/local/queue.go deadLetter writer; they record source,
consumer, payload, reason).

1. **Cloud path:** replay = read the DL message (it carries the
   ORIGINAL payload; attributes carry tako-queue/tako-topic — verify
   against pkg/backends/gcp/queue.go publish attrs + the dead-letter
   attributes in DeadLetter()), then re-publish to the ORIGINAL
   source topic (TopicName(env, queue)) with the original attributes,
   then ack the DL message. For topic-sourced deliveries the message
   must return only to the FAILED subscriber, not re-fan-out:
   inspect how push subscriptions bind (one sub per subscriber
   service) — republishing to the source topic re-fans-out to ALL.
   Correct approach: publish directly to the failed subscriber's
   PUSH endpoint? No — simplest correct: publish to the source topic
   is WRONG for topics; instead use Pub/Sub's
   subscriptions/<failed-sub>:  there is no per-sub injection API.
   DECISION for the implementer: for queue-sourced dead letters
   (consumer==""), republish to the queue topic (only one worker —
   safe). For topic-sourced (consumer=<name>), document that replay
   re-delivers via the subscriber's push endpoint DIRECTLY with an
   OIDC token minted as... the CLI runs as the OPERATOR (gcloud ADC),
   who may not satisfy run.invoker on the private service — they
   hold run.admin typically, so `gcloud auth print-identity-token`
   with the right audience works (precedent: the W9 operator probe in
   validate/shop.md). Implement queue replay first-class; topic
   replay via direct push POST with operator identity; say so in
   --help and docs.
   Ack AFTER successful re-delivery/re-publish (at-least-once again:
   a crash between = duplicate, which the contract already allows).
2. **Local path:** re-dispatch the JSON file's payload through the
   dispatcher (the dev process owns it — but the CLI is a SEPARATE
   process from `tako dev`... local dlq replay must go through the
   RUNNING dev server or operate on files only when dev is down.
   Simplest honest local story: `tako dlq --replay` requires the dev
   server down (bbolt lock is the guard — reuse its error) and
   re-enqueues by... there is no broker when dev is down. ALTERNATIVE
   (recommended): local replay MOVES the file to a
   deadletter/replay/ inbox that the dev server drains on boot and
   on watch — small dispatcher addition, keeps the CLI processless.
   Whichever is chosen, write it into the DX transcript FIRST.)
3. **Ladder:** tier 2 (recoverable): no typed confirmation; --replay
   and --ack are mutually exclusive (error if both). Help text +
   docs/cli-review-1.md table row + reference/cli.md + queue.md
   update ("Errors you'll meet" gains the mutual-exclusion text).
4. **Gate:** validate/dlq-replay.md — gallery's boom caption (the F4
   'boom' path — grep gallery for boom/notifyOwner failure injection)
   produces a dead letter live; --replay returns it; the worker fails
   it again (still boom) → back on the DLQ (prove the move
   semantics); then fix-and-replay (upload a good caption variant or
   use the empty-payload Permanent case for the ack path). Local
   equivalent scripted in the same file.

## U4 — Shared cache: plan + recipe (NO implementation)

Deliverable: docs/shared-cache-plan.md + a recipe section linked
from docs/reference/cache.md. NO new SDK surface, NO WithShared.

Contents required:
1. **The recommendation and why:** recipe-first. A Memorystore
   dependency (VPC connector + standing instance) contradicts
   tako's zero-infra defaults for a v0-scale app.
2. **The recipe (write it as runnable Go the user pastes):**
   cache-aside on an Entity — `type CacheRow struct {ID string
   entity:"id"; Val []byte; Expires time.Time}` + Get/fill/Put with
   lazy expiry (delete-on-read-expired), when it beats per-instance
   Cache (read-heavy, small values, N instances needing agreement)
   and when it does NOT (hot keys → Datastore contention; latency
   ~ms vs ns). Include the GetOrFill-style singleflight caveat
   (api-review-1's known double-fill) and how the lease
   (tako.Lock) can guard expensive fills.
3. **The future in-framework shape** (design only): `WithShared()`
   on Cache; what deploy would provision (Memorystore Redis basic
   tier, serverless VPC connector, per-service egress settings,
   secret-free auth via IAM); costs (~$35+/mo instance + connector);
   conformance implications (a real Redis in CI? testcontainers vs
   the local memory impl remaining the local backend); and the
   TRIGGER conditions to revisit ("an example needs cross-instance
   cache coherence that entity-cache latency can't meet").
4. File it under Discovered work in the task log as
   "shared cache: recipe shipped, WithShared designed, unscheduled".

## Loose ends you inherit (do not lose)

- U1 open question: the live-gate DOMAIN (ask the user before the
  U1 walk).
- The user executes `git push origin main` + `git tag v0.1.0` +
  push tag at their leisure — never do it for them.
- Still-parked (logged, unscheduled): multi-service dev routing,
  styled error pages, migrating tako rename, session cache,
  email account-creation UI, VPC, per-command --help.
- tako-test-02 has cloudscheduler + cloudtasks APIs enabled from
  Stage 6; compute/monitoring are NOT yet enabled (preflight will
  say so — that's the designed UX, let it).
