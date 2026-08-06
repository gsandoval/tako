# validate: per-route cache policy (FL12)

The claim: a page can say how long shared caches may keep it, nothing
changes for a page that says nothing, and the one combination that
leaks — caching an authenticated page — fails analysis.

The gap this closes is not in the friction log; it is SR-6 in flauta's
backlog, which is worth noting because the friction log is not the
whole picture. `page.go` set `no-cache` on every rendered page
unconditionally, so a site that publishes twice a week revalidated
every reader's request against the app and the entity store.

## 1. Opt in, per route

`examples/journal`'s permalink takes `tako.Route{MaxAge: 5 * time.Minute}`;
its home page takes nothing.

```
cd examples/journal
go run ../../cmd/tako dev
curl -s -i localhost:8080/2026/<slug> | grep -i cache-control
curl -s -i localhost:8080/          | grep -i cache-control
```

Expect:

```
Cache-Control: public, max-age=300
Cache-Control: no-cache
```

`public` is the point — a private max-age would only help the browser
that already has the page.

## 2. The refusal

```
go test ./pkg/analyzer/ -run TestErrors/cachesignin -v
```

Two shapes, exact text:

```
app.go:17:10: Dashboard sets MaxAge on a route that requires sign-in — a shared cache would serve one signed-in visitor's page to another; drop MaxAge, or drop SignIn (tako/cache-signin)
app.go:26:60: tako.Route.MaxAge takes a constant between 1s and a year — it is baked into the Cache-Control header at build time, not decided per request (tako/max-age)
```

The first is the one that matters. Nothing about that failure is
visible from the app: tests pass, the page renders, and one day a
reader sees somebody else's page.

## 3. Nothing else moved

```
go test ./...
```

Every other example still answers `no-cache`, because zero means what
it always meant.
