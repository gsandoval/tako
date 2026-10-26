package codegen

import (
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"strings"

	"github.com/gsandoval/tako/internal/routes"
	"github.com/gsandoval/tako/pkg/analyzer"
)

// Generate writes one main package per service under dir/.tako/gen/ and
// returns the written file paths, relative to dir. Output is gofmt-clean
// and meant to be read: one visible line per endpoint, a plain decode
// function per request struct, stdlib routing, no reflection.
func Generate(g *analyzer.AppGraph, dir string) ([]string, error) {
	if analyzer.IsCfireProject(dir) {
		return GenerateCfire(g, dir)
	}
	var written []string
	for _, svc := range g.Services {
		var src []byte
		var err error
		switch {
		case svc.Job:
			src, err = jobMain(g, svc)
		case svc.Singleton:
			src, err = singletonMain(g, svc)
		case asyncOnly(g, svc):
			src, err = asyncServiceMain(g, svc)
		default:
			src, err = serviceMain(g, svc)
		}
		if err != nil {
			return nil, err
		}
		rel := filepath.Join(".tako", "gen", svc.Name, "main.go")
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, src, 0o644); err != nil {
			return nil, err
		}
		written = append(written, rel)
		// The site is embedded in the binary (§2.11 v0): its content —
		// the built output under WithBuild, the source dir otherwise —
		// is staged next to the generated main for go:embed. Builds run
		// BEFORE Generate (deploy, dev); a missing output here writes a
		// placeholder so the embed still compiles and says why.
		if site := siteOf(g, svc); site != nil {
			if err := stageSite(dir, filepath.Dir(path), site); err != nil {
				return nil, err
			}
		}
		// The test harness (AB2), beside the main it mirrors — written
		// here rather than into the app's source tree, and mapped in by
		// `tako test` with -overlay.
		if h := serviceHarness(g, svc); h != nil {
			hrel := filepath.Join(".tako", "gen", svc.Name, harnessDir, harnessFile)
			hpath := filepath.Join(dir, hrel)
			if err := os.MkdirAll(filepath.Dir(hpath), 0o755); err != nil {
				return nil, err
			}
			if err := os.WriteFile(hpath, h, 0o644); err != nil {
				return nil, err
			}
			written = append(written, hrel)
		}
	}
	return written, nil
}

// Mains filters a Generate result down to the service mains.
//
// The harness is generated code like the rest, but it is TEST code, and
// a run command's most-read line should not list it: `tako dev` printed
// `.tako/gen/journal/harness/tako_harness_test.go` beside the service it
// was about to start, which is noise in the loop people watch all day.
func Mains(written []string) []string {
	out := make([]string, 0, len(written))
	for _, rel := range written {
		base := filepath.Base(rel)
		if base == "main.go" || base == "main.cf" {
			out = append(out, rel)
		}
	}
	return out
}

// HarnessOverlay maps each service's generated test harness into the
// package it belongs to, in the shape `go test -overlay` expects.
//
// The virtual path is <package dir>/tako_harness_test.go, which is why
// the file can declare the app's package and reach its unexported
// symbols without ever existing in the source tree.
func HarnessOverlay(g *analyzer.AppGraph, dir string) map[string]string {
	out := map[string]string{}
	for _, svc := range g.Services {
		real := filepath.Join(dir, ".tako", "gen", svc.Name, harnessDir, harnessFile)
		if _, err := os.Stat(real); err != nil {
			continue
		}
		pkgDir, err := packageDir(dir, g.Module, svc.Package)
		if err != nil {
			continue
		}
		out[filepath.Join(pkgDir, harnessFile)] = real
	}
	return out
}

// packageDir resolves an import path to its directory within the module.
func packageDir(dir, module, pkgPath string) (string, error) {
	if pkgPath == module {
		return dir, nil
	}
	rel := strings.TrimPrefix(pkgPath, module+"/")
	if rel == pkgPath {
		return "", fmt.Errorf("package %s is outside module %s", pkgPath, module)
	}
	return filepath.Join(dir, filepath.FromSlash(rel)), nil
}

// siteOf finds the site the service serves: the package's app service
// (never a single-handler async service) mounts it.
func siteOf(g *analyzer.AppGraph, svc analyzer.Service) *analyzer.Resource {
	if asyncOnly(g, svc) {
		return nil
	}
	for i, r := range g.Resources {
		if r.Kind == "site" && r.Package == svc.Package {
			return &g.Resources[i]
		}
	}
	return nil
}

