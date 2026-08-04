# validate/docs.md — Z7: `tako docs` serves the documentation the binary shipped with

The claim: the guide, the feature reference and the SDK's exported
surface are readable **from the binary**, with no checkout, no network,
and no GitHub tab showing whatever happens to be on main.

Three things have to be true for that claim to be worth anything:

- **Z7a** — the pages are inside the binary. Move the binary away from
  the repository and it still serves them.
- **Z7b** — the links between them work as routes, and the ones that
  point at files the binary cannot serve say so instead of dangling.
- **Z7c** — the api page is the API. It is generated from the same lock
  file the freeze test compares against, so it cannot list a symbol tako
  does not export, or miss one it does.

**Setup:** run from the repository root unless a step says otherwise.
Nothing here touches a cloud project.

## 1. The automated truth

```
go test ./pkg/docserver/ ./docs/ -v
```

→ green, and specifically:

- `TestEveryLinkBetweenDocsResolves` — every link between docs points at
  a page that exists, every `#anchor` at a heading that exists, and
  every link into the repository at a file that is there.
- `TestEveryExportedSymbolHasAReferencePage` — every symbol in the lock
  is mapped to the page that documents it, except the ones deliberately
  listed as undocumented.
- `TestApiPageListsEverySymbolInTheLock` — nothing in the lock is
  missing from the rendered page.
- `TestEveryEmbeddedPageRenders` — the page count matches the file
  count, each page once.
- `TestRenderSanitizes` — scripts and event handlers do not survive the
  renderer; code fences and their language classes do.
- `TestEmbeddedSurfaceMatchesTheSDKLock` (in `./docs`) — the embedded
  copy is the lock, byte for byte.

## 2. It runs

```
go run ./cmd/tako docs --port 8090
```

→

```
📖 tako docs → http://localhost:8090
   guide (9 chapters) · reference (24 pages) · api (189 symbols)
   ^C to stop
```

Open it. Check by eye, because these are the things a test cannot judge:

- the home page offers the three sections;
- a guide chapter reads like the markdown does, with its code blocks
  intact and its sidebar showing where you are;
- a reference page's `Errors you'll meet` block is verbatim;
- the api page groups symbols under the reference page that explains
  them, in the order `reference/README.md` groups its pages.

## 3. The links, by hand

On `/reference/entity`:

- a link to another page (`entity.md` → `/reference/entity`) navigates;
- a link with an anchor lands on the section, not the top;
- a link to an example's source is **not a link** — it is the path,
  in monospace, for you to open in your own checkout.

## 4. Z7a — away from the repository

The whole point. Build the binary, take it somewhere with no tako
source, and serve:

```
go build -o /tmp/tako ./cmd/tako
cd /tmp && ./tako docs --port 8091
```

→ the same startup line, the same pages. If this step needs the
repository, the feature does not exist.

## 5. The failures

```
# with a server already on 8090
go run ./cmd/tako docs --port 8090
tako: port 8090 is already in use — pass --port to pick another

go run ./cmd/tako docs --bogus
tako: unknown flag "--bogus" (tako docs accepts --port <n> and --open)

go run ./cmd/tako docs --port abc
tako: --port wants a number between 1 and 65535, got "abc"
```

## 6. The link check earns its place

Break a link and confirm the suite says so — this is the check that
nothing had before Z7:

```
# point reference/alerts.md at a page that does not exist
go test ./pkg/docserver/ -run TestEveryLinkBetweenDocsResolves
```

→ names the file, the destination, and what is missing. Restore the
page afterwards and re-run.
