package local

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gsandoval/tako/pkg/backends"
)

// SignedURLEndpoint serves /_tako/signed/<token>: the local stand-in for
// direct-to-storage transfers. It is the enforcer of a signed URL's
// constraints — size cap, content type, expiry — with each rejection a
// distinct, exact text (they are product UX). In the cloud, GCS plays
// this role (Phase F).
func SignedURLEndpoint(signer *URLSigner, store *BucketStore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Browsers PUT signed uploads cross-origin; the token in the path
		// is the whole authorization, so a permissive CORS policy adds no
		// exposure. GCS behaves equivalently once bucket CORS is
		// provisioned (Phase F).
		w.Header().Set("Access-Control-Allow-Origin", "*")
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", "GET, PUT")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		token := strings.TrimPrefix(r.URL.Path, "/_tako/signed/")
		claims, err := signer.verify(token)
		if err != nil {
			signedError(w, http.StatusForbidden, err.Error())
			return
		}
		switch claims.Method {
		case "put":
			if r.Method != http.MethodPut {
				signedError(w, http.StatusMethodNotAllowed, "signed upload URLs accept PUT only")
				return
			}
			servePut(w, r, claims, store)
		case "get":
			if r.Method != http.MethodGet {
				signedError(w, http.StatusMethodNotAllowed, "signed download URLs accept GET only")
				return
			}
			serveGet(w, r, claims, store)
		default:
			signedError(w, http.StatusForbidden, "invalid signature")
		}
	})
}

func servePut(w http.ResponseWriter, r *http.Request, c signedClaims, store *BucketStore) {
	ct := r.Header.Get("Content-Type")
	if !contentTypeMatches(c.ContentType, ct) {
		signedError(w, http.StatusUnsupportedMediaType,
			fmt.Sprintf("content type %q does not match required %q", ct, c.ContentType))
		return
	}
	if c.MaxSize > 0 && r.ContentLength > c.MaxSize {
		signedError(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("upload is %d bytes; the signed URL allows at most %d", r.ContentLength, c.MaxSize))
		return
	}
	body := io.Reader(r.Body)
	if c.MaxSize > 0 {
		// A client can lie about (or omit) Content-Length; the stream
		// itself is capped too.
		body = &cappedReader{r: r.Body, remaining: c.MaxSize}
	}
	err := store.Put(r.Context(), c.Bucket, c.Key, backends.BlobData{
		Reader:      body,
		ContentType: ct,
		Size:        r.ContentLength,
	})
	if errors.Is(err, errTooLarge) {
		// The partial object must not survive a rejected upload.
		_ = store.Delete(r.Context(), c.Bucket, c.Key)
		signedError(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("upload exceeds the signed URL's %d-byte limit", c.MaxSize))
		return
	}
	if err != nil {
		signedError(w, http.StatusInternalServerError, "upload failed")
		return
	}
	w.WriteHeader(http.StatusOK)
}

func serveGet(w http.ResponseWriter, r *http.Request, c signedClaims, store *BucketStore) {
	data, err := store.Get(r.Context(), c.Bucket, c.Key)
	if errors.Is(err, backends.ErrNotFound) {
		signedError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		signedError(w, http.StatusInternalServerError, "download failed")
		return
	}
	if closer, ok := data.Reader.(io.Closer); ok {
		defer closer.Close()
	}
	if data.ContentType != "" {
		w.Header().Set("Content-Type", data.ContentType)
	}
	if data.Size >= 0 {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", data.Size))
	}
	_, _ = io.Copy(w, data.Reader)
}

// contentTypeMatches applies a SignedPut pattern: "" accepts anything,
// "image/*" matches the prefix before '*', anything else must match
// exactly.
func contentTypeMatches(pattern, ct string) bool {
	if pattern == "" {
		return true
	}
	if prefix, ok := strings.CutSuffix(pattern, "*"); ok {
		return strings.HasPrefix(ct, prefix)
	}
	return ct == pattern
}

var errTooLarge = errors.New("stream exceeds size cap")

// cappedReader errors with errTooLarge as soon as more than remaining
// bytes flow through it.
type cappedReader struct {
	r         io.Reader
	remaining int64
}

func (c *cappedReader) Read(p []byte) (int, error) {
	if c.remaining < 0 {
		return 0, errTooLarge
	}
	n, err := c.r.Read(p)
	c.remaining -= int64(n)
	if c.remaining < 0 {
		return 0, errTooLarge
	}
	return n, err
}

func signedError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