// stageSite copies the site's serve root into <svcDir>/site.
func stageSite(moduleDir, svcDir string, site *analyzer.Resource) error {
	src := site.Dir
	if site.BuildOut != "" {
		src = site.BuildOut
	}
	dst := filepath.Join(svcDir, "site")
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	absSrc := filepath.Join(moduleDir, filepath.FromSlash(src))
	entries, err := os.ReadDir(absSrc)
	if err != nil || len(entries) == 0 {
		// WithBuild output that hasn't been built yet (tako test, first
		// analyze): a placeholder keeps go:embed compiling and explains.
		return os.WriteFile(filepath.Join(dst, "index.html"),
			[]byte("<!-- tako: site not built yet — WithBuild runs on tako dev and tako deploy -->\n"), 0o644)
	}
	return os.CopyFS(dst, os.DirFS(absSrc))
}

// jobMain renders the main of a Cloud Run JOB (§2.7): boot, decode
// the handed-over params, run the body once, exit — no server, no
// request timeout.
func jobMain(g *analyzer.AppGraph, svc analyzer.Service) ([]byte, error) {
	alias := pkgAlias(g, svc)
	var h analyzer.Handler
	for _, cand := range g.Handlers {
		if cand.Service == svc.Name {
			h = cand
		}
	}
	src := fmt.Sprintf(`// Code generated by tako. DO NOT EDIT.
//
// Job %s (%s), generated from the app's declarations: one execution =
// decode --tako-job-params, run the body, exit. Cloud Run retries a
// failed task up to its maxRetries; there is no request deadline to
// outlive.
package main

import (
	runtime "github.com/gsandoval/tako/pkg/runtime"

	%s %q
)

func main() {
	runtime.Boot()
	runtime.RunJobMain(%q, %s.%s)
}
`, svc.Name, h.Binding, alias, svc.Package, analyzer.ShortVar(h), alias, analyzer.ShortVar(h))
	out, err := format.Source([]byte(src))
	if err != nil {
		return nil, fmt.Errorf("generated main for job %s does not format: %w", svc.Name, err)
	}
	return out, nil
}

// singletonMain renders the main of a Singleton service: the
// lease-guarded loop plus the bare health endpoint Cloud Run requires.
func singletonMain(g *analyzer.AppGraph, svc analyzer.Service) ([]byte, error) {
	alias := pkgAlias(g, svc)
	var h analyzer.Handler
	for _, cand := range g.Handlers {
		if cand.Service == svc.Name {
			h = cand
		}
	}
	src := fmt.Sprintf(`// Code generated by tako. DO NOT EDIT.
//
// Singleton %s (%s): min=1/max=1 keeps an instance alive; the LEASE is
// what makes it exactly-one — during deploys two revisions run
// briefly, and only the lease holder's loop executes (spec §4.4).
package main

import (
	runtime "github.com/gsandoval/tako/pkg/runtime"

	%s %q
)

func main() {
	runtime.Boot()
	go runtime.RunSingletonLoop(%q, %s.%s)
	runtime.Serve(runtime.SingletonHealth(%q))
}
`, svc.Name, h.Binding, alias, svc.Package, analyzer.ShortVar(h), alias, analyzer.ShortVar(h), analyzer.ShortVar(h))
	out, err := format.Source([]byte(src))
	if err != nil {
		return nil, fmt.Errorf("generated main for singleton %s does not format: %w", svc.Name, err)
	}
	return out, nil
}

