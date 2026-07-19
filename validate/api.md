# validate/api.md — Gate F1: API endpoints, complete (local only)

What this gate approves: request binding, the error-helper surface, and —
above all — every new error text, analyzer and HTTP. Read each error as
product UX: does it say what's wrong, where, and why?

Prerequisite: `PORT=8090` if 8080 is busy (Firestore emulator).

## 1. Start

```
cd examples/hello
PORT=8090 go run ../../cmd/tako dev
```

**Observe:** three endpoints listed (`/hello`, `/greet/{name}`,
`POST /echo`).

## 2. Good requests

```
curl localhost:8090/greet/memo              # {"message":"Hello, memo!"}
curl "localhost:8090/greet/memo?loud=true"  # {"message":"HELLO, MEMO!"}
curl -X POST localhost:8090/echo -d '{"message":"hi"}'   # {"message":"hi"}
```

## 3. Bad requests — judge each HTTP error text

```
curl -i localhost:8090/greet/nobody
# 404 {"error":"not found"}

curl -i "localhost:8090/greet/memo?loud=banana"
# 400 {"error":"query \"loud\": \"banana\" is not a bool"}

curl -i -X POST localhost:8090/echo -d '{oops'
# 400 {"error":"body: invalid JSON"}

curl -i -X POST localhost:8090/echo -d '{"message":""}'
# 422 {"error":"message must not be empty"}     ← handler's own Errorf
```

## 4. Analyzer errors — judge each text (all recover on revert)

In `hello.go`, try each of these one at a time, save, read the dev
console, revert:

a. Change the `Greet` route to `"GET /greet"` (keep the `path:"name"`
   tag) → `tako/route-params` (tag without parameter).
b. Change the tag to `path:"nick"` (keep `{name}` in the route) → two
   `tako/route-params` errors (missing + extra).
c. Add `tako.WithTTL(time.Minute)` to `Hello`'s options →
   `tako/invalid-option`, message lists what tako.API accepts.
d. Add an untagged field `Extra string` to `GreetReq` →
   `tako/body-on-get`.
e. Rename `GreetReq` → `greetReq` (and its uses) →
   `tako/unexported-req`.
f. Change `Loud bool` to `Loud time.Time` (import time) →
   `tako/bind-type`.

Each failure keeps the last good build serving (curl still answers).

## 5. Payload shape is the compiler's job

Change `greet`'s signature to `func greet(name string) string`.
**Observe:** the failure is a *Go compile error* naming the function —
not a tako analysis error. Revert.

## 6. Generated binding code

```
cat .tako/gen/hello/main.go
```

Judge: `decodeGreet`/`decodeEcho` — plain field assignments, one helper
call per tagged field, no reflection. Would you trust this in review?

## 7. Handler log correlation

Add `ctx.Log().Info("greeting", "name", req.Name)` to `greet`, save,
curl. **Observe:** the log line carries a `req=<id>` attribute alongside
the request line. Revert.

## 8. Tests

```
cd ../.. && go test ./... && cd examples/hello && go test ./...
```

Record the verdict in `docs/validation-log.md` (feature F1, gate F1).
