package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The scaffold has to produce a project that RUNS, and the trap is that
// it almost does. `go mod tidy` skips directories beginning with a dot,
// and the generated main lives in .tako/gen — so a scaffold that only
// tidied left the runtime's own dependencies (goldmark, bluemonday,
// x/net) out of go.sum, succeeded, and made the very next command fail
// with six "missing go.sum entry" lines naming packages the user has
// never heard of.
//
// Fetching the runtime by name is what closes it, and this is the guard
// on that, since the failure is invisible until someone runs the app.
func TestTheScaffoldResolvesTheRuntimeItsGeneratedMainImports(t *testing.T) {
	src, err := os.ReadFile("new.go")
	if err != nil {
		t.Fatal(err)
	}
	body, ok := funcBodies(string(src))["runNew"]
	if !ok {
		t.Fatal("runNew is gone from new.go — if it moved, move this check with it")
	}
	if !strings.Contains(body, `goGet(opts.dir, "github.com/gsandoval/tako/pkg/runtime")`) {
		t.Error("tako new no longer fetches pkg/runtime — go mod tidy cannot see .tako/gen, so " +
			"the generated main's dependencies never reach go.sum and the scaffolded app " +
			"fails on the first tako dev")
	}
	if !strings.Contains(body, "generateInto(opts.dir)") {
		t.Error("tako new no longer generates — the project would be one command short of running")
	}
}

// A scaffold that omitted .env from .gitignore would ship, one project
// at a time, the exact failure SC4's gitignore guard exists to prevent.
func TestTheScaffoldIgnoresTheFilesThatMustNotBeCommitted(t *testing.T) {
	for _, kind := range []string{"api", "site"} {
		files := scaffold(newOpts{dir: "demo", module: "demo", kind: kind}, "demo", "v1.2.3")
		ignore := files[".gitignore"]
		for _, want := range []string{".tako/", ".env"} {
			if !strings.Contains(ignore, want) {
				t.Errorf("--kind %s: .gitignore is missing %q:\n%s", kind, want, ignore)
			}
		}
	}
}

// The whole reason --kind site exists: the missing pieces of a site are
// not a line of Go, they are FILES, and their absence surfaces as "the
// site has no templates/ directory" — a message about a checkout
// wearing the costume of a broken app.
func TestTheSiteKindWritesTheFilesASiteCannotStartWithout(t *testing.T) {
	files := scaffold(newOpts{dir: "demo", module: "demo", kind: "site"}, "demo", "v1.2.3")
	for _, want := range []string{
		"web/templates/layout.html",
		"web/templates/home.html",
		"demo.go",
	} {
		if _, ok := files[want]; !ok {
			t.Errorf("a site scaffold without %s starts and then fails at the first page", want)
		}
	}
	if !strings.Contains(files["demo.go"], `tako.Site("./web"`) {
		t.Error("the site kind must declare the site it just wrote templates for")
	}
	// The layout is the file tako.Site requires by name.
	if !strings.Contains(files["web/templates/layout.html"], "{{ block \"main\" . }}") {
		t.Error("the layout must have a block for a page to fill")
	}
}

// The go.mod says which tako wrote it, so a CLI and an SDK that
// disagree is answerable from the transcript.
func TestTheScaffoldPinsTheVersionThatWroteIt(t *testing.T) {
	files := scaffold(newOpts{dir: "demo", module: "demo", kind: "api"}, "demo", "v1.2.3")
	if !strings.Contains(files["go.mod"], "require github.com/gsandoval/tako v1.2.3") {
		t.Errorf("go.mod does not pin the generating version:\n%s", files["go.mod"])
	}
	// --replace is what makes the command usable before a tag exists.
	repl := scaffold(newOpts{dir: "demo", module: "demo", kind: "api", replace: "/src/tako"}, "demo", "(devel)")
	if !strings.Contains(repl["go.mod"], "replace github.com/gsandoval/tako => /src/tako") {
		t.Errorf("--replace did not write a replace directive:\n%s", repl["go.mod"])
	}
	if strings.Contains(repl["go.mod"], "(devel)") {
		t.Errorf("a go.mod cannot require \"(devel)\":\n%s", repl["go.mod"])
	}
}

// Only a released tag can be depended on. A pseudo-version or a +dirty
// build names a commit no proxy will serve, and the scaffold has to say
// that rather than blame the network.
func TestOnlyARealTagCountsAsReleased(t *testing.T) {
	for _, v := range []string{"v0.1.0", "v1.2.3", "v2.0.0-rc1"} {
		if !released(v) {
			t.Errorf("%s should be depend-able", v)
		}
	}
	for _, v := range []string{
		"(devel)", "",
		"v0.0.0-20260811030652-56fb6c149b22",
		"v0.0.0-20260811030652-56fb6c149b22+dirty",
		"v1.2.3+dirty",
		"1.2.3",
	} {
		if released(v) {
			t.Errorf("%s is not something a go.mod can fetch", v)
		}
	}
}

// A directory name is not a Go identifier, and finding that out as a
// compile error inside code you did not write is a bad first minute.
func TestTheModuleAndPackageNamesComeFromTheDirectory(t *testing.T) {
	for dir, pkg := range map[string]string{
		"blog":         "blog",
		"my-blog":      "myblog",
		"Blog2":        "blog2",
		"2fast":        "fast",
		"...":          "",
		"my_service-1": "myservice1",
	} {
		if got := packageName(dir); got != pkg {
			t.Errorf("packageName(%q) = %q, want %q", dir, got, pkg)
		}
	}
}

// Half a project is worse than none: a directory that exists but does
// not build is something the user now has to clean up before retrying.
func TestAFailedScaffoldLeavesNothingBehind(t *testing.T) {
	t.Chdir(t.TempDir())
	err := runNew([]string{"demo", "--replace", filepath.Join("does", "not", "exist")})
	if err == nil {
		t.Fatal("a replace pointing nowhere cannot resolve, and must fail")
	}
	if _, statErr := os.Stat("demo"); statErr == nil {
		t.Error("the half-written project was left on disk")
	}
}

func TestAnExistingDirectoryIsNeverWrittenInto(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.MkdirAll(filepath.Join("demo", "important"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := runNew([]string{"demo"})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("tako new must refuse an existing directory, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join("demo", "important")); statErr != nil {
		t.Error("it touched the directory it refused")
	}
}