// asyncServiceMain renders the main for a worker/subscriber service: it
// registers its one handler on the wired backend (local mode consumes
// in-process) and serves the push endpoint deliveries arrive on in the
// cloud (Pub/Sub push lands here in Phase F).
func asyncServiceMain(g *analyzer.AppGraph, svc analyzer.Service) ([]byte, error) {
	alias := pkgAlias(g, svc)
	var b strings.Builder
	fmt.Fprintf(&b, `// Code generated by tako. DO NOT EDIT.
//
// Service %s, generated from the app's declarations. This file is meant to
// be read; to change it, change the app (or the generator), never this
// output.
package main

import (
	"net/http"

	runtime "github.com/gsandoval/tako/pkg/runtime"

	%s %q
)

func main() {
	runtime.Boot() // wire backends: local under .tako/data, gcp from Phase E

	// A worker declares no routes of its own, so this mux holds only
	// tako's reserved endpoints and is served directly.
	takoMux := http.NewServeMux()

`, svc.Name, alias, svc.Package)

	for _, h := range g.Handlers {
		if h.Service != svc.Name {
			continue
		}
		register := "RegisterWorker"
		if h.Kind == "subscribe" {
			register = "RegisterSubscriber"
		}
		fmt.Fprintf(&b, "\t// %s\n", analyzer.HandlerLine(h))
		fmt.Fprintf(&b, "\truntime.%s(%q, %s.%s)\n", register, analyzer.ShortVar(h), alias, analyzer.ShortVar(h))
		fmt.Fprintf(&b, "\t// Cloud deliveries arrive as pushes: 204 acks, 500 retries, 400 = permanent.\n")
		fmt.Fprintf(&b, "\ttakoMux.Handle(%q, runtime.PushHandler(%q, %s.%s))\n\n",
			routes.PatternPush, analyzer.ShortVar(h), alias, analyzer.ShortVar(h))
	}

	b.WriteString("\truntime.Serve(takoMux)\n}\n")
	src, err := format.Source([]byte(b.String()))
	if err != nil {
		return nil, fmt.Errorf("generated main for service %s does not format: %w", svc.Name, err)
	}
	return src, nil
}

