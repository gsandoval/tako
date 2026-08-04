package docserver

import (
	"strings"
	"testing"
)

// page is a stand-in for a real page: enough to resolve links against.
func fixture(t *testing.T) (map[string]*Page, *Page) {
	t.Helper()
	here := &Page{Section: "reference", Name: "alerts", Route: "/reference/alerts", File: "reference/alerts.md"}
	there := &Page{Section: "guide", Name: "cloud", Route: "/guide/cloud", File: "guide/cloud.md"}
	index := &Page{Section: "reference", Name: "", Route: "/reference/", File: "reference/README.md"}
	entity := &Page{Section: "reference", Name: "entity", Route: "/reference/entity", File: "reference/entity.md"}
	return map[string]*Page{
		here.Route: here, there.Route: there, index.Route: index, entity.Route: entity,
	}, here
}

func TestResolveClassifiesDestinations(t *testing.T) {
	byRoute, here := fixture(t)
	for _, tc := range []struct {
		dest   string
		kind   LinkKind
		target string
		anchor string
	}{
		{"entity.md", LinkInternal, "/reference/entity", ""},
		{"entity.md#queries", LinkInternal, "/reference/entity", "queries"},
		{"../guide/cloud.md", LinkInternal, "/guide/cloud", ""},
		{"README.md", LinkInternal, "/reference/", ""},
		{"#shutdown", LinkAnchor, "", "shutdown"},
		{"../../examples/shop/app.go", LinkRepo, "examples/shop/app.go", ""},
		{"../domain-options.md", LinkRepo, "docs/domain-options.md", ""},
		{"https://cloud.google.com", LinkExternal, "https://cloud.google.com", ""},
	} {
		got := resolve(here.File, tc.dest, byRoute)
		if got.Kind != tc.kind || got.Target != tc.target || got.Anchor != tc.anchor {
			t.Errorf("resolve(%q) = kind %v target %q anchor %q; want kind %v target %q anchor %q",
				tc.dest, got.Kind, got.Target, got.Anchor, tc.kind, tc.target, tc.anchor)
		}
	}
}

func TestRenderRewritesLinksAndKeepsAnchors(t *testing.T) {
	byRoute, here := fixture(t)
	src := "# Alerts\n\n## The two metrics\n\nSee [entities](entity.md#queries) and\n" +
		"[the shop](../../examples/shop/app.go), or [back up](#the-two-metrics).\n"
	body, anchors, links, err := render(src, here, byRoute)
	if err != nil {
		t.Fatal(err)
	}
	if !anchors["the-two-metrics"] {
		t.Errorf("heading anchors were not collected: %v", anchors)
	}
	if !strings.Contains(body, `id="the-two-metrics"`) {
		t.Errorf("the heading kept no id, so nothing can link to it:\n%s", body)
	}
	if !strings.Contains(body, `href="/reference/entity#queries"`) {
		t.Errorf("a doc link was not rewritten to a route:\n%s", body)
	}
	// A file the binary cannot serve is named, not linked: a dead <a>
	// tells the reader less than the path does.
	if strings.Contains(body, `href="../../examples/shop/app.go"`) {
		t.Errorf("a repo path was left as a link:\n%s", body)
	}
	if !strings.Contains(body, `<code>examples/shop/app.go</code>`) {
		t.Errorf("a repo path was not shown as a path:\n%s", body)
	}
	if len(links) != 3 {
		t.Errorf("recorded %d links, want 3", len(links))
	}
}

// The reference writes "From [examples/shortlink](../../examples/shortlink/app.go):"
// a lot, and printing the path after text the path already contains says
// the same thing twice.
func TestRepoLinkWhoseTextIsThePathIsNotSaidTwice(t *testing.T) {
	byRoute, here := fixture(t)
	body, _, _, err := render("From [examples/shortlink](../../examples/shortlink/app.go):\n", here, byRoute)
	if err != nil {
		t.Fatal(err)
	}
	if want := "From <code>examples/shortlink/app.go</code>:"; !strings.Contains(body, want) {
		t.Errorf("got:\n%s\nwant it to contain:\n%s", body, want)
	}
	if strings.Count(body, "examples/shortlink") != 1 {
		t.Errorf("the path is printed more than once:\n%s", body)
	}
}

// The input is tako's own documentation, which is exactly why it is
// sanitized: a renderer with an exception for trusted input is a
// renderer with an exception, and this one listens on a port.
func TestRenderSanitizes(t *testing.T) {
	byRoute, here := fixture(t)
	src := "# Title\n\n<script>alert(1)</script>\n\n<div onclick=\"steal()\">x</div>\n\n```go\nfoo()\n```\n"
	body, _, _, err := render(src, here, byRoute)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"<script", "onclick"} {
		if strings.Contains(body, bad) {
			t.Errorf("%q survived sanitizing:\n%s", bad, body)
		}
	}
	// …and the things docs are made of survive it.
	if !strings.Contains(body, "<pre>") || !strings.Contains(body, "language-go") {
		t.Errorf("code fences did not survive:\n%s", body)
	}
}

func TestParseSurfaceGroupsMethodsUnderTheirType(t *testing.T) {
	syms, err := parseSurface(strings.Join([]string{
		"const ProfileBatch Profile",
		"func Entity[T any](name string) *EntityHandle[T]",
		"type EntityHandle[T any] struct{}",
		"    func (*EntityHandle[T]).Get(ctx Ctx, id string) (T, error)",
		"    func (*EntityHandle[T]).Put(ctx Ctx, entity T) error",
	}, "\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(syms) != 3 {
		t.Fatalf("got %d top-level symbols, want 3", len(syms))
	}
	if syms[0].Kind != "const" || syms[0].Name != "ProfileBatch" {
		t.Errorf("const parsed as %+v", syms[0])
	}
	if syms[1].Name != "Entity" || syms[1].Page != "entity.md" {
		t.Errorf("func parsed as %+v", syms[1])
	}
	if len(syms[2].Methods) != 2 || syms[2].Methods[0].Name != "Get" {
		t.Errorf("methods parsed as %+v", syms[2].Methods)
	}
	if syms[2].Methods[0].Page != syms[2].Page {
		t.Errorf("a method landed on a different page from its type")
	}
}

// The api page's grouping is read out of the reference's own README so
// there is only ever one ordering to keep true.
func TestSectionsComeFromTheReferenceIndex(t *testing.T) {
	readme := strings.Join([]string{
		"# The feature reference",
		"",
		"## What is API, and what is not",
		"",
		"- **Example tests import things** — see [testing.md](testing.md) for what yours should use.",
		"",
		"## Data",
		"",
		"- [entity.md](entity.md) — get/put/query",
		"- [cache.md](cache.md) — per-instance TTL cache",
	}, "\n")
	got := sections(readme)
	if len(got) != 1 || got[0].Name != "Data" {
		t.Fatalf("got %+v, want one Data section — a heading whose bullets are prose is not a group", got)
	}
	if len(got[0].Pages) != 2 || got[0].Pages[0] != "entity.md" {
		t.Errorf("pages parsed as %v", got[0].Pages)
	}
}
