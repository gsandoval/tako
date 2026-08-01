package analyzer

import (
	"os"
	"path/filepath"
	"testing"
)

// The shape rules, both directions. The refusals are golden-pinned by
// testdata/errors/domain; this pins what must be ACCEPTED, so tightening
// the validator can never quietly start rejecting real domains.
func TestDomainProblem(t *testing.T) {
	valid := []string{
		"example.com",
		"blog.example.com",
		"a.b.c.example.co.uk",
		"my-blog.example.com",
		"x1.example.com",
		"1blog.example.com",
	}
	for _, d := range valid {
		if why := domainProblem(d); why != "" {
			t.Errorf("domainProblem(%q) = %q, want accepted", d, why)
		}
	}
	invalid := []string{
		"", "blog", "https://blog.example.com", "blog.example.com/",
		"blog.example.com:8080", "blog.example.com.", "*.example.com",
		"Blog.example.com", "blog..example.com", "-blog.example.com",
		"blog-.example.com", "blog_1.example.com", "blog example.com",
	}
	for _, d := range invalid {
		if domainProblem(d) == "" {
			t.Errorf("domainProblem(%q) accepted, want a reason", d)
		}
	}
}

func TestDomainProblemLengthLimits(t *testing.T) {
	label := ""
	for i := 0; i < 63; i++ {
		label += "a"
	}
	if why := domainProblem(label + ".example.com"); why != "" {
		t.Errorf("63-char label rejected: %s", why)
	}
	if domainProblem(label+"a.example.com") == "" {
		t.Error("64-char label accepted, want rejection")
	}
}

// A tako.Service claim addresses the whole service — that is what lets
// an API-only service (no site anywhere) have a domain at all.
func TestDomainFromHandlerReachesService(t *testing.T) {
	graph := &AppGraph{
		Services:  []Service{{Name: "api", Package: "app/api"}},
		Resources: []Resource{{Kind: "service", Package: "app/api", Domain: "api.example.com", Pos: "app.go:1:1"}},
		Handlers: []Handler{
			{Kind: "api", Var: "api.Create", Service: "api", Pos: "app.go:1:1"},
			{Kind: "api", Var: "api.Read", Service: "api", Pos: "app.go:2:1"},
		},
	}
	var errs ErrorList
	applyDomains(graph, &errs)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if got := graph.Services[0].Domain; got != "api.example.com" {
		t.Errorf("service domain = %q, want api.example.com", got)
	}
}

// The claim lands on the package's APP service and never on a sibling
// worker service carved out of the same package.
func TestDomainFromSiteReachesMountingServiceOnly(t *testing.T) {
	graph := &AppGraph{
		Services: []Service{
			{Name: "blog", Package: "app/blog"},
			{Name: "blog-makethumb", Package: "app/blog"},
		},
		Resources: []Resource{
			{Kind: "service", Package: "app/blog", Domain: "blog.example.com", Pos: "app.go:1:1"},
		},
		Handlers: []Handler{
			{Kind: "page", Var: "blog.Home", Service: "blog", Pos: "app.go:2:1"},
			{Kind: "worker", Var: "blog.MakeThumb", Service: "blog-makethumb", Pos: "app.go:3:1"},
		},
	}
	var errs ErrorList
	applyDomains(graph, &errs)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if got := graph.Services[0].Domain; got != "blog.example.com" {
		t.Errorf("app service domain = %q, want blog.example.com", got)
	}
	if got := graph.Services[1].Domain; got != "" {
		t.Errorf("worker service domain = %q, want empty — a worker takes no public traffic", got)
	}
}

// V4: a declared tako.Service names the package's APP service
// <package>-<var>; satellites carved out of the same package keep the
// names their own handlers gave them.
func TestServiceDeclarationNamesTheAppService(t *testing.T) {
	dir := t.TempDir()
	writeModule(t, dir, "blog", `
var api = tako.Service(tako.WithDomain("blog.example.com"))

var jobs = tako.Queue[string]("jobs")

var Home = tako.Page("GET /", home)
var Work = tako.Worker(jobs, work)

func home(ctx tako.Ctx, _ struct{}) (tako.HTML, error) { return tako.HTML{}, nil }
func work(ctx tako.Ctx, _ string) error                { return nil }
`)
	graph, err := Analyze(Config{Dir: dir, Env: []string{"GOWORK=off"}})
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]string{}
	for _, s := range graph.Services {
		names[s.Name] = s.Domain
	}
	if _, ok := names["blog-api"]; !ok {
		t.Errorf("app service should be named blog-api, got %v", names)
	}
	if names["blog-api"] != "blog.example.com" {
		t.Errorf("domain did not reach the renamed service: %v", names)
	}
	if _, ok := names["blog-work"]; !ok {
		t.Errorf("worker service keeps its own name, got %v", names)
	}
	if names["blog-work"] != "" {
		t.Error("a worker takes no public traffic and must take no domain")
	}
}

// Undeclared packages keep the bare package name — that IS the honest
// name for a service nobody bothered to name (V4, opt-in).
func TestUndeclaredPackageKeepsItsName(t *testing.T) {
	dir := t.TempDir()
	writeModule(t, dir, "hello", `
var Hello = tako.API("GET /hello", hello)

func hello(ctx tako.Ctx, _ struct{}) (string, error) { return "hi", nil }
`)
	graph, err := Analyze(Config{Dir: dir, Env: []string{"GOWORK=off"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Services) != 1 || graph.Services[0].Name != "hello" {
		t.Errorf("want one service named hello, got %v", graph.Services)
	}
}

// writeModule lays down a one-package fixture module that reaches the
// SDK by replace directive, the way testdata/errors fixtures do.
func writeModule(t *testing.T, dir, pkg, body string) {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile("testdata/errors/authprovider/go.sum")
	if err != nil {
		t.Fatal(err)
	}
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module "+pkg+"\n\ngo 1.26\n\nrequire github.com/gsandoval/tako v0.0.0\n\nrequire (\n\tgo.etcd.io/bbolt v1.5.0 // indirect\n\tgolang.org/x/sys v0.47.0 // indirect\n)\n\nreplace github.com/gsandoval/tako => "+root+"\n")
	write("go.sum", string(src))
	write("app.go", "package "+pkg+"\n\nimport \"github.com/gsandoval/tako\"\n"+body)
}