// serviceMain renders the generated main package for one service.
func serviceMain(g *analyzer.AppGraph, svc analyzer.Service) ([]byte, error) {
	alias := pkgAlias(g, svc)
	site := siteOf(g, svc)
	usesApp := statusOf(g, svc) != nil // the status page is named from here too
	for _, h := range g.Handlers {
		if h.Service == svc.Name || h.Package == svc.Package {
			usesApp = true
		}
	}
	callees := remoteCallees(g, svc)
	callBlock, callNetURL := "", false
	if len(callees) > 0 {
		callBlock, callNetURL = remoteCallBlock(callees)
	}
	foreignBg := foreignBackgroundHandlers(g, svc)
	needsTime := false
	for _, r := range g.Resources {
		if r.Kind == "queue" && (r.MaxAttempts > 0 || r.MinBackoffSeconds > 0 || r.MaxBackoffSeconds > 0) {
			if r.Package == svc.Package || (len(foreignBg) > 0 && isPrimaryService(g, svc)) {
				needsTime = true
			}
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, `// Code generated by tako. DO NOT EDIT.
//
// Service %s, generated from the app's declarations. This file is meant to
// be read; to change it, change the app (or the generator), never this
// output.
package main

import (
`, svc.Name)
	if site != nil {
		b.WriteString("\t\"embed\"\n")
	}
	if strings.Contains(callBlock, "fmt.Sprint") {
		b.WriteString("\t\"fmt\"\n")
	}
	if site != nil {
		b.WriteString("\t\"io/fs\"\n")
	}
	b.WriteString("\t\"net/http\"\n")
	if callNetURL {
		b.WriteString("\tneturl \"net/url\"\n")
	}
	if len(callees) > 0 || len(foreignBg) > 0 {
		b.WriteString("\t\"os\"\n")
	}
	if needsTime {
		b.WriteString("\t\"time\"\n")
	}
	b.WriteString("\n")
	if len(callees) > 0 {
		b.WriteString("\ttako \"github.com/gsandoval/tako\"\n")
	}
	b.WriteString("\truntime \"github.com/gsandoval/tako/pkg/runtime\"\n")
	if usesApp {
		fmt.Fprintf(&b, "\n\t%s %q\n", alias, svc.Package)
	}
	importedPkgs := map[string]string{}
	if usesApp {
		importedPkgs[svc.Package] = alias
	}
	calleePkgs := map[string]string{}
	for _, c := range callees {
		calleePkgs[c.handler.Package] = c.service
	}
	for _, c := range callees {
		if calleePkgs[c.handler.Package] != "" && c.handler.Package != svc.Package {
			pkgAliasName := calleePkgs[c.handler.Package]
			fmt.Fprintf(&b, "\t%s %q\n", pkgAliasName, c.handler.Package)
			importedPkgs[c.handler.Package] = pkgAliasName
			calleePkgs[c.handler.Package] = ""
		}
	}
	for _, h := range foreignBg {
		if importedPkgs[h.Package] == "" {
			pkgAliasName := pkgAliasForPath(g, h.Package)
			fmt.Fprintf(&b, "\t%s %q\n", pkgAliasName, h.Package)
			importedPkgs[h.Package] = pkgAliasName
		}
	}
	b.WriteString(")\n\n")
	if site != nil {
		fmt.Fprintf(&b, "// The site (%s) is embedded at build time — the binary IS the\n// deployment, so rollback rolls the site back too.\n", site.Name)
		b.WriteString("//\n//go:embed all:site\nvar siteFS embed.FS\n\n")
	}
	b.WriteString("func main() {\n")

	// Unconditional (RC3). The guard this replaces asked "does this
	// service have backends to wire", which is the right question for
	// backends and the wrong one for everything else Boot installs:
	// Cloud Logging (so lines carry a severity and a trace) and the
	// request metrics every service is supposed to export. A
	// resource-less service got none of it — hello deployed with no
	// severity on any line and no requests/request_ms at all, so an
	// alert built on them watched a series nothing wrote.
	b.WriteString("\truntime.Boot() // observability always; backends when this package declares any\n\n")
	b.WriteString(callBlock)
	writeQueueConfigs(&b, g, func(pkg string) bool { return pkg == svc.Package }, "\t", true)
	writeReservedMux(&b, g, svc, true)

	// The status page is registered before anything can answer: every
	// response tako produces for itself renders through it (FL11).
	if sp := statusOf(g, svc); sp != nil {
		fmt.Fprintf(&b, "\t// %s answers every response this service produces itself —\n\t// 404, 403, 405, 500 — so they wear the app rather than plain text.\n", sp.Var)
		fmt.Fprintf(&b, "\truntime.RegisterStatusPage(%s.%s)\n\n", alias, analyzer.ShortName(sp.Var))
	}
	b.WriteString("\tmux := http.NewServeMux()\n\n")
	if site != nil {
		fmt.Fprintf(&b, "\t// site %s: declared routes win (more specific patterns); the site\n\t// serves everything else with §2.11's headers. Its templates/ back\n\t// site.Render and are never served statically.\n", site.Name)
		b.WriteString("\tsiteRoot, err := fs.Sub(siteFS, \"site\")\n\tif err != nil {\n\t\tpanic(err)\n\t}\n")
		fmt.Fprintf(&b, "\truntime.RegisterSiteTemplates(siteRoot, runtime.SiteOptions{%s})\n", siteOptionsLiteral(site))
		fmt.Fprintf(&b, "\tmux.Handle(\"/\", runtime.SiteHandler(siteRoot, runtime.SiteOptions{%s}))\n\n", siteOptionsLiteral(site))
	}
	decoders := writeMounts(&b, g, svc, alias)

	var localBg []analyzer.Handler
	for _, h := range g.Handlers {
		if h.Package == svc.Package {
			localBg = append(localBg, h)
		}
	}
	writeBackgroundRegistrations(&b, localBg, func(analyzer.Handler) string { return alias }, "\t", true)

	if len(foreignBg) > 0 {
		b.WriteString("\t// Multi-package local dev: run foreign-package background handlers in-process.\n")
		b.WriteString("\tif os.Getenv(\"TAKO_ENV\") == \"\" {\n")
		writeQueueConfigs(&b, g, func(pkg string) bool { return pkg != svc.Package }, "\t\t", false)
		writeBackgroundRegistrations(&b, foreignBg, func(h analyzer.Handler) string { return importedPkgs[h.Package] }, "\t\t", false)
		b.WriteString("\t}\n\n")
	}

	served := "mux"
	if hasAuth(g) || hasBucket(g, svc) || hasCron(g, svc) {
		served = "runtime.Reserved(takoMux, mux)"
	}
	if hasAuth(g) {
		fmt.Fprintf(&b, "\truntime.Serve(runtime.WithDevBar(%s)) // the dev bar injects locally only\n}\n", served)
	} else {
		fmt.Fprintf(&b, "\truntime.Serve(%s)\n}\n", served)
	}

	for _, h := range decoders {
		writeDecoder(&b, alias, h)
	}

	src, err := format.Source([]byte(b.String()))
	if err != nil {
		return nil, fmt.Errorf("generated main for service %s does not format: %w", svc.Name, err)
	}
	return src, nil
}
