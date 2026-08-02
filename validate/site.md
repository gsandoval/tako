# Validate W1 — Site: embedded static, caching headers, WithBuild/WithDevProxy

Stage 3 opens. journal is born as the smallest tako website: one
declaration, zero handlers, correct headers, one binary. Prereqs for
§4: gcloud ADC and the test project (no new APIs, grants, or plan
objects — judge that too). No installed `tako` needed: run the CLI as
`go run ../../cmd/tako …` from the example dir (or install once with
`go build -o ~/bin/tako ./cmd/tako` from the repo root). Dev serves
on $PORT (default 8080 — often taken); every step pins PORT=8090.

## 1. The static shell, locally

```
cd examples/journal && PORT=8090 go run ../../cmd/tako dev
curl -sI localhost:8090/                              # HTML
curl -sI localhost:8090/assets/site.9f86d081.css      # fingerprinted
curl -s -o /dev/null -w '%{http_code} -> %{redirect_url}\n' localhost:8090/about-us
curl -sI localhost:8090/about                         # pretty URL
```

**Judge:** HTML → `Cache-Control: no-cache`, a strong quoted ETag,
`Content-Security-Policy-Report-Only: default-src 'self'`, nosniff,
referrer-policy; the fingerprinted asset → `public, max-age=31536000,
immutable` and NO document security headers; `/about-us` → 308 to
`/about`; `/about` resolves `about/index.html`; an unknown path 404s
(journal declares no SPA()). Replay the home request with
`If-None-Match: <etag>` → 304. Edit `web/index.html` while dev runs —
the change serves after the reload (the site is re-staged per cycle).

## 2. WithBuild — any generator, no tako knowledge of it

With hugo installed: scaffold a throwaway site (`hugo new site tmp`)
or, without hugo, any command that writes static output works — the
point is tako runs YOUR command and serves YOUR output dir:

```
# temporary edit to examples/journal/app.go:
#   tako.Site("./web", tako.SiteConfig{Build: tako.Build{Command: "hugo --minify", Out: "public"}})   (hugo)
#   tako.Site("./web", tako.SiteConfig{Build: tako.Build{Command: "sh build.sh", Out: "dist"}})       (any script)
PORT=8090 go run ../../cmd/tako dev      # observe: ⛅ site  <cmd> → <out> (N files), then serving
```

**Judge:** the build narrates and re-runs on change; a failing command
(`exit 1`) stops with `site build failed: <cmd>: … — <stderr tail>`
BEFORE anything serves or deploys; an output-less command errors with
the its-output-dir-is-empty text. Revert the edit.

## 3. WithDevProxy — HMR dev servers

```
# temporary: tako.Site("./web", tako.SiteConfig{DevProxy: "http://localhost:1313"})
hugo server -p 1313 &     # or any dev server (vite, astro, python -m http.server)
PORT=8090 go run ../../cmd/tako dev      # site requests proxy through; deploy ignores the proxy
```

**Judge:** local requests hit the generator's server (its live-reload
works through tako); the analyzer/build path still requires a real
dir/build for DEPLOY (the proxy is dev-only). Revert.

## 4. Full cycle — the binary IS the site

```
go run ../../cmd/tako deploy --project <test-project>
BASE=<the ✓ journal URL>
curl -sI $BASE/ ; curl -sI $BASE/assets/site.9f86d081.css
curl -s -o /dev/null -w '%{http_code} -> %{redirect_url}\n' $BASE/about-us
```

**Judge, against §1's local answers:** byte-identical headers — same
ETag values (same content, same hash), same cache-control split, same
308. The plan shows NOTHING new beyond C-phase kinds (no site objects:
the binary carries it); `analyzing` says `1 service, 0 endpoints,
1 site`. Then `go run ../../cmd/tako destroy --project <test-project>` (typed name) leaves the project clean.

Record the verdict in docs/validation-log.md (feature W1, gate W1).
