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

What the FINAL USER does is one command when their api changes:

```
tako gen client ts --out web/src/api.ts
```

— then imports it like any source file; their own frontend toolchain
(vite, tsc, esbuild) typechecks it as part of their normal build.
Nothing below is part of their workflow — it is THIS GATE proving the
promises behind that one command:

```
go run ../../cmd/tako gen client ts
diff .tako/gen/client/ts/api.ts web/src/api.ts
npx --yes -p typescript tsc --noEmit --strict --target es2020 --lib es2020,dom web/src/api.ts web/src/app.ts
```

**Judge:** the `diff` proves determinism — a committed client is
byte-identical to a fresh generation (shop also pins this with a
test, the recommended pattern so frontend builds never need tako
installed); the `npx tsc --strict` stands in for the user's own
toolchain and proves the emitted types hold up under the strictest
settings, including app.ts — the real consumer. Also judge: the
client EXCLUDES billing.Charge with its reason ("private — browsers
cannot mint OIDC"), and app.ts reads as typed methods +
`instanceof ApiError`, no hand-written fetch anywhere.

## 3. Dev: the whole story on one port

```
PORT=8090 go run ../../cmd/tako dev     # then open http://localhost:8090
```

- FIRST: a fresh checkout (or a deleted .tako/) is an EMPTY database
  — .tako/data IS the local store, and deleting it is the documented
  reset story. So stock the shelf: sign in (dev bar or the header
  link — the bar and the header both reflect the session, and the
  bar's controls follow it: "sign in as …" when anonymous, "switch
  to …" + "sign out" when signed in) and use the "stock the shelf"
  form. That form is addProduct through the generated client.
  Buy something: the order
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
  curl -s -c /tmp/dev.jar -o /dev/null -d dev_user=alice localhost:8090/_tako/api/session
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
go run ../../cmd/tako deploy --project <test-project>
```

(No `--yes`: that flag only pre-answers the destructive-plan prompt —
replaces and rename suspects — for CI, where nobody can type `y`. A
fresh deploy is all creates, so no prompt fires; if one ever DOES,
that is the safety machinery worth reading, not skipping.)

**Known transient on a FRESH project/repo:** the first service create
can fail with `Image '…' not found` moments after the images pushed —
Artifact Registry is eventually consistent right after a just-created
repo's first writes. Re-run the deploy: reconciliation is stateless,
so the repo and images skip and the walk continues where it stopped.

**Judge the plan + summary:** `run job shop-settle` (a JOB kind, not
a service); `iam grants runjob shop-settle` carrying run.developer
(runWithOverrides — the params channel — lives there, not in
run.invoker; found live); billing "ingress: IAM-only (callers:
shop-sa)"; the call graph line; the ⚠ pricing the sync hop into
scale-to-zero billing.

### Authenticating to the deployed app (complete curl recipe)

The cloud has no dev bar — sign-in is the REAL email/password
provider. Everything below is copy-pasteable; run it once and every
later curl just uses the cookie jar.

```
# 1. The shop URL from the deploy's ✓ line, and the browser API key
#    the deploy injected into the service:
B=https://<shop-url-from-the-✓-line>
APIKEY=$(gcloud run services describe shop --project <test-project>   --region us-central1 --format json | python3 -c 'import json,sys; env=json.load(sys.stdin)["spec"]["template"]["spec"]["containers"][0]["env"]; print([e["value"] for e in env if e["name"]=="TAKO_AUTH_APIKEY"][0])')

# 2. Create a throwaway email user (signUp also returns a fresh ID
#    token). Re-running later: swap signUp for signInWithPassword,
#    same body, same response shape.
curl -s "https://identitytoolkit.googleapis.com/v1/accounts:signUp?key=$APIKEY"   -H 'Content-Type: application/json'   -d '{"email":"walker@example.com","password":"<pick-one>","returnSecureToken":true}'   > /tmp/idp.json
IDTOKEN=$(python3 -c 'import json; print(json.load(open("/tmp/idp.json"))["idToken"])')

# 3. Exchange the ID token for a tako session — the Set-Cookie lands
#    in the jar, and every following curl sends it with -b:
curl -s -c /tmp/shop.jar -o /dev/null -X POST $B/_tako/api/session   -H 'Content-Type: application/json' -d "{\"idToken\":\"$IDTOKEN\"}"
curl -s -b /tmp/shop.jar $B/api/me     # → {"signedIn":true,...} proves it
```

(Browser alternative: once the user EXISTS — step 2 — the header's
"sign in" link opens the framework's email/password form and sets the
same cookie; the page then works signed in, stock-the-shelf included.
The form signs IN only; creating the account is step 2's job.)

Now the walk, all with `-b /tmp/shop.jar`:

```
# stock the shelf, buy, see the order + receipt + (soon) invoiceNo:
curl -s -b /tmp/shop.jar -X POST $B/api/products -H 'Content-Type: application/json'   -d '{"name":"gate tea","priceCents":400}'
curl -s -b /tmp/shop.jar -X POST $B/api/orders -H 'Content-Type: application/json'   -d '{"productId":"<id-from-above>","qty":2}'
curl -s -b /tmp/shop.jar $B/api/orders
```

- **Private front door:** `curl -X POST <billing-url>/charge` (the
  billing URL is its own ✓ line) → 403 from Google, not tako. Then
  the operator probe — your own identity token plus the session ID
  read out of the jar:

  ```
  SID=$(awk '$6=="_session" {print $7}' /tmp/shop.jar)
  curl -s -X POST <billing-url>/charge \
    -H "Authorization: Bearer $(gcloud auth print-identity-token)" \
    -H "X-Tako-Session: $SID" -H 'Content-Type: application/json' \
    -d '{"orderId":"probe","amountCents":123}'
  ```

  → 200, and `chargedBy` is the session's uid — operator access and
  user propagation in one probe.
- **The revoke-grant judgment (W9's criterion):**
  `gcloud run services remove-iam-policy-binding billing --member serviceAccount:shop-sa@… --role roles/run.invoker`
  → orders fail (~1–2 min) with the EXACT text naming the missing
  grant → `tako deploy` plans precisely `~ update iam grants run
  billing` → orders heal.
- **The job outlives any request deadline (W10's criterion):** place
  ~6 orders, then

  ```
  curl -s -b /tmp/shop.jar -X POST $B/api/settle \
    -H 'Content-Type: application/json' \
    -d '{"day":"<today>","throttleMs":60000}'
  ```

  — the
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
JOB deletes with everything else; typed name; clean project. The
walker account survives by design (destroy never touches Identity
Platform users); delete it when done:

```
LOCALID=$(python3 -c 'import json; print(json.load(open("/tmp/idp.json"))["localId"])')
curl -s -X POST "https://identitytoolkit.googleapis.com/v1/projects/<test-project>/accounts:delete" \
  -H "Authorization: Bearer $(gcloud auth application-default print-access-token)" \
  -H "x-goog-user-project: <test-project>" \
  -H 'Content-Type: application/json' -d "{\"localId\":\"$LOCALID\"}"
```

Record ONE verdict in docs/validation-log.md — the row closes W7,
W8, W9, and W10, and with them STAGE 3. What follows is Stage 4's
decomposition (scheduler, custom domains, the operator era).
