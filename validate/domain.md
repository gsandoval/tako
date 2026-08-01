# validate/domain.md — U1: a service answers on your domain

The claim: `tako.WithDomain("blog.example.com")` puts a service on a
custom domain with a Google-managed certificate, at no standing cost;
the two steps that are genuinely yours (proving ownership, publishing
DNS) are named before anything is built and narrated until they're done;
and the whole thing comes off cleanly.

**Setup:** run the CLI as `go run ../../cmd/tako …` from
`examples/blog` — the demanding example (a Hugo site plus dynamic
comment routes, one service, one origin).

**`<your-domain>`** is a domain you control, in a zone you can edit —
say `blog.example.com`. **`<test-project>`** is the scratch project.

**Why blog is edited, not shipped, with a domain:** a domain in the tree
would drag a DNS record and a verified base domain through `go test ./...`
and every routine deploy. Add the option for the walk, remove it after
(§8). The declaration itself is golden-tested without any of that.

## 1. The refusals, before anything is built

Add to `examples/blog/app.go`, inside the `tako.Site(...)` call:

```go
	tako.WithDomain("<your-domain>"),
```

Analysis first — these cost nothing and never touch the cloud:

```
go test ./pkg/analyzer -run 'TestErrors/domain|TestDomain'
```

→ the four rules, golden-pinned: shape (`tako/domain` — scheme, port,
trailing dot, no dot, wildcard, uppercase, bad label, bad character),
two claims on one service (`tako/domain-conflict`), and a domain in
front of a package nothing public reaches (`tako/domain-unreachable`).

**Judge the region gate** — preflight, before any build:

```
go run ../../cmd/tako deploy --project <test-project> --region europe-west2 --plan
```

→ `Cloud Run domain mappings are not available in europe-west2 — deploy
to one of asia-east1, …` and the mapping-capable list. One gap, one fix.

**Judge the ownership gate.** If `<your-domain>` is not yet verified,
the same command in a supported region stops with:

```
<your-domain> is not verified for <test-project> — prove you own it once:
gcloud domains verify <zone> (this opens Search Console), then re-run tako deploy
```

Do it: `gcloud domains verify <zone>` (the parent zone covers its
subdomains). Re-run — preflight passes.

## 2. The plan: one mapping, planned last, priced honestly

```
go run ../../cmd/tako deploy --project <test-project> --plan
```

**Judge:**
- `+ create  domain mapping <your-domain>` — and it is the LAST line
  before the counts, AFTER `run service blog`. A mapping names a live
  route; ordering is not cosmetic here.
- The ⚠ is about MATURITY, not money: *"Google ships these as preview
  and does not recommend them for production (latency); no standing
  charge."* There is no dollar warning because there is no standing
  charge — `docs/domain-options.md` is where the alternatives are priced.

## 3. Deploy: the records you must publish

```
go run ../../cmd/tako deploy --project <test-project>
```

**Judge** the domain block after the ✓ service line:

```
  domain  <your-domain>            awaiting DNS
    create these records in the DNS zone for <zone>:
      <label>                      CNAME  ghs.googlehosted.com.
    then re-run tako deploy — the certificate provisions once the
    records resolve (usually ~15 minutes, up to 24 hours).
```

Record names come back RELATIVE to the zone — that is what a registrar's
host field wants, and printing the FQDN would get `blog.example.com.example.com`
created. An apex domain shows `@` with A/AAAA records instead.

**Judge the security summary:** `public ingress on <your-domain> AND its
run.app URL (a domain mapping adds an address, it does not close one)`.
Confirm it: `curl -o /dev/null -w "%{http_code}" <run.app URL>` → 200.

## 4. Publish the records, then resume

Create them at your registrar. Then:

```
go run ../../cmd/tako deploy --project <test-project>
```

**Judge:** the plan is all skips — `0 creates, 0 updates` — and the
domain block STILL reports, because a converged deploy is exactly the
run the instructions told you to make. Repeat until:

```
  domain  <your-domain>            certificate ACTIVE → https://<your-domain>
```

Then the real proof:

```
curl -sI https://<your-domain>/ | head -3
curl -s https://<your-domain>/ | head -5
```

→ 200, a valid certificate (no `curl` TLS complaint), blog's Hugo home
page. Load it in a browser and check the padlock.

## 5. Convergence: no drift on re-deploy

Re-run deploy once more with nothing changed. **Judge:** `skip  domain
mapping <your-domain>` — never `~ update`. A mapping that reported drift
every deploy would overwrite a correct object forever; the live spec hash
must reproduce the desired one exactly (`TestDomainMappingLiveHashMatchesSpecHash`
pins the same trap offline).

## 6. Re-pointing is a REPLACE, and says so first

Move the domain to another service — declare it on a handler in a
different package instead of on blog's site — and plan:

```
go run ../../cmd/tako deploy --project <test-project> --plan
```

**Judge:** `! update  domain mapping <your-domain>` with the reason
*"the mapped service is immutable (blog → …) — delete+create; the domain
stops answering until the new certificate provisions, which can take up
to 24 hours"*, and a real deploy PROMPTS `[y/N]` before doing it.
Answer `n`: *"aborted — nothing was changed"*. Put the declaration back.

## 7. Destroy: it comes off first, and it comes off clean

```
go run ../../cmd/tako destroy --project <test-project>
```

**Judge:**
- `domain mapping <your-domain>` appears in the preview list.
- After the typed app name, `✗ domain mapping <your-domain>` is the
  FIRST deletion — before the run service it pointed at.
- The project is pristine: zero mappings, zero services.

```
gcloud run services list --project <test-project>
```

**Judge what destroy deliberately did NOT touch:** your DNS records
still exist at your registrar, and `gcloud domains list-user-verified`
still lists your domain. Both are yours, not the project's. Remove the
DNS record yourself.

## 8. Put the example back

Remove the `tako.WithDomain(...)` line from `examples/blog/app.go` and
confirm `git status` is clean under `examples/`, then:

```
go test ./...
```

## Agent-side equivalent (no domain of your own)

Everything above except §4's ACTIVE certificate and §6's live replace can
be validated with a domain whose zone is verified but which has no DNS
records: the mapping is created, sits `awaiting DNS`, prints its records,
converges on re-deploy, and destroys cleanly. That a never-resolved
mapping still deletes is itself part of the claim — check it.
