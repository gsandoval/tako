package codegen

import (
	"strings"
	"testing"

	"github.com/gsandoval/tako/pkg/analyzer"
)

// Every SiteConfig field the analyzer records has to reach the generated
// main, because that literal is the ONLY way a declaration becomes
// runtime behaviour. A field read by the analyzer and dropped here is a
// config option that silently does nothing — which looks like the
// feature not working rather than like a bug.
//
// journal has no codegen golden, so NotFound's wiring is pinned here
// rather than by a rendered file.
func TestSiteOptionsCarryEveryDeclaredField(t *testing.T) {
	got := siteOptionsLiteral(&analyzer.Resource{
		SPA:         true,
		DevProxy:    "http://localhost:5173",
		TrustedHTML: true,
		NotFound:    "404.html",
		Redirects:   []analyzer.SiteRedirect{{From: "/old", To: "/new"}},
	})
	for _, want := range []string{
		"SPA: true",
		`DevProxy: "http://localhost:5173"`,
		"TrustedHTML: true",
		`NotFound: "404.html"`,
		`Redirects: map[string]string{"/old": "/new"}`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("generated SiteOptions is missing %s:\n  %s", want, got)
		}
	}
}

// A site that declares nothing generates an empty literal — no field
// appears by default, so an app that never asked for a behaviour does
// not get one.
func TestSiteOptionsAreEmptyWhenNothingIsDeclared(t *testing.T) {
	if got := siteOptionsLiteral(&analyzer.Resource{}); got != "" {
		t.Errorf("undeclared site generated %q, want an empty literal", got)
	}
}
