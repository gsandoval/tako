# validate: query fragments (FL10)

The claim: a query assembled across a function boundary derives the
same indexes as the same query written inline, a helper derives none of
its own, and a query tako cannot follow is refused rather than
under-derived.

The failure this exists to prevent is RC1's, one call frame away: a
composite serves only queries that constrain its leading properties, so
a shape with no index answers 500 in production while every local test
passes — the local backend is bbolt and has no indexes to be wrong
about.

## 1. The closure and the helper derive the same index

`examples/journal` used a closure; FL10's commit promotes it to a
package-level fragment. The derived index must not move.

```
cd examples/journal
go run ../../cmd/tako gen graph --json | \
  python3 -c "import json,sys; g=json.load(sys.stdin); \
  print([(r['name'], r.get('composites')) for r in g['resources'] if r['kind']=='entity'])"
```

Expect `(published, -created)` — the same tuple the closure produced,
which is the whole point. Check by hand that `published` in `app.go` is
now a package-level func taking and returning `*tako.EntityQuery[Post]`.

## 2. A fragment asks for nothing on its own

Covered by `TestAnUncalledFragmentAsksForNothing`. Read the fixture's
`neverCalled`: before FL10 it derived `(author, section)` — a real
Datastore index, minutes to build and one of a project's 200, for a
query that does not exist.

## 3. Two OrderBys keep their order

```
go test ./pkg/analyzer/ -run TestOrderBysKeepTheOrderTheyAreWrittenIn -v
```

`OrderBy("Section").OrderBy("-PublishAt")` must derive
`(state, section, -publishAt)`. `ast.Inspect` visits a chained call
outermost-first, so before FL10 this derived
`(state, -publishAt, section)` — a composite whose sort suffix is
backwards is one Datastore will not use, which fails exactly like a
missing index.

## 4. What cannot be followed is refused

```
go test ./pkg/analyzer/ -run TestErrors/queryescapes -v
```

Two shapes, exact text asserted:

```
app.go:31:2: this query is stored in a struct field, so the composite indexes it needs cannot be derived — assemble it in one function, or in helpers that take and return *tako.EntityQuery[Article] (tako/query-escapes)
app.go:40:16: this query is passed to something tako cannot resolve statically (a function value, a method on an interface, or another package), so the composite indexes it needs cannot be derived — pass it to a helper in this package that takes and returns *tako.EntityQuery[Article] (tako/query-escapes)
```

Check by hand that both positions point at the escaping expression, not
at the function.

## 5. No example regressed

```
go test ./...
```

All five examples analyze and pass. A false positive here blocks a
deploy, so this step is not optional.
