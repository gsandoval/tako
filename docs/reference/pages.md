# tako.Page

Server-rendered HTML: browser forms in, templates out, htmx as a
static asset — no client framework, no Node in production
([journal chapter](../guide/04-journal.md)).

## Declare

Exactly like `tako.API` / `tako.api` — a var/binding, a constant route, options
([examples/journal](../../examples/journal/app.go), [examples/cfire-journal](../../examples/cfire-journal/journal.cf)) — but the handler
returns `tako.HTML`:

**Go**
```go
var Home    = tako.Page("GET /", home)
var Read    = tako.Page("GET /p/{id}", read)
var Save    = tako.Page("POST /p", save, tako.Route{SignIn: true})
var Publish = tako.Page("POST /p/{id}/publish", publish, tako.Route{SignIn: true})
```

**cfire**
```cfire
Home    := tako.page("GET /", home)
Read    := tako.page("GET /p/{id}", read_post)
Save    := tako.page("POST /p", save_post)
Publish := tako.page("POST /p/{id}/publish", publish_post)
```

Page takes the same options as `tako.API` — `Route.SignIn` and
`Route.Private`. Service-wide settings (domain, scaling) live on
[`tako.Service`](service.md).

Pages render through a declared [`tako.Site`](site.md); its
`templates/` subtree backs `site.Render` and is never served raw.

## Use

Req binds exactly like API — `form:` tags cover browser form posts,
never JSON:

**Go**
```go
type SaveReq struct {
	Title string `form:"title"`
	Body  string `form:"body"`
}
```

**cfire**
```cfire
type SaveReq struct {
	title: str `form:"title" json:"title"`
	body:  str `form:"body" json:"body"`
}
```

A field that repeats binds as `[]string`, which is what a checkbox
group is: every box carries the same name and the handler receives all
of them. No JavaScript assembles the list, so bulk selection keeps
working with scripting off.

```go
type UnpublishReq struct {
	Selected []string `form:"selected"`
}
```

```html
<input type="checkbox" name="selected" value="{{ .ID }}">
```

`site.Render(ctx, "post.html", p)` fills the layout's `content`
block; `site.Fragment(ctx, "post-card.html", p)` renders the template
alone, for htmx partials. `tako.Redirect(target)` is
POST-redirect-GET. One handler serves both worlds with one visible
branch:

```go
if ctx.IsHTMX() {
	return site.Fragment(ctx, "post-card.html", p)
}
return tako.Redirect("/p/" + p.ID) // POST-redirect-GET (303)
```

### Redirect vs RedirectPermanent

`tako.Redirect` is 303 See Other — a statement about THIS response,
which is what a POST-redirect-GET is.

`tako.RedirectPermanent` is 301 Moved Permanently — a statement about
the URL. Use it when a resource moved, so crawlers update their index
and shared links survive:

```go
func read(ctx tako.Ctx, req ReadReq) (tako.HTML, error) {
	p, err := posts.Get(ctx, req.ID)
	if tako.IsNotFound(err) {
		if m, mErr := moved.Get(ctx, req.ID); mErr == nil {
			return tako.RedirectPermanent("/p/" + m.To)
		}
		return tako.HTML{}, tako.NotFound()
	}
	...
}
```

`SiteConfig.Redirects` covers the same ground for routes known at
analysis time — and only those. It is a map literal read by the
analyzer, so a slug your users rename next week cannot be in it. That is
the gap `RedirectPermanent` fills: the target comes out of the entity
store, per request.

**htmx is identical for both** — `HX-Redirect` and 200. Permanence is
something you say to crawlers and caches about a URL; a client-side
navigation is neither, and a 301 htmx cannot follow would be a
regression dressed as correctness.

CSRF on unsafe-method page routes is automatic: put `{{ csrf }}`
inside every form — it expands to a hidden `_csrf` input; htmx can
send the same token as an `X-CSRF-Token` header (`{{ csrfToken }}`).
Anonymous browsers get a double-submit cookie; signed-in sessions
carry their own token, so a stolen anonymous token cannot ride one.

`{{ markdown .Body }}` renders authored markdown with goldmark, then
sanitizes with bluemonday — an XSS payload vanishes, not escapes;
`site.Markdown(ctx, src)` is the same pipeline as a fragment.
`tako.SiteConfig{TrustedHTML: true}` on the Site is the explicit, graph-visible
opt-out for content that is never user-supplied.

Templates also receive two built-in identity helpers bound to the
current request: `{{ signedIn }}` (`bool`) and `{{ user }}`
(`tako.User`, so `{{ user.Email }}` or `{{ user.Name }}` works in
`layout.html` and any page or partial without threading the user
through every view struct).

