# validate: the fourth friction review (FL15–FL19)

Five tasks from one re-read of `flautas/docs/friction.md`. Four are
checkable on a laptop; FL19's own symptom needs a fresh-ADC machine and
a real project, so its live half belongs to the release-cycle walk.

Everything below runs from a checkout, no cloud.

```
go build -o /tmp/tako ./cmd/tako
cd examples/journal && PORT=8130 /tmp/tako dev
```

Leave that running for §1–§3; §4 needs it running, which is the point.

## 1. FL15 — a form may repeat a field

The claim: a checkbox group reaches the handler whole, and a route
segment refuses to be one.

Sign in as alice on the dev bar, write three posts, then tick two boxes
on the home list and press "Unpublish selected".

Expect: the two chosen posts leave the list and the third stays. The
count line drops by exactly two. **One-of-two is the failure this
closed** — a scalar binding took the first ticked id and reported
success for all of them — so the count is the assertion, not the
absence of an error.

Then look at the markup:

```
curl -s localhost:8130/ | grep 'type="checkbox"'
```

Expect: plain `<input type="checkbox" name="selected" value="…">`, all
sharing one name, inside a plain `<form action="/p/unpublish">`. No
script builds the list; turning JavaScript off changes nothing.

The refusals, on a real app. Temporarily make `PermaReq.Year` a
`[]string` and run `go run ../../cmd/tako gen graph`:

```
tako: app.go:190:2: field Year: path:-tagged fields cannot be []string — a route segment is one value; a field that repeats binds with query: or form: (tako/bind-type)
```

Then give `UnpublishReq` a `[]int` field:

```
app.go:279:2: field Weird: form:-tagged fields must be string, []string, bool, int, int64, or float64; got []int (tako/bind-type)
```

Revert both.

## 2. FL17 — a blob carries its headers, and so does a stylesheet

```
curl -s -i localhost:8130/assets/css/site.9f86d081.css | grep -i '^x-content-type-options\|^content-type\|^cache-control'
curl -s -i localhost:8130/ | grep -i '^x-content-type-options\|^content-security-policy'
```

Expect: the **stylesheet** carries `nosniff` (this is the half that was
missing — nosniff was set only inside an HTML-only branch) and does NOT
carry CSP. The page carries both.

The blob half is in `examples/gallery`'s suite, where a thumbnail is
uploaded and served:

```
cd examples/gallery && go test -run TestUploadDownloadRoundtrip ./...
```

Expect green: the served original answers `no-cache` + `nosniff`, and
the thumbnail — which declares `Route{MaxAge: 24 * time.Hour}` —
answers `public, max-age=86400` + `nosniff`.

## 3. FL18 — the dev bar behaves

```
curl -s localhost:8130/ | grep -o 'id="tako-dev-bar"'
```

Expect one match, inside `</body>`. Then an htmx swap:

```
curl -s -H "HX-Request: true" -X POST -d "title=x&body=b&_csrf=<token>" \
  localhost:8130/p
```

Expect the post card **and nothing else** — no bar. Before this, every
swap carried a `position:fixed` strip into whatever it replaced.

In a browser: `#tako-dev-bar { display: none }` in the app's own
stylesheet hides it. That rule working is the contract.

## 4. FL16 — the readiness probe proves it reached this child

With journal still serving on 8130, start a second app on the same
port:

```
cd examples/blog && PORT=8130 /tmp/tako dev
```

Expect: **no `ready` line**, a non-zero exit, and the child's own
error last:

```
tako: serve: port 8130 is already in use by another process — set PORT (e.g. PORT=8090 tako dev) or free it (`lsof -i :8130`)
tako: the service exited during startup
```

The defect this closed printed `ready in 590ms` first, because a TCP
dial to a port someone else holds succeeds. Two tako apps on one
machine is the normal case for anyone working on tako and an app
together.

Also confirm the probe stays invisible: nothing in the dev console
mentions `/_tako/z/dev`, and `request_ms` counts no sample for it.

## 5. FL19 — calls are billed to the project they were given

No local symptom: the 429 needs a real project and ADC with no quota
project set. What is checkable here is the wiring —

```
go test ./pkg/provision/gcp/ -run "TestEveryCallIsBilled|TestTheQuotaProjectDenial"
```

— and, on the next release-cycle walk against a live project:

```
gcloud auth application-default login          # deliberately no quota project
tako deploy --project <p> --yes
tako logs --project <p>
```

Expect logs to render rather than answer HTTP 429 for a consumer that
is not `<p>`. If the deployer lacks `serviceusage.services.use`,
expect the named grant instead of a bare 403:

```
cannot bill API calls to <p> — grant: gcloud projects add-iam-policy-binding <p> --member user:$(gcloud config get account) --role roles/serviceusage.serviceUsageConsumer
```
