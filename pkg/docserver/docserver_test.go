package docserver

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gsandoval/tako/docs"
)

// repoRoot is where the embedded docs came from: link destinations that
// point outside the embedded set are checked against the real tree.
const repoRoot = "../.."

func built(t *testing.T) *Site {
	t.Helper()
	site, err := Build(docs.FS)
	if err != nil {
		t.Fatalf("building the docs: %v", err)
	}
	return site
}

func TestEveryEmbeddedPageRenders(t *testing.T) {
	site := built(t)
	// Exactly the embedded files, each once. The counts are printed on
	// startup, and an ordering that listed a page twice said "25 pages"
	// for a reference of 24 before this asserted it.
	for _, tc := range []struct {
		section string
		pages   []*Page
	}{
		{SectionGuide, site.Guide}, {SectionReference, site.Reference},
	} {
		files, err := fs.Glob(docs.FS, tc.section+"/*.md")
		if err != nil {
			t.Fatal(err)
		}
		if len(tc.pages) != len(files) {
			t.Errorf("%s lists %d pages for %d files", tc.section, len(tc.pages), len(files))
		}
		seen := map[string]bool{}
		for _, p := range tc.pages {
			if seen[p.Route] {
				t.Errorf("%s is listed twice", p.Route)
			}
			seen[p.Route] = true
		}
	}
	for _, p := range append(append([]*Page{}, site.Guide...), site.Reference...) {
		if p.Title == "" {
			t.Errorf("%s has no H1, so nothing can title it", p.File)
		}
		if strings.TrimSpace(p.Body) == "" {
			t.Errorf("%s rendered to nothing", p.File)
		}
	}
}

// The link check is the test that earns this feature's testing section:
// reference/alerts.md gained links to observability.md and footprint.md
// during Y2 and nothing verified they resolved.
func TestEveryLinkBetweenDocsResolves(t *testing.T) {
	site := built(t)
	internal, anchors, repo, external := 0, 0, 0, 0
	for _, l := range site.Links {
		switch l.Kind {
		case LinkInternal:
			internal++
			target := site.Page(l.Target)
			if target == nil {
				t.Errorf("%s links to %q, which is not a page", l.From, l.Dest)
				continue
			}
			if l.Anchor != "" && !target.Anchors[l.Anchor] {
				t.Errorf("%s links to %q — %s has no section with that anchor", l.From, l.Dest, target.File)
			}
		case LinkAnchor:
			anchors++
			from := site.Page(routeFor(l.From))
			if from != nil && !from.Anchors[l.Anchor] {
				t.Errorf("%s links to %q, and has no section with that anchor", l.From, l.Dest)
			}
		case LinkRepo:
			repo++
			if _, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(l.Target))); err != nil {
				t.Errorf("%s links to %q, and %s does not exist", l.From, l.Dest, l.Target)
			}
		case LinkExternal:
			external++
		}
	}
	// Stated so a rewrite that silently stops recording links reads as a
	// change rather than as a pass.
	t.Logf("checked %d links between docs, %d anchors, %d into the repository; %d external, unchecked",
		internal, anchors, repo, external)
	if internal == 0 || repo == 0 {
		t.Fatal("no links were recorded — the link renderer is not being reached")
	}
}

func TestEveryExportedSymbolHasAReferencePage(t *testing.T) {
	site := built(t)

	// Symbols nothing documents. Empty, and meant to stay that way:
	// adding to this list is the deliberate act it should be, and the
	// api page prints whatever is in it under its own heading, so the
	// gap is visible to a reader and not only here.
	known := map[string]bool{}

	pages := map[string]bool{}
	for _, p := range site.Reference {
		pages[p.Name+".md"] = true
	}
	for _, s := range site.Symbols {
		switch {
		case s.Page == "" && !known[s.Name]:
			t.Errorf("%s is exported and no reference page documents it — add it to symbolPage", s.Name)
		case s.Page != "" && !pages[s.Page]:
			t.Errorf("%s is mapped to %s, which is not a reference page", s.Name, s.Page)
		}
	}
	for name := range known {
		found := false
		for _, s := range site.Symbols {
			if s.Name == name {
				found = true
			}
		}
		if !found {
			t.Errorf("%q is listed as undocumented but is no longer exported — drop it from the list", name)
		}
	}
}

// The api page is generated from the lock, so the only way it can lose a
// symbol is a bug in the grouping. This is the same completeness habit
// the apply-coverage and preset-default tests have.
func TestApiPageListsEverySymbolInTheLock(t *testing.T) {
	site := built(t)
	grouped := map[string]bool{}
	for _, g := range site.Groups {
		for _, p := range g.Pages {
			for _, s := range p.Symbols {
				grouped[s.Decl] = true
				for _, m := range s.Methods {
					grouped[m.Decl] = true
				}
			}
		}
	}
	body := get(t, site, "/api")
	for _, s := range site.Symbols {
		if !grouped[s.Decl] {
			t.Errorf("%s is in the lock and not on the api page", s.Name)
		}
		if !strings.Contains(body, "sym-"+s.Name) {
			t.Errorf("%s has no anchor on the rendered api page", s.Name)
		}
	}
}

func TestRoutes(t *testing.T) {
	site := built(t)
	h := Handler(site)
	for _, tc := range []struct {
		path   string
		status int
		want   string
	}{
		{"/", 200, "tako docs"},
		{"/guide/", 200, "The Tako guide"},
		{"/guide/01-hello", 200, "endpoints and the loop"},
		{"/reference/", 200, "The feature reference"},
		{"/reference/entity", 200, "typed entity stores"},
		{"/api", 200, "Every exported symbol"},
		{"/guide", 301, ""},
		{"/reference/nope", 404, "No page here"},
		{"/nope", 404, "No page here"},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if rec.Code != tc.status {
			t.Errorf("GET %s: status %d, want %d", tc.path, rec.Code, tc.status)
		}
		if tc.want != "" && !strings.Contains(rec.Body.String(), tc.want) {
			t.Errorf("GET %s: body does not mention %q", tc.path, tc.want)
		}
	}
}

// Every page a reader can reach has to be reachable from the nav, or
// the only way to it is a link from somewhere else.
func TestNavCoversEveryPage(t *testing.T) {
	site := built(t)
	for _, section := range []string{SectionGuide, SectionReference} {
		listed := map[string]bool{}
		for _, item := range nav(site, section, "") {
			listed[item.Route] = true
		}
		for _, p := range append(append([]*Page{}, site.Guide...), site.Reference...) {
			if p.Section == section && !listed[p.Route] {
				t.Errorf("%s is served at %s and is in no nav", p.File, p.Route)
			}
		}
	}
}

func get(t *testing.T, site *Site, path string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	Handler(site).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: %d", path, rec.Code)
	}
	return rec.Body.String()
}
