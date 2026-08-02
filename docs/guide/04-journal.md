# 4 · journal — a website, no SPA required

[examples/journal](../../examples/journal/app.go) is the
server-rendered showcase: a multi-user blog in ~120 lines, with
interactivity from htmx as a static asset — no client framework, no
Node in production.

```go
var posts = tako.Entity[Post]("posts")
var site  = tako.Site("./web", tako.SiteConfig{Redirects: map[string]string{"/about-us": "/about"}})
var auth  = tako.Auth(tako.AuthConfig{Google: true})

var Home    = tako.Page("GET /", home)
var Read    = tako.Page("GET /p/{id}", read)
var Save    = tako.Page("POST /p", save, tako.Route{SignIn: true})
var Publish = tako.Page("POST /p/{id}/publish", publish, tako.Route{SignIn: true})
```

## Sites and pages

`tako.Site` serves `./web` embedded in the binary — fingerprinted
assets immutable, HTML no-cache, ETags, security headers. Its
`templates/` back `site.Render(ctx, "post.html", p)` and are never
served raw. `WithBuild` runs any static generator (Hugo, esbuild…) on
the deploying machine; `SPA()` and `WithDevProxy` cover the
JS-framework shapes.

Pages bind browser FORMS (`form:` tags), never JSON; CSRF on unsafe
page routes is automatic (`{{ csrf }}` in forms, a meta tag for
fetch). `tako.Redirect("/p/"+id)` is POST-redirect-GET.

## One handler, both worlds

```go
if ctx.IsHTMX() {
    return site.Fragment(ctx, "post-card.html", p) // swap into the list
}
return tako.Redirect("/p/" + p.ID)                 // no-JS fallback: 303
```

`Fragment` renders without the layout; `Redirect` automatically emits
`HX-Redirect` under htmx. Progressive enhancement is one visible
branch.

## Markdown, sanitized by default

`{{ markdown .Body }}` renders authored content with goldmark, then
STRIPS scripts and handlers (bluemonday) — an XSS payload vanishes,
not escapes. Trusting raw HTML is an explicit, graph-visible opt-out
(`tako.SiteConfig{TrustedHTML: true}`), never a default.

## Auth and sessions

`tako.Auth(tako.AuthConfig{Google: true})` is one declaration: `/api/_session*`
routes mount, a framework sign-in page appears, `WithAuth` gates
routes with a 401, `ctx.User()` is guaranteed past it, and ownership
stays YOUR rule (`403 only the author can publish`). Sessions are
**revocable records in the entity store** — delete one in the console
and that browser is signed out on its next request. Locally the dev
bar signs in test users; in the cloud it's real Identity Platform
(the one manual step: [the Google provider toggle](cloud.md)).

## Run it

```
cd examples/journal
PORT=8090 tako dev     # open localhost:8090, sign in via the dev bar
tako test              # 401s/403s, CSRF, fragments, sanitization — as user-style tests
```
