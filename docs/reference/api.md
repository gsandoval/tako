# tako.API

JSON endpoints: one declaration is the route, the docs, and the
callable endpoint ([hello chapter](../guide/01-hello.md)).

## Declare

A package-level var, a Go 1.22+ method-and-pattern route (a string
constant — analyzer-enforced), a handler `func(Ctx, Req) (Res, error)`
([examples/hello](../../examples/hello/hello.go)):

```go
var Hello = tako.API("GET /hello", hello)
var Greet = tako.API("GET /greet/{name}", greet)
var Echo = tako.API("POST /echo", echo)
```

The only two settings, both on `tako.Route`: `SignIn` (401 without a session;
`ctx.User()` guaranteed past it) and `Private` (IAM-only
ingress — other services, not the public internet). Both describe the
ROUTE they sit on.

Settings that describe the whole service — the domain it answers on, how
it scales — live on [`tako.Service`](service.md) instead, because a
service has many routes and none of them is a more honest place to put a
service-wide fact:

```go
var (
	api     = tako.Service(tako.ServiceConfig{Domain: "api.example.com", Profile: tako.ProfileLatency})
	Create  = tako.API("POST /api/links", create)
	Resolve = tako.API("GET /api/links/{slug}", resolve)
	List    = tako.API("GET /api/links", list)
)
```

## Use

Req fields bind by tag — `path:`, `query:`, `form:` — and untagged
fields decode from the JSON body. An empty Req struct is fine.
`query:` fields must be string, bool, int, int64, or float64.

```go
// GreetReq binds {name} from the route and loud from the query string.
type GreetReq struct {
	Name string `path:"name"`
	Loud bool   `query:"loud"`
}
```

Errors are values with status: `tako.Errorf(422, "message must not be
empty")` surfaces as that status with body `{"error":"..."}`;
`tako.NotFound()` is the 404. Anything else is an opaque 500 with the
real error only in your logs.

A `tako.Blob` result streams with its own Content-Type instead of
being JSON-encoded, and a `tako.Blob` field makes the request
multipart ([examples/gallery](../../examples/gallery/app.go)):

```go
// A tako.Blob field makes the request multipart: img streams from the
// form field, caption arrives beside it.
type UploadReq struct {
	Caption string    `form:"caption"`
	Img     tako.Blob `form:"img"`
}

// Returning tako.Blob streams the object: correct Content-Type, no JSON.
func serveImage(ctx tako.Ctx, req ImageReq) (tako.Blob, error) {
	return photos.Get(ctx, req.ID) // miss → IsNotFound → 404
}
```

## Local

`tako dev` serves everything on one port (default 8080) and reloads
on save. Binding code is *generated*, readable, reflection-free — the
main under `.tako/gen/<pkg>/` is the whole request path.

```
curl 'localhost:8080/greet/ada?loud=true'
curl -X POST localhost:8080/echo -d '{"message":"hi"}'
```

## Cloud

Package = service: your API routes deploy as one Cloud Run service
running as its own service account, with IAM derived from the code.
The grouping is derived, never declared — `tako.Service` names that
service and carries its settings, it does not create or split it
([service.md](service.md)).
The default profile scales to zero — an idle API costs nothing.
`WithPrivate` closes the front door: browsers and curl get 403 from
Cloud Run itself; callers get `run.invoker` derived from the call
edge. Mixing private and public routes in one package is an analysis
error — private endpoints need their own package.

## Test it

Handlers are directly callable — no server needed
([examples/hello](../../examples/hello/hello_test.go); use
`tako.NewTestApp(t)` and `app.Ctx()` once the app declares resources):

```go
got, err := Greet.Call(ctx, GreetReq{Name: "memo", Loud: true})

_, err = Greet.Call(ctx, GreetReq{Name: "nobody"})
if !tako.IsNotFound(err) {
	t.Fatalf("err = %v, want IsNotFound", err)
}
```

## Errors you'll meet

At analysis time, positioned and specific:

```
tako.API route must be a string constant — routes are read at analysis time, not run time; got variable "route" (tako/const-route)
route "GET /greet/{name}" has path parameter {name} but GreetReq has no field tagged path:"name" (tako/route-params)
field When: query:-tagged fields must be string, bool, int, int64, or float64; got time.Time (tako/bind-type)
GET route "GET /search" cannot bind a JSON body, but SearchReq field Extra has no path:/query: tag (tako/body-on-get)
tako.Blob field Img needs a form:"…" tag — blobs bind from multipart form fields (tako/blob-binding)
GET route "GET /b" cannot receive an upload — requests with tako.Blob fields need a non-GET route (tako/blob-binding)
tako.Route must be written as a literal with constant fields — configuration is read at analysis time, not run time; got variable "cfg" (tako/config-literal)
```

On the wire: bad input is a 400 — `{"error":"body: invalid JSON"}`,
`{"error":"query \"loud\": \"banana\" is not a bool"}`; `NotFound()`
is a 404 `{"error":"not found"}`; anonymous on a `WithAuth` route is
a 401 `sign in to continue`.

## Not yet

Responses are JSON or a `tako.Blob` stream — nothing else. Multipart
uploads buffer through the service (32MB in memory, then temp files);
large uploads belong on signed URLs — see [bucket.md](bucket.md).
No per-endpoint rate limiting (rate is a queue setting — `WithDelivery`, see [queue.md](queue.md)).
