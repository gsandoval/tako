package runtime

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// The failure policy, which is the part of this feature most likely to
// matter on a bad day: a missing or broken 404 page falls back to plain
// text. A 404 never becomes a 500.
//
// The visitor already could not find what they wanted. An error page
// that errors turns "we don't have that" into "we are broken", which is
// a worse thing to say and a harder one to debug.
func TestABrokenNotFoundPageFallsBackToPlainText(t *testing.T) {
	layout := "{{ define \"layout.html\" }}<html>{{ block \"content\" . }}{{ end }}</html>{{ end }}"
	cases := map[string]fstest.MapFS{
		"the page is not there": {
			"templates/layout.html": {Data: []byte(layout)},
		},
		"the page does not parse": {
			"templates/layout.html": {Data: []byte(layout)},
			"templates/404.html":    {Data: []byte(`{{ define "content" }}{{ .Nope | nosuchfunc }}{{ end }}`)},
		},
		"the page fails at render": {
			"templates/layout.html": {Data: []byte(layout)},
			"templates/404.html":    {Data: []byte(`{{ define "content" }}{{ .Missing.Field }}{{ end }}`)},
		},
	}
	for name, fsys := range cases {
		t.Run(name, func(t *testing.T) {
			opts := SiteOptions{NotFound: "404.html"}
			RegisterSiteTemplates(fsys, opts)
			t.Cleanup(func() { RegisterSiteTemplates(fstest.MapFS{"templates/x": {Data: []byte("")}}, SiteOptions{}) })

			srv := httptest.NewServer(SiteHandler(fsys, opts))
			t.Cleanup(srv.Close)
			res, err := srv.Client().Get(srv.URL + "/nothing/here")
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			if res.StatusCode != http.StatusNotFound {
				t.Fatalf("status = %d, want 404 — a broken 404 page must not become a 500", res.StatusCode)
			}
		})
	}
}

// A generator that already emits 404.html into the site directory works
// without a template. The template is tried FIRST — an app that has both
// means the one it renders itself — and a static file is the fallback.
func TestAStaticNotFoundFileIsServedWhenThereIsNoTemplate(t *testing.T) {
	fsys := fstest.MapFS{
		"templates/layout.html": {Data: []byte(`{{ define "layout.html" }}<html>{{ block "content" . }}{{ end }}</html>{{ end }}`)},
		"404.html":              {Data: []byte("<!doctype html><p>generator-made 404</p>")},
	}
	opts := SiteOptions{NotFound: "404.html"}
	RegisterSiteTemplates(fsys, opts)
	t.Cleanup(func() { RegisterSiteTemplates(fstest.MapFS{"templates/x": {Data: []byte("")}}, SiteOptions{}) })

	srv := httptest.NewServer(SiteHandler(fsys, opts))
	t.Cleanup(srv.Close)
	res, err := srv.Client().Get(srv.URL + "/nothing/here")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body := make([]byte, 512)
	n, _ := res.Body.Read(body)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", res.StatusCode)
	}
	if !strings.Contains(string(body[:n]), "generator-made 404") {
		t.Errorf("static 404 not served: %q", body[:n])
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("content-type = %q", ct)
	}
}

// Declaring nothing keeps today's behaviour exactly. The feature is opt
// in, and an app that never asked for it must not start rendering
// something.
func TestWithoutNotFoundDeclaredThe404StaysPlain(t *testing.T) {
	fsys := fstest.MapFS{"index.html": {Data: []byte("home")}}
	srv := httptest.NewServer(SiteHandler(fsys, SiteOptions{}))
	t.Cleanup(srv.Close)
	res, err := srv.Client().Get(srv.URL + "/nothing/here")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("content-type = %q, want plain text — the feature is opt-in", ct)
	}
}
