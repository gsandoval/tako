# validate/secrets.md — SC4 (`tako secrets`)

Run from `examples/gallery`, which declares one secret.

## 1. What the app declares, and which have a value

```
tako secrets
```

```
tako secrets · local · .env
  NAME          VALUE  DECLARED AT
  notifier-key  set    app.go:35:2
  1 declared, all set
```

**Expect** the declaration position. "stripe-key is missing" is a
sentence; "stripe-key is missing, app.go:42" is somewhere to go. Before
this, the only way to learn a secret's name was a boot failure, one
name at a time.

## 2. A value is never printed

`TestNoCommandEverPrintsAValue` seeds a sentinel value and runs `list`,
`set` and `rm`, failing if the sentinel appears in output **or in an
error**. There is no `get` and no `--show`.

The way this stops being true is somebody adding a column "just for
debugging", so the test is not "does list have a value column" — it is
"can a stored value reach the output by any path".

## 3. A value never arrives as a flag

There is no `--value`. It comes from stdin or an echo-off prompt:

```
printf '%s' "$KEY" | tako secrets set notifier-key --project <id>
```

A flag lands in shell history, in `ps`, and in any CI log that echoes
its commands.

## 4. The gitignore guard, fired on this repo

```
cd examples/gallery && printf 'x' | tako secrets set notifier-key
```

```
tako: .env is not gitignored, and this writes a secret into it
      add it first — one line, once:
        echo '.env' >> .gitignore

      (examples/gallery commits a .env on purpose: a dummy value so the
       demo runs out of the box. That is a demo's choice, not a default.)
```

**Expect** the refusal, and note what it fired on: gallery's `.env` IS
committed, deliberately, and the message names that exact case. The
convention lived only as a comment inside that file, and a comment is
not a guard — this is the worst failure the command could cause.

## 5. The file stays the developer's

`TestWritingKeepsTheRestOfTheFileExactly` writes into a `.env` holding
comments, a blank line and another key, and asserts the result
byte-for-byte. `rm` takes its line and nothing else. A command that
reformatted the file is one people stop using.

A new `.env` is created 0600 (`TestANewEnvIsNotWorldReadable`).

## 6. Live, against tako-test-01 (2026-08-10)

**List, cloud.** The physical column earns its place — in `prod` the
name is unprefixed, so it is not the obvious transformation:

```
tako secrets · cloud · tako-test-01 / prod
  NAME          VALUE  IN SECRET MANAGER            DECLARED AT
  notifier-key  set    notifier-key · 2 version(s)  app.go:35:2
  1 declared, all set
```

**Set** adds a version rather than replacing one — services read
`:latest`, and the version is what Secret Manager audits:

```
$ printf 'nk_dev_demo' | tako secrets set notifier-key --project tako-test-01
  ✓ added a new version of notifier-key — services read the latest
```

Verified by re-listing: `1 version(s)` → `2 version(s)`.

**Delete, and the refusal first.** A throwaway secret created with
gcloud, then:

```
  tako-sc4-probe   1 version(s), created 2026-08-10
  this destroys the secret and all 1 version(s), permanently — Secret
  Manager has no undelete, and the versions ARE the audit trail.
  tako-sc4-probe is not declared by this app — an orphan, which is the safe case
  for this command.
  type the secret's name to confirm: tako-sc4-probe
  > wrong-name
tako: that is not the name — nothing was deleted
```

`gcloud secrets describe` confirmed it still existed. Then with the
name typed correctly:

```
  ✓ destroyed tako-sc4-probe and 1 version(s)
```

and `gcloud secrets describe` answered `NOT_FOUND`. **`--yes` does not
reach this prompt** — the ladder reserves that flag for things a re-run
can put back, and this is not one.

## 7. What SC4 found in SC2

`tako setup`'s remainder listed every declared secret as having no
value, without checking. Against `tako-test-01` it told an operator to
create `notifier-key`, which already existed with a version in it. It
now checks, and names `tako secrets set` rather than a raw gcloud line.

## 8. The suite

```
go test ./... && for d in examples/*/; do (cd $d && tako test -count=1 ./...); done
```

**Expect** green. `:addVersion` is classified as non-idempotent — a
retry after a response tako never saw would leave two versions holding
one value, which is harmless to read and a phantom entry in the audit
trail somebody later has to explain.
