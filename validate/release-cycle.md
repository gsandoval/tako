# validate/release-cycle.md — the pre-release deploy/destroy cycle

Not a feature gate. This is the short walk that proves the release
pass's two riskiest changes reach real infrastructure together:

- **X1** renamed the cron's identity. The deployed name comes from the
  declaring VARIABLE now, so Cloud Scheduler must target
  `/_tako/cron/Cleanup` on a job named `<service>-cron-cleanup`, and the
  handler must actually run when it fires.
- **P2** rewrote every URL in `pkg/provision/gcp` and
  `pkg/backends/gcp` through `Client.url`. 130 call sites moved. The
  unit suite and the new fake-server tests cover them, but nothing had
  watched them provision anything real.

Everything else in the release pass is either internal (the replay
ordering fix, P8/P9) or already gated (P1).

**Setup:** `export PROJECT=<scratch-project>` and run from
`examples/gallery`. The env prefix keeps this disjoint from anything
else in the project — every resource is named `x1-*`, and destroy is
scoped to it.

## 1. Deploy

gallery declares a secret, and every env gets its own — so preflight
will stop for `x1-notifier-key` before anything is built. Create it
first (any value; nothing reads it in this walk):

```
export PROJECT=tako-test-01
cd examples/gallery
printf 'nk_x1_release_check' | gcloud secrets create x1-notifier-key --data-file=- --project $PROJECT
go run ../../cmd/tako deploy --project $PROJECT --env x1 --yes
```

Delete it at the end: destroy never touches secrets, by design — they
are data you own.

**Observe:** preflight passes, the plan classifies, and every service
reports a URL. Five services: `x1-gallery`, `x1-gallery-makethumb`,
`x1-gallery-notifyowner`, `x1-gallery-onuploaded`,
`x1-gallery-warmfeed`.

A failure here is a P2 regression — a URL built wrong reaches the API
as a 404 or a malformed-path HTML page.

## 2. X1: the cron carries its variable's name

```
gcloud scheduler jobs list --project $PROJECT --location us-central1 \
  --format='value(name,httpTarget.uri,schedule)'
```

**Observe** — the job name ends `-cron-cleanup` and the URI ends
`/_tako/cron/Cleanup`, from `var Cleanup = tako.Cron("every 24h",
cleanupOrphans, …)`:

```
…/jobs/x1-gallery-cron-cleanup    https://x1-gallery-….run.app/_tako/cron/Cleanup    0 0 * * *
```

**The failure this is looking for:** `cleanupOrphans` anywhere in either
column. That is the function name, and X1 removed it as an identity — if
it appears, the analyzer and the SDK have drifted apart again.

## 3. The cron actually runs under that name

Names agreeing is not the same as the route resolving. Fire it:

```
gcloud scheduler jobs run x1-gallery-cron-cleanup \
  --project $PROJECT --location us-central1
sleep 15
go run ../../cmd/tako logs gallery --project $PROJECT --env x1 --since 5m
```

**Observe:** a `POST /_tako/cron/Cleanup → 200` request line. A 404
means the Scheduler job and the service disagree about the cron's name —
the exact bug X1 existed to make impossible.

**A 403 first is likely but not certain.** The Scheduler job's invoker
grant takes a minute or two to propagate through Cloud Run's front door:
the first two 2026-08-02 runs both hit it, the X2 run did not and
succeeded on the first fire. Treat it as a window to ride out, not a
step to expect — loop until the 200 arrives:

```
until gcloud scheduler jobs run x1-gallery-cron-cleanup \
    --project $PROJECT --location us-central1 >/dev/null 2>&1 &&
  go run ../../cmd/tako logs gallery --project $PROJECT --env x1 --since 2m | grep -q 'cron ok'
do sleep 15; done
```

## 4. P2: the paths that only run against the real API

Serve a request and upload, which exercises the run, storage, pubsub
and datastore adapters:

```
URL=$(gcloud run services describe x1-gallery --project $PROJECT \
  --region us-central1 --format 'value(status.url)')
curl -s -o /dev/null -w "%{http_code}\n" "$URL/api/photos"
```

→ `200`.

Then confirm the provisioner's own reads work, since destroy depends on
them:

```
go run ../../cmd/tako deploy --project $PROJECT --env x1 --plan
```

**Observe:** converged — no changes planned. A re-plan that wants to
recreate something means an observation path builds a URL the API
answers differently from the creation path.

## 5. Destroy, and confirm nothing is left

```
go run ../../cmd/tako destroy --plan --project $PROJECT --env x1
go run ../../cmd/tako destroy --project $PROJECT --env x1
```

`--plan` lists every object and the DATA block; the real run takes the
typed app name.

**Destroy is convergent, so a transient failure is not a problem** — it
walks the same plan again. Transients on idempotent calls now retry
automatically and narrate it (`↻ DELETE <resource>: HTTP 503 — retrying
in 1s`); a failure that survives the retries still leaves the rest
standing, and re-running the same command finishes it.

```
gcloud secrets delete x1-notifier-key --project $PROJECT --quiet
gcloud run services list --project $PROJECT --format='value(metadata.name)'
gcloud scheduler jobs list --project $PROJECT --location us-central1 --format='value(name)'
gcloud pubsub topics list --project $PROJECT --format='value(name)'
gcloud iam service-accounts list --project $PROJECT --format='value(email)'
```

**Observe:** nothing prefixed `x1-` in any of them. An env-scoped
destroy deliberately leaves the framework bucket, which is shared
project machinery, not this app's.

## Verdict

Not a gate — record the outcome in the task log. A failure in §2, §3 or
§4 blocks the release; §1 and §5 failing means something larger.
