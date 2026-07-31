# validate/blog.md — S2: the static-generator site (Hugo + htmx islands)

The claim: a site OWNED by a static generator — Hugo builds it, tako
runs the build and ships the output — with dynamic surface exactly
where it belongs: htmx islands calling tako fragments. One repo, one
deployable, no second pipeline.

**Setup:** run the CLI as `go run ../../cmd/tako …` from
examples/blog. Hugo must be installed (`brew install hugo`) — it is
the example's OWN toolchain, like shop's npx; `go test` never needs
it (the fragment tests read templates from site/static directly).

## 1. The shape

```
cd examples/blog && go run ../../cmd/tako gen graph
```

**Judge:** ONE service: two fragment routes, the comments entity, the
site. The composite (post, created) was DERIVED from the one query
the handler makes. Note what is NOT here: no page routes for posts —
Hugo owns those.

## 2. Dev: the generator inside the loop

```
PORT=8091 go run ../../cmd/tako dev     # then open http://localhost:8091
```

- The startup line `⛅ site  cd site && hugo --minify → site/public`
  IS WithBuild running — tako invoked Hugo, then served its output.
- Browse: home lists both posts (Hugo's list template); open one —
  the comments island fills in below the article (watch the network
  tab: one `GET /frag/comments?post=…` after load).
- Comment. The island re-renders IN PLACE with your comment — no
  navigation. That round trip is: static page → htmx → tako fragment
  → entity store → HTML back.
- Edit `site/content/posts/hello-tako.md`, save, reload: the change
  is live — the dev loop reran Hugo (the ⛅ line repeats) without
  restarting anything you care about.
- The templates trick, worth understanding once: the fragment
  templates live in `site/static/templates/` — Hugo copies them into
  `public/templates/`, which is the one subtree a tako Site treats as
  templates and never serves raw. One build, both worlds.

## 3. The suite, the product way

```
go run ../../cmd/tako test
```

**Judge:** green WITHOUT Hugo installed — the tests register
templates from site/static. Read them as documentation: the island
round trip (empty state → no-JS 303 back to the static page → htmx
in-place re-render, oldest first), the CSRF 403 coming from the shim
(not the handler), and the site-local-path 422 (a comment's post
value round-trips into a redirect; an open redirect is not a
feature).

## 4. Cloud

```
go run ../../cmd/tako deploy --project <test-project>
```

**Judge the honest output:** the plan carries the comments composite
index; the summary prints
`⚠ 1 mutating route(s) without WithAuth: POST /frag/comments` —
TRUE and intended here (anonymous comments), the framework saying it
out loud is the point.

Then the walk on the live URL (B = the ✓ line):

```
curl -s $B/ | grep title                 # Hugo's home, embedded in the binary
curl -s -c /tmp/blog.jar "$B/frag/comments?post=/posts/hello-tako/"   # island + csrf
TOKEN=$(curl -s -b /tmp/blog.jar -c /tmp/blog.jar "$B/frag/comments?post=/posts/hello-tako/" | grep -o '_csrf" value="[0-9a-f]*"' | cut -d'"' -f3)
curl -s -b /tmp/blog.jar -H 'HX-Request: true' \
  -d "post=/posts/hello-tako/&name=walker&body=live!&_csrf=$TOKEN" $B/frag/comments
```

→ the returned island lists `walker · live!`, persisted in
Datastore under the derived composite.

```
go run ../../cmd/tako destroy --project <test-project>
```

**Judge:** DATA names the comments kind; typed name; clean project.
Record the verdict in docs/validation-log.md.
