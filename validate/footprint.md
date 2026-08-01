# validate/footprint.md — S1: WithFootprint, the explicit-compute escape hatch

The claim: a user can pin exactly the compute values they mean —
field-by-field over a profile — and the pins are validated at BUILD
time, visible in every summary, honored LIVE on Cloud Run, and priced
in the deploy output when they cost standing money.

**Setup:** run the CLI as `go run ../../cmd/tako …` from
examples/gallery. Cloud steps need a test project with the usual APIs
enabled and the gallery secret present
(`printf dev-key | gcloud secrets create notifier-key --project <test-project> --data-file=-`).

**Running this alongside `validate/dlq-replay.md`?** Both walks deploy
gallery. Deploy once with `--env dlq`, run §4–§6 here against that
environment, then let dlq-replay's §6 do the destroy. One deploy, one
teardown, both verdicts.

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

And `WithTraffic`'s own (V6):

- `tako.Traffic{Timeout: 500 * time.Millisecond}` →
  `traffic Timeout must be at least one second; got 500ms` — the
  nanosecond trap, the same one `Footprint` used to guard
- `tako.Traffic{SessionAffinity: 3}` →
  `traffic SessionAffinity must be tako.Inherit, tako.On, or tako.Off`
- `tako.WithTraffic(tako.Traffic{})` →
  `traffic sets nothing — drop tako.WithTraffic or pin at least one field`
- `WithTraffic` on an `API` or `Page` →
  `tako.WithTraffic describes the whole service, not one route — declare it on tako.Service instead (tako/service-option)`
- `tako.Traffic{SessionAffinity: tako.On}` on a **JobHandler** →
  `a job's only traffic setting is Timeout — an execution serves nobody,
  so there are no clients to keep affinity with and no connections to
  negotiate HTTP/2 over`
- the same on a **Singleton** → its twin, naming the one instance

**And the migration signal itself:** `tako.Footprint{Timeout: …}` no
longer compiles. `unknown field Timeout in struct literal of type
tako.Footprint` is the whole upgrade instruction — the field moved, and
the compiler says where to look.

All golden-tested: `go test ./pkg/analyzer -run 'TestErrors/footprint|TestErrors/serviceoption|TestErrors/trafficopt'`.

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

## 6. Traffic: the other half of the split (V6)

`Footprint` sizes the machine; `Traffic` says how requests reach it.
Confirm both land, and that they land independently.

Read the **v2** API — `gcloud run services describe` renders the v1
view, where these appear as annotations if at all:

```
knobs() {
  curl -s -H "Authorization: Bearer $(gcloud auth print-access-token)" \
    "https://run.googleapis.com/v2/projects/$PROJECT/locations/us-central1/services/$1" \
  | python3 -c "
import json,sys
d=json.load(sys.stdin); t=d['template']; c=t['containers'][0]
print('  resources      :', json.dumps(c.get('resources')))
print('  ports          :', json.dumps(c.get('ports')))
print('  sessionAffinity:', t.get('sessionAffinity'))
print('  timeout        :', t.get('timeout'))
print('  concurrency    :', t.get('maxInstanceRequestConcurrency'))
"
}
```

### 6a. The profile supplies traffic too

```
knobs dlq-gallery              # profile default
knobs dlq-gallery-makethumb    # profile batch + footprint
```

→ `timeout: 300s` and `timeout: 900s` respectively, with
`sessionAffinity: None` and `ports: [{"name": "http1", …}]` on both.
That difference comes from `TrafficKnobs`, not `ProfileKnobs` — batch
differs from default in how long ONE unit of work may run, and the two
tables now say so separately.

### 6b. An explicit claim, on a service that renames nothing

Put it on a SUBSCRIBER — declaring `tako.Service` would rename the app
service and orphan it mid-walk. In `examples/gallery/app.go`:

```go
NotifyOwner = tako.Subscribe(photoReady, notifyOwner,
	tako.WithTraffic(tako.Traffic{
		Timeout:         45 * time.Second,
		SessionAffinity: tako.On,
		HTTP2:           tako.On,
	}))
```

Redeploy, then `knobs dlq-gallery-notifyowner`:

```
  ports          : [{"name": "h2c", "containerPort": 8080}]
  sessionAffinity: True
  timeout        : 45s
```

Three things, each the wire form of one field:

1. **`timeout: 45s`** — the override beat the profile's 300s.
2. **`sessionAffinity: True`** — no profile turns this on, so its only
   possible source is your declaration.
3. **`ports[0].name: "h2c"`** — the protocol for the last hop, and it
   carries a `containerPort`. A name without a number is refused
   outright (it was, once).

**Judge:** the footprint on that service is untouched — `resources`
still shows the default profile's `cpu 1 / 512Mi`. Setting traffic did
not disturb compute, which is the entire point of the split.

Confirm it speaks HTTP/2 for real, not just that the flag is set:

```
curl -i --http2-prior-knowledge "$(gcloud run services describe dlq-gallery \
  --project $PROJECT --region us-central1 --format 'value(status.url)')/api/photos"
```

→ `HTTP/2 200`.

Revert the edit and redeploy before teardown.

## 7. Teardown

```
go run ../../cmd/tako destroy --plan --project <test-project> --env dlq
go run ../../cmd/tako destroy --project <test-project> --env dlq
```

The `--plan` line is V5: the same block destroy prints, without arming
the prompt.

Clean project; delete the secret when done
(`gcloud secrets delete dlq-notifier-key --project <test-project> --quiet`).
Record the verdict in docs/validation-log.md.
