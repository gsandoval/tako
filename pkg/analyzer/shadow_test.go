package analyzer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The shadow check is a query OVER a graph, not a stage of building
// one, and it reads the site directory from disk — so it is tested
// against hand-built graphs and real temp directories rather than a
// testdata package. The message text is asserted exactly, like every
// other text a user reads.

// siteGraph is one service, one site, and the given routes.
func siteGraph(routes ...string) *AppGraph {
	g := &AppGraph{
		Module:   "example.com/app",
		Services: []Service{{Name: "app", Package: "example.com/app"}},
		Resources: []Resource{{
			Kind:    "site",
			Name:    "./web",
			Package: "example.com/app",
			Dir:     "web",
		}},
	}
	for i, r := range routes {
		g.Handlers = append(g.Handlers, Handler{
			Kind:    "page",
			Var:     "app.Read",
			Package: "example.com/app",
			Service: "app",
			Route:   r,
			Pos:     "app.go:3" + string(rune('1'+i)) + ":2",
		})
	}
	return g
}

// withFiles writes the site files and returns the module dir.
func withFiles(t *testing.T, files ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, f := range files {
		p := filepath.Join(dir, "web", filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// The gap flauta shipped a workaround for: a two-segment wildcard page
// takes the stylesheet, and nothing says so.
func TestAWildcardRouteShadowsSiteFilesAtItsDepth(t *testing.T) {
	dir := withFiles(t, "index.html", "assets/site.css", "assets/app.js")
	got := ShadowedSiteFiles(dir, siteGraph("GET /{section}/{slug}"))
	if len(got) != 1 {
		t.Fatalf("got %d shadows, want 1: %+v", len(got), got)
	}
	want := "app.go:31:2: ⚠ GET /{section}/{slug} shadows 2 files the site serves — " +
		"/assets/app.js, /assets/site.css reach Read instead of the site, and answer 404; " +
		"move them deeper than the route's 2 segments, or narrow the pattern (tako/site-shadow)"
	if got[0].Warning() != want {
		t.Errorf("warning text:\n got: %s\nwant: %s", got[0].Warning(), want)
	}
}

// One file reads as one file. A warning that says "1 files reach" is a
// warning written by a machine that did not care.
func TestOneShadowedFileReadsAsOne(t *testing.T) {
	dir := withFiles(t, "index.html", "assets/site.css")
	got := ShadowedSiteFiles(dir, siteGraph("GET /{section}/{slug}"))
	if len(got) != 1 {
		t.Fatalf("got %d shadows, want 1", len(got))
	}
	want := "app.go:31:2: ⚠ GET /{section}/{slug} shadows 1 file the site serves — " +
		"/assets/site.css reaches Read instead of the site, and answers 404; " +
		"move it deeper than the route's 2 segments, or narrow the pattern (tako/site-shadow)"
	if got[0].Warning() != want {
		t.Errorf("warning text:\n got: %s\nwant: %s", got[0].Warning(), want)
	}
}

// flauta's workaround must be silent, or the warning trains people to
// ignore it: assets moved to depth 3 are genuinely out from under a
// two-segment route.
func TestFilesDeeperThanTheRouteAreNotShadowed(t *testing.T) {
	dir := withFiles(t, "index.html", "assets/css/site.css", "assets/js/app.js")
	if got := ShadowedSiteFiles(dir, siteGraph("GET /{section}/{slug}")); len(got) != 0 {
		t.Errorf("warned about files the route does not match: %s", got[0].Warning())
	}
}

// A generated site can put hundreds of files under one route.
func TestManyShadowedFilesAreSummarized(t *testing.T) {
	dir := withFiles(t, "a/1.css", "a/2.css", "a/3.css", "a/4.css", "a/5.css")
	got := ShadowedSiteFiles(dir, siteGraph("GET /{section}/{slug}"))
	if len(got) != 1 {
		t.Fatalf("got %d shadows, want 1", len(got))
	}
	if !strings.Contains(got[0].Warning(), "/a/1.css, /a/2.css, /a/3.css and 2 more reach") {
		t.Errorf("long file list not summarized: %s", got[0].Warning())
	}
}

// Method matters. A form post at the same shape takes nothing from a
// browser fetching a stylesheet.
func TestAPostRouteDoesNotShadowStaticGets(t *testing.T) {
	dir := withFiles(t, "assets/site.css")
	if got := ShadowedSiteFiles(dir, siteGraph("POST /{section}/{slug}")); len(got) != 0 {
		t.Errorf("a POST route shadowed a GET asset: %s", got[0].Warning())
	}
}

// The site's own root route is not a shadow of the site.
func TestARootRouteDoesNotShadowEverything(t *testing.T) {
	dir := withFiles(t, "index.html", "assets/css/site.css")
	if got := ShadowedSiteFiles(dir, siteGraph("GET /")); len(got) != 0 {
		t.Errorf("the root route swallowed the site: %s", got[0].Warning())
	}
}

// templates/ backs site.Render and is never served statically, so it
// cannot be shadowed and must not be reported.
func TestTemplatesAreNotServedAndNotReported(t *testing.T) {
	dir := withFiles(t, "templates/layout.html", "templates/404.html")
	if got := ShadowedSiteFiles(dir, siteGraph("GET /{a}/{b}")); len(got) != 0 {
		t.Errorf("reported a template as shadowed: %s", got[0].Warning())
	}
}

// An app with no site has nothing to shadow.
func TestNoSiteMeansNoWarning(t *testing.T) {
	g := siteGraph("GET /{section}/{slug}")
	g.Resources = nil
	if got := ShadowedSiteFiles(withFiles(t, "assets/site.css"), g); len(got) != 0 {
		t.Errorf("warned with no site declared: %s", got[0].Warning())
	}
}

// A WithBuild output that has not been generated yet reports nothing
// rather than guessing — which is why the callers run this after the
// site build.
func TestAnUnbuiltSiteIsSilent(t *testing.T) {
	g := siteGraph("GET /{section}/{slug}")
	g.Resources[0].BuildOut = "web/dist"
	if got := ShadowedSiteFiles(t.TempDir(), g); len(got) != 0 {
		t.Errorf("warned about an unbuilt site: %s", got[0].Warning())
	}
}

// Two routes over the same site are reported separately, each with the
// files it takes and the handler it takes them to.
func TestEachRouteReportsItsOwnFiles(t *testing.T) {
	dir := withFiles(t, "assets/site.css", "media/logo.png", "deep/enough/x.css")
	got := ShadowedSiteFiles(dir, siteGraph("GET /assets/{name}", "GET /media/{key}"))
	if len(got) != 2 {
		t.Fatalf("got %d shadows, want 2: %+v", len(got), got)
	}
	if got[0].Route != "GET /assets/{name}" || got[0].Files[0] != "/assets/site.css" {
		t.Errorf("first shadow is %s over %v", got[0].Route, got[0].Files)
	}
	if got[1].Route != "GET /media/{key}" || got[1].Files[0] != "/media/logo.png" {
		t.Errorf("second shadow is %s over %v", got[1].Route, got[1].Files)
	}
}

// Patterns the app cannot register together are a startup panic with
// its own message; the shadow check declines rather than piling on.
func TestConflictingPatternsAreDeclinedNotReported(t *testing.T) {
	dir := withFiles(t, "assets/site.css")
	g := siteGraph("GET /{a}/{b}")
	g.Handlers = append(g.Handlers, Handler{
		Kind: "api", Var: "app.Dup", Package: "example.com/app",
		Service: "app", Route: "GET /{x}/{y}", Pos: "app.go:40:2",
	})
	if got := ShadowedSiteFiles(dir, g); len(got) != 0 {
		t.Errorf("reported shadows on a router that cannot exist: %s", got[0].Warning())
	}
}
