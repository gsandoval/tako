# 5 · shop — many services, one repo

[examples/shop](../../examples/shop/app.go) is the "website with an
API" endgame: four deployables from one package tree, a generated
frontend client, and background compute.

## The generated TypeScript client

```
tako gen client ts --out web/src/api.ts
```

One deterministic, zero-dependency file: a typed method per api
endpoint, interfaces mirrored from the Go signatures, `ApiError`
carrying the server's status and message, session cookie and CSRF
pre-wired. Shop COMMITS the client (a test pins freshness — drift
fails the suite) so frontend builds never need tako installed. The
frontend is typed methods and `instanceof ApiError`, zero hand-written
fetch. Private endpoints are EXCLUDED with the reason printed.

## Services by mkdir (§ typed calls)

```go
// billing/billing.go — a new package, therefore a new service
var Charge = tako.API("POST /charge", charge, tako.WithPrivate())

// shop/app.go
receipt, err := billing.Charge.Call(ctx, billing.ChargeReq{...})
```

`WithPrivate` closes the front door: browsers and curl get 403 from
Cloud Run itself; the caller's service account gets `run.invoker`
derived from the call edge. In dev and tests, `.Call` is a direct
function call (breakpoints step from `placeOrder` into `charge`); in
the cloud it's an OIDC-authenticated hop that also carries the
deadline budget, the trace id, and **the caller's session** — billing
sees the same verified, revocable user, and a worker's anonymity
stays honest. Typed errors cross verbatim: billing's 422 is your 422.
Calls never silently retry, and the deploy prices sync hops honestly
(cold-start and chain-depth warnings).

## Jobs — compute that outlives requests

```go
var settle = tako.Job[SettleParams]("settle")
var Settle = tako.JobHandler(settle, runSettle)

settle.Run(ctx, SettleParams{Day: day})   // hands off, returns now
```

In the cloud that's a Cloud Run JOB — no request timeout exists; a
six-minute settlement just finishes. Locally executions ride the
queue dispatcher, so `DrainQueues` waits for them in tests.

### Sharding (T4)

`Settle` declares `tako.WithParallel(4)`: four tasks per execution,
each asking `ctx.Task()` for its `(index, count)` and taking the
orders whose ID hashes to its shard. Each task writes an idempotent
per-shard subtotal; every task then attempts the merge, and whoever
finds all four present writes the identical final report — no
leader, no lock, retry-safe.

## Singleton — exactly one, everywhere

```go
var Invoicer = tako.Singleton(assignInvoices)
```

Bookkeeping-grade invoice numbers are strictly sequential — genuinely
SERIAL work. The singleton runs as a min=1/max=1 service whose loop
executes only while holding a lease, so even mid-deploy (two
revisions briefly live) exactly one instance sweeps. Un-invoiced
orders converge to numbered ones, in arrival order, and the logs say
so.

## Run it

```
cd examples/shop
PORT=8090 tako dev    # sign in, stock the shelf, buy — watch the
                      # receipt (billing), the invoice number
                      # (singleton), and trigger /api/settle (job)
tako test
```

Deployed: `run job shop-settle`, an IAM-only billing with its caller
listed, the call graph printed, and every grant derived. The full
walk — including revoking a grant and watching one deploy heal it —
is [validate/shop.md](../../validate/shop.md).
