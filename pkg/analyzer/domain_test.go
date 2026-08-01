package analyzer

import "testing"

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

// A handler's claim addresses its whole service — that is what lets an
// API-only service (no site anywhere) have a domain at all.
func TestDomainFromHandlerReachesService(t *testing.T) {
	graph := &AppGraph{
		Services: []Service{{Name: "api", Package: "app/api"}},
		Handlers: []Handler{
			{Kind: "api", Var: "api.Create", Service: "api", Domain: "api.example.com", Pos: "app.go:1:1"},
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

// A site's claim lands on the service that MOUNTS the site — the app
// service — and never on a sibling worker service carved out of the same
// package.
func TestDomainFromSiteReachesMountingServiceOnly(t *testing.T) {
	graph := &AppGraph{
		Services: []Service{
			{Name: "blog", Package: "app/blog"},
			{Name: "blog-makethumb", Package: "app/blog"},
		},
		Resources: []Resource{
			{Kind: "site", Package: "app/blog", Domain: "blog.example.com", Pos: "app.go:1:1"},
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

// Agreement is not a conflict: the same name declared twice converges.
func TestDomainAgreementIsNotAConflict(t *testing.T) {
	graph := &AppGraph{
		Services:  []Service{{Name: "blog", Package: "app/blog"}},
		Resources: []Resource{{Kind: "site", Package: "app/blog", Domain: "blog.example.com", Pos: "app.go:1:1"}},
		Handlers: []Handler{
			{Kind: "page", Var: "blog.Home", Service: "blog", Domain: "blog.example.com", Pos: "app.go:2:1"},
		},
	}
	var errs ErrorList
	applyDomains(graph, &errs)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if got := graph.Services[0].Domain; got != "blog.example.com" {
		t.Errorf("service domain = %q, want blog.example.com", got)
	}
}
