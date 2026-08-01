# singleton.md — `tako.Singleton`

An exactly-one loop, everywhere: one instance runs the body across all
instances and all revisions, with clean handoff between deploys. The
machinery is min=1/max=1 **plus** the §4.4 lease — the loop executes
only while holding the lease, so even mid-deploy (two revisions briefly
live) exactly one instance works. min=1/max=1 alone is NOT the
guarantee; the lease is.

Use it for genuinely SERIAL work. Shop's invoicer assigns
bookkeeping-grade invoice numbers — strictly sequential, no gaps, no
duplicates — which is what makes it a Singleton and not a worker.

## Declare

From [examples/shop](../../examples/shop/app.go):

```go
Invoicer = tako.Singleton("invoicer", assignInvoices)
```

The name must be a string constant (analyzer-enforced). `Singleton`
accepts exactly one option, `tako.WithFootprint`, and only its CPU and
Memory fields — the scaling knobs (min=1/max=1/concurrency=1) are the
exactly-one machinery and are framework-owned.

## Use

The body is `func(Ctx) error` — a loop that watches `ctx.Done()`
(a lost lease cancels it) and returns to be restarted:

```go
// assignInvoices is the singleton loop: sweep un-invoiced orders into
// strictly sequential numbers, forever, exactly once across every
// instance and revision (the lease is the guarantee).
func assignInvoices(ctx tako.Ctx) error {
	for {
		if _, err := invoiceSweep(ctx); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(2 * time.Second):
		}
	}
}
```

Returning — error or not — is a restart with a 5s backoff, narrated:
`♾ singleton invoicer returned — restarting in 5s`.

## Local

Under `tako dev` the loop is a goroutine in the app process — the only
instance — and the in-process lease still guards against doubled
registration. The loop is invisible by design when idle; a productive
sweep logs, and that line in the dev console IS the singleton working:
`invoicer: assigned invoice numbers orders=3 through=3`.

## Cloud

`tako deploy` creates a dedicated service (shop: `shop-invoicer`) with
the singleton knobs — CPU 1, 512Mi, concurrency 1, **min 1 / max 1** —
that admits nobody's traffic but health checks. Its generated main runs
`RunSingletonLoop`: campaign forever for the `singleton-<name>` lease
(an object in the framework bucket, the same §4.4 mechanism the deploy
lease uses), run the body while holding it, restart with the 5s backoff
when it returns, resume campaigning when the lease is lost. Handoff
needs no cleanup — a crashed holder's lease TTL (30s) expires and the
next campaigner takes over.

Cost-honest: min=1 means one instance always on, billing continuously
whether there is work or not. That standing instance is the price of
exactly-one.

## Test it

Factor one iteration out of the loop and test it directly — shop's
`invoiceSweep` pattern ([api_test.go](../../examples/shop/api_test.go),
`TestInvoiceNumbersAreStrictlySequential`):

```go
ctx := runtime.NewCtx(t.Context())
n, err := invoiceSweep(ctx)
if err != nil || n != 3 {
	t.Fatalf("sweep = %d, %v", n, err)
}
```

A second sweep finds nothing: numbering is idempotent and the sequence
never rewinds.

## Errors you'll meet

- Any option other than `WithFootprint`:
  `tako.Singleton accepts only WithFootprint — min=1/max=1 plus the
  lease is the rest of the contract (tako/invalid-option)`
- A footprint setting the scale-out:
  `a singleton footprint cannot set MaxInstances — the lease is what
  makes a singleton exactly-one, so a max above 1 only buys instances
  that pay to lose the race; CPU, Memory and StartupBoost are yours
  (tako/singleton-max)`
- A footprint touching the other scaling fields:
  `a singleton footprint sets CPU, Memory and StartupBoost only — min=1
  and concurrency=1 are what keep the loop's host alive and alone
  (tako/footprint)`
- A singleton traffic claim beyond the timeout:
  `a singleton's only traffic setting is Timeout — exactly one instance
  serving nothing but its own health check has no clients to keep
  affinity with or negotiate HTTP/2 over (tako/traffic)`
- A non-constant name: `tako.Singleton name must be a string constant —
  names are read at analysis time, not run time; got …` (tako/const-name)

## Not yet

- No leadership API — you cannot ask "which instance holds the lease"
  from code; the logs and the lease object in the framework bucket are
  the observability.
- The lease TTL (30s) and the 5s restart backoff are framework-owned,
  not knobs.
- Compute is CPU/Memory only; everything else about the service shape
  is fixed by design.
