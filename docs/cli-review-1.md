# CLI review 1 — uniformity, discoverability, side effects (2026-07-30)

User-requested Stage 4 deliverable: does the `tako` CLI feel uniform —
are commands discoverable, are side effects explicit, do the
confirmation mechanisms (`--yes`, typed names, prompts) compose into
one legible policy? Reviewed against the shipped surface (12 commands,
2 gen subcommands). Verdict: the bones are uniform because almost
everything shares one implementation path; the POLICY was real but
unstated. Findings below; fixes landed with this review.

## The command surface, by what it touches

| Command | Touches | Mutates | Confirmation | Cloud flags |
|---|---|---|---|---|
| `dev` | local only (.tako/) | local files | — | — |
| `test` | local only | .tako/gen | — | — |
| `build` | local; `--push` uploads images | cloud with `--push` | — | — |
| `gen graph` | local, read-only | nothing | — | — |
| `gen client ts` | local | writes one file | — | — |
| `logs` | cloud, read-only | nothing | — | project/region/env |
| `dlq` | cloud, read-only; `--ack` deletes messages | with `--ack` | explicit flag IS the consent | project/region/env |
| `deploy` | cloud | creates/updates | y/N prompt on replaces + rename suspects; `--yes` pre-answers | project/region/env, --plan, --canary, --yes |
| `promote` | cloud | traffic only | — (reversible by rollback) | project/region/env |
| `rollback` | cloud | traffic only | — (reversible by promote) | project/region/env |
| `prune` | cloud | deletes orphans | stateless: none (derivable); `--data`: counts + typed confirmation | project/region/env, --data |
| `destroy` | cloud | deletes everything | typed app name, ALWAYS, no bypass | project/region/env |

## The confirmation ladder (now stated in --help)

The policy that was implicit is one rule: **friction scales with
irreversibility.**

1. **Reversible or derivable** (traffic moves, orphaned compute,
   regenerated files): no ceremony — `rollback` undoes `promote`,
   a deploy re-derives what prune removed.
2. **Risky but recoverable** (replaces, rename suspects): an
   interactive y/N at deploy; `--yes` exists ONLY to pre-answer this
   prompt in CI. It never touches data confirmations.
3. **Data, gone forever** (buckets, entity kinds, dead-letter
   messages): typed confirmation (`destroy`, `prune --data`) or an
   explicit single-purpose flag whose name says it (`dlq --ack`).
   `--yes` deliberately does NOT bypass this tier — data destruction
   is never automatic (settled decision 12).

## Findings

1. **The ladder was unstated.** Nothing told a user that `--yes`
   can't skip the destroy confirmation, or why promote asks nothing.
   → FIXED: `tako` help now groups commands by side-effect tier and
   states the ladder in three lines.
2. **`tako gen` bare dumped the full usage** instead of listing its
   own subcommands. → FIXED: bare `gen` (and unknown gen subcommands)
   print the gen namespace with one line per generator.
3. **Flag-form asymmetry:** `--project x` and `--project=x` both
   worked, but `--canary` accepted only `--canary=N`. → FIXED: both
   forms everywhere parseTarget reaches.
4. **`build --push` is the one local command that silently crosses
   into the cloud** on a flag. Held as acceptable — the flag name
   carries it — but the help now sits `build` at the boundary line
   with its `--push` consequence spelled out.
5. **Non-uniformity kept ON PURPOSE:** `destroy` takes a typed NAME
   while `prune --data` types a literal word after printing counts.
   Destroy names the whole app (you must know what you are killing);
   prune's counts are the object, the word is the seal. Both are
   tier-3 ceremonies; unifying the mechanics would weaken the
   stronger one.
6. **Discoverability of cloud flags:** project/region/env resolve
   uniformly (flag → GOOGLE_CLOUD_PROJECT → gcloud config) across
   every cloud command via one parser — already uniform, now
   documented once at the bottom of help instead of per-command.

## Not changed (and why)

- No `--yes` on destroy/prune (tier 3 is non-bypassable, by design).
- No command renames (stability > polish at R-stage).
- No per-command `--help` subsystem (the single usage screen still
  fits; revisit if the surface grows past one screen).