## Local

`tako dev`, then the browser. `tako.Auth` gives the dev bar: sign in
as alice/bob, no GCP anywhere. Templates re-parse on reload; parse
errors surface at render time with the template named.

## Cloud

Pages ride the same Cloud Run app service as everything else in the
package — no extra infrastructure, scale-to-zero pricing. Documents
ship no-cache with security headers (CSP-Report-Only, nosniff);
fragments skip the document headers. Under htmx a redirect is 200 +
`HX-Redirect` (htmx cannot follow a 303 into a swap).

## Test it

User-style: a cookie-jarred client, the token scraped from the form
([examples/journal](../../examples/journal/pages_test.go)):

```go
var tokenRe = regexp.MustCompile(`name="_csrf" value="([0-9a-f]+)"`)

form := url.Values{"title": {"First post"}, "body": {"Hello, journal."}, "_csrf": {token}}
res, err := c.PostForm(srv.URL+"/p", form)
```

Assert the 303 and its `Location`; post with `HX-Request: true` and
`X-CSRF-Token` to pin the fragment path. `tako test` runs the suite.

## Errors you'll meet

A form posted without its token — or with a stale one — is a 403 with
exactly this body:

```
CSRF token missing or invalid — forms need {{ csrf }}
```

An anonymous request to a `Route.SignIn` page is a 401 `sign in to
continue`. `site.Render` against a layoutless site fails with:

```
site.Render needs templates/layout.html (the shell every page renders inside) — a site whose shell is generated (Hugo, etc.) renders fragments via site.Fragment instead
```

A typo'd template name:

```
template post.htm not found under web/templates/ (templates parse at startup; the layout is layout.html)
```

Handler errors with explicit statuses (`tako.NotFound()`,
`tako.Errorf(403, "only the author can publish")`) render as-is;
everything else is an opaque 500 with the request id, real error only
in your logs.

## Not yet

A **404** can wear a static file (`tako.SiteConfig{NotFound: "404.html"}`),
and **every error status** (401, 403, 404, 405, 500) can render through
a declared [`tako.StatusPage`](site.md#status-pages)
using your site's templates (`Status.Code`, `Status.Message`,
`Status.Policy`, `Status.RequestID`). What a `tako.Page` handler itself
returns is `tako.HTML` (always 200 OK) or an `error` (such as
`tako.NotFound()`, `tako.Deny(msg)`, or `tako.Errorf(403, ...)`), which
then renders through the package's `tako.StatusPage`.

Rendering is Go templates only: static generator output plus Go SSR is
the v0 model, no Node runtime services.

## Caching a page

Every rendered page answers `Cache-Control: no-cache`. That is the safe
default and a costly one: a read-heavy site revalidates every visitor's
request against the app and the entity store, which on Datastore is a
billed read per visitor per page.

`Route.MaxAge` says otherwise, per route:

```go
var Article = tako.Page("GET /{section}/{slug}", article, tako.Route{
	MaxAge: 5 * time.Minute,
})
```

That renders `Cache-Control: public, max-age=300`. Zero — every route
that does not ask — keeps `no-cache`, so nothing changes until you say
so. The value is a constant between one second and a year, because it
is baked into the header at build time rather than decided per request.

**It is refused on a `SignIn` route.** A shared cache holding an
authenticated page hands it to whoever asks next, and nothing about that
failure is visible from the app — the tests pass, the page renders, and
one day a reader sees someone else's page. So it fails analysis:

```
app.go:64:2: Dashboard sets MaxAge on a route that requires sign-in — a shared cache would serve one signed-in visitor's page to another; drop MaxAge, or drop SignIn (tako/cache-signin)
```

**A cacheable page carries no CSRF token.** A response a shared cache
stores is handed to the next visitor, so it must contain nothing that
belongs to one — and a CSRF token is exactly that. tako mints no cookie
on a page with `MaxAge`, which means `{{ csrf }}` renders empty there:
a cacheable page cannot carry a form. Same bargain as `cache-signin`,
same reason.

That is also what makes the header REACHABLE. Cloud Run's frontend
downgrades `public` to `private` whenever a response carries a
`Set-Cookie`, so before this a cacheable page was publicly cached on
every visit except the first — the one a shared cache exists for:

```
cache-control: private, max-age=300   ← first visit, Set-Cookie present
cache-control: public,  max-age=300   ← cookie already held
```

Static assets are already handled and need nothing: a fingerprinted file
(`app.3f2a1c.js`) gets `public, max-age=31536000, immutable`, everything
else under the site gets `no-cache` with a strong ETag.
