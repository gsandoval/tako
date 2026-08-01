# The tako CLI — the command surface and the confirmation ladder

A CLI has no Declare/Use, so this page bends the rubric: the surface
by side-effect tier (exactly as `tako` help groups it), the
confirmation ladder, flag resolution, errors, and honest limits.

## Local — nothing outside this directory is touched

```
tako dev [--chaos]       run the app locally with reload (--chaos: seeded
                         duplicate/reordered deliveries)
tako test [args]         analyze, refresh generated code, then go test ./...
tako gen graph [--json | --mermaid]
tako gen client ts [--out <path>]
tako build [--push <repo>]
```

`tako test` never runs against stale generated code — it regenerates
first and calls out drift (`regenerated: shop was stale`). Bare
`tako gen` prints the gen namespace (one line per generator), not the
full usage.

`build --push` is **the one flag that crosses into the cloud**: without
it, `build` produces digest-pinned distroless images locally (no
Docker daemon); with it, they upload to `<repo>/<service>` on Artifact
Registry. Held as acceptable because the flag name carries it — and
help sits `build` at the boundary line with the consequence spelled
out.

## Cloud, read-only

```
tako logs [service] [--since 15m]     recent logs for the deployed services
tako dlq [service]                    peek dead-lettered messages per async service
tako dlq <service> --replay <id>      put one dead letter back into delivery
tako dlq --local [carrier]            the same, against .tako/data
```

## Cloud, mutating

```
tako deploy [--plan] [--canary <pct>] [--yes]
tako promote           send 100% of every service's traffic to latest
tako rollback          return traffic to the previous revision set
tako prune [--data]    delete orphans tako once managed
tako dlq [service] --ack   drain the printed dead-letter messages for good
tako dlq <service> --replay <id>|--all   re-deliver, then remove from the DLQ (a MOVE)
tako destroy           delete everything labeled for this app
```

`deploy --plan` stops after the classified plan; `--canary` holds new
revisions at a traffic percentage. `rollback` is instant for traffic;
infra shape rolls back via git revert + `tako deploy`.

## The confirmation ladder

One rule, stated in `tako` help: **friction scales with
irreversibility** (from [cli-review-1](../cli-review-1.md)).

1. **Reversible or derivable** — no ceremony. `rollback` undoes
   `promote`; a deploy re-derives what `prune` removed (stateless
   orphans: compute, messaging, indexes).
2. **Risky but recoverable** — `deploy` prompts y/N on replaces and
   rename suspects. `--yes` exists ONLY to pre-answer this prompt in
   CI; it never touches data confirmations.
3. **Data, gone forever** — typed confirmations, never bypassable.
   `destroy` types the app name, always (you must know what you are
   killing). `prune --data` prints counts, then types a literal word
   (the counts are the object, the word is the seal). `dlq --ack` is
   an explicit single-purpose flag whose name says it. `--yes`
   deliberately does not reach this tier — data destruction is never
   automatic.

The tier-3 mechanics differ on purpose: unifying destroy's typed name
with prune's counts-then-word would weaken the stronger ceremony.

## Project, region, env

Every cloud command resolves its target identically, through one
parser:

- `--project` → else `GOOGLE_CLOUD_PROJECT` → else gcloud config
- `--region` → else `TAKO_REGION` → else `us-central1`
- `--env` → default `prod` (preview environments like `--env pr-42`
  touch only their own slice)

`--flag value` and `--flag=value` both work, everywhere.

## Errors you'll meet

- Unknown flags name their command's real surface, e.g.
  `unknown flag "--x" (tako gen graph accepts --json or --mermaid)`,
  `unknown flag "--x" (tako gen client ts accepts --out <path>)`.
- `tako gen client generates TypeScript today — run: tako gen client ts`
- Bare `tako` (or an unknown command) prints the full usage — which
  ends with the confirmation ladder and the flag-resolution rule.
- Deploy preflight failures print copy-pasteable remediation (enable
  command, grant command, or console step) — see
  [the cloud guide](../guide/cloud.md).

## Not yet

- No `--yes` on `destroy` / `prune --data` — tier 3 is non-bypassable
  by design, not a missing feature.
- No per-command `--help`: the single usage screen still fits;
  revisit if the surface grows past one screen.
- `tako gen client` speaks TypeScript only.
