package main

// `tako new` (SC3): the app you do not have yet.
//
// Everything else in this CLI assumes an app already exists. The guide's
// entry point is "the six examples", and the README's is three shell
// commands — which is fine for an API and wrong for a site, where the
// pieces you are missing (a templates directory, a layout, the site
// declaration) announce themselves as `the site has no templates/
// directory`: a message about a checkout wearing the costume of a
// broken app.
//
// It writes the smallest thing that runs and gets out of the way. No
// template repository, no --from <url>, no wizard.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/gsandoval/tako/pkg/analyzer"
	"github.com/gsandoval/tako/pkg/codegen"
)

const newUsage = `tako new <name> [--kind api|site] [--module <path>] [--replace <dir>]

  Creates a directory holding the smallest tako app that runs.

  --kind api    endpoints and JSON (the default)
  --kind site   server-rendered pages: the site declaration, a layout,
                and a page — the files whose absence is otherwise a
                confusing runtime error
  --module      the Go module path (default: the directory name)
  --replace     develop against a tako checkout instead of a release
`

type newOpts struct {
	dir     string
	module  string
	kind    string
	replace string
}

func runNew(args []string) error {
	opts := newOpts{kind: "api"}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if name, value, ok := strings.Cut(arg, "="); ok && strings.HasPrefix(name, "--") {
			args = append(args[:i], append([]string{name, value}, args[i+1:]...)...)
			arg = name
		}
		switch arg {
		case "--kind", "--module", "--replace":
			if i+1 >= len(args) {
				return fmt.Errorf("%s needs a value", arg)
			}
			i++
			switch arg {
			case "--kind":
				opts.kind = args[i]
			case "--module":
				opts.module = args[i]
			case "--replace":
				opts.replace = args[i]
			}
		default:
			if strings.HasPrefix(arg, "--") {
				return fmt.Errorf("unknown flag %q (tako new accepts --kind, --module, --replace)", arg)
			}
			if opts.dir != "" {
				return fmt.Errorf("tako new takes one name, got %q and %q", opts.dir, arg)
			}
			opts.dir = arg
		}
	}
	if opts.dir == "" {
		fmt.Fprint(os.Stderr, newUsage)
		os.Exit(2)
	}
	if opts.kind != "api" && opts.kind != "site" {
		return fmt.Errorf("unknown --kind %q (api or site)", opts.kind)
	}
	if opts.module == "" {
		opts.module = filepath.Base(opts.dir)
	}
	// A package name is a Go identifier; a directory name is not
	// necessarily one. Saying so here beats a compile error in generated
	// code the user did not write.
	pkg := packageName(filepath.Base(opts.dir))
	if pkg == "" {
		return fmt.Errorf("%q does not give a usable Go package name — use letters, digits and "+
			"hyphens, starting with a letter", filepath.Base(opts.dir))
	}
	if _, err := os.Stat(opts.dir); err == nil {
		return fmt.Errorf("%s already exists — tako new will not write into a directory it did not create", opts.dir)
	}

	version, dev := takoVersion()
	files := scaffold(opts, pkg, version)
	for rel, body := range files {
		path := filepath.Join(opts.dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			return err
		}
	}

	fmt.Printf("tako new · %s\n", opts.dir)
	for _, rel := range sortedKeys(files) {
		fmt.Printf("  + %s\n", filepath.Join(opts.dir, rel))
	}

	// Tidy, then fetch the RUNTIME explicitly. The second half is the
	// one that is easy to leave out and impossible to miss afterwards.
	//
	// The app's own file imports the root package alone, so tidy
	// resolves bbolt and x/sys and stops. The generated main imports
	// pkg/runtime, which pulls goldmark, bluemonday and x/net — and
	// `go mod tidy` never learns that, because it skips directories
	// beginning with a dot and the generated code lives in `.tako/gen`.
	// So a scaffold that only tidied succeeded, and the very next
	// command failed with six "missing go.sum entry" lines naming
	// packages the user has never heard of. Asking for the runtime by
	// name is what closes it.
	if err := tidy(opts.dir); err != nil {
		return unresolvedTako(err, opts, version, dev)
	}
	if err := goGet(opts.dir, "github.com/gsandoval/tako/pkg/runtime"); err != nil {
		return unresolvedTako(err, opts, version, dev)
	}
	if err := generateInto(opts.dir); err != nil {
		return err
	}
	fmt.Printf("\n  tako %s\n\nNext:\n  cd %s && tako dev\n", version, opts.dir)
	return nil
}

