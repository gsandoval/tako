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

| option | `tako.Service` | Worker / Subscribe / Job / Singleton | API / Page |
|---|---|---|---|
| `WithDomain` | yes | no — nothing public to answer on | no |
| `WithProfile` | yes | yes | no |
| `WithFootprint` | yes | yes | no |

## Local

Nothing. `tako dev` runs every service in one process; the declaration
shapes what `tako deploy` provisions, not what `dev` runs. The service
NAME does show up in `tako gen graph` and the dev banner.

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
