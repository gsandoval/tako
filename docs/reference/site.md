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

`SiteConfig{Build: tako.Build{Command: …, Out: …}}` runs a real generator and serves its
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

`SiteConfig.SPA` serves index.html for unknown paths (single-page apps own
their routing; without it, unknown paths 404). `SiteConfig.DevProxy`
forwards site requests to a generator's own dev server (Vite/Hugo
HMR) during `tako dev`. `SiteConfig.TrustedHTML` sets the markdown posture
([pages.md](pages.md)).

A custom domain is not a site setting — it belongs to the SERVICE that
serves the site, so it goes on [`tako.Service`](service.md) as
`ServiceConfig{Domain: …}`. A service with no site at all can have one too;
[domains.md](domains.md) is the whole story.

## Use

Serving comes with the headers nobody configures by hand correctly:
strong content ETags everywhere (304 on If-None-Match), immutable
caching for fingerprinted assets (`app.3f2a1c.js`), no-cache for
everything else (HTML above all), security headers on documents.
Pretty URLs work (`/about` → `about/index.html`); `SiteConfig.Redirects`
answers with a permanent 308. Declared API/Page routes win over the
site by mux precedence; the site takes the rest.

That precedence is worth one more sentence, because it is not about
which route you declared first — it is about which pattern is more
specific. `GET /{year}/{slug}` is more specific than the site's `/`
for **every** two-segment URL, so it takes `/assets/site.css` too, and
answers 404 for a stylesheet that is sitting right there. tako warns
when it sees this (`tako/site-shadow` in [errors.md](errors.md)),
naming the files, because nothing else about it looks like a failure:

```
app.go:73:2: ⚠ GET /{year}/{slug} shadows 3 files the site serves — …
```

The usual fix is to keep static files deeper than your wildcard routes
(`/assets/css/site.css` clears a two-segment route). Shadowing on
purpose is fine — a `/media/{key}` route serving its own files is not a
mistake — and the warning never fails a build.

### The 404 page

```go
tako.Site("./web", tako.SiteConfig{NotFound: "404.html"})
```

One field, both ways a request can miss: a mistyped URL that matches no
static file, and a handler returning `tako.NotFound()`. To a visitor
they are one experience, and only one of them could ever be answered by
something a handler returns — nothing runs on a static miss, so there is
nothing to return a status from. That is why this is site config rather
than a result type.

It resolves as a **template first** (`templates/404.html`, rendered
inside `layout.html` like any page), then as a **static file** in the
site directory — so a generator that already emits `404.html` just
works.

A missing or broken 404 page falls back to plain text. **A 404 never
becomes a 500**: the visitor already could not find what they wanted,
and an error page that errors turns "we don't have that" into "we are
broken".

htmx requests get the rendered document like everyone else — one
behaviour, no mode switch. A partial saying "not found" swapped into a
page is a worse answer than a page.

### Status pages

`NotFound` names a template. When the page needs **data** — a masthead
built from config, a nav, a count — declare a status page instead:

```go
var Status = tako.StatusPage(status)

func status(ctx tako.Ctx, s tako.Status) (tako.HTML, error) {
	return site.Render(ctx, "status.html", StatusView{
		Code: s.Code, Detail: s.Message, RequestID: s.RequestID,
		Total: postCount(ctx),
	})
}
```

It answers **every response tako produces itself**, not just the 404: a
mistyped URL, a handler's `tako.NotFound()`, an expired CSRF token, a
method the site does not serve, a handler that failed. One page, one
place, and a status tako learns to answer later needs no new field.

Before this, each of those was written separately and looked it — a 404
rendered with no data, a 403 as plain text, and security headers that
differed depending on whether the declared 404 resolved as a template
or as a static file. They are one writer now.

`tako.Status` carries what tako can honestly say:

| field | |
|---|---|
| `Code` | the status being answered |
| `Path` | the request path |
| `RequestID` | ties the page to the log line that explains it |
| `Message` | safe, already-public text; **empty for any 5xx** |
| `Policy` | the `tako.Policy` that refused this request; empty for every status tako answers on its own account |

There is deliberately no error field. A 5xx's detail is logged against
`RequestID` and never handed to the page, so leaking it is not
something the type lets you do.

`Policy` is what makes a 403 legible. Several things answer 403 on a
guarded route — your policy, a stale CSRF token, a handler's own
refusal — and a page that guesses from the code alone tells a reader
who is simply not a moderator that their form expired. journal branches
on it in one line; see [auth.md](auth.md).

The page runs with an ordinary `Ctx` — session, logger, queries — so a
status page can greet a signed-in visitor and read the store like any
other page.

**It can never make things worse.** If it panics, errors, or renders
nothing, tako falls back to the same plain text with the same status
code. That guarantee is what makes it safe to render the 500 through
it: a 500 does mean something in the process is already wrong, and the
recover is what pays for asking the app anyway.

One per package. `NotFound` and a status page together is fine — the
status page wins, and `NotFound` keeps serving the static-file case a
generator emits.

The `templates/` subtree is the one part never served raw: it backs
`site.Render` / `site.Fragment` ([pages.md](pages.md)).

It is a **tree**, and every template is named by its path relative to
`templates/`:

```
web/templates/
├── layout.html            → the shell
├── admin/nav.html         → site.Render(ctx, "admin/nav.html", v)
├── site/index.html        → site.Render(ctx, "site/index.html", v)
└── shared/card.html       → {{ template "shared/card.html" . }}
                             site.Fragment(ctx, "shared/card.html", v)
```

