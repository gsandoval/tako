# validate/policies.md — AB1 (Route.Allow) and AB4 (tako.Environment)

The two Stage 13 slices, walked end to end against `examples/journal`.
Authorization is the one feature where a passing test and a working door
are not the same claim, so every step below asks for a status code AND
what the visitor is told.

Run from `examples/journal` unless a step says otherwise.

## 1. The declaration is visible

```
go run ../../cmd/tako gen graph
```

**Expect** the two guarded routes to name their door, and only those two:

```
GET /moderate → Moderate 🔒 Moderators (app.go:182)
POST /moderate/{id}/retract → Retract 🔒 Moderators (app.go:183)
```

A `🔒` with no name is a route that requires a session and nothing more.
That difference is the whole point: before this, both looked the same.

## 2. The generated mount carries the name

```
go run ../../cmd/tako test -run TestTheDeskShowsEveryonesWork ./...
grep -n "moderate" .tako/gen/journal/main.go
```

**Expect** `runtime.RequireAuth(), runtime.RequirePolicy("Moderators")`
on both mounts. `RequireAuth` must be there even though `app.go` never
says `SignIn: true` — `Allow` implies it, and the graph is where that
implication becomes real.

## 3. The three ways to be refused, in a browser

```
PORT=8090 tako dev
```

1. **Signed out**, open `http://localhost:8090/moderate` → **401**, the
   journal's own page (masthead, "Back to the journal"), not plain text.
2. **Signed in via the dev bar as alice**, reload → **200**, the desk,
   with bob's draft on it. This is the local affordance: `tako dev` mints
   `.test` identities, and journal's policy admits those only while
   `tako.Environment() == tako.EnvLocal`.
3. **Retract** one of bob's posts → 303 back to the desk, and the post is
   gone from `/`.

## 4. The same identity, judged as a deploy judges it

Stop `tako dev`, then:

```
TAKO_BACKEND=gcp PORT=8090 tako dev
```

Sign-in through the dev bar is disabled here (it is local-only), so this
step is the automated one's manual twin: `go test -run
TestAStrangerAtTheDeskGetsAPageThatSaysWhy ./...` signs in locally and
then flips the environment, which is exactly what step 4 cannot do by
hand. **Expect** the test to show a **403** whose heading is "Not your
desk" and whose body carries the policy's own sentence.

The failure this catches: with `Status.Policy` unread, the same 403
renders "That form expired", because that is what 403 meant here until
the journal had a door.

## 5. The rule holds off the mount

```
go test -run TestTheDoorHoldsOnTheCallPath ./...
```

**Expect** green. A policy that only ran at the generated mount would be
bypassed by every in-package call, every test, and every cross-service
call — the last being the one place a trust boundary is actually
crossed.

## 6. The declaration is refused when it cannot work

From the repo root:

```
go test ./pkg/analyzer -run TestErrors/policyshape
```

**Expect** three diagnostics, one per mistake, at exact positions:
`tako/policy-var` (a function literal has no name to print or share),
`tako/unexported-policy` (the generated service names it from outside
the package), `tako/policy-private` (a private route has no session, so
every call would be refused).

## 7. AB4 on its own

```
go test . -run TestEnv ./...           # from the repo root
```

**Expect** `EnvLocal` with `TAKO_BACKEND` unset, `EnvCloud` with `gcp`,
and `EnvLocal` for anything unrecognised — a mistyped environment can
only turn a local affordance off in production, never on.
