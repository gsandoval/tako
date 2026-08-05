package routes

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The examples' BROWSER code is written against these routes, and
// nothing else in the repo checks it.
//
// FL5 moved the session surface into the namespace and updated every
// Go mount, every doc and shop's index.html — but not shop's app.ts,
// which kept calling /api/_session/signin and DELETE /api/_session for
// a day. The Go tests stayed green the whole time: no test drives a
// browser, and the example's session walk is a human step in
// validate/shop.md. A framework URL move that silently breaks the
// acceptance suite is exactly the failure the lock file above exists to
// prevent, one layer out.
//
// So: every path an example's frontend names that speaks about sessions
// must be a path tako actually mounts.
func TestExampleFrontendsUseTheReservedRoutes(t *testing.T) {
	// Query strings and fragments are the app's business, the path is
	// not: /_tako/signin?next=/ is this test's business up to the "?".
	pathLit := regexp.MustCompile(`/[A-Za-z0-9_./{}-]*`)

	mounted := map[string]string{
		Session: "Session",
		SignOut: "SignOut",
		SignIn:  "SignIn",
	}

	root := filepath.Join("..", "..", "examples")
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// dist/ is esbuild output and node_modules/ is not ours;
			// .tako/ is generated. Sources only — a stale artifact is
			// rebuilt, a stale source ships.
			switch d.Name() {
			case "dist", "node_modules", ".tako":
				return filepath.SkipDir
			}
			return nil
		}
		switch filepath.Ext(path) {
		case ".ts", ".js", ".html":
		default:
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, lit := range pathLit.FindAllString(string(src), -1) {
			if !mentionsSession(lit) {
				continue
			}
			if _, ok := mounted[lit]; ok {
				continue
			}
			rel, _ := filepath.Rel(root, path)
			t.Errorf("examples/%s names %q, which tako does not mount.\n"+
				"  the session surface is %s (POST/DELETE), %s (form post) and %s (the page).\n"+
				"  A frontend is not covered by any Go test here, so this URL 404s in the\n"+
				"  browser while the suite stays green.", rel, lit, Session, SignOut, SignIn)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// mentionsSession reports whether a path literal is talking about the
// session surface at all. Anything else an example's frontend fetches
// is the app's own route and none of this test's business.
func mentionsSession(path string) bool {
	for _, word := range []string{"session", "signin", "signout", "sign-in", "sign-out"} {
		if strings.Contains(strings.ToLower(path), word) {
			return true
		}
	}
	return false
}
