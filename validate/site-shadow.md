# validate: the shadow warning (FL6)

The claim: when a declared route takes URLs the site has files at, tako
says so — naming the files, at the declaration, in `tako dev` and in
`tako deploy` — and never fails anything.

The failure this exists to prevent leaves no other trace. The route
answers 404 for a stylesheet, which is an honest answer to a request
the handler cannot serve; nothing logs, nothing errors, and the site is
unstyled in production.

## 1. It fires on a real app

`examples/journal` has the shape: `GET /{year}/{slug}` (`app.go:73`)
over a site with `/assets/…` at depth 2.

```
cd examples/journal
go run ../../cmd/tako dev
```

Expect, between `analyzing` and `generating`:

```
  app.go:73:2: ⚠ GET /{year}/{slug} shadows 3 files the site serves — /about/index.html, /assets/htmx.min.e209dda5.js, /assets/site.9f86d081.css reach Perma instead of the site, and answer 404; move them deeper than the route's 2 segments, or narrow the pattern (tako/site-shadow)
```

Check by hand that the position is real and the claim is true:

- `app.go:73` is the `Perma` declaration.
- `curl -s -o /dev/null -w '%{http_code}\n' localhost:8080/assets/site.9f86d081.css`
  answers `404` — the warning is not theoretical.
- The dev server **starts**. A warning that stopped the app would be a
  rule, and this is not one.

## 2. It stays quiet when the files are out from under the route

Move the assets a level deeper and re-run:

```
mkdir -p web/assets/css && git mv web/assets/site.9f86d081.css web/assets/css/
```

Expect: no `site-shadow` line for the stylesheet — three segments clear
a two-segment route. Restore with `git checkout web/`.

## 3. It is not a build failure

```
go run ../../cmd/tako deploy --plan --project <id>
```

Expect the same line among the other `⚠` warnings after the plan, and
an exit code of 0 (`echo $?`). Deploy warnings state costs at decision
time; this one states a surprise.

**Needs a project.** Without one the command stops at
`tako: no project set` before analysis, so this step belongs to a
release-cycle walk with credentials, not to a local run. What a local
run does cover: `warnSiteShadows` calls the same
`analyzer.ShadowedSiteFiles` step 1 exercises, after `build.Sites`, and
prints it the way `warnDomains` beside it does.

## 4. Automated

```
go test ./pkg/analyzer -run 'Shadow|Wildcard|Templates|Route|NoSite|Unbuilt|Conflicting|OneShadowed|ManyShadowed|FilesDeeper'
```

Ten cases, message text asserted exactly: the depth-2 hit, the
one-file singular wording, files deeper than the route (silent), the
"and N more" summary, a POST route not shadowing a GET asset, a `GET /`
route not swallowing the site, `templates/` excluded, no site declared,
an unbuilt `Build` output, and patterns that cannot coexist (declined,
because that is a startup panic with its own message).
