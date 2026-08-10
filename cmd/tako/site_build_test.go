package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// A declared site with SiteConfig.Build is a build OUTPUT: nobody
// commits it, and every example that has one gitignores it. So every
// command that assembles the app has to run the build first, and each
// one has to remember on its own — which is the shape of defect that
// only shows up on a fresh clone, in whichever command was forgotten.
//
// It was forgotten in `tako test`. The generated harness mounts the
// declared site directory, so blog's page tests failed on a clean
// checkout with "the site has no templates/ directory" — a message
// about the checkout wearing the costume of a broken app. This test is
// the guard the fix deserves: a fifth command that serves the app and
// skips the build fails here rather than on someone else's laptop.
func TestEveryCommandThatAssemblesTheAppBuildsItsSites(t *testing.T) {
	for _, c := range []struct{ file, fn string }{
		{"main.go", "runTest"},
		{"deploy.go", "runDeploy"},
		{"build.go", "runBuild"},
		{"../../pkg/devserver/devserver.go", "cycle"},
	} {
		src, err := os.ReadFile(c.file)
		if err != nil {
			t.Errorf("%s: %v — if it moved, move this check with it", c.file, err)
			continue
		}
		body, ok := funcBodies(string(src))[c.fn]
		if !ok {
			t.Errorf("%s is gone from %s — if it was renamed, rename it here too", c.fn, c.file)
			continue
		}
		if !strings.Contains(body, "build.Sites(") {
			t.Errorf("%s (%s) never calls build.Sites — an app whose site is generated serves "+
				"an empty one there, and a fresh clone has no site at all", c.fn, c.file)
		}
	}
}

// funcBodies maps top-level func and method names to their bodies.
// Package-level funcs close at a column-zero brace, which is enough
// here and needs no parser.
func funcBodies(src string) map[string]string {
	decl := regexp.MustCompile(`(?m)^func (?:\([^)]*\) )?(\w+)\(`)
	locs := decl.FindAllStringSubmatchIndex(src, -1)
	out := make(map[string]string, len(locs))
	for i, loc := range locs {
		end := len(src)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		out[src[loc[2]:loc[3]]] = src[loc[1]:end]
	}
	return out
}
