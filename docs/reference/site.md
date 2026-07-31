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
tako.WithTTL is not a tako.Site option (tako.Site accepts SPA, WithBuild, WithDevProxy, WithRedirect, WithTrustedHTML) (tako/invalid-option)
a package serves one site — onesite.site is already declared; remove onesite.admin or merge the two (tako/one-site)
```

At render time, `site.Render` on a fragments-only site:

```
site.Render needs templates/layout.html (the shell every page renders inside) — a site whose shell is generated (Hugo, etc.) renders fragments via site.Fragment instead
```

## Not yet

Two options exist to be honest about the edge, and fail analysis
until their slices land:

```
tako.WithDomain is not available yet — custom domains (mapping + TLS + HSTS) are their own slice (tako/not-yet)
tako.WithCDN is not available yet — bucket + Cloud CDN serving is its own when-scale-justifies slice (tako/not-yet)
```

v0 embeds every site in the app service's binary; `WithCDN` is the
planned upgrade when scale justifies billable serving infrastructure.