// generateInto runs the analysis and codegen the other commands run, so
// the new project is complete rather than one command short.
func generateInto(dir string) error {
	graph, err := analyzer.Analyze(analyzer.Config{Dir: dir})
	if err != nil {
		return fmt.Errorf("the scaffold does not analyze — this is a tako bug: %v", err)
	}
	if _, err := codegen.Generate(graph, dir); err != nil {
		return fmt.Errorf("the scaffold does not generate — this is a tako bug: %v", err)
	}
	return nil
}

// takoVersion is the version of the binary doing the scaffolding, so a
// generated go.mod says which tako made it. A CLI and an SDK that
// disagree is a support question nobody can answer from a transcript.
func takoVersion() (version string, dev bool) {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" || info.Main.Version == "(devel)" {
		return "(devel)", true
	}
	// A RELEASED version is what a scaffold can depend on. Anything else
	// — "(devel)", a +dirty build, or the pseudo-version Go stamps from
	// VCS state (v0.0.0-20260811030652-56fb6c14) — names a commit no
	// proxy will serve, so the scaffold cannot pin it and the failure
	// has to say why rather than blaming the network.
	return info.Main.Version, !released(info.Main.Version)
}

// released reports a plain semantic version tag: v1.2.3, or v1.2.3-rc1.
// A build metadata suffix (+dirty) or a pseudo-version's embedded
// timestamp means there is no such tag to fetch.
func released(v string) bool {
	if !strings.HasPrefix(v, "v") || strings.Contains(v, "+") {
		return false
	}
	base, pre, _ := strings.Cut(strings.TrimPrefix(v, "v"), "-")
	if strings.Count(base, ".") != 2 {
		return false
	}
	for _, part := range strings.Split(base, ".") {
		if part == "" || strings.TrimLeft(part, "0123456789") != "" {
			return false
		}
	}
	// A pseudo-version's first hyphen-part is a 14-digit timestamp.
	if len(pre) >= 14 && strings.TrimLeft(pre[:14], "0123456789") == "" {
		return false
	}
	return true
}

func scaffold(opts newOpts, pkg, version string) map[string]string {
	require := "require github.com/gsandoval/tako " + version + "\n"
	if opts.replace != "" {
		abs, err := filepath.Abs(opts.replace)
		if err == nil {
			opts.replace = abs
		}
		require = "require github.com/gsandoval/tako v0.0.0\n\nreplace github.com/gsandoval/tako => " + opts.replace + "\n"
	}
	files := map[string]string{
		"go.mod": "module " + opts.module + "\n\ngo 1.26\n\n" + require,
		// .env is here from the first commit on purpose: `tako secrets
		// set` refuses to write into a .env git would commit, and a
		// scaffold that omitted the line would ship that failure one
		// project at a time.
		".gitignore": ".tako/\n.env\n",
	}
	if opts.kind == "site" {
		files[pkg+".go"] = siteApp(pkg)
		files["web/templates/layout.html"] = layoutHTML(opts.module)
		files["web/templates/home.html"] = homeHTML()
		return files
	}
	files[pkg+".go"] = apiApp(pkg)
	return files
}

