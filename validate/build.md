# validate/build.md — Gate C1: Build + push (partial cycle)

What this gate approves: the first cloud artifact. A digest-pinned,
deterministic, distroless image built with no Docker daemon — pushed to
YOUR test project's Artifact Registry, pulled by digest, and serving the
same app `tako dev` serves. No deploy yet (C2).

Prereqs: a dedicated GCP test project with an Artifact Registry docker
repo (e.g. `us-central1-docker.pkg.dev/<project>/tako`), `gcloud auth
login` (or ADC), and docker for the run step.

## 1. Local build — no daemon, no cloud

```
cd examples/hello
tako build          # (or: go run ../../cmd/tako build)
tako build          # run it AGAIN
```

**Observe:** static linux/amd64 binary → `base gcr.io/distroless/
static-debian12@sha256:…` (tag resolved and PINNED) → OCI layout at
`.tako/images/hello`. **Judge the determinism:** the second run prints
the IDENTICAL `✓ hello sha256:…` digest — same source, same image, the
property C2's did-anything-change reconcile stands on.

Also: `cd ../shortlink && tako build` — the app service builds; the
worker service prints
`skipping shortlink-countclick (async-only — its per-service main arrives with cloud async)`
(never silent).

## 2. Push — pinned, to your registry

```
cd ../hello
REPO=us-central1-docker.pkg.dev/<project>/tako     # your repo
tako build --push $REPO
```

**Observe:** `✓ hello  $REPO/hello@sha256:…` — the reference C2 will
deploy. No `:latest` anywhere. (Auth comes from gcloud/ADC; with no
credentials the error tells you exactly that.)

## 3. THE point: run the pushed artifact by digest

```
docker run --rm -e PORT=8080 -p 8080:8080 $REPO/hello@sha256:<digest-from-§2>
curl localhost:8080/hello
```

**Judge:** the image pulled BY PIN serves the same responses `tako dev`
serves. Ctrl-C when done.

## 4. Inspect the artifact

```
docker inspect $REPO/hello@sha256:<digest> | jq '.[0].Config | {User, Entrypoint, Labels}'
docker inspect $REPO/hello@sha256:<digest> | jq '.[0].RootFS.Layers | length'
```

**Observe:** `User: "nonroot"`, `Entrypoint: ["/app/hello"]`, labels
`tako.app` / `tako.service`; layer count = distroless's few + exactly
one appended layer (the binary). No shell, no package manager, no
source in the image.

## 5. Everything else still green

```
cd ../.. && go test ./pkg/build/ -count=1   # in-memory-registry suite: push/pull-back, determinism, static ELF, nonroot inheritance
go test ./...
```

Record the verdict in `docs/validation-log.md` (feature C1, gate C1).
