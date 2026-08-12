package runtime

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"
)

// Templates parse as a TREE, keyed by path relative to templates/.
//
// They used to parse as one flat directory: RegisterSiteTemplates read
// templates/ with fs.ReadDir and skipped every entry where e.IsDir(), so a
// file in a subdirectory was never registered and site.Render named it back at
// you as "not found". The app that reported it had seventeen templates in one
// folder — the admin's and the public site's — told apart by a filename
// prefix, which is a convention rather than a boundary.
//
// The second consumer is the one that made it worth doing: an app that serves
// more than one site wants each site to own its templates. A directory per
// site is the obvious shape and was the one shape that could not be expressed.

const treeLayout = `{{ define "layout.html" }}<html>{{ block "content" . }}{{ end }}</html>{{ end }}`

// reset puts the package-level template state back, so one test's site does
// not leak into the next.
func resetSite(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		RegisterSiteTemplates(fstest.MapFS{"templates/x": {Data: []byte("")}}, SiteOptions{})
	})
}

func TestAPageInASubdirectoryIsRegistered(t *testing.T) {
	resetSite(t)
	RegisterSiteTemplates(fstest.MapFS{
		"templates/layout.html":     {Data: []byte(treeLayout)},
		"templates/admin/nav.html":  {Data: []byte(`{{ define "content" }}ADMIN NAV{{ end }}`)},
		"templates/site/index.html": {Data: []byte(`{{ define "content" }}PUBLIC INDEX{{ end }}`)},
	}, SiteOptions{})

	for name, want := range map[string]string{
		"admin/nav.html":  "ADMIN NAV",
		"site/index.html": "PUBLIC INDEX",
	} {
		got, err := renderSite(context.Background(), name, nil)
		if err != nil {
			t.Fatalf("render %s: %v", name, err)
		}
		if !strings.Contains(string(got), want) {
			t.Errorf("render %s = %q, want it to contain %q", name, got, want)
		}
	}
}

// The reason a flat parse could not simply be pointed at a tree: ParseFS names
// a template by its BASE name, so two files called nav.html in different
// directories are one template, and the second silently wins.
func TestTwoTemplatesWithTheSameBaseNameDoNotCollide(t *testing.T) {
	resetSite(t)
	RegisterSiteTemplates(fstest.MapFS{
		"templates/layout.html":    {Data: []byte(treeLayout)},
		"templates/admin/nav.html": {Data: []byte(`{{ define "content" }}THE ADMIN ONE{{ end }}`)},
		"templates/site/nav.html":  {Data: []byte(`{{ define "content" }}THE PUBLIC ONE{{ end }}`)},
	}, SiteOptions{})

	admin, err := renderSite(context.Background(), "admin/nav.html", nil)
	if err != nil {
		t.Fatal(err)
	}
	site, err := renderSite(context.Background(), "site/nav.html", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(admin), "THE ADMIN ONE") {
		t.Errorf("admin/nav.html rendered %q", admin)
	}
	if !strings.Contains(string(site), "THE PUBLIC ONE") {
		t.Errorf("site/nav.html rendered %q — the two collided on their base name", site)
	}
}

// A PARTIAL in a subdirectory has to work twice over: any page must be able to
// {{ template }} it by path, and site.Fragment must be able to render it
// alone — and Fragment executes a template by its own NAME, so the name it is
// defined under and the name it is looked up by have to be the same string.
func TestAPartialInASubdirectoryIsUsableByPathAndAsAFragment(t *testing.T) {
	resetSite(t)
	RegisterSiteTemplates(fstest.MapFS{
		"templates/layout.html":       {Data: []byte(treeLayout)},
		"templates/shared/card.html":  {Data: []byte(`<b>A CARD</b>`)},
		"templates/site/listing.html": {Data: []byte(`{{ define "content" }}{{ template "shared/card.html" . }}{{ end }}`)},
	}, SiteOptions{})

	page, err := renderSite(context.Background(), "site/listing.html", nil)
	if err != nil {
		t.Fatalf("a page including a nested partial: %v", err)
	}
	if !strings.Contains(string(page), "A CARD") {
		t.Errorf("the page did not include the nested partial: %q", page)
	}

	frag, err := renderFragment(context.Background(), "shared/card.html", nil)
	if err != nil {
		t.Fatalf("rendering a nested partial as a fragment: %v", err)
	}
	if !strings.Contains(string(frag), "A CARD") {
		t.Errorf("the fragment rendered %q", frag)
	}
}

// Every flat site that worked before still works, named exactly as before.
// This is the compatibility claim, and it is the whole reason this change can
// land without a version bump: for a template at the top level, "path relative
// to templates/" IS its base name.
func TestAFlatSiteIsUnchanged(t *testing.T) {
	resetSite(t)
	RegisterSiteTemplates(fstest.MapFS{
		"templates/layout.html": {Data: []byte(treeLayout)},
		"templates/index.html":  {Data: []byte(`{{ define "content" }}{{ template "card.html" . }}{{ end }}`)},
		"templates/card.html":   {Data: []byte(`<b>FLAT CARD</b>`)},
	}, SiteOptions{})

	page, err := renderSite(context.Background(), "index.html", nil)
	if err != nil {
		t.Fatalf("a flat page: %v", err)
	}
	if !strings.Contains(string(page), "FLAT CARD") {
		t.Errorf("the flat page did not include its partial: %q", page)
	}
	frag, err := renderFragment(context.Background(), "card.html", nil)
	if err != nil {
		t.Fatalf("a flat fragment: %v", err)
	}
	if !strings.Contains(string(frag), "FLAT CARD") {
		t.Errorf("the flat fragment rendered %q", frag)
	}
}

// A parse error names the template by its PATH, not by its base name —
// otherwise an app with admin/nav.html and site/nav.html is told which file is
// broken by a name two files share.
func TestAParseErrorNamesTheTemplateByItsPath(t *testing.T) {
	resetSite(t)
	RegisterSiteTemplates(fstest.MapFS{
		"templates/layout.html":    {Data: []byte(treeLayout)},
		"templates/admin/bad.html": {Data: []byte(`{{ define "content" }}{{ .X | nosuchfunc }}{{ end }}`)},
	}, SiteOptions{})

	_, err := renderSite(context.Background(), "admin/bad.html", nil)
	if err == nil {
		t.Fatal("a template that does not parse rendered without error")
	}
	if !strings.Contains(err.Error(), "admin/bad.html") {
		t.Errorf("the error is %q — it should name the template by its path", err)
	}
}

// Only templates/layout.html is the shell. A layout.html deeper in the tree is
// an ordinary template with an ordinary name, so a site bundle can carry one
// without silently becoming the root for everybody.
func TestOnlyTheTopLevelLayoutIsTheShell(t *testing.T) {
	resetSite(t)
	RegisterSiteTemplates(fstest.MapFS{
		"templates/layout.html":       {Data: []byte(treeLayout)},
		"templates/admin/layout.html": {Data: []byte(`<i>NOT THE SHELL</i>`)},
		"templates/page.html":         {Data: []byte(`{{ define "content" }}THE PAGE{{ end }}`)},
	}, SiteOptions{})

	got, err := renderSite(context.Background(), "page.html", nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "NOT THE SHELL") {
		t.Errorf("a nested layout.html was used as the shell: %q", got)
	}
	if !strings.Contains(string(got), "THE PAGE") {
		t.Errorf("the page rendered %q", got)
	}
	if _, err := renderFragment(context.Background(), "admin/layout.html", nil); err != nil {
		t.Errorf("a nested layout.html should be an ordinary template: %v", err)
	}
}
