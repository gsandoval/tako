package journal

// The W1 contract, tested the user way: the site serves with the §2.11
// headers.
//
// These run through servePages — the FULL mux, declared routes and all,
// exactly as codegen mounts it. They used to mount the SiteHandler
// alone, and that is precisely how the app shipped with its own
// stylesheet 404ing: GET /{year}/{slug} beats the site's "/" for every
// two-segment URL, so /assets/site.css reached Perma, while a test that
// mounted no routes went on passing. A site test that cannot see the
// routes in front of the site is testing something the app does not do.

import (
	"io"
	"net/http"
	"regexp"
	"testing"
)

// assetRef finds the site-absolute URLs a rendered page pulls in —
// href= and src= both, which is the stylesheet and the script.
var assetRef = regexp.MustCompile(`(?:href|src)="(/[^"]+)"`)

func shellAssets(t *testing.T, res *http.Response) []string {
	t.Helper()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, m := range assetRef.FindAllStringSubmatch(string(body), -1) {
		out = append(out, m[1])
	}
	return out
}

func TestStaticPageServesWithRevalidationHeaders(t *testing.T) {
	// W2 made / a rendered page; /about remains the static half.
	srv := servePages(t)
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
	srv := servePages(t)
	res, err := srv.Client().Get(srv.URL + "/assets/css/site.9f86d081.css")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("the stylesheet answered %d — a route in front of the site is taking it", res.StatusCode)
	}
	if cc := res.Header.Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
		t.Fatalf("asset cache-control = %q", cc)
	}
}

// Every asset the shell asks for is actually served, through the mux
// the app really has. This is the test the stylesheet regression needed:
// it reads the rendered page and follows what it references, so an
// asset that moves back under a route's depth fails here rather than in
// a browser.
func TestEveryAssetTheShellReferencesIsServed(t *testing.T) {
	srv := servePages(t)
	res, err := srv.Client().Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	refs := shellAssets(t, res)
	if len(refs) == 0 {
		t.Fatal("the shell references no assets — this test is not testing anything")
	}
	for _, ref := range refs {
		got, err := srv.Client().Get(srv.URL + ref)
		if err != nil {
			t.Fatal(err)
		}
		got.Body.Close()
		if got.StatusCode != 200 {
			t.Errorf("%s = %d (the shell references it; the site does not serve it)", ref, got.StatusCode)
		}
	}
}

func TestPrettyURLAndRedirect(t *testing.T) {
	srv := servePages(t)
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