So `admin/nav.html` and `site/nav.html` are two templates, not one — an app
can keep its admin's markup and its public site's in separate directories, and
a site bundle can own the templates it renders with. A template at the top
level is named exactly as before, because its path IS its base name.

Only the top-level `layout.html` is the shell. One deeper in the tree is an
ordinary template with an ordinary name.

`layout.html` is OPTIONAL — a site whose shell comes from a static
generator (Hugo, etc.) renders only fragments, and blog's fragment
templates ride Hugo's `static/` dir into `public/templates/` so one
build feeds both worlds. Only `site.Render` needs the layout, and it
says so at call time.

## Replacing the templates while it runs

Templates parse once at boot, which is right for a site whose templates
ride the binary — and wrong for an app whose *editors* write them. Three
calls replace the set in a running service:

```go
// The candidate is a SET, not a file: templates reference each other, so
// one validated file can still produce a set that will not render.
set, err := site.PrepareTemplates(ctx, next)
if err != nil {
    var bad *tako.TemplateErrors
    if errors.As(err, &bad) {
        return site.Fragment(ctx, "admin/errors.html", bad.Failed)  // path → why
    }
    return tako.HTML{}, err
}

// Prove it RENDERS before anyone reads it — see below for why parsing is
// not enough. Every surface you would be sorry to break, and each one
// the way it is served: Render for pages, Fragment for fragments.
for _, page := range publicPages {
    if _, err := set.Render(ctx, page, sampleData(page)); err != nil {
        return site.Fragment(ctx, "admin/errors.html", map[string]string{page: err.Error()})
    }
}
for _, frag := range publicFragments {
    if _, err := set.Fragment(ctx, frag, sampleData(frag)); err != nil {
        return site.Fragment(ctx, "admin/errors.html", map[string]string{frag: err.Error()})
    }
}

return tako.HTML{}, site.UseTemplates(ctx, set)
```

**Why rendering is a separate step.** Go resolves
`{{ template "card.html" }}` at *execute* time, not parse time. A
candidate that lost a partial parses perfectly and fails on the first
page that uses it — so a gate on parsing alone would publish exactly the
break it exists to prevent. `TemplateSet.Render` renders against the
candidate without publishing it, and it is the same call `site.Render`
makes, so what you prove is what visitors get.

**Check a fragment with `Fragment`, not `Render`.** `Render` puts the
named template inside the layout, so handed a fragment — which defines
no `content` block — it renders an empty shell, executes nothing of the
fragment, and returns no error. A surface checked the wrong way is a
surface not checked at all.

`PrepareTemplates` refuses a candidate with any parse error and names
**every** template that failed, so an editor hears about all their
mistakes on one save. Nothing is published unless you publish it, and a
render already under way finishes against the set it started with.

**The swap is per instance, and this is the part to design around.** It
replaces the set in the process that served the request. A service
running more than one instance leaves the others serving what they
booted with until they restart, and a restart brings back the templates
the binary shipped with.

Converging is the app's job, and tako has no startup callback to hang it
on: `PrepareTemplates` needs a `Ctx` and a wired site, both of which
arrive with the first request. So the shape that works is **lazy** — the
first render in a process notices its stored generation is newer than
the one it is serving and publishes it, once, before answering:

```go
func currentTemplates(ctx tako.Ctx) error {
    want, err := settings.Get(ctx, "templates")   // the app's own record
    if err != nil || want.Generation == loaded.Load() {
        return err
    }
    set, err := site.PrepareTemplates(ctx, storedFS(ctx, want.Generation))
    ...
    loaded.Store(want.Generation)
    return site.UseTemplates(ctx, set)
}
```

Tako has no "tell every instance" mechanism either; if an editor must
never see their change flicker between instances, run that service at
one instance.

`PrepareTemplates` and `UseTemplates` are also not atomic as a pair: two
editors saving at once can have the second publish and the first then
publish a set prepared before it, silently reverting them. Serialize
saving in your app — a [lock](lock.md) around the read-modify-publish is
enough.

Tako never stores a replaced set. The app owns its storage, decides what
a draft is, and decides when to reload — there is no hook that would
decide for it.

## Local

`tako dev` serves the directory from disk. `SiteConfig.Build` commands
re-run on change, and the output dir is not watched, so no rebuild
loops. `SiteConfig.DevProxy` applies only under the local backend — the
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
HTML, `public, max-age=31536000, immutable` on fingerprinted assets,
and `X-Content-Type-Options: nosniff` on **every** file — a stylesheet
and a script are as worth pinning to their declared type as a
document. CSP and `Referrer-Policy` stay on HTML only: they describe a
document's privileges, and a stylesheet has none.

## Errors you'll meet

At analysis time:

```
tako.Site directory ./web does not exist (add it, or set SiteConfig.Build if a generator creates it) (tako/site-dir)
tako.SiteConfig.Redirects must be a map literal with constant keys and values — redirects are read at analysis time, not run time (tako/config-literal)
a package serves one site — onesite.site is already declared; remove onesite.admin or merge the two (tako/one-site)
```

`ServiceConfig.Domain`'s own refusals — bad shape, two domains on one service, a
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

There is no CDN option. It was removed in U1a once the custom-domain
research settled on Cloud Run domain mappings rather than a load
balancer: with no balancer there is nothing to switch on, and an option
that cannot be implemented is worse than one that does not exist. If
you need edge caching, `docs/domain-options.md` compares the two paths
tako has designed but not shipped (a global external Application Load
Balancer, and Firebase Hosting) and names the conditions that would
trigger either.
