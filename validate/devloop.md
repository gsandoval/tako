# validate/devloop.md — items 35 and 27

Two defects found by walking, not by testing. Both were filed during
Stage 13 and fixed after it.

## 35 — `tako dev` used to kill its own child

## 1. The app that could not be developed

```
cd examples/hello && PORT=8121 tako dev
```

**Expect** `ready in …ms` and a working service:

```
curl localhost:8121/hello        → 200
curl localhost:8121/_tako/z/dev  → 200, the child's nonce
```

Before the fix this printed:

```
tako: port 8121 is answering, but not with this app — another process has it.
```

…and exited 1, having killed the healthy child it had just started.
`hello` is Gate G0's app: no auth, no bucket, no cron, and therefore no
reserved mux — which is where the readiness route used to live. It is
answered by `Serve` now, so every service that serves anything can
identify itself.

The path is `/_tako/z/dev`, not `/_tako/dev`. Worth writing down: a
walk that curled the shorter one measured nothing and read like a
confirmation.

## 2. The two verdicts are told apart

```
go test ./pkg/devserver -run "WaitReady|404sTheProbe|ChildsExit"
```

**Expect** green across three cases:

- a listener answering **200 with a different nonce** is another dev
  loop, and is reported immediately;
- a listener answering **404** is *not* proof of anything — the message
  says something answered and never identified itself, and no longer
  sends anyone to free a port tako itself released;
- **the child exiting outranks both**. A real collision IS the child
  failing to bind, and it explains itself on the way out.

## 3. A bare service still identifies itself

```
go test ./pkg/runtime -run TestDevReadyAnswersOnlyWithANonce
```

**Expect** green, including the case a service with no framework mux —
the regression this whole entry is.

## 27 — two routes Go's router cannot order

## 4. The pair is refused at analysis time

```
go test ./pkg/analyzer -run TestErrors/routeconflict
```

**Expect** exactly one diagnostic:

```
app.go:15:2: SetRole cannot be mounted beside a route this service already declares:
POST /moderate/{id}/retract — Go's router refuses a pair where neither pattern is
more specific, and the service panics at startup. Make one of them narrower
(a literal segment beats a wildcard) or move one to a different path (tako/route-conflict)
```

One, not two: the fixture also declares `GET /admin/new` beside
`GET /admin/{slug}`, which Go orders happily — a literal beats a
wildcard, and refusing that would break the trick every app uses.

The pair reported is the exact shape that panicked while AB10b's
moderation screen was being written, found by running the example rather
than by analysis.

## 5. The check cannot disagree with the router

The rule registers the declared patterns on a real `http.ServeMux` and
reports what it says, rather than restating Go's precedence rules. And
it checks `analyzer.MuxPattern(route)` — the same string codegen mounts,
so a check of one string and a mount of another is not possible.
