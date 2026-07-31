# validate/footprint.md — S1: WithFootprint, the explicit-compute escape hatch

The claim: a user can pin exactly the compute values they mean —
field-by-field over a profile — and the pins are validated at BUILD
time, visible in every summary, honored LIVE on Cloud Run, and priced
in the deploy output when they cost standing money.

**Setup:** run the CLI as `go run ../../cmd/tako …` from
examples/gallery. Cloud steps need a test project with the usual APIs
enabled and the gallery secret present
(`printf dev-key | gcloud secrets create notifier-key --project <test-project> --data-file=-`).

## 1. The declaration reads as intent

examples/gallery/app.go — the resizer:

```go
MakeThumb = tako.Worker(thumbs, makeThumb,
    tako.WithProfile(tako.Batch),
    tako.WithFootprint(tako.Footprint{Memory: "4Gi", Concurrency: 4, MaxInstances: 5}))
```

**Judge:** the profile carries the intent (batch work), the footprint
pins only what the profile can't know (decode memory, per-instance
pressure, fan-out cap). CPU and min-instances are NOT pinned — they
inherit. That layering is the whole design.

## 2. Summaries carry the claim

```
cd examples/gallery && go run ../../cmd/tako gen graph
```

**Judge:** the service line reads
`service gallery-makethumb (package gallery, profile batch + footprint (memory 4Gi, concurrency 4, max 5))`
— profile and footprint, side by side, nothing hidden in JSON.

## 3. The analyzer refuses what Cloud Run would refuse (errors are product)

Break it on purpose, read the texts, put it back:

- `CPU: 3` → `footprint CPU must be 1, 2, 4, or 8 vCPUs; got 3`
- `Memory: "1GB"` → the Mi/Gi form and the 128Mi–32Gi range, named
- `CPU: 8` alone → the RESOLVED judgment:
  `resolves to 8 vCPUs with 2Gi memory — 8 vCPUs need at least 4Gi; pin Memory too`
  (the base comes from the batch profile — the analyzer checked the
  COMBINATION, not the literal)
- a second, different footprint on another handler in the same
  package → `tako/mixed-footprint` names both claimants
- `Concurrency: 10` on a JobHandler or `MinInstances: 2` on a
  Singleton → the honest scoping texts (tasks aren't serving
  instances; min=1/max=1 is the exactly-one machinery)

All golden-tested: `go test ./pkg/analyzer -run TestErrors/footprint`.

## 4. Cloud: pinned values land, inherited values come from the profile

```
go run ../../cmd/tako deploy --project <test-project>
```

**Judge the summary:** gallery-makethumb's block carries
`compute: 2 cpu / 4Gi / concurrency 4 / instances 0–5 (profile batch + footprint …)`
— 2 cpu and min 0 INHERITED from batch, the three pins yours.

Then the live service:

```
gcloud run services describe gallery-makethumb --project <test-project> \
  --region us-central1 --format json | python3 -c '
import json,sys; t=json.load(sys.stdin)["spec"]["template"]
c=t["spec"]["containers"][0]["resources"]["limits"]
print(c, t["spec"]["containerConcurrency"], t["metadata"]["annotations"].get("autoscaling.knative.dev/maxScale"))'
```

→ `{'cpu': '2', 'memory': '4Gi'} 4 5`. A second
`deploy --plan` shows no drift (the footprint is part of the spec
hash — changing a value plans `~ update run service`).

## 5. Standing money is priced, not absorbed

In a scratch module (or by temporarily adding `MinInstances: 2` to
any footprint), run `deploy --plan`:

```
⚠ footprint on <service> keeps 2 instance(s) always on — they bill continuously whether traffic comes or not
```

(It prints with the PLAN — the decision point — not after apply.)

**Judge:** the escape hatch never silently buys always-on instances.

```
go run ../../cmd/tako destroy --project <test-project>
```

Clean project; delete the secret when done
(`gcloud secrets delete notifier-key --project <test-project> --quiet`).
Record the verdict in docs/validation-log.md.
