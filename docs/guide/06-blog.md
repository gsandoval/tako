# 6 · blog — a static generator with dynamic islands

[examples/blog](../../examples/blog/app.go) is the static-site
showcase: Hugo builds the site, tako ships it, and htmx islands call
back into tako fragments for the one dynamic thing a blog needs —
comments.

```go
var comments = tako.Entity[Comment]("comments")

var site = tako.Site("./site/public",
    tako.SiteConfig{Build: tako.Build{Command: "cd site && hugo --minify", Out: "./site/public"}})

var Comments   = tako.Page("GET /frag/comments", listComments)
var AddComment = tako.Page("POST /frag/comments", addComment)
```

## The division of labor

Hugo owns `./site` — content, layouts, styling, the whole shell.
`WithBuild` makes its build part of tako's: `tako dev` reruns Hugo on
every save (and doesn't watch the output dir, so no rebuild loops),
`tako deploy` runs it once and embeds `site/public` in the service
binary. There are no page routes for posts here — the analyzer's
graph shows two fragment routes and nothing else, because static
pages aren't handlers.

## The island

Each Hugo post template carries one dynamic slot:

```html
<section id="comments" hx-get="/frag/comments?post={{ .RelPermalink }}"
         hx-trigger="load" hx-swap="outerHTML"></section>
```

The fragment renders the comments *and* the form that grows them;
posting re-renders the island in place (htmx) or 303s back to the
static page (no JS). The comment's key is the post's `RelPermalink` —
the one stable identity a generator gives every page — and because
the handler queries `Where("Post", =).OrderBy("Created")`, the
composite index (post, created) is derived, planned, and destroyed
with everything else.

## The templates trick

Fragment templates live in `site/static/templates/` — Hugo copies
them into `public/templates/`, and `templates/` is the one subtree a
tako Site never serves raw. One build feeds both worlds; tests
register the same files straight from `site/static`, so `go test`
never needs Hugo installed.

A site without `templates/layout.html` is fine — the shell belongs
to Hugo. Only `site.Render` (full pages) needs a layout, and it says
so if called without one.

## Honesty at the edges

- Anonymous comments mean a mutating route without `WithAuth`; the
  deploy summary says exactly that, out loud, because it's true.
- The comment's post path round-trips into a redirect, so the
  handler accepts only site-local paths — an open redirect is not a
  feature. The 422 texts are part of the example.

Run it: `cd examples/blog && tako dev` (Hugo required:
`brew install hugo`).
