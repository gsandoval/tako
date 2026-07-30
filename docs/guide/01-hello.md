# 1 · hello — endpoints and the loop

The whole app ([examples/hello](../../examples/hello/hello.go)):

```go
package hello

var Hello = tako.API("GET /hello", hello)
var Greet = tako.API("GET /greet/{name}", greet)
var Echo  = tako.API("POST /echo", echo)

type GreetReq struct {
    Name string `path:"name"`
    Loud bool   `query:"loud"`
}

func greet(ctx tako.Ctx, req GreetReq) (Greeting, error) {
    if req.Name == "nobody" {
        return Greeting{}, tako.NotFound()
    }
    ...
}
```

Everything the framework does starts from this shape:

- **A handler is a package-level var** made by a constructor. The var
  is the registration, the docs, and (for APIs) the callable
  endpoint. No comment directives, no init side effects.
- **Requests bind by tags** — `path:`, `query:` for GET;
  untagged fields decode from the JSON body on writes. Binding code
  is *generated*, readable, and reflection-free.
- **Errors are values with status**: `tako.NotFound()`,
  `tako.Errorf(422, "message must not be empty")` — the body is
  `{"error": "..."}` with that status; anything else is an opaque 500
  with the real error only in your logs.

## Run it

```
cd examples/hello
tako dev
curl localhost:8080/hello
curl 'localhost:8080/greet/ada?loud=true'
curl -X POST localhost:8080/echo -d '{"message":"hi"}'
```

Edit a handler; the app reloads on save. The generated main under
`.tako/gen/hello/` is meant to be read — that's the whole request
path, no magic left.

The analyzer is the other half of the DX: mistakes fail at build
time with positioned, specific errors (`tako gen graph` runs just the
analysis). A non-constant route, a body on a GET, a `{param}` with no
tagged field — each has an exact message, because error text is
product surface here.

## Deploy it

```
tako deploy --project <your-project>
```

Preflight tells you anything missing (APIs, permissions) with the
exact command to fix it; the plan lists what will exist; the ✓ line
is your URL. `tako destroy` (typed name) removes it all. That cycle —
plan, apply, converge, destroy — is the same for every example after
this one; only the graph grows.
