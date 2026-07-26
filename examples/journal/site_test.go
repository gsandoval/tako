package journal

// The W1 contract, tested the user way: the site serves with the §2.11
// headers. These tests run against the same SiteHandler the generated
// main mounts — os.DirFS(./web) here, go:embed in the binary; identical
// handler code (the local/cloud parity bar).

import (
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gsandoval/tako/pkg/runtime"
)

func siteHandler() *httptest.Server {
	return httptest.NewServer(runtime.SiteHandler(os.DirFS("web"), runtime.SiteOptions{
		Redirects: map[string]string{"/about-us": "/about"},
	}))
}

func TestStaticPageServesWithRevalidationHeaders(t *testing.T) {
	// W2 made / a rendered page; /about remains the static half.
	srv := siteHandler()
	defer srv.Close()
	res, err := srv.Client().Get(srv.URL + "/about")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 || res.Header.Get("Cache-Control") != "no-cache" {
		t.Fatalf("about = %d, cache-control %q", res.StatusCode, res.Header.Get("Cache-Control"))
	}
	if res.Header.Get("ETag") == "" || res.Header.Get("Content-Security-Policy-Report-Only") == "" {
		t.Fatal("about missing ETag or CSP-Report-Only")
	}
}

func TestFingerprintedAssetIsImmutable(t *testing.T) {
	srv := siteHandler()
	defer srv.Close()
	res, err := srv.Client().Get(srv.URL + "/assets/site.9f86d081.css")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if cc := res.Header.Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
		t.Fatalf("asset cache-control = %q", cc)
	}
}

func TestPrettyURLAndRedirect(t *testing.T) {
	srv := siteHandler()
	defer srv.Close()
	res, err := srv.Client().Get(srv.URL + "/about")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("/about = %d", res.StatusCode)
	}
	// The old address permanently forwards (the client follows; the
	// final URL is the new address).
	res, err = srv.Client().Get(srv.URL + "/about-us")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 || res.Request.URL.Path != "/about" {
		t.Fatalf("/about-us landed on %q (%d)", res.Request.URL.Path, res.StatusCode)
	}
}
