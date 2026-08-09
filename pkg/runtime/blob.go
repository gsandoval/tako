package runtime

import (
	"fmt"
	"io"
	"net/http"
	"strconv"

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
		reqID := newRequestID()
		session := loadSession(r)
		if conf.requireAuth && session == nil {
			http.Error(w, authRequired, http.StatusUnauthorized)
			return
		}
		ctx := &requestCtx{
			Context: r.Context(),
			log:     requestLogger(r, reqID),
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
		if blob.Size >= 0 {
			h.Set("Content-Length", strconv.FormatInt(blob.Size, 10))
		}
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
		if blob.Reader != nil {
			_, _ = io.Copy(w, blob.Reader)
			if c, ok := blob.Reader.(io.Closer); ok {
				_ = c.Close()
			}
		}
	})
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
