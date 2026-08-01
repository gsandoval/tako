# tako.WithDomain

A custom domain with a Google-managed certificate, at no standing
infrastructure cost. It is the one option that belongs to no single
constructor: it addresses a **service**, and `Site`, `API`, and `Page`
can all declare it.

## Declare

`WithDomain(fqdn)` goes on [`tako.Service`](service.md), the package's
app-service declaration. Both of these are complete:

```go
// A site's service.
var (
	web  = tako.Service(tako.WithDomain("blog.example.com"))
	site = tako.Site("./site/public",
		tako.WithBuild("cd site && hugo --minify", "./site/public"))
)

// An API-only service — no tako.Site anywhere in the package.
var (
	api     = tako.Service(tako.WithDomain("api.example.com"))
	Create  = tako.API("POST /api/links", create)
	Resolve = tako.API("GET /api/links/{slug}", resolve)
	List    = tako.API("GET /api/links", list)
)
```

**One declaration addresses every route in the service.** No endpoint
carries the claim, because none of them is a more honest place to put it
than any other — that is exactly why `tako.Service` exists.

Declaring it also NAMES the service `<package>-<var>`: `api` in package
`shortlink` gives `shortlink-api`. See [service.md](service.md).

## Which service, exactly

This is the part worth being explicit about, because tako never asks you
to group endpoints into a service — it derives the grouping:

- **Package = service.** Every `API`, `Page`, and `Cron` handler in a
  package deploys together, as that package's app service.
- **Each `Worker` and `Subscribe` handler is carved into its own
  service**, so a slow consumer scales without dragging your API.
- A `Site` is embedded in its package's app service — one origin for
  static assets and dynamic routes both.

So `tako.Service` describes the package's APP service — the one running
its API, Page and Cron handlers — and never the satellites. `tako gen
graph` shows you the answer before you deploy:

```
shortlink: 4 services
  service shortlink-api (package shortlink, profile default, domain api.ker.gs)
    POST /api/links → Create
    GET /api/links/{slug} → Resolve
    GET /api/links → List
  service shortlink-countclick (package shortlink, profile default)
    worker CountClick ← queue clicks
  …
```

One package, four services, and the domain sits on exactly one of them.
The workers get no domain and could not take one: they receive work over
authenticated push, never public traffic.

**One service, one domain**, and one `tako.Service` per package
(`tako/one-service`), so two names in one service is not a state you can
reach.

Envs do not prefix the domain the way they prefix every other resource
name (`u1-shortlink`, `u1-clicks`). A domain is globally unique by
nature, so two environments declaring the same one is a contradiction
only you can resolve; the second deploy fails with the 409 below rather
than tako inventing `u1-api.example.com`.

## Cloud

`tako deploy` creates a Cloud Run domain mapping — no load balancer, no
bucket, **no standing charge**. The plan prints a ⚠ about MATURITY
rather than money, because Google ships domain mappings as preview and
does not recommend them for production. See
[domain-options.md](../domain-options.md) for what tako deliberately did
not build, what those cost, and what would make tako switch. Switching
would be a tako change, never a change to your code.

Two steps are yours, and deploy names both:

1. **Prove you own the domain**, once per project. Preflight refuses
   until then: `gcloud domains verify example.com` (it opens Search
   Console). Verifying a parent covers its subdomains.
2. **Publish the DNS records** deploy prints:

```
  domain  api.ker.gs               awaiting DNS
    create these records in the DNS zone for ker.gs:
      api                          CNAME  ghs.googlehosted.com.
    then re-run tako deploy — the certificate provisions once the
    records resolve (usually ~15 minutes, up to 24 hours).
```

Names come back RELATIVE to the zone, which is what a registrar's host
field wants; an apex domain shows `@` with A/AAAA records. Deploys are
stateless, so re-running just reports where the certificate got to —
including when nothing else changed:

```
  domain  api.ker.gs               certificate ACTIVE → https://api.ker.gs
```

A mapping **adds** an address; it does not close one. The `run.app` URL
keeps serving, and the security summary says so:

```
  public ingress on api.ker.gs AND its run.app URL (a domain mapping adds an address, it does not close one)
```

Re-pointing a domain at a different service is a REPLACE, not an update:
the mapped route is immutable, so deploy prompts before deleting and
recreating, and says the domain goes dark until the new certificate
issues.

`tako destroy` removes the mapping first, before the service it points
at. It never touches your DNS records or your domain verification —
those live at your registrar and in your Google account.

## Local

Nothing. `tako dev` serves on `localhost`; a domain is a cloud address
and the local backend has no equivalent to fake. The declaration is
inert locally, which is why it is safe to leave in your code.

## Test it

The declaration is analyzer-level, so assert it on the graph rather than
over HTTP:

```go
graph, _ := analyzer.Analyze(analyzer.Config{Dir: "."})
// graph.Services[i].Domain == "api.example.com"
```

`tako gen graph` is the same answer, human-readable.

## Errors you'll meet

At analysis time:

```
tako.WithDomain needs a bare domain name — no scheme, port, path, or trailing dot; got "https://blog.example.com/" (tako/domain)
tako.WithDomain needs a fully-qualified domain — "blog" has no dot (tako/domain)
tako.WithDomain does not support wildcards — got "*.example.com"; declare each subdomain on its own service (tako/domain)
this package declares the domain "blog.example.com", but nothing here serves public traffic — queue workers, topic subscribers, and jobs are reached over authenticated push or not at all, so the name would answer nothing; declare an API, Page or Site route here, or move the domain to the package that has one (tako/domain-unreachable)
tako.WithDomain describes the whole service, not one route — declare it on tako.Service instead: var svc = tako.Service(tako.WithDomain(…)) (tako/service-option)
a package declares one service — oneservice.api is already declared; remove oneservice.admin or merge the two (tako/one-service)
```

At deploy time, before anything is built:

```
Cloud Run domain mappings are not available in europe-west2 — deploy to one of asia-east1, asia-northeast1, asia-southeast1, europe-north1, europe-west1, europe-west4, us-central1, us-east1, us-east4, us-west1 (tako deploy --region <region>), or drop tako.WithDomain and use the run.app URL
blog.example.com is not verified for tako-test-02 — prove you own it once: gcloud domains verify example.com (this opens Search Console), then re-run tako deploy
```

And if the name is already spoken for:

```
blog.example.com is already mapped in tako-test-02 — a domain answers for one service, and tako will not take it from whatever holds it; remove that mapping (gcloud beta run domain-mappings delete --domain blog.example.com --region us-central1) or choose another name
```

## Not yet

**No edge caching.** Your Cloud Run service serves every byte; the
runtime's caching headers work at the browser, not at an edge. There is
no `WithCDN` — see [domain-options.md](../domain-options.md).

**About ten regions**: `asia-east1`, `asia-northeast1`,
`asia-southeast1`, `europe-north1`, `europe-west1`, `europe-west4`,
`us-central1`, `us-east1`, `us-east4`, `us-west1`.

**No wildcards**, no bring-your-own certificate, root path only (you
cannot map `example.com/blog` to one service and `example.com/shop` to
another — that needs the load balancer), 64-character limit, and TLS 1.0
and 1.1 cannot be disabled.
