# validate/stage2-prep.md — Gate C1.5: Stage-1 gap closure + cloud groundwork

What this gate approves: the four items raised before C2 — async service
mains (real, not placeholders), `gen graph` output modes, no silent
analyzer ignores, and the cloud permission model.

## 1. Worker services are real services now

```
cd examples/shortlink
tako build
```

**Observe:** BOTH services build — no more "skipping". Then run the
generated worker service itself and speak its push contract:

```
cat .tako/gen/shortlink-countclick/main.go     # readable: register + push endpoint
PORT=8091 go run ./.tako/gen/shortlink-countclick &
curl -s -o /dev/null -w "%{http_code}\n" -X POST localhost:8091/_tako/push -d '{"id":"c1","slug":"<existing-slug>"}'   # 204 ack
curl -s -o /dev/null -w "%{http_code}\n" -X POST localhost:8091/_tako/push -d '{"id":"c2","slug":"missing"}'          # 500 retry
curl -s -X POST localhost:8091/_tako/push -d 'not json' -w " %{http_code}\n"                                          # 400 permanent
kill %1
```

**Judge:** 204/500/400 is exactly Pub/Sub's ack contract — Phase F adds
the envelope + OIDC on this same endpoint, nothing fake today.
(`go test ./pkg/runtime -run Push` pins the mapping.)

## 2. gen graph modes

```
cd ../gallery
tako gen graph                       # human summary (default)
tako gen graph --json | jq '.services | length'    # → 5, clean JSON
tako gen graph --mermaid             # paste into a mermaid renderer
```

**Judge the diagram:** 5 service subgraphs, resources shaped by family,
binding edges (queue→worker, topic→subscribers, dotted bucket-event).

## 3. No silent ignores

Add `var s = tako.Site("./web")` to any example, run `tako dev`:

```
app.go:N:C: tako.Site is not available yet — the web slice (sites, pages, auth) arrives with Phase G (tako/not-yet)
```

Remove it again. (Golden fixture errors/notyet covers Site + Job texts.)

## 4. The permission model

Read `docs/tako-cloud-permissions.md` — this is a design review, the
gate's judgment call: two layers (simple one-time deployer bundle on a
dedicated project; derived least-privilege runtime SAs), preflight-as-UX,
the four-way error taxonomy, and the no-self-escalation rule. C1's push
errors already implement the taxonomy:

```
cd ../.. && go test ./pkg/build -run TestPushErrorTaxonomy -v
```

401 → gcloud auth login; 403 → the exact add-iam-policy-binding command
with YOUR repo/project/location filled in; 404 → the exact repositories
create command. CLAUDE.md now requires a "Cloud errors & permissions"
section in every C-phase proposal.

## 5. Everything still green

```
go test ./... && go test ./pkg/backends/conformance -backend=local
```

Record the verdict in `docs/validation-log.md` (feature C1.5, gate C1.5).
