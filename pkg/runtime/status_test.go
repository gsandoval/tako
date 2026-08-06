package runtime

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	tako "github.com/gsandoval/tako"
)

// withStatusPage installs a status page for one test and takes it back
// down — it is package state, like the site renderers.
func withStatusPage(t *testing.T, fn func(tako.Ctx, tako.Status) (tako.HTML, error)) {
	t.Helper()
	RegisterStatusPage(tako.StatusPage(fn))
	t.Cleanup(func() { RegisterStatusPage(tako.StatusHandle{}) })
}

// statusSite wires a real site whose status.html says which code it is
// answering. The tests render through it rather than through a
// test-only constructor, so what they exercise is the path an app
// takes.
func statusSite(t *testing.T) *tako.SiteHandle {
	t.Helper()
	fsys := fstest.MapFS{
		"templates/layout.html": {Data: []byte(`{{ define "layout.html" }}<html><nav>the app</nav>{{ block "content" . }}{{ end }}</html>{{ end }}`)},
		"templates/status.html": {Data: []byte(`{{ define "content" }}{{ .Code }} at {{ .Path }} req {{ .RequestID }}{{ end }}`)},
	}
	RegisterSiteTemplates(fsys, SiteOptions{})
	t.Cleanup(func() { RegisterSiteTemplates(fstest.MapFS{"templates/x": {Data: []byte("")}}, SiteOptions{}) })
	return tako.Site(".")
}

// A status page can never make things worse. This is the whole failure
// policy, generalised from FL2's "a 404 never becomes a 500" to every
// response tako produces for itself — and it is what makes it safe to
// call the app's own renderer on the 500 path, which page.go used to
// refuse to do.
func TestABrokenStatusPageFallsBackToPlainText(t *testing.T) {
	cases := map[string]func(tako.Ctx, tako.Status) (tako.HTML, error){
		"it panics": func(tako.Ctx, tako.Status) (tako.HTML, error) {
			panic("status page is broken")
		},
		"it errors": func(tako.Ctx, tako.Status) (tako.HTML, error) {
			return tako.HTML{}, errors.New("template exploded")
		},
		"it renders nothing": func(tako.Ctx, tako.Status) (tako.HTML, error) {
			return tako.HTML{}, nil
		},
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			withStatusPage(t, fn)
			rec := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/gone", nil)
			writeStatus(rec, r, tako.Status{Code: http.StatusNotFound})

			if rec.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404", rec.Code)
			}
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
				t.Errorf("content-type = %q, want plain text", ct)
			}
			if body := rec.Body.String(); !strings.Contains(body, "Not Found") {
				t.Errorf("body = %q", body)
			}
		})
	}
}

// The app's page wins when it works, and it is told which status it is
// answering — one page, every code.
func TestTheStatusPageAnswersEveryCode(t *testing.T) {
	site := statusSite(t)
	withStatusPage(t, func(ctx tako.Ctx, s tako.Status) (tako.HTML, error) {
		return site.Render(ctx, "status.html", s)
	})
	for _, code := range []int{http.StatusNotFound, http.StatusForbidden,
		http.StatusMethodNotAllowed, http.StatusInternalServerError} {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/somewhere", nil)
		writeStatus(rec, r, tako.Status{Code: code})
		if rec.Code != code {
			t.Errorf("status = %d, want %d", rec.Code, code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "the app") {
			t.Errorf("%d did not wear the app: %q", code, body)
		}
		if !strings.Contains(body, "/somewhere") {
			t.Errorf("%d was not told the path: %q", code, body)
		}
	}
}

// Every tako-rendered page carries the same headers. They used to
// differ by which of four writers answered — a declared 404 resolved as
// a template got CSP and Referrer-Policy, the same page resolved as a
// static file got neither, and nothing decided that.
func TestEveryRenderedStatusCarriesTheSameHeaders(t *testing.T) {
	site := statusSite(t)
	withStatusPage(t, func(ctx tako.Ctx, s tako.Status) (tako.HTML, error) {
		return site.Render(ctx, "status.html", s)
	})
	want := map[string]string{
		"Content-Type":                        "text/html; charset=utf-8",
		"Cache-Control":                       "no-cache",
		"X-Content-Type-Options":              "nosniff",
		"Content-Security-Policy-Report-Only": "default-src 'self'",
		"Referrer-Policy":                     "strict-origin-when-cross-origin",
	}
	rec := httptest.NewRecorder()
	writeStatus(rec, httptest.NewRequest(http.MethodGet, "/x", nil), tako.Status{Code: 404})
	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("rendered page: %s = %q, want %q", k, got, v)
		}
	}
	// The static-file path is the one that used to be different.
	fsys := fstest.MapFS{"404.html": {Data: []byte("<html>static</html>")}}
	RegisterStatusPage(tako.StatusHandle{})
	srv := httptest.NewServer(SiteHandler(fsys, SiteOptions{NotFound: "404.html"}))
	t.Cleanup(srv.Close)
	res, err := http.Get(srv.URL + "/missing")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	for k, v := range want {
		if got := res.Header.Get(k); got != v {
			t.Errorf("static 404: %s = %q, want %q", k, got, v)
		}
	}
}

// A 5xx must never carry the failure's text to the visitor: the detail
// is logged against the request id, and the type is what keeps it from
// travelling.
func TestA500TellsTheStatusPageNothingItCouldLeak(t *testing.T) {
	site := statusSite(t)
	var seen tako.Status
	withStatusPage(t, func(ctx tako.Ctx, s tako.Status) (tako.HTML, error) {
		seen = s
		return site.Render(ctx, "status.html", s)
	})
	rec := httptest.NewRecorder()
	writeStatus(rec, httptest.NewRequest(http.MethodGet, "/x", nil),
		tako.Status{Code: 500, RequestID: "759268", Message: "pq: connection refused on 10.0.0.4"})
	if seen.Message != "" {
		t.Errorf("the status page was handed %q on a 500", seen.Message)
	}
	if seen.RequestID != "759268" {
		t.Errorf("request id = %q, want it kept — it is what ties the page to the log", seen.RequestID)
	}
	if strings.Contains(rec.Body.String(), "connection refused") {
		t.Error("the 500 body carries the failure text")
	}
}

// With no status page and no declared 404, the floor is plain text with
// the right code — what tako did before any of this existed.
func TestWithNothingDeclaredTheFloorIsPlainText(t *testing.T) {
	RegisterStatusPage(tako.StatusHandle{})
	fallbackNotFound = ""
	rec := httptest.NewRecorder()
	writeStatus(rec, httptest.NewRequest(http.MethodGet, "/x", nil), tako.Status{Code: 404})
	if rec.Code != 404 || !strings.Contains(rec.Body.String(), "Not Found") {
		t.Errorf("floor = %d %q", rec.Code, rec.Body.String())
	}
}
