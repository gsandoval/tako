# validate: the declared provider is the only door (FL14)

The claim: a session may only come from a provider the app declared,
whatever else the Identity Platform project has enabled.

The failure this prevents is that `tako.Auth(tako.AuthConfig{Google:
true})` **reads** like a restriction. Before FL14 it decided which
buttons the framework's sign-in page rendered and nothing more, so any
Identity Platform token for the project produced a session — including
one from a provider some other app's deploy turned on.

## 1. A project with more providers than the app declares

Not contrived: this is what tako-test-02 looks like, and it is how the
gap was found.

```
curl -s "https://identitytoolkit.googleapis.com/admin/v2/projects/<p>/config" \
  -H "Authorization: Bearer $(gcloud auth print-access-token)" \
  -H "x-goog-user-project: <p>"
```

Expect `signIn.email.enabled: true` while the app under test declares
`AuthConfig{Google: true}`. (The `x-goog-user-project` header is
required — user ADC carries no quota project.)

## 2. Mint a real token from the undeclared provider

```
# create an account (admin API; self-service signup may be disabled)
curl -X POST ".../v1/projects/<p>/accounts" -H "Authorization: Bearer …" \
  -d '{"email":"probe@example.com","password":"…","emailVerified":false}'

# sign in as it for an ID token
curl -X POST ".../v1/accounts:signInWithPassword?key=<apiKey>" \
  -d '{"email":"probe@example.com","password":"…","returnSecureToken":true}'
```

**Decode the token and check it is real** — `aud` is the project,
`exp` is in the future, `firebase.sign_in_provider` is `password`. A
refusal of a token that was invalid anyway proves nothing, and this is
the step that makes the test worth running.

## 3. Offer it to the deployed app

```
curl -i -X POST https://<service>/_tako/api/session \
  -H "Content-Type: application/json" -d '{"idToken":"<token>"}'
```

Expect **401** and exactly:

```
the ID token did not verify: this token was minted by email, and this app declares google — tako.AuthConfig decides which doors exist
```

## 4. Prove the check is what refused it

A security test that has never seen the vulnerability is a guess.
Disable the one condition in `userFromClaims` and re-run:

```
go test ./pkg/runtime/ -run 'UndeclaredProvider|UnknownProvider'
```

Both must fail — "a password token was accepted by an app that declares
only google". Restore it.

## 5. Clean up

Delete the probe account (`/v1/projects/<p>/accounts:delete`) and
destroy the deployment. An account left behind is a real account.

## What this does NOT check

Sessions already minted. The rule applies at sign-in, so a session
established before a declaration changed keeps working until it expires
or is revoked — deliberately, so changing `AuthConfig` never signs
anybody out mid-visit.
