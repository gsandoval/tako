# tako.Site

The app's web-serving story rooted at one directory: static assets,
templates, frontend builds. One site per package. The
[journal](../guide/04-journal.md) and [blog](../guide/06-blog.md)
chapters teach the two shapes.

## Declare

```go
// site serves ./web embedded in the service binary; its templates/
// subtree backs site.Render and is never served statically.
var site = tako.Site("./web", tako.SiteConfig{Redirects: map[string]string{"/about-us": "/about"}})
```

`WithBuild(command, outDir)` runs a real generator and serves its
output — Hugo in [examples/blog](../../examples/blog/app.go), esbuild
in [examples/shop](../../examples/shop/app.go):

```go
var site = tako.Site("./site/public",
	tako.SiteConfig{Build: tako.Build{Command: "cd site && hugo --minify", Out: "./site/public"}})

var site = tako.Site("./web/dist",
	tako.SiteConfig{Build: tako.Build{
		Command: "cd web && npx --yes esbuild src/app.ts --bundle --format=esm --outfile=dist/app.js && cp index.html dist/",
		Out:     "./web/dist",
	}})
```

`SPA()` serves index.html for unknown paths (single-page apps own
their routing; without it, unknown paths 404). `WithDevProxy(url)`
forwards site requests to a generator's own dev server (Vite/Hugo
HMR) during `tako dev`. `WithTrustedHTML()` sets the markdown posture
([pages.md](pages.md)).

A custom domain is not a site setting — it belongs to the SERVICE that
serves the site, so it goes on [`tako.Service`](service.md) via
`WithDomain`. A service with no site at all can have one too;
[domains.md](domains.md) is the whole story.

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

`tako.Service(tako.ServiceConfig{Domain: "blog.example.com"})` puts the service on a custom domain
with a Google-managed certificate, via a Cloud Run domain mapping — no
standing infrastructure charge, but two manual steps that are yours
(proving ownership, publishing DNS). [domains.md](domains.md) has the
transcripts, the refusals, and the limits; [domain-options.md](../domain-options.md)
prices what tako deliberately did not build.

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
tako.Site directory ./web does not exist (add it, or set SiteConfig.Build if a generator creates it) (tako/site-dir)
tako.SiteConfig.Redirects must be a map literal with constant keys and values — redirects are read at analysis time, not run time (tako/config-literal)
a package serves one site — onesite.site is already declared; remove onesite.admin or merge the two (tako/one-site)
```

`WithDomain`'s own refusals — bad shape, two domains on one service, a
domain in front of a package nothing public reaches, and the two deploy
gates — live in [domains.md](domains.md).

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
