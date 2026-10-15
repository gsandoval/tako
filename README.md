# Tako

Write Go. Get a cloud.

📖 **Documentation:** [tako.memo.engineer](https://tako.memo.engineer)

Tako is a Go framework over Google Cloud Run: business logic as plain
Go packages — APIs, background workers, server-rendered websites —
with a CLI that runs everything locally in one process (no Docker, no
emulators) and derives the whole cloud from your code (no Terraform,
no YAML, no Dockerfiles). Infrastructure, wiring, and least-privilege
IAM come from static analysis of the declarations you already wrote.

```go
package hello

import "github.com/gsandoval/tako"

var Greet = tako.API("GET /greet/{name}", greet)

type GreetReq struct {
	Name string `path:"name"`
	Loud bool   `query:"loud"`
}

func greet(ctx tako.Ctx, req GreetReq) (Greeting, error) {
	msg := "Hello, " + req.Name + "!"
	if req.Loud {
		msg = strings.ToUpper(msg)
	}
	return Greeting{Message: msg}, nil
}

type Greeting struct {
	Message string `json:"message"`
}
```

## Five minutes to a URL

**0. Install** (Go 1.26+):

```
go install github.com/gsandoval/tako/cmd/tako@latest
```

From a checkout of this repo, `make install` does the same thing with
your working tree. Either way the binary lands in `$GOBIN` (falling
back to `$GOPATH/bin`, usually `~/go/bin`) — run `make where` to see
the exact path, and make sure it's on your `PATH`.

**1. A project** (~1 min):

```
mkdir hello && cd hello
go mod init hello
go get github.com/gsandoval/tako
```

Save the file above as `hello.go` (add `"strings"` to the imports).

**2. Run it locally** (~30 s):

```
tako dev
```

```
curl 'localhost:8080/greet/ada?loud=true'
{"message":"HELLO, ADA!"}
```

Edit the handler; it reloads on save. Everything local lives under
`.tako/` — delete it to reset.

**3. Deploy it** (~2–3 min on first run — image build + service
creation). You need the [gcloud CLI](https://cloud.google.com/sdk)
authenticated (`gcloud auth application-default login`) and a GCP
project with billing enabled:

```
tako deploy --project <your-project-id>
```

Preflight checks what the app needs and prints the exact command for
anything missing (like enabling an API). Then a plan of everything
that will exist, then:

```
✓ hello → https://hello-…-uc.a.run.app
```

```
curl 'https://hello-…-uc.a.run.app/greet/ada'
{"message":"Hello, ada!"}
```

**4. Tear it down**:

```
tako destroy --project <your-project-id>
```

It lists everything it will delete — including data, loudly — and
asks you to type the app's name.

## Where to next

The framework grows through six worked examples — each one is a
tutorial chapter and part of the test suite:

| | Adds |
|---|---|
| [hello](docs/guide/01-hello.md) | endpoints, binding, the dev loop |
| [shortlink](docs/guide/02-shortlink.md) | entities, transactions, queues + workers, chaos testing |
| [gallery](docs/guide/03-gallery.md) | buckets, signed URLs, event fan-out, dead letters, cron, locks |
| [journal](docs/guide/04-journal.md) | server-rendered site, htmx fragments, sanitized markdown, Google sign-in |
| [shop](docs/guide/05-shop.md) | generated TypeScript client, private services + typed calls, jobs, singletons |
| [blog](docs/guide/06-blog.md) | Hugo-built static site, htmx islands calling tako fragments |

Plus the two stories: [local](docs/guide/local.md) (what dev/test
actually run) and [cloud](docs/guide/cloud.md) (what deploy creates,
the permissions model, the one manual setup step, what destroy
leaves). And when you need one feature's whole answer — declare, use,
local, cloud, test, errors — enter the
[feature reference](docs/reference/README.md) sideways: one page per
feature, every page the same shape.

## The opinions, briefly

- **Declarations are code.** Package-level vars via constructors —
  no comment directives, no reflection in the request path, generated
  code you can read.
- **Local is in-process.** One binary, breakpoints across every
  boundary; a shared conformance suite — not emulators — keeps local
  and GCP honest with each other.
- **The cloud is derived.** Stateless reconciliation with classified
  plans; IAM comes from usage edges; removing code revokes its access.
- **Costs stay visible.** Sync calls, cold starts, at-least-once
  delivery, per-instance caches — priced in warnings and doc
  comments, never absorbed silently.
- **Friction scales with irreversibility.** Reversible things are
  frictionless; destructive plans prompt; data destruction is always
  a typed confirmation.
