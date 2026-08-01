# Custom domains: what tako uses, and what it deliberately does not

`tako.WithDomain("blog.example.com")` is a declaration of INTENT. How
tako satisfies it — a Cloud Run domain mapping, a Firebase Hosting site,
a load balancer — is tako's business, not your code's. This page records
which mechanism ships today, what the alternatives cost, and the
conditions that would make tako switch.

That indirection is the point: **swapping the mechanism is a tako change,
never a change to your app.** It is why choosing the cheapest option now
is not a trap.

## First, a thing about tako that narrows the question

A `tako.Site` is embedded in the app service's binary. One service, one
URL — a site's static assets and its dynamic handlers always answer on
the same origin (`examples/blog` is one package: Hugo-built static plus
`Page` handlers at `/frag/comments`). So there is no separate static
origin to route to, and every option below fronts exactly one thing: a
Cloud Run service.

## The four options, priced

Measured 2026-07-31. Verify before quoting — cloud pricing moves.

| | Standing cost | Per GB | Edge CDN | Status |
|---|---|---|---|---|
| **Cloud Run domain mapping** ← *shipped* | **$0** | Cloud Run egress ~$0.085–0.12 | no | preview |
| Firebase Hosting rewrite | $0 | 360 MB/day free, then **$0.15** | yes, always on | GA |
| Global external Application Load Balancer | **$18.25/mo** | $0.008/GiB processing + egress; CDN $0.08/GiB + $0.0075/10k lookups | opt-in switch | GA |
| Cloudflare (or any external CDN) | $0 | free tier | yes | outside GCP |

Three facts that decided it:

1. **The load balancer's $18.25/mo is a floor, not an average.** GCP
   charges a flat $0.025/hour covering the FIRST FIVE forwarding rules,
   so the HTTP→HTTPS redirect rule rides free and the price is identical
   at zero traffic. Standing infrastructure for a v0 app contradicts the
   same doctrine that rejects Terraform, emulators, and Memorystore.
2. **Firebase Hosting costs more, not less.** It bills transfer at
   $0.15/GB whether a response was an edge cache HIT or a Cloud Run
   passthrough, against Cloud Run's own ~$0.085–0.12/GB. Its free tier
   (360 MB/day) is smaller than it looks. Hosting buys latency and fewer
   Cloud Run instance-seconds — not a smaller bill.
3. **No option can lock Cloud Run's front door.** Serverless NEG backends
   AND Firebase Hosting rewrites both require the service to allow
   unauthenticated invocations. Restricting ingress to the load balancer
   is not available in any of them, so it was never a reason to pick one.

Cloudflare is a fine answer for a user who already runs it, but tako's
reconciler is GCP-only and label-driven; a second provider's credentials
and a second state model are not something the framework will own. Point
it at the `run.app` URL yourself if you want it.

## What the shipped option costs you

Google ships domain mappings as **preview** and states they are *"not
production-ready and not supported at General Availability"* (latency),
so `tako deploy` prints that at plan time — the honest ⚠ here is about
MATURITY, not money, because there is no standing charge to warn about.

Also true, and enforced:

- **~10 regions only** — `asia-east1`, `asia-northeast1`,
  `asia-southeast1`, `europe-north1`, `europe-west1`, `europe-west4`,
  `us-central1`, `us-east1`, `us-east4`, `us-west1`. Preflight refuses
  anything else and lists these.
- **No edge caching at all.** Your Cloud Run service serves every byte.
  The runtime's caching headers still work; they just work at the
  browser, not at an edge.
- Root path only; no wildcard certificates; 64-character limit; TLS 1.0
  and 1.1 cannot be disabled.

## Designed, not shipped: the load balancer

Reach for this when an example needs something only it has:

- **Cloud Armor** (WAF, rate limiting, geo rules) or **IAP**.
- **One domain across several services** — `/api/*` to one, `/*` to
  another, via url-map path routing.
- A **static anycast IP** you can hand to a third party.
- **Wildcard certificates**, or bringing your own certificate.

What it would provision: a global address, a managed certificate, one
serverless NEG per backing service, a backend service, a url-map, an
HTTPS target proxy, a :443 forwarding rule, plus a second url-map,
target proxy and :80 rule for the HTTP→HTTPS redirect. All are
long-running Compute operations, so the appliers need a compute
operations waiter (`run.go`'s `waitOperation`, against compute's own
global/region endpoints). Certificates carry no labels, so the managed
marker would be a name prefix rather than the real labels domain
mappings enjoy.

**Trigger to revisit:** an example demands a WAF, IAP, or one domain
fanning across several services.

## Designed, not shipped: Firebase Hosting

Reach for this if domain mappings are retired, or an example needs edge
caching that Cloud Run's own headers cannot deliver.

It works fine on a project tako deployed itself — *"a Firebase project IS
a Google Cloud project that has additional Firebase-specific
configurations and services enabled"*, and Firebase can be added to an
existing project (it stamps a `firebase:enabled` label and shares IAM and
billing). The rewrite names a Cloud Run service by `serviceId` and
`region`; the service needs only to allow unauthenticated invocations and
live in a supported region.

What it would provision: `projects.addFirebase` once, then per domain a
Hosting site, a version carrying `rewrites: [{glob: "**", run: {...}}]`,
a finalize, a release, and a custom-domain registration.

The frictions to design around: **site IDs are globally unique across all
of Firebase** and capped at 30 characters (a bucket-name-class collision
problem — `BucketName` is the precedent for folding the project in); the
`customDomains` API is **v1beta1**; Hosting is governed by **Firebase
Terms of Service rather than the Google Cloud ToS**; and DNS is heavier
for the user (a TXT verification record plus A records).

**Trigger to revisit:** Cloud Run domain mappings are deprecated or
retired, or an example needs edge caching.

## Sources

- [Cloud Run: mapping custom domains](https://docs.cloud.google.com/run/docs/mapping-custom-domains)
- [Cloud Run: DomainMapping REST resource](https://docs.cloud.google.com/run/docs/reference/rest/v1/namespaces.domainmappings)
- [Cloud Load Balancing pricing](https://cloud.google.com/load-balancing/pricing)
- [Firebase pricing](https://firebase.google.com/pricing) · [Hosting → Cloud Run](https://firebase.google.com/docs/hosting/cloud-run) · [Firebase and GCP projects](https://firebase.google.com/docs/projects/learn-more)
