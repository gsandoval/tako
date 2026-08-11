# validate/entity-index.md — FL20 (`entity:"index"` decides what is indexed)

Run from the repo root.

## 1. What was wrong, in one example

`examples/journal`'s entity, unchanged by this slice:

```go
type Post struct {
	ID        string    `entity:"id" json:"id"`
	Owner     string    `json:"owner"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`   // author-supplied markdown
	Published bool      `entity:"index" json:"published"`
	Created   time.Time `entity:"index" json:"created"`
}
```

Two fields are declared queryable. Before FL20 all six went up as
indexed Datastore properties, because the tags could not reach the write
path — so **journal could not have stored a post longer than about 250
words once deployed**, and the error would have named an index nobody
wrote. It passed every local test, because the local backend scans and
indexes nothing.

That is the shape of the bug: invisible on a laptop, fatal in the cloud,
and blamed on the app.

## 2. The contract, both backends

```
go test ./pkg/backends/conformance -backend=local -run Entity -count=1 -v
```

```
--- PASS: TestEntity/an_unindexed_field_holds_more_than_an_index_could
--- PASS: TestEntity/a_declared_field_is_still_queryable_after_a_write
--- PASS: TestEntity/an_indexed_value_past_the_cap_is_refused_the_same_way_everywhere
```

The third one is a **behaviour change to `local`**, and deliberate.
Nothing local does requires it — it never indexes — but without it an
app writes a 2 KB indexed title happily on a laptop and fails on the
first deploy. A limit visible only in production is one the conformance
suite was supposed to have made visible here (decision 9). Both
backends now refuse it with the same sentence, from one
`backends.IndexedTooLong`.

## 3. The proof that had to be at the wire

Conformance states the contract but can only prove it against a live
project, and against `local` these cases passed **before and after** the
fix. The defect lived in exactly one place — the properties the store
hands Datastore — so that is where it is pinned:

```
go test ./pkg/backends/gcp -run 'Index|Declared|Cap' -count=1 -v
```

```
--- PASS: TestOnlyDeclaredFieldsGoUpIndexed
--- PASS: TestAnEntityWithNoDeclaredFieldsIndexesNothing
--- PASS: TestAnIndexedValuePastTheCapIsRefusedBeforeItIsSent
--- PASS: TestAnUndeclaredValuePastTheCapIsFine
```

**Expect** an undeclared field to go up present but `excludeFromIndexes`
— stored, readable, not in an index, no cap. Excluded is not absent:
reads are unchanged.

## 4. Both mutations, because the two directions fail differently

Over-indexing fails the WRITE, loudly. Under-indexing fails the READ,
**silently** — a Datastore filter on an excluded property matches
nothing and reports no error. A fix that traded one for the other would
look like progress.

Restoring the pre-FL20 behaviour (index every mirrored scalar):

```
--- FAIL: TestOnlyDeclaredFieldsGoUpIndexed
    body is not declared entity:"index" and went up INDEXED — this is the
    defect: a 5 KB body is refused for an index nobody asked for
--- FAIL: TestAnEntityWithNoDeclaredFieldsIndexesNothing
    note went up indexed for an entity that declares no queryable field
```

Excluding everything instead (index nothing):

```
--- FAIL: TestOnlyDeclaredFieldsGoUpIndexed
    slug is declared entity:"index" and went up EXCLUDED — a filter on it
    would match nothing, and Datastore reports no error for that
--- FAIL: TestAnIndexedValuePastTheCapIsRefusedBeforeItIsSent
    an indexed value past the cap must be refused
```

## 5. The framework's own kinds

`pkg/runtime/session.go` writes sessions through the same contract, and
`eachSession` looks them up **by uid** — which is what sign-out,
revocation and role stale-marking are built on. Declaring the wrong
field there would not error; revocation would quietly stop revoking, and
no test would catch it, because `local` answers the query whatever was
declared.

So there are not two names to keep in agreement:

```go
const sessionUIDField = "uid"

var sessionIndexed = []string{sessionUIDField}
// eachSession: Filters: []backends.Filter{{Field: sessionUIDField, ...}}
```

**Expect** `go test ./pkg/runtime -count=1` green, including
`TestARoleChangeLandsOnTheNextRequest` and `TestDisablingEndsTheSession`.

## 6. Nothing an app writes changes

```
go test ./... && for d in examples/*/; do (cd $d && tako test -count=1 ./...); done
```

**Expect** the root suite and all seven examples green, with **no edit
to any example**. The tags were already right; they simply did nothing
on the way in. `entity.go`'s `indexedFields()` is the one place an app's
schema now reaches a write, fed by the handle's cached schema — so an
app cannot pass the wrong set, because it never passes one.

## 7. What is NOT covered here

The live path. These cases prove the payload tako sends; only a deploy
against a real project proves Datastore accepts it. `-backend=gcp`
conformance against a live project is the remaining check, and it is
the one flauta's next deploy will make for us.
