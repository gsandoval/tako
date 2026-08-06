package codegen

import (
	"go/token"
	"testing"

	"github.com/gsandoval/tako/pkg/analyzer"
)

// The import alias is written by tako into generated code, so it has to
// be a legal Go identifier — and the last segment of a module path very
// often is not. `github.com/gsandoval/tako-example-hello` used to emit
//
//	tako-example-hello "github.com/gsandoval/tako-example-hello"
//
// which is a parse error at the hyphen, so the whole service failed to
// generate. A hyphen in a repository name is the normal case, not an
// exotic one.
func TestAliasFromPathIsAlwaysALegalIdentifier(t *testing.T) {
	cases := []struct {
		pkg  string
		want string
	}{
		// Every example today, unchanged.
		{"hello", "hello"},
		{"shortlink", "shortlink"},
		// The shapes that broke it.
		{"github.com/gsandoval/tako-example-hello", "tako_example_hello"},
		{"github.com/org/my-service", "my_service"},
		{"example.com/x/9lives", "pkg9lives"},
		{"example.com/x/v2.0", "v2_0"},
		// A keyword parses fine and imports nowhere.
		{"example.com/x/range", "range_"},
	}
	for _, c := range cases {
		got := aliasFromPath(c.pkg)
		if got != c.want {
			t.Errorf("aliasFromPath(%q) = %q, want %q", c.pkg, got, c.want)
		}
		// token.IsIdentifier is Go's own answer, keywords included —
		// better than restating the rule here and getting it subtly wrong.
		if !token.IsIdentifier(got) {
			t.Errorf("aliasFromPath(%q) = %q, which Go will not accept as an identifier", c.pkg, got)
		}
	}
}

// The alias a service actually gets is the package's own name, not its
// path: generated code is a product, and `tako_example_hello.GreetReq`
// beside a graph that calls the same type `hello.GreetReq` is two
// names for one thing.
func TestPkgAliasPrefersThePackagesOwnName(t *testing.T) {
	const path = "github.com/gsandoval/tako-example-hello"
	g := &analyzer.AppGraph{
		Handlers: []analyzer.Handler{{Var: "hello.Hello", Package: path, Service: "hello"}},
	}
	svc := analyzer.Service{Name: "hello", Package: path}
	if got := pkgAlias(g, svc); got != "hello" {
		t.Errorf("pkgAlias = %q, want %q", got, "hello")
	}
}

// With nothing in the graph to read a name from, the path is all
// there is — and it still has to parse.
func TestPkgAliasFallsBackToThePath(t *testing.T) {
	svc := analyzer.Service{Name: "x", Package: "github.com/org/my-service"}
	if got := pkgAlias(&analyzer.AppGraph{}, svc); got != "my_service" {
		t.Errorf("pkgAlias = %q, want %q", got, "my_service")
	}
}
