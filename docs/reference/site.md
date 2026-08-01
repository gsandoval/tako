# tako.Site

The app's web-serving story rooted at one directory: static assets,
templates, frontend builds. One site per package. The
[journal](../guide/04-journal.md) and [blog](../guide/06-blog.md)
chapters teach the two shapes.

## Declare

```go
// site serves ./web embedded in the service binary; its templates/
// subtree backs site.Render and is never served statically.
var site = tako.Site("./web", tako.WithRedirect("/about-us", "/about"))
```

`WithBuild(command, outDir)` runs a real generator and serves its
output — Hugo in [examples/blog](../../examples/blog/app.go), esbuild
in [examples/shop](../../examples/shop/app.go):

```go
var site = tako.Site("./site/public",
	tako.WithBuild("cd site && hugo --minify", "./site/public"))

var site = tako.Site("./web/dist",
	tako.WithBuild("cd web && npx --yes esbuild src/app.ts --bundle --format=esm --outfile=dist/app.js && cp index.html dist/", "./web/dist"))
```

`SPA()` serves index.html for unknown paths (single-page apps own
their routing; without it, unknown paths 404). `WithDevProxy(url)`
forwards site requests to a generator's own dev server (Vite/Hugo
HMR) during `tako dev`. `WithTrustedHTML()` sets the markdown posture
([pages.md](pages.md)).

`WithDomain(fqdn)` is a SERVICE-level option, not a site-level one:
declare it on the site, or on any one of the service's `API` or `Page`
handlers, and the whole service answers there. One service, one domain —
which is why a service with no site at all can still have one:

```go
// a site's service
var site = tako.Site("./site/public", tako.WithDomain("blog.example.com"))

// an API-only service — no site anywhere in the package
var Create = tako.API("POST /links", create, tako.WithDomain("api.example.com"))
```

See [Cloud](#cloud) for what it provisions and the two manual steps.

## Use

Serving comes with the headers nobody configures by hand correctly:
strong content ETags everywhere (304 on If-None-Match), immutable
caching for fingerprinted assets (`app.3f2a1c.js`), no-cache for
everything else (HTML above all), security headers on documents.
Pretty URLs work (`/about` → `about/index.html`); `WithRedirect`
answers with a permanent 308. Declared API/Page routes win over the
site by mux precedence; the site takes the rest.

The `templates/` subtree is the one part never served raw: it backs
`site.Render` / `site.Fragment` ([pages.md](pages.md)).
`layout.html` is OPTIONAL — a site whose shell comes from a static
generator (Hugo, etc.) renders only fragments, and blog's fragment
templates ride Hugo's `static/` dir into `public/templates/` so one
build feeds both worlds. Only `site.Render` needs the layout, and it
says so at call time.

## Local

`tako dev` serves the directory from disk. `WithBuild` commands
re-run on change, and the output dir is not watched, so no rebuild
loops. `WithDevProxy` applies only under the local backend — the
deployed site never proxies. Blog needs Hugo on your machine
(`brew install hugo`); shop needs Node — production needs neither.

## Cloud

`tako deploy` runs the build once on the deploying machine and embeds
`outDir` in the service binary — no bucket, no load balancer, no CDN
bill; the site scales to zero with its service. Node and Hugo never
reach production.

`WithDomain("blog.example.com")` puts the service on a custom domain
with a Google-managed certificate, via a Cloud Run domain mapping — no
standing infrastructure charge. Two manual steps are yours, and
`tako deploy` narrates both:

1. **Prove you own the domain**, once per project — preflight refuses
   until you do, with the command: `gcloud domains verify example.com`
   (it opens Search Console).
2. **Create the DNS records** the deploy prints. Cloud Run returns them
   relative to your zone, which is what a registrar's host field wants:

```
  domain  blog.ker.gs              awaiting DNS
    create these records in the DNS zone for ker.gs:
      blog                         CNAME  ghs.googlehosted.com.
    then re-run tako deploy — the certificate provisions once the
    records resolve (usually ~15 minutes, up to 24 hours).
```

Deploys are stateless and resumable, so re-running picks up wherever the
certificate got to — including when nothing else changed:

```
  domain  blog.ker.gs              certificate ACTIVE → https://blog.ker.gs
```

A mapping ADDS an address; it does not close one. The `run.app` URL
keeps serving, and the security summary says so. Domain mappings are
available in about ten regions and carry no edge caching — the trade,
the prices of the alternatives, and the conditions that would make tako
switch mechanisms are all in
[domain-options.md](../domain-options.md).

## Test it

Run the same `SiteHandler` the generated main mounts —
`os.DirFS` here, `go:embed` in the binary
([examples/journal](../../examples/journal/site_test.go)):

```go
func siteHandler() *httptest.Server {
	return httptest.NewServer(runtime.SiteHandler(os.DirFS("web"), runtime.SiteOptions{
		Redirects: map[string]string{"/about-us": "/about"},
	}))
}
```

Assert the header contract: `Cache-Control: no-cache` plus ETag on
HTML, `public, max-age=31536000, immutable` on fingerprinted assets.

## Errors you'll meet

At analysis time:

```
tako.Site directory ./web does not exist (add it, or use WithBuild if a generator creates it) (tako/site-dir)
tako.WithTTL is not a tako.Site option (tako.Site accepts SPA, WithBuild, WithDevProxy, WithRedirect, WithTrustedHTML, WithDomain) (tako/invalid-option)
a package serves one site — onesite.site is already declared; remove onesite.admin or merge the two (tako/one-site)
tako.WithDomain needs a bare domain name — no scheme, port, path, or trailing dot; got "https://blog.example.com/" (tako/domain)
tako.WithDomain needs a fully-qualified domain — "blog" has no dot (tako/domain)
service blog cannot answer on both "www.example.com" (a handler) and "blog.example.com" (the site) — one service, one domain; split the packages or align the domains (tako/domain-conflict)
the site declares the domain "blog.example.com", but no service in this package serves public traffic — queue workers, topic subscribers, and jobs are reached over authenticated push or not at all, so the name would answer nothing; declare an API or Page route here, or move the domain to the package that has one (tako/domain-unreachable)
```

At deploy time, before anything is built:

```
Cloud Run domain mappings are not available in europe-west2 — deploy to one of asia-east1, asia-northeast1, asia-southeast1, europe-north1, europe-west1, europe-west4, us-central1, us-east1, us-east4, us-west1 (tako deploy --region <region>), or drop tako.WithDomain and use the run.app URL
blog.example.com is not verified for tako-test-02 — prove you own it once: gcloud domains verify example.com (this opens Search Console), then re-run tako deploy
```

At render time, `site.Render` on a fragments-only site:

```
site.Render needs templates/layout.html (the shell every page renders inside) — a site whose shell is generated (Hugo, etc.) renders fragments via site.Fragment instead
```

## Not yet

Every site is embedded in the app service's binary — one service, one
URL — so a site's static assets and its dynamic handlers always answer
on the same origin.

There is no `WithCDN`. It was removed in U1a once the custom-domain
research settled on Cloud Run domain mappings rather than a load
balancer: with no balancer there is nothing to switch on, and an option
that cannot be implemented is worse than one that does not exist. If
you need edge caching, `docs/domain-options.md` compares the two paths
tako has designed but not shipped (a global external Application Load
Balancer, and Firebase Hosting) and names the conditions that would
trigger either.
