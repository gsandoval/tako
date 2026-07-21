# validate/signed.md — Gate F6: Signed URLs (local only)

What this gate approves: the direct-transfer flow's feel — ticket →
PUT straight to storage → the event spine takes over — and above all
*the rejection texts when a client sends the wrong thing*.

Prerequisite: `PORT=8090`; three files:

```
printf 'FAKEJPEG' > /tmp/cat.jpg
printf 'notes' > /tmp/notes.txt
dd if=/dev/zero of=/tmp/big.bin bs=1m count=21 2>/dev/null
```

## 1. Start

```
cd examples/gallery && rm -rf .tako
PORT=8090 go run ../../cmd/tako dev
```

**Observe:** `5 services, 7 endpoints, 1 worker, 3 subscribers`, and in
the generated `.tako/gen/gallery/main.go` the explicit mount:
`mux.Handle("/_tako/signed/", runtime.SignedURLHandler())` — no hidden
routes.

## 2. Ticket → direct PUT → the spine takes over

```
curl -s -X POST localhost:8090/api/uploads -d '{"caption":"cat"}'
```

**Observe:** `{"photo":{...,"ready":false},"uploadURL":"http://localhost:8090/_tako/signed/<token>"}`.
Then PUT the bytes straight to storage (use YOUR url):

```
curl -s -X PUT --data-binary @/tmp/cat.jpg -H 'Content-Type: image/jpeg' '<uploadURL>'
```

**Watch the console:** the familiar F5 chain fires off the signed
upload — `⚙ photos.Uploaded → OnUploaded ok`, `⚙ thumbnails → MakeThumb
ok`, both `photo-ready` subscribers. `curl -s localhost:8090/api/photos`
→ `"ready":true`. The handler never proxied a byte.

## 3. Rejections (the centerpiece)

Wrong type — mint a ticket, PUT the .txt:

```
curl -s -X PUT --data-binary @/tmp/notes.txt -H 'Content-Type: text/plain' '<uploadURL>'
  → 415 {"error":"content type \"text/plain\" does not match required \"image/*\""}
```

Oversized — PUT the 21MB file:

```
curl -s -X PUT --data-binary @/tmp/big.bin -H 'Content-Type: image/jpeg' '<uploadURL>'
  → 413 {"error":"upload is 22020096 bytes; the signed URL allows at most 20971520"}
```

Tampered — append a character to the token:

```
curl -s -X PUT --data-binary @/tmp/cat.jpg -H 'Content-Type: image/jpeg' '<uploadURL>x'
  → 403 {"error":"invalid signature"}
```

Judge: would a client-side engineer know exactly what to fix from each
of these bodies alone? Confirm the rejected uploads left no objects
(`curl -s localhost:8090/api/objects`) and no pipeline noise in the
console.

## 4. Signed download

Use the photo id from §2 (or pick one from
`curl -s localhost:8090/api/photos`). The endpoint returns the signed
URL as a JSON string — quoted — so unwrap it with `jq -r` (or strip the
quotes by hand) before curling it:

```
ID=$(curl -s localhost:8090/api/photos | jq -r '.[0].id')
DL=$(curl -s localhost:8090/api/photos/$ID/download | jq -r .)
echo "$DL"                # http://localhost:8090/_tako/signed/<token>
curl -si "$DL" | head -4  # 200, bytes stream, correct Content-Type + Length
curl -s -X PUT "$DL"      # → 405 {"error":"signed download URLs accept GET only"}
```

## 5. URLs survive reloads

Save any app file to force a reload (or restart tako dev), then re-PUT
a fresh image to the ORIGINAL §2 url: still accepted — the signing key
persists at `.tako/data/signing.key`.

## 6. Conformance + tests

```
cd ../.. && go test ./pkg/backends/conformance -backend=local
go test ./... && cd examples/gallery && go test ./...
```

Record the verdict in `docs/validation-log.md` (feature F6, gate F6).
