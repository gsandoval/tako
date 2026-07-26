# validate/journal.md — W6 MILESTONE: journal, complete (closes W3–W6)

The second batched Stage 3 gate: the htmx + dynamic-markdown + auth
showcase whole — a server-driven interactive website with no SPA, no
JSON round-trips for the UI, and no client framework beyond a vendored
htmx. One script, local and cloud (F9 style). This is Stage 3's
mid-point DX review — notes beyond pass/fail are welcome in the log.

**Setup:** run the CLI as `go run ../../cmd/tako …` from
examples/journal (or `go build -o ~/bin/tako ./cmd/tako` once, from
the repo root). Dev pins PORT=8090. Cloud (§6) needs the one genuinely
manual Identity Platform step — see there.

## 1. The whole app, at a glance

```
cd examples/journal && go run ../../cmd/tako gen graph
```

**Judge:** one service; four pages with 🔒 on the two writes; entity
posts (indexes + the derived composite serves the home query); site
./web; `auth (google)`. The app is ~120 lines; everything else is
declarations.

## 2. Dev: sign in, write, watch fragments

```
PORT=8090 go run ../../cmd/tako dev     # then open http://localhost:8090
```

- The dev bar (bottom): **sign in as alice** — no GCP anywhere.
- Write a post IN THE INLINE HOME FORM with markdown in the body
  (heading, emphasis, a link, a code fence): with JS on, the new
  post-card swaps in atop the list with NO navigation (watch the
  network tab: one POST, a fragment response, no redirect).
- Open the post: markdown rendered — real `<h1>`, `<em>`, nofollow
  link, highlighted-ready code block.
- The publish toggle swaps JUST the button (network tab again).
- Sign out, retry a write → 401 `sign in to continue`; sign in as
  bob and try toggling alice's post → 403 `only the author can
  publish`.
- No-JS check (disable JS or curl): the same form 303s to the post —
  one handler, both worlds.

## 3. The XSS judgment (W4's centerpiece)

Publish a post whose body is:

```
# Fine heading

<script>alert("xss")</script>
<p onclick="evil()">click me</p>
```

**Judge:** the heading renders; the script and the handler VANISH
(view source: no `<script`, no `onclick`, no escaped remnants —
stripped, not escaped). This is the sanitized default; the opt-out is
`tako.WithTrustedHTML()` and journal does not take it.

## 4. Sessions are records you can see and kill

While signed in locally: `ls .tako/data/` — sessions live in the
entity store, same as the cloud. Sign-out deletes the record (the
suite pins that a revoked session's cookie is dead on the next
request).

## 5. The suite, the product way

```
go run ../../cmd/tako test
```

**Judge:** green, and read pages_test.go — 401/403/ownership/
revocation/fragment/markdown contracts as user-style tests.

## 6. Cloud — the same app, real Google sign-in

```
go run ../../cmd/tako deploy --project <test-project>
```

**Preflight will stop you** with the one genuinely manual step of the
whole feature — `the Google sign-in provider is not configured …
console → Identity Platform → Providers → add Google` — judge that
text, do the click (accept the OAuth consent defaults), and re-run.
(Everything else is automatic: the API and initialization were REST'd
in W5; a fresh project gets those as preflight lines too.)

**Judge the plan + summary:** `posts (published, -created)` composite;
`datastore [user]` covering entities AND sessions; identitytoolkit in
the probed list; NO ⚠ mutating-routes warning (journal's writes are
closed — that warning line exists now and fires when they aren't).

Then in the browser against the ✓ URL: sign in with Google (the
provider you just added), write a markdown post, watch the fragment
swap, verify the XSS post from §3 is inert in the cloud too, sign
out, confirm 401. In the Datastore console (namespace prod): your
`_tako_session` record while signed in, `posts` entities; delete the
session record by hand → your browser is signed out on next action —
operator revocation, live.

```
go run ../../cmd/tako destroy --project <test-project>
```

**Judge:** DATA names `posts` (sessions purge silently — credentials,
not content); typed name; clean project.

Record ONE verdict in docs/validation-log.md — the row closes W3, W4,
W5, and W6. W7 (tako gen client ts; shop is born) follows.
