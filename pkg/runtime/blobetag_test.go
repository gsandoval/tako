package runtime

// FL25: a blob route could not be revalidated.
//
// The site path computes a strong content ETag for every static file and
// answers 304; the blob path — the same runtime, one file over — sent
// none, so `no-cache` on a photograph meant "fetch all 758 KB again"
// rather than "ask first". Measured on a real deployment:
//
//	/assets/site/css/site.css   cache-control: no-cache   etag: "80df747330fe7cf6"
//	/assets/media/….jpg         cache-control: no-cache   etag: NONE
//
// Route.MaxAge is the wrong instrument for this and was refused by the
// app that filed it: a freshness lifetime serves a REPLACED image stale
// under a published article. A validator cannot — different bytes,
// different ETag.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tako "github.com/gsandoval/tako"
)

// closeSpy is a reader that records being closed, because the 304 path
// returns without copying and a GCS body left open is a leaked
// connection.
type closeSpy struct {
	io.Reader
	closed bool
}

func (c *closeSpy) Close() error { c.closed = true; return nil }

func blobRoute(t *testing.T, body *closeSpy, etag string) http.Handler {
	t.Helper()
	e := tako.API("GET /media/{key}", func(ctx tako.Ctx, _ struct{}) (tako.Blob, error) {
		return tako.Blob{Reader: body, ContentType: "image/jpeg", Size: -1, ETag: etag}, nil
	})
	return BlobHandler(e, nil)
}

func TestABlobCarriesItsValidator(t *testing.T) {
	body := &closeSpy{Reader: strings.NewReader("the bytes")}
	w := httptest.NewRecorder()
	blobRoute(t, body, `"9f8c"`).ServeHTTP(w, httptest.NewRequest("GET", "/media/harvest.jpg", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if got := w.Header().Get("ETag"); got != `"9f8c"` {
		t.Errorf("ETag = %q — without it no-cache means download it all again, every time", got)
	}
	if w.Body.String() != "the bytes" {
		t.Errorf("body = %q", w.Body.String())
	}
}

func TestAMatchingIfNoneMatchAnswers304(t *testing.T) {
	body := &closeSpy{Reader: strings.NewReader("the bytes")}
	r := httptest.NewRequest("GET", "/media/harvest.jpg", nil)
	r.Header.Set("If-None-Match", `"9f8c"`)
	w := httptest.NewRecorder()
	blobRoute(t, body, `"9f8c"`).ServeHTTP(w, r)

	if w.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304 — the reader already has these bytes", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Errorf("304 carried a body of %d bytes", w.Body.Len())
	}
	// RFC 9110: a 304 must not carry Content-Length describing a body it
	// is not sending.
	if got := w.Header().Get("Content-Length"); got != "" {
		t.Errorf("304 declared Content-Length %q", got)
	}
	if got := w.Header().Get("ETag"); got != `"9f8c"` {
		t.Errorf("304 dropped the ETag (%q) — the reader needs it for the next request", got)
	}
	if !body.closed {
		t.Error("the blob's reader was left open on the 304 path — a GCS body is a connection")
	}
}

func TestANonMatchingIfNoneMatchStillSendsTheBytes(t *testing.T) {
	body := &closeSpy{Reader: strings.NewReader("the new bytes")}
	r := httptest.NewRequest("GET", "/media/harvest.jpg", nil)
	r.Header.Set("If-None-Match", `"stale"`) // what the reader holds
	w := httptest.NewRecorder()
	blobRoute(t, body, `"9f8c"`).ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — the object was replaced", w.Code)
	}
	if w.Body.String() != "the new bytes" {
		t.Errorf("body = %q", w.Body.String())
	}
}

// A browser sends back every validator it holds for the URL, and a
// wildcard when it holds any.
func TestIfNoneMatchAcceptsAListAndAStar(t *testing.T) {
	for _, header := range []string{`"other", "9f8c"`, `W/"9f8c"`, `*`} {
		body := &closeSpy{Reader: strings.NewReader("the bytes")}
		r := httptest.NewRequest("GET", "/media/harvest.jpg", nil)
		r.Header.Set("If-None-Match", header)
		w := httptest.NewRecorder()
		blobRoute(t, body, `"9f8c"`).ServeHTTP(w, r)
		if w.Code != http.StatusNotModified {
			t.Errorf("If-None-Match: %s → %d, want 304", header, w.Code)
		}
	}
}

// Every blob an app builds itself, until it opts in. Nothing about the
// response changes, and a conditional request is answered with the bytes
// rather than a 304 tako cannot justify.
func TestABlobWithoutAnETagIsUntouched(t *testing.T) {
	body := &closeSpy{Reader: strings.NewReader("generated")}
	r := httptest.NewRequest("GET", "/media/harvest.jpg", nil)
	r.Header.Set("If-None-Match", `*`)
	w := httptest.NewRecorder()
	blobRoute(t, body, "").ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: nothing identifies these bytes", w.Code)
	}
	if got := w.Header().Get("ETag"); got != "" {
		t.Errorf("an ETag appeared from nowhere: %q", got)
	}
	if w.Body.String() != "generated" {
		t.Errorf("body = %q", w.Body.String())
	}
}
