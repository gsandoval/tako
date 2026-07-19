# validate/bucket.md — Gate F3: Bucket + Blob (local only)

What this gate approves: blob streaming both directions (multipart in,
Content-Type-correct streaming out), the keyed bucket verbs, and the
inspectability of `.tako/data/`.

Prerequisite: `PORT=8090` if 8080 is busy; any image file (or
`printf 'hello' > /tmp/x.jpg` works fine).

## 1. Start

```
cd examples/gallery
PORT=8090 go run ../../cmd/tako dev
```

**Observe:** four endpoints plus resource lines `bucket photos` and
`entity photos`.

## 2. Upload via curl (multipart)

```
curl -s -F caption=cat -F 'img=@/path/to/cat.jpg;type=image/jpeg' \
  localhost:8090/api/photos
# {"id":"<16 chars>","caption":"cat"}
```

## 3. Download — streaming with the right headers

```
curl -sI localhost:8090/api/photos/<id>/image | grep -i content-
# Content-Type: image/jpeg     Content-Length: <original size>

curl -s localhost:8090/api/photos/<id>/image -o /tmp/out.jpg
cmp /path/to/cat.jpg /tmp/out.jpg && echo identical
```

Also: `curl -i localhost:8090/api/photos/nope/image` → 404 JSON error.

## 4. List objects

```
curl -s localhost:8090/api/objects
# [{"key":"…","size":…,"contentType":"image/jpeg","updated":"…"}]
```

## 5. Inspect `.tako/data/` — the gate's centerpiece

```
ls .tako/data/buckets/photos/
file .tako/data/buckets/photos/<id>     # it IS your jpeg, byte for byte
open .tako/data/buckets/photos/<id>     # macOS: opens as the image
```

Judge: does the on-disk layout explain itself? (Objects are plain files;
content types live in `.meta/`; `rm -rf .tako` resets everything.)

## 6. Analyzer errors (one at a time in app.go, revert each)

a. Remove `form:"img"` from the Img field → blob-needs-form-tag.
b. Change Upload's route to `"GET /api/photos"` → no-uploads-on-GET.
c. Add an untagged `Extra string` to UploadReq → no-mixing-blob-and-body.

All three are `tako/blob-binding`; judge each text.

## 7. Missing-file error text

```
curl -i -X POST -F caption=cat localhost:8090/api/photos
# 400 {"error":"form \"img\": missing file upload"}
```

## 8. Conformance + tests

```
cd ../.. && go test ./pkg/backends/conformance -backend=local
go test ./... && cd examples/gallery && go test ./...
```

Record the verdict in `docs/validation-log.md` (feature F3, gate F3).
