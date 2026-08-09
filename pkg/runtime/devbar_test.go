package runtime

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	tako "github.com/gsandoval/tako"
)

// FL18: the dev bar belongs on documents, and an app can move it.
//
// Two friction entries, both local-only and both invisible in
// production. The bar is injected into every text/html response under
// tako dev, and it could not tell a page from an htmx partial: a
// fragment has no </body>, so the bar landed at the END of whatever
// was swapped in — a position:fixed strip appearing inside the target,
// once per swap, stacking with the real one.
func TestTheDevBarLandsOnPagesAndNotOnFragments(t *testing.T) {
	devBarTemplates(t)

	page := tako.Page("GET /p", func(ctx tako.Ctx, _ struct{}) (tako.HTML, error) {
		return devBarSite.Render(ctx, "p.html", nil)
	})
	frag := tako.Page("GET /f", func(ctx tako.Ctx, _ struct{}) (tako.HTML, error) {
		return devBarSite.Fragment(ctx, "frag.html", nil)
	})
	mux := http.NewServeMux()
	mux.Handle("GET /p", PageHandler(page, nil))
	mux.Handle("GET /f", PageHandler(frag, nil))
	mux.Handle("GET /data", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	h := WithDevBar(mux)

	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		return rec
	}

	t.Run("a page carries exactly one bar, inside the document", func(t *testing.T) {
		body := get("/p").Body.String()
		if n := strings.Count(body, `id="tako-dev-bar"`); n != 1 {
			t.Fatalf("page carries %d bars, want 1:\n%s", n, body)
		}
		if strings.Index(body, `id="tako-dev-bar"`) > strings.Index(body, "</body>") {
			t.Errorf("the bar was appended after </body>:\n%s", body)
		}
	})

	t.Run("a fragment carries none", func(t *testing.T) {
		body := get("/f").Body.String()
		if strings.Contains(body, "tako-dev-bar") || strings.Contains(body, "dev_user") {
			t.Errorf("the dev bar rode along on an htmx swap:\n%s", body)
		}
		// And the fragment still ARRIVES — skipping injection must not
		// mean skipping the body.
		if !strings.Contains(body, "<p>fragment</p>") {
			t.Errorf("the fragment body was dropped:\n%s", body)
		}
	})

	t.Run("a non-HTML response is untouched", func(t *testing.T) {
		if body := get("/data").Body.String(); body != `{"ok":true}` {
			t.Errorf("JSON response = %q", body)
		}
	})
}

// The id is the contract an app targets. Hiding the bar is one CSS rule
// — which is the whole ask, since any app with its own fixed bottom bar
// hits this and would otherwise guess at a selector over inline styles.
func TestTheDevBarHasAStableHook(t *testing.T) {
	devBarTemplates(t)
	page := tako.Page("GET /p", func(ctx tako.Ctx, _ struct{}) (tako.HTML, error) {
		return devBarSite.Render(ctx, "p.html", nil)
	})
	rec := httptest.NewRecorder()
	WithDevBar(PageHandler(page, nil)).ServeHTTP(rec, httptest.NewRequest("GET", "/p", nil))

	body := rec.Body.String()
	if !strings.Contains(body, `<div id="tako-dev-bar"`) {
		t.Fatalf("the bar has no id to target:\n%s", body)
	}
	// Content-Length has to describe what was actually written, or the
	// document truncates in the browser.
	if got, want := rec.Header().Get("Content-Length"), len(body); got != "" && got != itoa(want) {
		t.Errorf("Content-Length = %s, body is %d bytes", got, want)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

var devBarSite = tako.Site(".")

func devBarTemplates(t *testing.T) {
	t.Helper()
	RegisterSiteTemplates(fstest.MapFS{
		"templates/layout.html": {Data: []byte(`{{ define "layout.html" }}<html><body>{{ block "content" . }}{{ end }}</body></html>{{ end }}`)},
		"templates/p.html":      {Data: []byte(`{{ define "content" }}<p>page</p>{{ end }}`)},
		"templates/frag.html":   {Data: []byte(`<p>fragment</p>`)},
	}, SiteOptions{})
	t.Cleanup(func() {
		RegisterSiteTemplates(fstest.MapFS{"templates/x": {Data: []byte("")}}, SiteOptions{})
	})
}
