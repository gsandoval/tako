# tako.Auth — providers, sessions, WithAuth, the dev bar

## Declare

```go
auth = tako.Auth(tako.AuthConfig{Google: true})   // journal: Google sign-in
auth = tako.Auth(tako.AuthConfig{Password: true})    // shop: email/password
```

One declaration injects the session surface: `POST /_tako/api/session`
(exchange a sign-in for a session), `DELETE /_tako/api/session` (sign out),
`GET /_tako/signin` (the framework-owned sign-in page — your
app just links here), and the form-friendly `POST
/_tako/api/session/signout`.

Sessions are **revocable entity records**: the cookie carries only an
opaque random id; the record holds the identity and the session-bound
CSRF token. Sign-out (or an operator, in the console) deletes the
record and the session is dead on the next request — revocation is
real, not a cookie waiting to expire. Sessions last 7 days.

## Use

`WithAuth()` gates a route with a 401; past it, `ctx.User()` is
guaranteed. On public routes use `ctx.MaybeUser()` — calling `User()`
on an unauthenticated request panics. From
[examples/journal](../../examples/journal/app.go):

```go
Save    = tako.Page("POST /p", save, tako.Route{SignIn: true})
Publish = tako.Page("POST /p/{id}/publish", publish, tako.Route{SignIn: true})
```

```go
p := Post{ID: tako.NewID(), Owner: ctx.User().ID, Title: req.Title, ...}
```

Ownership is the handler's own rule, not the framework's: journal's
publish checks `current.Owner != ctx.User().ID` and returns a 403.
[Shop](../../examples/shop/app.go)'s public `/api/me` probe shows
`if u, ok := ctx.MaybeUser(); ok { ... }`.

Sessions cross service calls automatically: an `Endpoint.Call` carries
the caller's session id in `X-Tako-Session`, and the callee's record
lookup IS the verification — revocation kills the identity mid-chain.
See [calls.md](calls.md).

## Local

`tako dev` does auth with no GCP anywhere near it. HTML responses get
the **dev bar**: sign in as test users `alice` / `bob`. The bar
reflects the session instead of listing every control — signed out it
offers "sign in as alice/bob"; signed in as alice it shows "switch to
bob" and "sign out". `/_tako/signin` serves a dev page listing
the same users. Dev sessions are real session records in the local
entity store, so sign-out and revocation behave exactly as in the
cloud.

## Cloud

`tako deploy` runs sign-in on **Identity Platform**. Most of it is
automated — initialization, enabling the email provider when declared,
registering sign-in domains per deploy — and the deploy injects
`TAKO_AUTH_APIKEY` / `TAKO_AUTH_DOMAIN` (the browser client config)
into every service; the sign-in page uses them to run the browser flow
and POST the resulting ID token to `/_tako/api/session`.

The one genuinely manual step — only with `tako.AuthConfig{Google: true}` — from
[the cloud guide](../guide/cloud.md):

> Firebase console → your project → Authentication → Sign-in method →
> Google → Enable (pick a support email) → Save.

The deploy blocks with that exact instruction until it's done. For the
email provider there is a complete copy-pasteable curl recipe
(create a user, mint an ID token, exchange it for the session cookie)
in [validate/shop.md](../../validate/shop.md) — the sign-in page's
email form signs IN only; creating the account is that recipe's job.

The honest cost: one entity Get per authenticated request (the session
lookup — v0 does not cache), plus Identity Platform's per-MAU pricing
past its free tier.

## Test it

Shop's [api_test.go](../../examples/shop/api_test.go) signs in the way
the dev bar does — post `dev_user` with a cookie jar:

```go
res, err := c.PostForm(srv.URL+"/_tako/api/session", url.Values{"dev_user": {name}})
```

then asserts the 401 text on anonymous writes, owner scoping, and that
a post after sign-out is 401 again ("session record deleted"). With
`tako.NewTestApp(t)`, `app.Ctx(app.SignIn("alice"))` injects an
identity directly.

## Errors you'll meet

- `401 {"error": "sign in to continue"}` — WithAuth's gate, exact text
  pinned by tests.
- `400 POST {"idToken": …} (the Identity Platform sign-in result)` —
  `/_tako/api/session` got a body without an ID token.
- `401 the ID token did not verify: token is for a different project`
  (also: `token expired`, `bad signature`, `unknown signing key`).
- `500 sign-in is not configured — tako deploy injects the auth client
  config, so this is a tako bug; re-run tako deploy`.
- Deploy blocked on the Google-provider console step above, with the
  instruction printed verbatim.

## Not yet

- Two providers: Google and email/password. No other OAuth providers.
- No roles or custom claims — authorization past "signed in" is your
  handler's code (journal's owner check is the pattern).
- No session cache: every authenticated request pays the entity Get.
- No account-creation UI for the email provider; the framework page
  signs in existing users only.
