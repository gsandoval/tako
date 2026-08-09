package runtime

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	tako "github.com/gsandoval/tako"
)

// The header contract, one row per KIND of response tako writes.
//
// This test exists because the contract has already drifted twice, both
// times invisibly. FL11 found four writers and three header sets, where
// a declared 404 carried CSP when it resolved as a template and not
// when it resolved as a static file — and nothing had decided that.
// FL17 found the second: securityHeaders was the only writer of
// nosniff, the site handler called it inside an HTML-only branch, and
// every .css and .js it served went out without one — while
// contentTypeOf's own comment said "nosniff is set — types must be
// right".
//
// A per-feature test could not catch either, because each writer was
// correct about the case it was written for. What was missing is the
// comparison. So: every kind in one table, and a new writer that
// invents a fourth header set has to come here and say so.
func TestEveryResponseKindCarriesItsHeaders(t *testing.T) {
	for _, tc := range []struct {
		kind string
		res  func(t *testing.T) *httptest.ResponseRecorder
		// want and wantAbsent are exact: a header named in neither is
		// not asserted, one named in both is a typo.
		want       map[string]string
		wantAbsent []string
	}{
		{
			kind: "rendered page",
			res:  func(t *testing.T) *httptest.ResponseRecorder { return renderPage(t, 0) },
			want: map[string]string{
				"Content-Type":            "text/html; charset=utf-8",
				"X-Content-Type-Options":  "nosniff",
				"Cache-Control":           "no-cache",
				"Referrer-Policy":         "strict-origin-when-cross-origin",
				"Content-Security-Policy-Report-Only": "default-src 'self'",
			},
		},
		{
			kind: "rendered page with MaxAge",
			res:  func(t *testing.T) *httptest.ResponseRecorder { return renderPage(t, 300) },
			want: map[string]string{
				"Cache-Control":          "public, max-age=300",
				"X-Content-Type-Options": "nosniff",
			},
		},
		{
			kind: "fragment",
			res:  fragmentResponse,
			want: map[string]string{
				"Content-Type":           "text/html; charset=utf-8",
				"X-Content-Type-Options": "nosniff",
			},
			// A fragment is injected into a document that already has
			// its own privileges; declaring a second set here would
			// describe nothing.
			wantAbsent: []string{"Content-Security-Policy-Report-Only", "Referrer-Policy"},
		},
		{
			kind: "site file, HTML",
			res:  func(t *testing.T) *httptest.ResponseRecorder { return siteFile(t, "/") },
			want: map[string]string{
				"Content-Type":            "text/html; charset=utf-8",
				"X-Content-Type-Options":  "nosniff",
				"Cache-Control":           "no-cache",
				"Content-Security-Policy-Report-Only": "default-src 'self'",
			},
		},
		{
			// The FL17 row. This one carried no nosniff at all.
			kind: "site file, not HTML",
			res:  func(t *testing.T) *httptest.ResponseRecorder { return siteFile(t, "/assets/plain.css") },
			want: map[string]string{
				"Content-Type":           "text/css; charset=utf-8",
				"X-Content-Type-Options": "nosniff",
				"Cache-Control":          "no-cache",
			},
			wantAbsent: []string{"Content-Security-Policy-Report-Only", "Referrer-Policy"},
		},
		{
			kind: "site file, fingerprinted",
			res:  func(t *testing.T) *httptest.ResponseRecorder { return siteFile(t, "/assets/app.3f2a1c9d.css") },
			want: map[string]string{
				"X-Content-Type-Options": "nosniff",
				"Cache-Control":          "public, max-age=31536000, immutable",
			},
		},
		{
			// The other FL17 row: bytes from outside the program.
			kind: "blob",
			res:  func(t *testing.T) *httptest.ResponseRecorder { return serveBlob(t, 0) },
			want: map[string]string{
				"Content-Type":           "image/png",
				"X-Content-Type-Options": "nosniff",
				"Cache-Control":          "no-cache",
			},
			wantAbsent: []string{"Content-Security-Policy-Report-Only"},
		},
		{
			kind: "blob with MaxAge",
			res:  func(t *testing.T) *httptest.ResponseRecorder { return serveBlob(t, 86400) },
			want: map[string]string{
				"X-Content-Type-Options": "nosniff",
				"Cache-Control":          "public, max-age=86400",
			},
		},
		{
			kind: "status page",
			res:  func(t *testing.T) *httptest.ResponseRecorder { return statusResponse(t, true) },
			want: map[string]string{
				"Content-Type":            "text/html; charset=utf-8",
				"X-Content-Type-Options":  "nosniff",
				"Cache-Control":           "no-cache",
				"Content-Security-Policy-Report-Only": "default-src 'self'",
			},
		},
		{
			kind: "status, plain-text floor",
			res:  func(t *testing.T) *httptest.ResponseRecorder { return statusResponse(t, false) },
			want: map[string]string{
				"Content-Type":           "text/plain; charset=utf-8",
				"X-Content-Type-Options": "nosniff",
				"Cache-Control":          "no-cache",
			},
		},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			rec := tc.res(t)
			for name, want := range tc.want {
				if got := rec.Header().Get(name); got != want {
					t.Errorf("%s = %q, want %q", name, got, want)
				}
			}
			for _, name := range tc.wantAbsent {
				if got := rec.Header().Get(name); got != "" {
					t.Errorf("%s = %q, want absent", name, got)
				}
			}
		})
	}
}

