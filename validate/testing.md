# validate/testing.md — Gate F7: Testing as a feature (local only)

What this gate approves: that the USER's testing story protects them —
above all, watching a deliberately non-idempotent worker get caught by
chaos mode with a seed that reproduces the failure exactly.

## 1. The suite reads like the spec promised

Open `examples/shortlink/app_test.go`. **Judge:** no runtime plumbing,
no httptest, no boot helpers — `tako.NewTestApp(t)`, `SignIn`, `Call`,
`DrainQueues`. Then run it the product way:

```
cd examples/shortlink
tako test        # (or: go run ../../cmd/tako test)
```

**Observe:** the analyze line, the generating line (with a
`regenerated: … was stale` note if `.tako/gen` had drifted — delete
`.tako` first to see it), and the pass.

## 2. Seed the bug (the centerpiece)

In `app.go`, gut `countClick`'s idempotency claim: replace its body with

```go
	return links.Update(ctx, c.Slug, func(l *Link) { l.Hits++ })
```

Run `go test -run Chaos -count=1 .` **Observe:**

```
--- FAIL: TestClicksCountExactlyOnceUnderChaos
    app_test.go:NN: tako: chaos enabled (seed <N>) — rerun with TAKO_CHAOS_SEED=<N> to reproduce
    app_test.go:NN: hits = 27, want exactly 20
```

(The non-chaos test still PASSES — that's the point: without chaos this
bug ships.)

## 3. The seed reproduces exactly

```
TAKO_CHAOS_SEED=<N> go test -run Chaos -count=1 .   # run it three times
```

**Judge:** the same hits count, every run. Change the seed: a different
(still failing) count. Restore `countClick`; both tests green again —
under any seed.

## 4. Watch chaos with your own eyes

```
rm -rf .tako && PORT=8090 tako dev --chaos
curl -s -X POST localhost:8090/api/links -d '{"slug":"go","url":"https://go.dev","owner":"memo"}'
for i in 1 2 3 4 5 6 7 8; do curl -s localhost:8090/api/links/go > /dev/null; done
curl -s 'localhost:8090/api/links?owner=memo' | jq '.[0].hits'
```

**Observe:** the banner's `☢ chaos: enabled (seed …)` line; ☢ lines
narrating `delivering to CountClick twice` / `again after ack`
interleaved with ⚙ acks — and hits is STILL exactly 8, because the
ledger holds in dev too. Ctrl-C when done.

## 5. Isolation

`TestAppsAreIsolated` (in the suite you already ran) proves two
NewTestApp instances share nothing — contexts carry their app's
backends.

## 6. Everything else still green

```
cd ../.. && go test ./pkg/backends/conformance -backend=local
go test ./... && (cd examples/gallery && go test ./...)
```

Record the verdict in `docs/validation-log.md` (feature F7, gate F7).
