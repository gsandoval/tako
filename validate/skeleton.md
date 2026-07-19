# validate/skeleton.md — Gate G0: the walking skeleton (local only)

What this gate approves: the *dev-loop feel* — analyze → generate → run,
sub-second reload, and the quality of the first analyzer error. Everything
later builds on this loop.

Prerequisite: port 8080 free, or export `PORT=<free port>` first (the
transcript below uses 8090; `tako dev` honors `PORT`).

## 1. Start the dev loop

```
cd examples/hello
go run ../../cmd/tako dev        # or a built tako binary
```

**Observe:** a banner like

```
tako dev · hello

  analyzing   1 service, 1 endpoint
  generating  .tako/gen/hello/main.go
  starting    http://localhost:8090

  GET /hello → Hello (hello.go:5)

ready in ~400ms — watching (backends: local, in-process)
```

Judge: does `ready in <time>` feel instant? Is the endpoint listing enough
to orient you?

## 2. Hit the endpoint

```
curl localhost:8090/hello
```

**Observe:** `{"message":"Hello from Tako!"}` and a request line
(`GET /hello → 200 (0.0ms)`) in the dev terminal.

## 3. Edit-reload feel (the core sensation)

Edit `hello.go`: change the message string, save, re-curl.

**Observe:** `reloaded in <time>` — judge whether it's genuinely sub-second
and whether the loop feels like "save, curl, see it". The reload line is
the only output; no re-printed banner noise.

## 4. The first analyzer error

In `hello.go`, replace the route with a variable:

```go
var route = "GET /hello"

var Hello = tako.API(route, hello)
```

**Observe** on save:

```
✗ analysis failed — still serving last good build
  hello.go:7:22: tako.API route must be a string constant — routes are read
  at analysis time, not run time; got variable "route" (tako/const-route)
```

Judge the error as product UX: does it say what's wrong, where, and why —
without reading docs? Confirm `curl` still answers (last good build).
Revert the edit; **observe** `reloaded in <time>` and the fix serving.

## 5. Inspect what was generated

```
cat .tako/gen/hello/main.go
```

Judge readability: could a user follow what their service does from this
file alone?

## 6. The graph

```
go run ../../cmd/tako gen graph
```

**Observe:** AppGraph JSON + a summary ending in
`GET /hello → Hello (hello.go:5)`. Run it twice — output must be identical.

## 7. Tests

```
cd ../.. && go test ./...        # framework: lock, goldens
cd examples/hello && go test ./...   # hello hits its endpoint over HTTP
```

**Observe:** all green.

## 8. Shut down

Ctrl-C the dev loop. **Observe:** prompt returns, port is freed
(`curl` now fails to connect).

Record the verdict in `docs/validation-log.md` (feature: S0 walking
skeleton, gate G0).