// nosniff is the one header EVERY kind carries, so it gets its own
// statement: a response tako writes never invites the browser to
// second-guess the type it declared.
func TestNosniffHasNoExceptions(t *testing.T) {
	for kind, res := range map[string]func(t *testing.T) *httptest.ResponseRecorder{
		"page":         func(t *testing.T) *httptest.ResponseRecorder { return renderPage(t, 0) },
		"fragment":     fragmentResponse,
		"site html":    func(t *testing.T) *httptest.ResponseRecorder { return siteFile(t, "/") },
		"site css":     func(t *testing.T) *httptest.ResponseRecorder { return siteFile(t, "/assets/plain.css") },
		"site txt":     func(t *testing.T) *httptest.ResponseRecorder { return siteFile(t, "/robots.txt") },
		"blob":         func(t *testing.T) *httptest.ResponseRecorder { return serveBlob(t, 0) },
		"status page":  func(t *testing.T) *httptest.ResponseRecorder { return statusResponse(t, true) },
		"status plain": func(t *testing.T) *httptest.ResponseRecorder { return statusResponse(t, false) },
	} {
		t.Run(kind, func(t *testing.T) {
			if got := res(t).Header().Get("X-Content-Type-Options"); got != "nosniff" {
				t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
			}
		})
	}
}

// --- the kinds -------------------------------------------------------

// headerSite is the declared site these fixtures render through — the
// same handle an app holds.
var headerSite = tako.Site(".")

func headerTemplates(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		RegisterSiteTemplates(fstest.MapFS{"templates/x": {Data: []byte("")}}, SiteOptions{})
	})
	RegisterSiteTemplates(fstest.MapFS{
		"templates/layout.html": {Data: []byte(`{{ define "layout.html" }}<html>{{ block "content" . }}{{ end }}</html>{{ end }}`)},
		"templates/p.html":      {Data: []byte(`{{ define "content" }}<p>page</p>{{ end }}`)},
		"templates/frag.html":   {Data: []byte(`<p>fragment</p>`)},
	}, SiteOptions{})
}

func renderPage(t *testing.T, maxAge int) *httptest.ResponseRecorder {
	t.Helper()
	headerTemplates(t)
	e := tako.Page("GET /p", func(ctx tako.Ctx, _ struct{}) (tako.HTML, error) {
		return headerSite.Render(ctx, "p.html", nil)
	})
	var opts []HandlerOption
	if maxAge > 0 {
		opts = append(opts, CacheFor(maxAge))
	}
	rec := httptest.NewRecorder()
	PageHandler(e, nil, opts...).ServeHTTP(rec, httptest.NewRequest("GET", "/p", nil))
	if rec.Code != 200 {
		t.Fatalf("page = %d %q", rec.Code, rec.Body)
	}
	return rec
}

func fragmentResponse(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	headerTemplates(t)
	e := tako.Page("GET /f", func(ctx tako.Ctx, _ struct{}) (tako.HTML, error) {
		return headerSite.Fragment(ctx, "frag.html", nil)
	})
	rec := httptest.NewRecorder()
	PageHandler(e, nil).ServeHTTP(rec, httptest.NewRequest("GET", "/f", nil))
	if rec.Code != 200 {
		t.Fatalf("fragment = %d %q", rec.Code, rec.Body)
	}
	return rec
}

func siteFile(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	SiteHandler(siteFixture(), SiteOptions{}).ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	if rec.Code != 200 {
		t.Fatalf("site %s = %d", path, rec.Code)
	}
	return rec
}

func serveBlob(t *testing.T, maxAge int) *httptest.ResponseRecorder {
	t.Helper()
	e := tako.API("GET /media", func(ctx tako.Ctx, _ struct{}) (tako.Blob, error) {
		return tako.Blob{
			ContentType: "image/png",
			Size:        4,
			Reader:      io.NopCloser(strings.NewReader("\x89PNG")),
		}, nil
	})
	var opts []HandlerOption
	if maxAge > 0 {
		opts = append(opts, CacheFor(maxAge))
	}
	rec := httptest.NewRecorder()
	BlobHandler(e, nil, opts...).ServeHTTP(rec, httptest.NewRequest("GET", "/media", nil))
	if rec.Code != 200 {
		t.Fatalf("blob = %d %q", rec.Code, rec.Body)
	}
	return rec
}

// statusResponse renders a 404 through the declared page, or through
// the plain-text floor when there is none.
func statusResponse(t *testing.T, declared bool) *httptest.ResponseRecorder {
	t.Helper()
	prev := statusPage
	t.Cleanup(func() { statusPage = prev })
	if declared {
		headerTemplates(t)
		statusPage = tako.StatusPage(func(ctx tako.Ctx, s tako.Status) (tako.HTML, error) {
			return headerSite.Render(ctx, "p.html", nil)
		})
	} else {
		statusPage = tako.StatusHandle{}
	}
	rec := httptest.NewRecorder()
	writeStatus(rec, httptest.NewRequest("GET", "/gone", nil), tako.Status{Code: http.StatusNotFound})
	if rec.Code != 404 {
		t.Fatalf("status = %d", rec.Code)
	}
	return rec
}
