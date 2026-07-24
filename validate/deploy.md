# validate/deploy.md — Gate C2: Minimal reconciler + hello deploys

What this gate approves: **the first full cycle** — clean project → live
URL → destroyed project — timed (target <~2 min to URL), with the
`--plan` output reviewed line by line. Prereqs: the dedicated test
project set up per docs/tako-cloud-permissions.md (APIs enabled, deployer
bundle granted), `gcloud auth application-default login`.

## 1. Preflight honesty (before touching anything)

```
cd examples/hello
tako deploy --plan --project <your-project>
```

If setup is incomplete, **judge the gaps**: every missing API/role is
one line with the exact `gcloud` command that fixes it — run them and
re-try until preflight passes. (Also worth trying: a typo'd project id →
`project "…" not found — check the id`.)

## 2. The plan, line by line (nothing applied)

With preflight green, the same `--plan` prints:

```
  plan
    create  artifact repo tako
    create  service account tako-hello
    create  run service hello
    3 creates, 0 updates, 0 skips, 0 orphans
```

**Judge:** is this everything you'd expect a hello deploy to touch, and
nothing more? `--plan` must have created NOTHING (check the console).

## 3. The full cycle — START YOUR TIMER

```
time tako deploy --project <your-project>
curl $(URL from the ✓ line)/hello
```

**Observe:** lease acquired (with your identity), ⛅ lines in reconcile
order, `✓ hello → https://…run.app` and the elapsed time (<~2 min
target), then the security summary honestly reporting *no grants* —
hello declares no resources. The curl answers the same as `tako dev`.

## 4. Idempotence, spec-hash, drift

```
tako deploy --project <your-project>          # → 3 skips, "✓ no changes" in seconds
```

Now hand-edit in the console (e.g. set max instances to 3 on the hello
service), then:

```
tako deploy --plan --project <your-project>
```

**Observe:** the run service line carries
`← DRIFT: modified outside tako; tako-owned fields will be overwritten`.
Deploy (no --plan) to heal it; re-deploy → skips again.

## 5. Logs and lease

```
tako logs --project <your-project> --since 30m
```

**Observe:** your curl's request line(s) from the deployed service. For
the lease: start `tako deploy` in one terminal and immediately in a
second — the second prints `deploy in progress by <you> since <when> —
waiting…` and proceeds when the first finishes.

## 6. Destroy — the project comes back clean

```
tako destroy --project <your-project>
```

**Observe:** the typed-confirmation prompt listing exactly the labeled
objects; after ✓ destroyed, verify in the console: no run services, no
tako SAs, no tako repo, no framework bucket. A clean project is the
stateless-reconcile claim made good.

## 7. Everything still green

```
cd ../.. && go test ./...
```

(Reconciler scenarios — all-creates, all-skips, digest-update, drift,
orphaned-kept, env prefixes — and the error-taxonomy tests run
cloud-free in `pkg/provision` and `pkg/provision/gcp`.)

Record the verdict in `docs/validation-log.md` (feature C2, gate C2).
