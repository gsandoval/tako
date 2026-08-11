# validate/new.md — SC3 (`tako new`) and SC5 (`BootGCPWith`)

## SC3

### 1. An app that runs, from nothing

```
tako new sc3api --replace /path/to/tako
cd sc3api && tako dev
```

```
tako new · sc3api
  + sc3api/.gitignore
  + sc3api/go.mod
  + sc3api/sc3api.go

  tako v0.0.0-20260811030652-56fb6c149b22+dirty

Next:
  cd sc3api && tako dev
```

```
$ curl localhost:8126/hello
{"message":"Hello from Tako!"}
$ curl localhost:8126/greet/ada
{"message":"Hello, ada!"}
$ curl -o /dev/null -w '%{http_code}' localhost:8126/greet/nobody
404
```

**Expect** no manual step between the two commands. That is the whole
bar, and it is the one the first two attempts missed (§4).

### 2. The site kind, which is the reason kinds exist

```
tako new sc3site --kind site --replace /path/to/tako
cd sc3site && tako dev
```

```
  + sc3site/.gitignore
  + sc3site/go.mod
  + sc3site/sc3site.go
  + sc3site/web/templates/home.html
  + sc3site/web/templates/layout.html
```

```html
<title>Hello from Tako!</title>
...
  <h1>Hello from Tako!</h1>
  <p>Edit web/templates/home.html and save — tako dev reloads.</p>
```

**Expect** a rendered page. A site's missing pieces are FILES, not a
line of Go, and their absence surfaces as `the site has no templates/
directory` — a message about a checkout wearing the costume of a broken
app. `--kind api` is the default because an API needs none of them.

### 3. `.gitignore` carries `.env` from the first commit

`TestTheScaffoldIgnoresTheFilesThatMustNotBeCommitted`. SC4's
`tako secrets set` refuses to write into a `.env` git would commit; a
scaffold that omitted the line would ship that failure one project at a
time.

### 4. What the build changed about the design

Both are the same lesson: the scaffold's bar is "the next command
works", and it is easy to be one step short of it.

**`go mod tidy` cannot see the generated main.** It skips directories
beginning with a dot, and generated code lives in `.tako/gen`. So the
first working version resolved bbolt and x/sys, reported success, and
`tako dev` died with six `missing go.sum entry` lines naming goldmark,
bluemonday and x/net — packages the user has never heard of. Fixed by
fetching `pkg/runtime` by name, and guarded by
`TestTheScaffoldResolvesTheRuntimeItsGeneratedMainImports`, because the
failure is invisible until somebody runs the app.

**A `go build` binary reports a pseudo-version, not `(devel)`.** The
design said "if the version is `(devel)`, offer `--replace`" — but Go
stamps `v0.0.0-20260811030652-56fb6c149b22+dirty` from VCS state, which
is not `(devel)` and equally cannot be fetched. `released()` now
recognises only a real tag, and `TestOnlyARealTagCountsAsReleased` pins
both directions.

### 5. It refuses rather than half-succeeds

- An existing directory is never written into
  (`TestAnExistingDirectoryIsNeverWrittenInto`), and the refusal does
  not touch what is there.
- A scaffold that cannot resolve its dependency removes itself
  (`TestAFailedScaffoldLeavesNothingBehind`): half a project is
  something the user has to clean up before retrying.

### 6. Known limit, until v0.1.0

`--replace` is required today, because this tako has no published
version to depend on. The refusal says exactly that and prints the
command with the flag in it. Once tagged, plain `tako new <name>` pins
the version of the binary that wrote it.

## SC5

`runtime.BootGCPWith(gcp.Options)` — the target of a CLI run is an
argument, not an ambient fact. flauta's tool set `TAKO_PROJECT` in its
own process and then called `BootGCP`, which is a program mutating its
environment to configure a library.

```
go test ./pkg/runtime -run BootGCPWith -count=1
```

**Expect** green: `TestBootGCPWithNamesTheFieldItNeeds` and
`TestBootGCPWithDoesNotAssumeProd`.

Booting for real needs a project, a metadata server and Secret Manager,
so what is testable is the refusal — and the refusal is the design:

- **It names the missing field**, and does NOT say "this is a tako
  bug". That sentence belongs to `BootGCP`, where an empty
  `TAKO_PROJECT` really is the deploy's failure; telling a hand-written
  caller their own omission is tako's fault sends them to file an issue.
- **`Env` is required, never defaulted to prod.** It is the Datastore
  namespace and the prefix on every derived name, so a tool that
  defaulted would read and write production because a field was empty.

`BootGCP` now reads the environment and calls `BootGCPWith`; both reach
one shared body, and `TestTheCloudBootWiresTheMetricsBackend` checks
that body rather than either door — so a third way in cannot boot a
service that is quietly different. `pkg/runtime`'s API lock was
regenerated for the one added symbol.
