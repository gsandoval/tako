# docs/

Two kinds of thing live here, and they are for different people.

## If you are learning tako

Read these. They ship inside the binary — `tako docs` serves them at
`localhost:8080`, so they are also the version that matches whatever
tako you have installed.

| | |
|---|---|
| [`guide/`](guide/) | The narrative. Nine chapters, one per example app, from hello to a deployed site. **Start here.** |
| [`reference/`](reference/) | One page per feature — entity, bucket, queue, pages, auth, alerts — with the errors each one can produce. |
| [`api/`](api/) | Every exported symbol, generated from the SDK lock. Answers "does this exist", not "how do I use it". |

The root [`README.md`](../README.md) is five minutes to a deployed URL
and is a better first stop than any of these.

## If you are working ON tako

Everything else in this directory is a record for whoever builds the
next thing. None of it is user documentation.

**Design, still normative:**

- [`tako-tech-spec.md`](tako-tech-spec.md) — the design. Interface rules
  (Part 1), SDK (Part 2), the example app (Part 3), internals (Part 4).
  Where it disagrees with the root Go package, **the package wins**.
- [`tako-cloud-permissions.md`](tako-cloud-permissions.md) — what a
  deploy needs from GCP and why, deployer and runtime split.
- [`domain-options.md`](domain-options.md) — the four ways to put a
  domain in front of a service, with live pricing and the trigger for
  each. Domain mappings ship; the rest is written down, not built.

**Plans, live:**

- [`tako-implementation-plan.md`](tako-implementation-plan.md) — phase
  milestones and exit criteria.
- [`tako-agent-plan.md`](tako-agent-plan.md) — the current slice and
  what is deliberately not scheduled.

**Records, dated and frozen:**

- [`task-log.md`](task-log.md) — append-only, one entry per task, 226
  of them. Every feature's proposal, what shipped, the real validation
  transcript, and the work discovered on the way. **It is 84% of the
  words in this directory and it is not meant to be read front to
  back** — search it for a feature ID (`FL11`, `RC1`, `Z7`) when you
  want to know why something is the way it is.
- [`validation-log.md`](validation-log.md) — the human gate verdicts.
- `api-review-1/2/3.md`, `cli-review-1.md`, `code-review-1/2.md` — the
  reviews, in order. They are cited from the reference pages and from
  comments in the code, so they explain decisions the tree still
  obeys.

Reviews are **frozen**. A later review that corrects an earlier one
says so in its own text; the earlier one is not edited, because a
record that changes is not a record.

## What is not here

`validate/` (repo root) holds the scripted manual validation — one file
per feature, the exact commands and what a human should observe. Those
are run before a gate, and their transcripts land in the task log.
