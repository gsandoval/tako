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

These are ANALYSIS-time errors, so nothing is deployed and nothing is
billed. Two ways to see them, and you want both:

**The fast way — they are all golden-tested**, texts asserted verbatim,
no editing:

```
go test ./pkg/analyzer -run 'TestErrors/footprint|TestErrors/serviceoption|TestErrors/trafficopt|TestErrors/mixedfootprint'
```

**The real way — break gallery and read what a user reads.** Every case
below is a one-line edit to `examples/gallery/app.go`, surfaced by:

```
go run ../../cmd/tako gen graph
```

which analyzes and prints. `git checkout examples/gallery/app.go`
between cases.

### 3a. A field Cloud Run does not offer

Line 80, change the footprint to `tako.Footprint{Memory: "4Gi", CPU: 3, Concurrency: 4, MaxInstances: 5}`:

```
app.go:80:22: footprint CPU must be 1, 2, 4, or 8 vCPUs; got 3 (tako/footprint)
```

Or `tako.Footprint{Memory: "1GB"}`:

```
app.go:80:22: footprint Memory must be like "512Mi" or "4Gi", between 128Mi and 32Gi; got "1GB" (tako/footprint)
```

### 3b. The RESOLVED combination, not the literal

Replace the whole footprint with `tako.Footprint{CPU: 8}`:

```
app.go:78:2: service gallery-makethumb resolves to 8 vCPUs with 2Gi memory — 8 vCPUs need at least 4Gi; pin Memory too (tako/footprint)
```

**This is the one to dwell on.** Nothing in the declaration says 2Gi —
that came from the batch profile. The analyzer checked the profile and
the override TOGETHER, which is what stops a footprint pinning one
field from silently pairing it with an inherited value Cloud Run will
not accept. Note it points at line 78, the handler, not at the field.

### 3c. Traffic's own refusals (V6)

Add an option to `NotifyOwner` on line 81:

```go
NotifyOwner = tako.Subscribe(ready, notifyOwner, tako.WithTraffic(tako.Traffic{}))
```

```
app.go:81:68: traffic sets nothing — drop tako.WithTraffic or pin at least one field (tako/traffic)
```

Then `tako.Traffic{Timeout: 500 * time.Millisecond}` (add `"time"` to
the imports) →
`traffic Timeout must be at least one second; got 500ms`, the
nanosecond trap `Footprint` used to guard.

The scoping twins are easier to read in the fixture than to build here
(`pkg/analyzer/testdata/errors/trafficopt/`) — a job's and a
singleton's only traffic setting is `Timeout`, because an execution
serves nobody and a singleton is one instance answering its own health
check.

### 3d. Two claims, one service

Put a second cron in gallery with a different footprint from
`Cleanup`'s. **Crons are how this collision is still possible at all:**
they run in their package's app service, whereas each worker,
subscriber, job and singleton owns its own service and `tako.Service`
is one per package.

```
service <pkg> cannot carry both footprint (cpu 2) from Sweep and footprint (cpu 4, memory 2Gi) from Purge — one service scales one way; split the packages or align the footprints (tako/mixed-footprint)
```

### 3e. The migration signal

`tako.Footprint{Timeout: 30 * time.Second}` no longer compiles:

```
unknown field Timeout in struct literal of type tako.Footprint
```

That compiler error IS the upgrade instruction — the field moved to
`Traffic`, and nothing can quietly keep setting it in the old place.

**Judge:** every text names the field, the value it got, the range or
rule it broke, and where to look. None of them says "invalid
configuration".

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
