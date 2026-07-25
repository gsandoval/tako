package runtime

import (
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func siteFixture() fstest.MapFS {
	return fstest.MapFS{
		"index.html":              {Data: []byte("<h1>home</h1>")},
		"about/index.html":        {Data: []byte("<h1>about</h1>")},
		"assets/app.3f2a1c9d.css": {Data: []byte("body{}")},
		"assets/plain.css":        {Data: []byte("p{}")},
		"robots.txt":              {Data: []byte("User-agent: *")},
	}
}

// TestSiteHeaders pins §2.11's header contract: fingerprinted assets
// immutable, everything else no-cache, strong ETags + 304s everywhere,
// security headers on documents only.
func TestSiteHeaders(t *testing.T) {
	h := SiteHandler(siteFixture(), SiteOptions{})

	req := func(path, inm string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest("GET", path, nil)
		if inm != "" {
			r.Header.Set("If-None-Match", inm)
		}
		h.ServeHTTP(rec, r)
		return rec
	}

	home := req("/", "")
	if home.Code != 200 || home.Body.String() != "<h1>home</h1>" {
		t.Fatalf("home = %d %q", home.Code, home.Body.String())
	}
	if cc := home.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("html cache-control = %q", cc)
	}
	if home.Header().Get("Content-Security-Policy-Report-Only") == "" ||
		home.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("document security headers missing")
	}
	etag := home.Header().Get("ETag")
	if len(etag) < 4 || etag[0] != '"' {
		t.Fatalf("etag = %q, want strong quoted tag", etag)
	}
	if not := req("/", etag); not.Code != 304 {
		t.Fatalf("If-None-Match = %d, want 304", not.Code)
	}

	asset := req("/assets/app.3f2a1c9d.css", "")
	if cc := asset.Header().Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
		t.Fatalf("fingerprinted cache-control = %q", cc)
	}
	if asset.Header().Get("Content-Security-Policy-Report-Only") != "" {
		t.Fatal("assets must not carry document security headers")
	}
	if cc := req("/assets/plain.css", "").Header().Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("plain asset cache-control = %q", cc)
	}
	if ct := req("/robots.txt", "").Header().Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Fatalf("robots content-type = %q", ct)
	}

	// Pretty URLs: extensionless paths resolve dir/index.html.
	if about := req("/about", ""); about.Code != 200 || about.Body.String() != "<h1>about</h1>" {
		t.Fatalf("pretty url = %d %q", about.Code, about.Body.String())
	}
	if missing := req("/nope", ""); missing.Code != 404 {
		t.Fatalf("unknown path = %d, want 404 (no SPA)", missing.Code)
	}
}

// TestSiteSPAAndRedirects pins the SPA fallback and 308 redirects.
func TestSiteSPAAndRedirects(t *testing.T) {
	h := SiteHandler(siteFixture(), SiteOptions{
		SPA:       true,
		Redirects: map[string]string{"/old": "/"},
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/app/anything/deep", nil))
	if rec.Code != 200 || rec.Body.String() != "<h1>home</h1>" {
		t.Fatalf("spa fallback = %d %q", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/old", nil))
	if rec.Code != 308 || rec.Header().Get("Location") != "/" {
		t.Fatalf("redirect = %d %q, want 308 /", rec.Code, rec.Header().Get("Location"))
	}

	// Traversal never escapes the FS root.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/../secret", nil))
	if rec.Code == 200 && rec.Body.String() != "<h1>home</h1>" {
		t.Fatalf("traversal served %q", rec.Body.String())
	}
}
