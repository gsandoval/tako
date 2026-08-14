package runtime

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	tako "github.com/gsandoval/tako"
)

// maxMultipartMemory is how much of a multipart form is held in memory
// before spilling to temp files (the stream still has no hard size cap —
// signed URLs with WithMaxSize are the story for large uploads, F6).
const maxMultipartMemory = 32 << 20

// BlobHandler adapts a blob-returning endpoint to net/http: the result
// streams with its own Content-Type instead of being JSON-encoded.
// Errors surface exactly like JSONHandler's.
func BlobHandler[Req any](e tako.Endpoint[Req, tako.Blob], decode func(*http.Request) (Req, error), opts ...HandlerOption) http.Handler {
	var conf handlerConf
	for _, o := range opts {
		o(&conf)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// incomingRequestID, not newRequestID: a propagated trace id
		// belongs to the chain that started it. This wrapper minted a
		// fresh one, so a blob route was the point where `tako logs`
		// stopped being able to follow a request across services —
		// nothing about returning bytes instead of JSON justifies that.
		reqID := incomingRequestID(r)
		session := loadSession(r)
		if conf.requireAuth && session == nil {
			http.Error(w, authRequired, http.StatusUnauthorized)
			return
		}
		// And the same cross-service context the other two build: the
		// session id and trace id an outgoing Call re-propagates, plus
		// the caller's deadline bounding this handler. Without it a blob
		// handler's own Calls arrived anonymous and unbounded.
		base, cancel := callContext(r, session, reqID)
		defer cancel()
		ctx := &requestCtx{
			Context: base,
			log:     requestLogger(r, reqID),
			client:  clientOf(r),
		}

		if session != nil {
			u := session.user()
			ctx.user = &u
		}
		var req Req
		if decode != nil {
			var err error
			if req, err = decode(r); err != nil {
				writeError(w, ctx, reqID, err)
				return
			}
		}
		blob, err := e.Call(ctx, req)
		if err != nil {
			writeError(w, ctx, reqID, err)
			return
		}
		ct := blob.ContentType
		if ct == "" {
			ct = "application/octet-stream"
		}
		h := w.Header()
		h.Set("Content-Type", ct)
		// A blob is the one response whose BYTES came from outside the
		// program — an upload, usually, stored and handed back. That
		// makes the declared type a claim about someone else's data,
		// and nosniff is what stops a browser from going looking for a
		// better answer than the one it was given.
		h.Set("X-Content-Type-Options", "nosniff")
		// Route.MaxAge, the same field pages read. A blob route that
		// declares none answers no-cache, which is what every blob
		// answered by omission before this — so nothing starts being
		// cached that was not asked to be.
		h.Set("Cache-Control", cacheControl(conf.maxAge))
		// The validator, and the answer to a reader that already has
		// these bytes (FL25). The site path has always done this for
		// static files; a blob route sent nothing, so no-cache meant
		// "download it all again" rather than "ask first" — 758 KB of
		// photograph per view, over whatever network the reader is on.
		//
		// Route.MaxAge cannot do this job: a lifetime keeps serving a
		// REPLACED object until it expires, while a validator changes
		// with the bytes.
		if blob.ETag != "" {
			h.Set("ETag", blob.ETag)
			if etagMatches(r.Header.Get("If-None-Match"), blob.ETag) {
				// No body, no Content-Length describing one, and the
				// reader closed: a GCS response body left open is a
				// leaked connection.
				closeBlob(blob.Reader)
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}
		if blob.Size >= 0 {
			h.Set("Content-Length", strconv.FormatInt(blob.Size, 10))
		}
		if blob.Reader != nil {
			_, _ = io.Copy(w, blob.Reader)
			closeBlob(blob.Reader)
		}
	})
}

func closeBlob(r io.Reader) {
	if c, ok := r.(io.Closer); ok {
		_ = c.Close()
	}
}

// etagMatches implements If-None-Match against one entity tag: a
// comma-separated list, "*" for "any representation I hold", and the
// weak comparison RFC 9110 requires here — W/"x" and "x" identify the
// same bytes for revalidation, and only a Range request needs the strong
// form, which tako does not serve.
func etagMatches(header, etag string) bool {
	if header == "" || etag == "" {
		return false
	}
	if strings.TrimSpace(header) == "*" {
		return true
	}
	want := strings.TrimPrefix(etag, "W/")
	for _, candidate := range strings.Split(header, ",") {
		if strings.TrimPrefix(strings.TrimSpace(candidate), "W/") == want {
			return true
		}
	}
	return false
}

// FormBlob binds a multipart form file field as a tako.Blob, streaming
// from the request. Its error texts surface as 400 bodies.
func FormBlob(r *http.Request, name string) (tako.Blob, error) {
	if err := r.ParseMultipartForm(maxMultipartMemory); err != nil {
		return tako.Blob{}, &bindError{msg: "body: invalid multipart form"}
	}
	f, header, err := r.FormFile(name)
	if err != nil {
		return tako.Blob{}, &bindError{msg: fmt.Sprintf("form %q: missing file upload", name)}
	}
	return tako.Blob{
		Reader:      f,
		ContentType: header.Header.Get("Content-Type"),
		Size:        header.Size,
	}, nil
}
