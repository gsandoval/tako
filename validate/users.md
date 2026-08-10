# validate/users.md — SC1 (tako users)

The operator's door onto the accounts an app already has. Run from
`examples/journal` unless a step says otherwise.

## 1. An app with a sign-in door, and nobody in it

```
rm -rf .tako/data && tako users list
```

```
tako users · local · .tako/data
  no accounts yet — tako users create <email> makes the first one
```

**Expect** the target on line one, always — before anything happens,
not mentioned afterwards. "Which accounts am I about to change" is the
question this command answers before it answers any other.

## 2. Making one

```
tako users create ana@example.com --name Ana --role moderator
tako users list
tako users show ana@example.com
```

```
tako users · local · .tako/data
  created  u_0def5785e109bf1b  ana@example.com  Ana  [moderator]
  no password set — this account signs in with Google; --password sets one

tako users · local · .tako/data
  ID                  EMAIL            NAME  ROLES
  u_0def5785e109bf1b  ana@example.com  Ana   moderator
  1 account

tako users · local · .tako/data
  id       u_0def5785e109bf1b
  email    ana@example.com
  name     Ana
  roles    moderator
  verified no
```

The password line is not decoration: journal declares Google sign-in,
so an account without one is normal. In an app declaring ONLY
email/password it says the other thing — that this account cannot sign
in at all yet — because there the same silence is a broken account.

## 3. A role the app does not declare

```
tako users grant ana@example.com moderatr
```

```
tako: "moderatr" is not a role this app declares
      declared: moderator (tako.Role)
      did you mean moderator?
```

**Expect** a refusal, and no `--force`. This is not spelling
pedantry: a role is a claim, and granting one no policy reads writes a
claim nothing reads. The store accepts it, the operator sees success,
and the person still cannot get in. The analyzer already has the
declared set (`AppGraph.Roles`), so the check is free — and it runs
before any credential is touched, so a typo against `--project` costs
no round trip and no 403.

## 4. The target is never inferred

```
tako users list --env staging
GOOGLE_CLOUD_PROJECT=someone-elses-prod tako users list
```

```
tako: --env names a cloud environment but no project was given
      add --project <id>, or drop --env to work on .tako/data

      tako users never takes the project from GOOGLE_CLOUD_PROJECT or from
      gcloud config the way the other cloud commands do: this one writes
      people, and a project inherited from a shell is how a dev fixture
      lands in production
```

**Expect** the second command to print `local · .tako/data` and list
the local accounts — the environment variable that steers every other
cloud command steers nothing here (D1, approved 2026-08-10). The
deviation is stated in `tako --help` rather than left for someone to
discover.

## 5. The whole point: a grant reaches a session already signed in

This is the loop the command exists for, and it was run end to end.

```
rm -rf .tako/data && tako dev &            # port 8101
curl -X POST localhost:8101/_tako/api/session -d 'dev_user=alice'
curl -i -X POST localhost:8101/_tako/api/session -d 'dev_user=bob'   # keep bob's cookie
curl -o /dev/null -w '%{http_code}' -b "_session=$B" localhost:8101/moderate
```

```
bob /moderate before grant: 403
```

alice is a moderator without being granted one, and that is journal's
doing rather than tako's: it declares
`OnFirstSignIn = tako.Subscribe(auth.FirstSignIn, appointFounder)`, so
the first person through the door is appointed. bob is the second.

Stop the dev server (step 7 says why), then:

```
tako users list
tako users grant bob@example.test moderator
```

```
tako users · local · .tako/data
  ID                  EMAIL               NAME   ROLES
  u_cb485c674cc76131  alice@example.test  alice  moderator
  u_ef7c83358d53ca90  bob@example.test    bob
  2 accounts

tako users · local · .tako/data
  granted  moderator to bob@example.test  [moderator]
  live sessions refresh on their next request — nobody is signed out
```

Restart `tako dev` and replay bob's ORIGINAL cookie:

```
bob /moderate after the CLI grant, SAME session: 200
```

**Expect** 403 → 200 with no re-sign-in. That is the SDK's
stale-marking working through the CLI, and it is the reason this
command calls `auth.Users()` instead of the store: the roles a request
sees come off the session, minted before the change, so a grant that
skipped the marking would have left bob at 403 until his session
expired — with nothing anywhere reporting a problem.

The transcript line says WHEN it lands for the same reason. An
operator who is not told sits and debugs a 403 that was about to fix
itself.

## 6. An account the CLI made can actually sign in

From `examples/shop`, which declares `Password: true`:

```
rm -rf .tako/data
printf 'hunter2\n' | tako users create ops@acme.com --name Ops --password
tako dev &                                  # port 8097
curl -i -X POST localhost:8097/_tako/api/session -d 'email=ops@acme.com&password=hunter2'
```

```
tako users · local · .tako/data
  password: 
  created  u_c7cf6ec674c52a27  ops@acme.com  Ops
  auth.Registered has no subscriber here — this is the CLI, not the app;
  run tako dev if OnRegistered must see this account

HTTP/1.1 200 OK
Set-Cookie: _session=5260fe6c4a6570cc06074889b95cfe64; Path=/; ...
{"uid":"u_c7cf6ec674c52a27"}
```

**Expect** the uid in the sign-in response to be the id the CLI
printed. Same account, same store, through the app's real door.

**Expect also** the `auth.Registered` line, and only because shop has a
subscriber that would care. The publish is real against a project and
absent on a laptop — this is the CLI, not the app, so nothing here runs
the subscriber. An app with nothing listening gets no line at all,
because there is nothing to miss.

## 7. `tako dev` holds the store, and says so

```
tako dev &
tako users list
```

```
tako: the local accounts are open in another process — `tako dev` holds
      .tako/data while it runs. Stop it and re-run, or sign in through the
      dev bar, which creates the account inside the running app.

      (if nothing is running, an earlier dev process was orphaned: find it
      with `lsof .tako/data/entity.db`)
```

bbolt takes an exclusive lock, so the two cannot share the file. The
store's own message guesses at "an orphaned service from a previous
run", which sends the reader hunting for a zombie when the holder is
the terminal next to this one — so this command says the likely thing
first and keeps the orphan case as the parenthetical it usually is.

**This is a real limitation, not a fixed problem.** It is filed as
discovered work; the fix (a read-only open for read-only commands, or a
seam that reaches a running dev server) is its own task.

## 8. The framework's own guards

From the repo root:

```
go test ./cmd/tako -count=1
```

**Expect** green, including:

- `TestTheUsersCommandNeverReachesPastTheSDK` — users.go imports no
  backend and no hooks. Proved by mutation: adding
  `internal/hooks` and one `hooks.Users.SetRoles` call fails it.
- `TestWiringTheAccountsAlsoWiresWhatSessionsNeed` — wiring the
  accounts without the entity store fails it, which is the exact shape
  where a grant would never reach a live session and nothing would say
  so. Proved by mutation.
- `TestNoProjectMeansLocalEvenWithAProjectInTheEnvironment` — proved by
  mutation: restoring the ordinary `GOOGLE_CLOUD_PROJECT` fallback
  fails it with "a dev fixture would land in production".

The compound acts themselves — a role change lands on the next
request, disabling ends the session — are pinned where they already
were, in `pkg/runtime/staleroles_test.go`. The CLI inherits them by
going through the SDK, which is the argument for D3 stated as a test
that did not have to be written.
