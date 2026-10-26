package main

import "path/filepath"

func scaffold(opts newOpts, pkg, version string) map[string]string {
	if opts.lang == "cfire" {
		return map[string]string{
			".gitignore": ".tako/\n.cfire/\n.env\n",
			pkg + ".cf":  cfireApp(pkg),
			"AGENTS.md":  agentsMD(pkg, "cfire"),
		}
	}
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
		"AGENTS.md":  agentsMD(pkg, "go"),
	}
	if opts.kind == "site" {
		files[pkg+".go"] = siteApp(pkg)
		files[pkg+"_test.go"] = siteTest(pkg)
		files["web/templates/layout.html"] = layoutHTML(opts.module)
		files["web/templates/home.html"] = homeHTML()
		return files
	}
	files[pkg+".go"] = apiApp(pkg)
	files[pkg+"_test.go"] = apiTest(pkg)
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
  <main>{{ block "content" . }}{{ end }}</main>
</body>
</html>
`
}

func homeHTML() string {
	return `{{ define "title" }}{{ .Title }}{{ end }}

{{ define "content" }}
  <h1>{{ .Title }}</h1>
  <p>Edit web/templates/home.html and save — tako dev reloads.</p>
{{ end }}
`
}

func apiTest(pkg string) string {
	return `package ` + pkg + `

import (
	"testing"

	"github.com/gsandoval/tako"
)

func TestGreet(t *testing.T) {
	app := tako.NewTestApp(t)

	got, err := Greet.Call(app.Ctx(), GreetReq{Name: "World"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Message != "Hello, World!" {
		t.Fatalf("got %q, want %q", got.Message, "Hello, World!")
	}
}
`
}

func siteTest(pkg string) string {
	return `package ` + pkg + `

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gsandoval/tako"
)

func TestHome(t *testing.T) {
	app := tako.NewTestApp(t)

	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /: status %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Hello from Tako!") {
		t.Fatalf("GET /: unexpected body:\n%s", rec.Body.String())
	}
}
`
}

func cfireApp(pkg string) string {
	return `package ` + pkg + `

import "core:tako"

type Greeting struct {
    message: str ` + "`json:\"message\"`" + `
}

type GreetReq struct {
    name: str ` + "`path:\"name\"`" + `
}

type EmptyReq struct {}

Hello := tako.api("GET /hello", hello_handler)
Greet := tako.api("GET /greet/{name}", greet_handler)

fn hello_handler(ctx: tako.Ctx, req: EmptyReq) -> Greeting! {
    return Greeting{message: "Hello from Tako in cfire!"}
}

fn greet_handler(ctx: tako.Ctx, req: GreetReq) -> Greeting! {
    if req.name == "nobody" {
        return Err(tako.not_found("not found"))
    }
    return Greeting{message: "Hello, " + req.name + "!"}
}
`
}

func agentsMD(pkg, lang string) string {
	return `# AGENTS.md — Working with Tako (` + pkg + `)

This application uses **Tako** (` + lang + ` SDK), a statically-analyzed framework over Google Cloud Run.

## Essential Commands

- ` + "`tako check`" + ` (or ` + "`tako check --json`" + `) — Fast (<1s) static analysis, site-shadow check, and code generation under ` + "`.tako/gen/`" + `. Run this after every edit.
- ` + "`tako test`" + ` — Analyze, regenerate ` + "`.tako/gen/`" + ` (and test harness overlay), and run tests. **Always prefer ` + "`tako test`" + ` over bare ` + "`go test`" + `** whenever routes, handlers, or templates change so ` + "`app.Handler()`" + ` reflects current mounts.
- ` + "`tako dev`" + ` — Run the local dev server with live reload and local bbolt/filesystem backends under ` + "`.tako/data/`" + `.
- ` + "`tako gen graph`" + ` (` + "`--json`" + ` | ` + "`--mermaid`" + `) — Inspect the derived services, handlers, resources, composite indexes, and IAM edges.
- ` + "`tako docs list`" + ` / ` + "`tako docs <topic>`" + ` / ` + "`tako docs search <query>`" + ` — Read embedded documentation and ` + "`tako docs api`" + ` (exported SDK surface) directly in the terminal.

## Static Analysis Rules to Follow

1. **Package-level declarations**: Every handler (` + "`API`" + `, ` + "`Page`" + `, ` + "`Worker`" + `, ` + "`Subscribe`" + `, ` + "`Cron`" + `, ` + "`Runner`" + `, ` + "`Singleton`" + `) and resource (` + "`Entity`" + `, ` + "`Bucket`" + `, ` + "`Queue`" + `, ` + "`Topic`" + `, ` + "`Cache`" + `, ` + "`Secret`" + `, ` + "`Lock`" + `, ` + "`Job`" + `, ` + "`Site`" + `, ` + "`Auth`" + `, ` + "`Role`" + `, ` + "`Policy`" + `) must be declared as a package-level ` + "`var`" + `. Handler vars must be **exported** (` + "`var Hello = ...`" + `).
2. **Compile-time constants**: Routes, resource names, cron schedules, and entity query field names (` + "`.Where(\"Field\", ...)`" + `, ` + "`.OrderBy(\"-Field\")`" + `) must be string constants.
3. **Entity struct tags**: Every ` + "`tako.Entity[T]`" + ` struct requires exactly one ` + "`string`" + ` field tagged ` + "`entity:\"id\"`" + `. Fields used in ` + "`.Where`" + ` or ` + "`.OrderBy`" + ` must be tagged ` + "`entity:\"index\"`" + `, and queries take the **Go struct field name**, not the ` + "`json`" + ` tag.
4. **Request binding**: Exported request structs bind ` + "`path:\"param\"`" + ` (matching ` + "`{param}`" + ` in the route), ` + "`query:\"k\"`" + `, ` + "`form:\"k\"`" + `, or ` + "`tako.Blob`" + ` with ` + "`form:\"file\"`" + `. Untagged fields decode from JSON bodies on non-GET ` + "`tako.API`" + ` routes (` + "`tako.Page`" + ` routes only accept ` + "`path:`" + `, ` + "`query:`" + `, or ` + "`form:`" + ` tags).
5. **Site templates**: ` + "`web/templates/layout.html`" + ` is the shared shell and must define ` + "`{{ block \"content\" . }}{{ end }}`" + `; each full page template must define ` + "`{{ define \"content\" }}`" + ` so it gets an isolated layout clone.
`
}
