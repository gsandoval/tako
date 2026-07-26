# Validate W2 MILESTONE — the server-rendered website (absorbs W1)

The first batched gate (CLAUDE.md Stage 3 amendment): one walk covering
the static story (W1) and the dynamic half (W2) — journal as a real
website with authored posts. Prereqs: gcloud ADC, the test project.
For the W1-only judgments in depth (WithBuild/hugo, WithDevProxy,
failure texts), validate/site.md remains runnable — §1 here condenses
its keepers.

**No installed `tako` needed:** every command below runs the CLI from
the repo (`go run ../../cmd/tako …` from an example dir). To install
it instead: `go build -o ~/bin/tako ./cmd/tako` from the repo root
(then plain `tako …` works anywhere). **Port:** dev serves on $PORT
(default 8080, which collides with anything already there — Java app
servers love it); every step here pins PORT=8090 explicitly. If a
boot reports the entity store held by another process, an orphaned
service from an earlier run has the lock — the error text names the
lsof command.

## 1. The static half (W1 condensed)

```
cd examples/journal && PORT=8090 go run ../../cmd/tako dev
curl -sI localhost:8090/about                          # static page
curl -sI localhost:8090/assets/site.9f86d081.css       # fingerprinted
curl -s -o /dev/null -w '%{http_code} -> %{redirect_url}\n' localhost:8090/about-us
```

**Judge:** no-cache + strong ETag + CSP-Report-Only on the page;
immutable on the asset; 308 on the redirect; replay with If-None-Match
→ 304. `curl localhost:8090/templates/layout.html` → 404 (templates
back site.Render, never serve).

## 2. Pages and forms

```
open http://localhost:8090/            # rendered home (empty-state copy)
open http://localhost:8090/write       # the form; view source: {{ csrf }} → hidden _csrf input
```

Write a post in the browser: title + body → Publish.

**Judge:** the POST answers 303 See Other to /p/<id> (devtools network
tab — POST-redirect-GET; refresh does NOT resubmit), the post page
renders inside the layout (title in <title>, nav present), home lists
it newest-first. Rendered pages carry the same document headers as
§1's static page — one policy.

## 3. CSRF, automatic

```
curl -s -c /tmp/j localhost:8090/write >/dev/null
curl -s -b /tmp/j -X POST -d 'title=x&body=y' localhost:8090/p        # no token
curl -s -X POST -d 'title=x&body=y' localhost:8090/p                  # no cookie at all
```

**Judge:** both → 403 `CSRF token missing or invalid — forms need
{{ csrf }}`. With the token (grab from /write's source) → 303. The
layout also exposes <meta name="csrf-token"> for fetch callers.

## 4. Escaping until W4

Publish a post whose body is `<script>alert("xss")</script>`.

**Judge:** the post page shows the tag as TEXT (view source:
`&lt;script&gt;`), nothing executes. This is the pre-markdown safety
floor; W4's sanitized markdown replaces it without opening the hole.

## 5. Full cycle

```
go run ../../cmd/tako deploy --project <test-project>
BASE=<the ✓ journal URL>
```

Re-run §§1–4's curl judgments against $BASE (browser optional but
satisfying). **Judge additionally:** the plan gained journal's FIRST
grant — `~/+ iam grants project` with datastore.user for journal-sa
(the posts entity's edge) and preflight now probes datastore; ETag
values match local (byte parity through the embed); the full write →
303 → read → home-lists round-trip works in the cloud.

## 6. Destroy

```
go run ../../cmd/tako destroy --project <test-project>    # DATA names the posts kind
```

Typed confirmation; clean project. Record the verdict in
docs/validation-log.md — this row closes W1 AND W2.
