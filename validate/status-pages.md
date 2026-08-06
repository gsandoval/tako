# validate: status pages (FL11)

The claim: every response tako produces **for itself** — a mistyped
URL, a handler's `tako.NotFound()`, an expired CSRF token, a sign-in
requirement, a method the site does not serve, a handler that failed —
renders through one page the app declares, with one set of headers, and
can never make things worse than the plain text it replaced.

The failure this exists to prevent is not one bug. It is that each of
those was written separately and looked it: a 404 rendered with nil
data, a 403 as plain text, and security headers that differed by
whether a declared 404 resolved as a template or a static file. flauta
filed two of them as separate gaps, days apart.

## 1. The 404 wears the app, with data

```
cd examples/journal
go run ../../cmd/tako dev
curl -s localhost:8080/no-such-page
```

Expect the masthead, the nav, AND a live count — the count is the part
that could not work before, because the 404 rendered with `nil`:

```
<title>Not found · journal</title>
<header><h1><a href="/">journal</a></h1>
  <nav><a href="/write">write</a> · <a href="/about">about</a> ·
<h2>Not found</h2>
<p><a href="/">Back to the journal</a> — 3 published posts.</p>
```

Then `curl -s localhost:8080/p/does-not-exist` — a handler's
`tako.NotFound()`, which is the other 404 path, and must produce the
same page.

## 2. Every other status too

```
curl -s -i -X POST localhost:8080/about   # 405
curl -s -i -X POST localhost:8080/p -d "title=x"   # 401, SignIn route
```

Both must answer `text/html` with the app's chrome, not plain text.
Check the headers are identical to the 404's — that is the cleanup.

## 3. It can never make things worse

```
go test ./pkg/runtime/ -run TestABrokenStatusPageFallsBackToPlainText -v
```

A status page that panics, errors, or renders nothing falls back to
plain text with the same code. This is the guarantee that makes it safe
to render a 500 through the app's own renderer, which `page.go` used to
refuse to do.

## 4. A 500 cannot leak

```
go test ./pkg/runtime/ -run TestA500TellsTheStatusPageNothingItCouldLeak -v
```

`tako.Status` has no error field, and `Message` is cleared for any 5xx
before the page sees it. The detail is in the log under `RequestID`.

## 5. The one-line spelling still works

`examples/blog` declares `SiteConfig{NotFound: "404.html"}` and no
status page — Hugo emits the file, tako serves it, and it now carries
the same headers the template path always did.

## 6. Only one per package

An app declaring two status pages fails analysis (`tako/one-status`),
like two sites or two services.
