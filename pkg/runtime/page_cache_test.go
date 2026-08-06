package runtime

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	tako "github.com/gsandoval/tako"
)

// Route.MaxAge is the only thing that lets a rendered page be cached.
// Without it every page answers no-cache, which on a read-heavy site
// is a revalidation — and on Datastore a billed read — per visitor per
// page.
func TestAPageCachesOnlyWhenItSaysSo(t *testing.T) {
	fsys := fstest.MapFS{
		"templates/layout.html": {Data: []byte(`{{ define "layout.html" }}<html>{{ block "content" . }}{{ end }}</html>{{ end }}`)},
		"templates/p.html":      {Data: []byte(`{{ define "content" }}post{{ end }}`)},
	}
	RegisterSiteTemplates(fsys, SiteOptions{})
	t.Cleanup(func() { RegisterSiteTemplates(fstest.MapFS{"templates/x": {Data: []byte("")}}, SiteOptions{}) })
	site := tako.Site(".")
	page := tako.Page("GET /p", func(ctx tako.Ctx, _ struct{}) (tako.HTML, error) {
		return site.Render(ctx, "p.html", nil)
	})

	cases := map[string]struct {
		opts []HandlerOption
		want string
	}{
		"a page that says nothing": {nil, "no-cache"},
		"a page with MaxAge":       {[]HandlerOption{CacheFor(300)}, "public, max-age=300"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h := PageHandler(page, nil, c.opts...)
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/p", nil))
			if got := rec.Header().Get("Cache-Control"); got != c.want {
				t.Errorf("Cache-Control = %q, want %q", got, c.want)
			}
		})
	}
}

// "public" is the whole point — a private max-age would only help the
// browser that already has the page.
func TestTheCacheHeaderIsPublic(t *testing.T) {
	if got := cacheControl(60); got != "public, max-age=60" {
		t.Errorf("cacheControl(60) = %q", got)
	}
	if got := cacheControl(0); got != "no-cache" {
		t.Errorf("cacheControl(0) = %q", got)
	}
}

// A cacheable page must not mint per-visitor state.
//
// Two reasons, and the second is the one only a live deploy showed.
// Correctness: a CSRF token baked into a page a shared cache stores is
// handed to the next visitor. Reachability: Cloud Run's frontend
// downgrades `public` to `private` whenever a response carries a
// Set-Cookie, so before this a cacheable page was publicly cacheable on
// every visit EXCEPT the first — the one a shared cache actually needs.
func TestACacheablePageMintsNoCSRFCookie(t *testing.T) {
	fsys := fstest.MapFS{
		"templates/layout.html": {Data: []byte(`{{ define "layout.html" }}<html>{{ block "content" . }}{{ end }}</html>{{ end }}`)},
		"templates/p.html":      {Data: []byte(`{{ define "content" }}post{{ end }}`)},
	}
	RegisterSiteTemplates(fsys, SiteOptions{})
	t.Cleanup(func() { RegisterSiteTemplates(fstest.MapFS{"templates/x": {Data: []byte("")}}, SiteOptions{}) })
	site := tako.Site(".")
	page := tako.Page("GET /p", func(ctx tako.Ctx, _ struct{}) (tako.HTML, error) {
		return site.Render(ctx, "p.html", nil)
	})

	cached := httptest.NewRecorder()
	PageHandler(page, nil, CacheFor(300)).ServeHTTP(cached, httptest.NewRequest(http.MethodGet, "/p", nil))
	if cookies := cached.Result().Cookies(); len(cookies) > 0 {
		t.Errorf("a cacheable page set %d cookie(s): %v — Cloud Run downgrades public to private when it sees one",
			len(cookies), cookies)
	}
	if got := cached.Header().Get("Cache-Control"); got != "public, max-age=300" {
		t.Errorf("Cache-Control = %q", got)
	}

	// An ordinary page still mints it: the double-submit cookie is what
	// makes an anonymous browser's later POST verifiable.
	plain := httptest.NewRecorder()
	PageHandler(page, nil).ServeHTTP(plain, httptest.NewRequest(http.MethodGet, "/p", nil))
	if len(plain.Result().Cookies()) == 0 {
		t.Error("an uncached page minted no CSRF cookie — anonymous POSTs depend on it")
	}
}
