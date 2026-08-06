package runtime

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	tako "github.com/gsandoval/tako"
)

// Every HTML document tako writes carries the same security posture,
// whichever code path answered.
//
// This is the property FL11 was cleaning up after and did not pin: a
// declared 404 got the CSP when it resolved as a TEMPLATE and not when
// it resolved as a STATIC FILE, because the two writers each kept their
// own copy of the list. FL11 unified the writers; the list stayed in
// three places (a page, the site, a status) until this test.
func TestEveryHTMLDocumentCarriesTheSameSecurityHeaders(t *testing.T) {
	want := map[string]string{
		"X-Content-Type-Options":              "nosniff",
		"Content-Security-Policy-Report-Only": "default-src 'self'",
		"Referrer-Policy":                     "strict-origin-when-cross-origin",
	}

	fsys := fstest.MapFS{
		"index.html":            {Data: []byte("<html>home</html>")},
		"templates/layout.html": {Data: []byte(`{{ define "layout.html" }}<html>{{ block "content" . }}{{ end }}</html>{{ end }}`)},
		"templates/p.html":      {Data: []byte(`{{ define "content" }}post{{ end }}`)},
	}
	RegisterSiteTemplates(fsys, SiteOptions{})
	t.Cleanup(func() { RegisterSiteTemplates(fstest.MapFS{"templates/x": {Data: []byte("")}}, SiteOptions{}) })
	site := tako.Site(".")

	// 1. A page handler's document.
	page := tako.Page("GET /p", func(ctx tako.Ctx, _ struct{}) (tako.HTML, error) {
		return site.Render(ctx, "p.html", nil)
	})
	pageRec := httptest.NewRecorder()
	PageHandler(page, nil).ServeHTTP(pageRec, httptest.NewRequest(http.MethodGet, "/p", nil))

	// 2. A static document from the site.
	siteRec := httptest.NewRecorder()
	SiteHandler(fsys, SiteOptions{}).ServeHTTP(siteRec, httptest.NewRequest(http.MethodGet, "/", nil))

	// 3. A status tako answered itself.
	statusRec := httptest.NewRecorder()
	RegisterStatusPage(tako.StatusPage(func(ctx tako.Ctx, s tako.Status) (tako.HTML, error) {
		return site.Render(ctx, "p.html", nil)
	}))
	t.Cleanup(func() { RegisterStatusPage(tako.StatusHandle{}) })
	writeStatus(statusRec, httptest.NewRequest(http.MethodGet, "/gone", nil), tako.Status{Code: 404})

	for name, rec := range map[string]*httptest.ResponseRecorder{
		"page": pageRec, "site": siteRec, "status": statusRec,
	} {
		for k, v := range want {
			if got := rec.Header().Get(k); got != v {
				t.Errorf("%s: %s = %q, want %q", name, k, got, v)
			}
		}
	}
}

// A fragment is a partial swapped into a document that already declares
// a CSP and a referrer policy, so it carries neither — but it still
// says how its own bytes are typed.
func TestAFragmentCarriesOnlyTheHeaderThatIsAboutItself(t *testing.T) {
	h := http.Header{}
	securityHeaders(h, true)
	if got := h.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("fragment: X-Content-Type-Options = %q", got)
	}
	for _, k := range []string{"Content-Security-Policy-Report-Only", "Referrer-Policy"} {
		if got := h.Get(k); got != "" {
			t.Errorf("fragment carries %s = %q — it belongs to the document it is swapped into", k, got)
		}
	}
}
