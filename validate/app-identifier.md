# validate: the app identifier (FL8)

The claim: an app whose Go module path names a host can deploy. tako
derives an identifier Google will accept wherever Google constrains the
characters, states it once, and leaves the module path as the app's
name everywhere a person reads it.

The failure this exists to prevent is total and has no app-side
workaround: `Config.Labels()` sent `github.com/gsandoval/flauta` as a
GCS label value, Google refused it with HTTP 400, and the deploy died
on its first create. Renaming the Go module is not a workaround.

## 1. The derived id is stated once, at the top

`examples/hello` now declares `module
github.com/gsandoval/tako-example-hello` — a host-shaped path, which
is what no example had and what let this ship.

```
cd examples/hello
go run ../../cmd/tako deploy --plan --project <project>
```

Expect the header to name both, and nothing else in the run to repeat
the id:

```
tako deploy · github.com/gsandoval/tako-example-hello → <project> (us-central1)
              id tako-example-hello-812a9c — labels and bucket names use this
```

Check by hand: the id is stable across two runs, and `tako deploy
--plan` in `examples/gallery` (module `gallery`) prints NO id line —
a module path that is already legal is its own id, and saying so would
be noise.

## 2. Generated code still reads as the app

The module move is also the first example whose last path segment is
not a Go identifier. Confirm the generated main imports it under the
package's own name:

```
go run ../../cmd/tako dev
sed -n '8,14p' .tako/gen/hello/main.go
```

Expect `hello "github.com/gsandoval/tako-example-hello"` and every
reference below it spelled `hello.Hello`, not `tako_example_hello.Hello`.
Then `curl localhost:8080/hello` and `curl localhost:8080/greet/<name>`.

## 3. An illegal env is refused, with its repair

The id is DERIVED because it comes from go.mod. An env is TYPED, so it
is refused instead — deriving `staging` from `Staging` would make
`tako destroy --env Staging` address something the user never named.

```
go run ../../cmd/tako deploy --plan --project <project> --env Staging
go run ../../cmd/tako deploy --plan --project <project> --env feature/x
go run ../../cmd/tako deploy --plan --project <project> --env ...
```

Expect the rule stated, the repair offered for the first two, and NO
repair for the third (there is nothing to suggest). Expect all three to
fail before any network call.

## 4. A name too long is refused before anything is created

Not scriptable against a real project without a pathological name;
covered by `TestAnOverlongBucketNameIsRefusedBeforeAnythingIsCreated`
in `pkg/provision`. Read the message and check it names all four parts
of the assembled name, because any of them may be the one to shorten.

## 5. Live — the half this cannot prove

`--plan` never creates anything, so it cannot exercise the failure FL8
fixes: the label is rejected at CREATE time. Proving it needs one real
deploy of an app with a host-shaped module path, plus a `tako destroy`,
and belongs to the next release-cycle walk. What is verified here is
everything up to the create; what is not is the create itself. Say so
rather than claim it.