func apiApp(pkg string) string {
	return `package ` + pkg + `

import "github.com/gsandoval/tako"

// A handler is a package-level var made by a constructor. The var IS the
// registration: no comment directives, no init side effects, and
// nothing to keep in sync with a config file.
var Hello = tako.API("GET /hello", hello)
var Greet = tako.API("GET /greet/{name}", greet)

type Greeting struct {
	Message string ` + "`json:\"message\"`" + `
}

func hello(ctx tako.Ctx, _ struct{}) (Greeting, error) {
	return Greeting{Message: "Hello from Tako!"}, nil
}

// GreetReq binds {name} from the route. Tagged fields bind from the
// path and query on reads; untagged ones decode from the JSON body on
// writes. The binding code is generated and reflection-free — read it
// under .tako/gen/.
type GreetReq struct {
	Name string ` + "`path:\"name\"`" + `
}

func greet(ctx tako.Ctx, req GreetReq) (Greeting, error) {
	if req.Name == "nobody" {
		// Errors are values with a status. Anything else is an opaque
		// 500 with the real error in your logs.
		return Greeting{}, tako.NotFound()
	}
	return Greeting{Message: "Hello, " + req.Name + "!"}, nil
}
`
}

func siteApp(pkg string) string {
	return `package ` + pkg + `

import "github.com/gsandoval/tako"

// The site is the directory served: static files as they are, and
// templates/ for anything rendered. A page's template is named after
// the page it renders.
var site = tako.Site("./web", tako.SiteConfig{})

var Home = tako.Page("GET /", home)

type HomeView struct {
	Title string
}

func home(ctx tako.Ctx, _ struct{}) (tako.HTML, error) {
	return site.Render(ctx, "home.html", HomeView{Title: "Hello from Tako!"})
}
`
}

func layoutHTML(title string) string {
	return `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{ block "title" . }}` + title + `{{ end }}</title>
</head>
<body>
  <main>{{ block "main" . }}{{ end }}</main>
</body>
</html>
`
}

func homeHTML() string {
	return `{{ define "title" }}{{ .Title }}{{ end }}

{{ define "main" }}
  <h1>{{ .Title }}</h1>
  <p>Edit web/templates/home.html and save — tako dev reloads.</p>
{{ end }}
`
}

// tidy resolves the module so `tako dev` works immediately rather than
// on the second command.
func tidy(dir string) error {
	cmd := exec.Command("go", "mod", "tidy")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s", strings.TrimSpace(string(out)))
	}
	return nil
}

// goGet resolves one package into the module's go.mod and go.sum.
func goGet(dir, pkg string) error {
	cmd := exec.Command("go", "get", pkg)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s", strings.TrimSpace(string(out)))
	}
	return nil
}

// unresolvedTako is the failure that matters, and it has two very
// different causes.
//
// A released tako that cannot be fetched is a network or a proxy
// problem. A tako built from a checkout has no published version at all
// — which is the state of this repository until v0.1.0 is tagged — and
// the fix is a replace directive, not a retry.
func unresolvedTako(err error, opts newOpts, version string, dev bool) error {
	_ = os.RemoveAll(opts.dir) // half a project is worse than none
	if dev {
		return fmt.Errorf("this tako was built from a checkout, so it has no published version to\n"+
			"      depend on — and the scaffold it wrote could not resolve one.\n\n"+
			"      Point the new app at that checkout:\n"+
			"        tako new %s --replace /path/to/tako\n\n"+
			"      (once tako is tagged and published, plain `tako new %s` is enough:\n"+
			"       the scaffold pins the version of the binary that wrote it)\n\n      go mod tidy said:\n      %s",
			opts.dir, opts.dir, indent(err.Error()))
	}
	return fmt.Errorf("could not resolve github.com/gsandoval/tako %s — check your network or\n"+
		"      GOPROXY, then try again.\n\n      go mod tidy said:\n      %s",
		version, indent(err.Error()))
}

func indent(s string) string {
	return strings.ReplaceAll(strings.TrimSpace(s), "\n", "\n      ")
}

// packageName turns a directory name into a Go package name: hyphens
// are legal in one and not the other.
func packageName(dir string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(dir) {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9' && b.Len() > 0:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}
