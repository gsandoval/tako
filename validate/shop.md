# validate/shop.md — W11 MILESTONE: shop, complete (closes W7–W10)

The third and final Stage 3 gate: the multi-service showcase whole —
a generated TypeScript client driving a JSON api, a PRIVATE billing
service reached by typed calls over OIDC with the user propagated, a
Cloud Run Job that outlives request deadlines, and a lease-guarded
singleton assigning bookkeeping-grade invoice numbers. One script,
local and cloud. **Stage 3 closes on this verdict.**

**Setup:** run the CLI as `go run ../../cmd/tako …` from
examples/shop. Dev pins PORT=8090. Node is needed for §2 (esbuild via
npx) and §6's client run — never by go test. The email/password
provider from W6 serves sign-in; no browser OAuth is required
anywhere in this walk.

## 1. The whole app, at a glance

```
cd examples/shop && go run ../../cmd/tako gen graph
```

**Judge:** FOUR deployables from ONE package tree (mkdir made
billing, not YAML): shop (api + site + auth), billing (private
`POST /charge`), shop-settle (`job Settle ← settle`), shop-invoicer
(`singleton Invoicer — invoicer, exactly one everywhere`). The 🔒s
sit on every write; `entity orders` carries the invoicer's fields.

## 2. The generated client IS the frontend contract

```
go run ../../cmd/tako gen client ts
diff .tako/gen/client/ts/api.ts web/src/api.ts
npx --yes -p typescript tsc --noEmit --strict --target es2020 --lib es2020,dom web/src/api.ts web/src/app.ts
```

**Judge:** the committed client is byte-identical to a fresh
generation (a TEST also pins this — drift fails the suite); it
excludes billing.Charge WITH the reason ("private — browsers cannot
mint OIDC"); tsc --strict is clean including app.ts, the real
consumer. Then read web/src/app.ts: the whole backend conversation is
typed methods and `instanceof ApiError`.

## 3. Dev: the whole story on one port

```
PORT=8090 go run ../../cmd/tako dev     # then open http://localhost:8090
```

- Sign in (dev bar or the header link): the header now SHOWS the
  session — "signed in as alice · sign out" — and switching between
  alice and bob swaps whose orders render. Buy something: the order
  lands with a `receiptId` — billing.Charge ran IN-PROCESS (set a
  breakpoint in billing/billing.go and buy again: the debugger steps
  straight from placeOrder into charge — that is the §2.12 dev
  story).
- The order shows an `invoiceNo` without any action of yours, and
  the dev console prints `invoicer: assigned invoice numbers` — that
  line IS the flow you're judging: the invoicer is a background
  SINGLETON loop (its own min=1 service in the cloud) that sweeps
  un-invoiced orders every 2s into strictly sequential numbers.
  Serial by design (bookkeeping-grade sequences hate contention),
  exactly-one by the lease. The judgment: numbering happens BY
  ITSELF and the numbers are sequential — not that you catch the 2s
  delay.
- The settle trigger, with a concrete session (curl needs the
  `_session` cookie a sign-in sets — a jar is the easy way):

  ```
  curl -s -c /tmp/dev.jar -o /dev/null -d dev_user=alice localhost:8090/api/_session
  curl -s -b /tmp/dev.jar -X POST localhost:8090/api/settle -H 'Content-Type: application/json' -d '{"day":"dev"}'
  ```

  → `{"started":true}` immediately; the ⚙ job line appears in the
  dev console and a `reports` record materializes.

## 4. The suite, the product way

```
go run ../../cmd/tako test
```

**Judge:** green, and read the tests as documentation:
billing_test.go (typed 422s crossing the call, ChargedBy = alice —
the SESSION crossed too, anonymity stays honest), api_test.go's W10
half (a settlement through DrainQueues, invoice numbers strictly
sequential and idempotent).

## 5. Analyzer judgments (the errors are the product)

Break things on purpose, read the texts, put them back:
- Call billing.ChargeFn (the raw function) instead of the Endpoint
  var → `tako/raw-call` names the var to use.
- Add a public route to package billing → `tako/mixed-ingress`
  explains Cloud Run's service-level enforcement.
- Give the Lock an option → `tako/invalid-option` says the lease
  policy is framework-owned.

## 6. Cloud — four deployables, every trust edge live

```
go run ../../cmd/tako deploy --project <test-project> --yes
```

**Judge the plan + summary:** `run job shop-settle` (a JOB kind, not
a service); `iam grants runjob shop-settle` carrying run.developer
(runWithOverrides — the params channel — lives there, not in
run.invoker; found live); billing "ingress: IAM-only (callers:
shop-sa)"; the call graph line; the ⚠ pricing the sync hop into
scale-to-zero billing.

Then, with a session from the email provider (see validate/journal.md
§6 for the token dance, or the dev-bar-less curl transcript in the
task log):

- **Private front door:** `curl -X POST <billing-url>/charge` → 403
  from Google, not tako. With YOUR OWN identity token
  (`gcloud auth print-identity-token`) + an `X-Tako-Session` header:
  200, and `chargedBy` is the session's uid — operator access and
  user propagation in one probe.
- **The revoke-grant judgment (W9's criterion):**
  `gcloud run services remove-iam-policy-binding billing --member serviceAccount:shop-sa@… --role roles/run.invoker`
  → orders fail (~1–2 min) with the EXACT text naming the missing
  grant → `tako deploy` plans precisely `~ update iam grants run
  billing` → orders heal.
- **The job outlives any request deadline (W10's criterion):** place
  ~6 orders, then
  `POST /api/settle {"day":"<today>","throttleMs":60000}` — the
  trigger returns instantly; the execution runs ~6 MINUTES (past the
  300s service request timeout) and Completes
  (`gcloud run jobs executions list --job shop-settle`); the
  `reports/<day>` entity carries the right order count and total.
- **Exactly-one invoicer:** the orders' invoice numbers are strictly
  sequential with no gaps or duplicates — assigned by whichever
  single instance holds the lease (delete the lease object
  `leases/run-shop-prod-singleton-Invoicer` in the framework bucket
  to watch a handoff, if you like).

```
go run ../../cmd/tako destroy --project <test-project>
```

**Judge:** DATA names products, orders, reports, sequences; the run
JOB deletes with everything else; typed name; clean project.

Record ONE verdict in docs/validation-log.md — the row closes W7,
W8, W9, and W10, and with them STAGE 3. What follows is Stage 4's
decomposition (scheduler, custom domains, the operator era).
