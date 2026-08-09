# tako.Service

The package's app service, declared. It is the home for every setting
that describes a whole deployable rather than one route — and the reason
those settings no longer ride whichever endpoint happened to be first.

## Declare

```go
package blog

var web = tako.Service(
	tako.ServiceConfig{
		Domain:    "blog.example.com",
		Profile:   tako.ProfileLatency,
		Footprint: tako.Footprint{Memory: "1Gi"},
	},
)
```

One per package (`tako/one-service`), no per-handler binding — the same
shape [`tako.Site`](site.md) and [`tako.Auth`](auth.md) already have.

**It is optional.** A package with handlers and no declaration still
deploys; you add one when you have something to say about the service.

## What it names

Declaring it names the app service **`<package>-<var>`** — `web` in
package `blog` gives `blog-web`, the same shape workers and subscribers
already use (`blog-makethumb`). A package that declares none keeps the
bare package name, which is the honest name for a service nobody
bothered to name.

```
blog: 1 service
  service blog-web (package blog, profile default, domain blog.example.com)
```

Renaming a service renames its identity in **every** provisioned object
— service account, IAM grants, push subscriptions. Adding or removing a
`tako.Service` declaration to an already-deployed app is therefore a
§4.6a replace: the plan shows the old objects as orphans and flags the
rename suspicion. Decide the name before you deploy, or accept one
migration.

## What it covers, and what it does not

`tako.Service` describes the APP service — the deployable running the
package's `API`, `Page` and `Cron` handlers. It does not touch the
services carved out of the same package for each `Worker`, `Subscribe`,
`JobHandler` and `Singleton`: those are one handler each, so the handler
IS the service and remains the honest place for their settings.

```go
// The app service's settings.
var api = tako.Service(tako.ServiceConfig{Profile: tako.ProfileLatency})

// This worker's own service — its own settings, on itself.
var MakeThumb = tako.Worker(thumbs, makeThumb,
	tako.Compute{
		Profile:   tako.ProfileBatch,
		Footprint: tako.Footprint{Memory: "4Gi", Concurrency: 4},
	})
```

| setting | `tako.Service` | Worker / Subscribe / Job / Singleton | API / Page |
|---|---|---|---|
| `ServiceConfig.Domain` | yes | no — nothing public to answer on | no |
| `Profile` | yes | yes | no |
| `Footprint` | yes | yes | no |

## Where it is running

```go
if tako.Environment() == tako.EnvLocal {
	v.DevUsers = devIdentities   // the local sign-in strip
}
```

`tako.Environment()` returns `tako.EnvLocal` under `tako dev`, `tako
test` and `go test`, and `tako.EnvCloud` in a service `tako deploy` put
in Cloud Run. Anything it does not recognise is `EnvLocal`, so a
mistyped environment can only turn a local affordance **off** in
production.

It is a fact about the deployment, **never a feature flag.** Use it for
what a development affordance may do — a seeded sign-in, a visible
fixture, an extra log line. An app that branches its *product* on this
has two products and tests one.

It is a package-level function rather than a method on `Ctx` because the
answer is not per-request: a rule like "these seeded identities are
admitted locally" stays pure and testable without a session, and a
constructor can ask before any request exists.

Before this existed, apps read `TAKO_BACKEND` — tako's private boot
switch, promised to nobody — and the first app to do it used the answer
to decide who may reach its admin. That is an authorization input, which
is why it is surface now.

## Local

Nothing. `tako dev` runs every service in one process; the declaration
shapes what `tako deploy` provisions, not what `dev` runs. The service
NAME does show up in `tako gen graph` and the dev banner.

`tako.Environment()` is `EnvLocal` here, and in tests.

## Test it

Assert it on the graph — this is an analyzer-level declaration:

```go
graph, _ := analyzer.Analyze(analyzer.Config{Dir: "."})
// graph.Services[i].Name == "blog-web"
// graph.Services[i].Domain == "blog.example.com"
```

## Errors you'll meet

```
a package declares one service — oneservice.api is already declared; remove oneservice.admin or merge the two (tako/one-service)
tako.ServiceConfig must be written as a literal with constant fields — configuration is read at analysis time, not run time; got variable "cfg" (tako/config-literal)
tako.ServiceConfig takes one configuration value, got 2 — merge them into a single literal (tako/one-config)
```

The `tako/service-option` text is the migration note for code written
before this existed: the option moved, and the error says where to.

## Not yet

- **No named grouping within a package.** One package still yields one
  app service; `tako.Service` names it, it does not split it. Two API
  services from one package would need explicit per-handler binding, and
  would give up the property that a service boundary is also a Go
  package boundary — so cross-service calls could silently become
  in-process function calls. Deliberately not built.
- Workers and subscribers cannot be merged back into the app service.
