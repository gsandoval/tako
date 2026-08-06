package analyzer

import (
	"fmt"
	"go/ast"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

// extractSite validates one tako.Site declaration: the served directory
// (a string constant) and its tako.SiteConfig — SPA, Build, DevProxy,
// Redirects, TrustedHTML.
func extractSite(dir string, pkg *packages.Package, name *ast.Ident, call *ast.CallExpr, errs *ErrorList) *Resource {
	if name.Name == "_" || len(call.Args) < 1 {
		return nil
	}
	bad := false
	fail := func(e *Error) { *errs = append(*errs, e); bad = true }

	siteDir, err := constString(pkg, call.Args[0])
	if err != nil {
		fail(&Error{
			Pos: relPos(dir, pkg.Fset, call.Args[0].Pos()),
			Msg: fmt.Sprintf("tako.Site directory must be a string constant — paths are read at analysis time, not run time; got %s",
				describeExpr(call.Args[0])),
			Rule: "const-name",
		})
		return nil
	}

	r := &Resource{
		Kind:    "site",
		Name:    siteDir,
		Var:     pkg.Name + "." + name.Name,
		Package: pkg.PkgPath,
		// Dir (and BuildOut below) are normalized MODULE-relative so
		// codegen and the build runner resolve them without package
		// disk locations.
		Dir: moduleRel(dir, pkg, siteDir),
		Pos: relPos(dir, pkg.Fset, name.Pos()),
	}
	cfg, cok := configFields(dir, pkg, call.Args[1:], "SiteConfig", errs)
	if !cok {
		bad = true
	}
	r.SPA = boolField(dir, pkg, cfg, "SPA", "SiteConfig", errs)
	// The markdown opt-out: raw HTML passes through. Recorded on the
	// graph so the posture is visible in review.
	r.TrustedHTML = boolField(dir, pkg, cfg, "TrustedHTML", "SiteConfig", errs)
	if u, set := stringField(dir, pkg, cfg, "DevProxy", "SiteConfig", errs); set {
		r.DevProxy = u
	}
	if nf, set := stringField(dir, pkg, cfg, "NotFound", "SiteConfig", errs); set {
		r.NotFound = nf
	}
	if b, set := nestedLit(dir, pkg, cfg, "Build", "Build", errs); set {
		cmd, ok1 := stringField(dir, pkg, b, "Command", "Build", errs)
		out, ok2 := stringField(dir, pkg, b, "Out", "Build", errs)
		if ok1 && ok2 {
			r.Build, r.BuildOut = cmd, moduleRel(dir, pkg, out)
		}
	}
	if e, set := fieldOf(cfg, "Redirects"); set {
		lit, ok := e.(*ast.CompositeLit)
		if !ok {
			fail(&Error{
				Pos:  relPos(dir, pkg.Fset, e.Pos()),
				Msg:  "tako.SiteConfig.Redirects must be a map literal with constant keys and values — redirects are read at analysis time, not run time",
				Rule: "config-literal",
			})
		} else {
			for _, el := range lit.Elts {
				kv, ok := el.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				from, ferr := constString(pkg, kv.Key)
				to, terr := constString(pkg, kv.Value)
				if ferr != nil || terr != nil {
					fail(&Error{
						Pos:  relPos(dir, pkg.Fset, kv.Pos()),
						Msg:  "tako.SiteConfig.Redirects keys and values must be string constants — they are read at analysis time",
						Rule: "const-name",
					})
					continue
				}
				r.Redirects = append(r.Redirects, SiteRedirect{From: from, To: to})
			}
			// Map iteration order is not source order; sort so the graph
			// (and every hash derived from it) is deterministic.
			sort.Slice(r.Redirects, func(i, j int) bool { return r.Redirects[i].From < r.Redirects[j].From })
		}
	}

	// The directory must exist at analysis time — unless a build creates
	// it, in which case the build step owns the check.
	if r.Build == "" {
		if abs := filepath.Join(pkgDir(pkg), siteDir); !dirExists(abs) {
			fail(&Error{
				Pos:  relPos(dir, pkg.Fset, call.Args[0].Pos()),
				Msg:  fmt.Sprintf("tako.Site directory %s does not exist (add it, or set SiteConfig.Build if a generator creates it)", siteDir),
				Rule: "site-dir",
			})
		}
	}

	if bad {
		return nil
	}
	return r
}

// pkgDir is the directory holding the package's source files.
func pkgDir(pkg *packages.Package) string {
	if len(pkg.GoFiles) > 0 {
		return filepath.Dir(pkg.GoFiles[0])
	}
	return "."
}

// moduleRel rewrites a package-relative path as module-relative (slash
// form) — the coordinate system codegen and the site build runner use.
func moduleRel(moduleDir string, pkg *packages.Package, p string) string {
	absModule, err := filepath.Abs(moduleDir)
	if err != nil {
		return filepath.ToSlash(p)
	}
	abs := filepath.Join(pkgDir(pkg), p)
	if rel, err := filepath.Rel(absModule, abs); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(p)
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// extractAuth validates one tako.Auth declaration.
func extractAuth(dir string, pkg *packages.Package, name *ast.Ident, call *ast.CallExpr, errs *ErrorList) *Resource {
	if name.Name == "_" {
		return nil
	}
	r := &Resource{
		Kind:    "auth",
		Name:    "auth",
		Var:     pkg.Name + "." + name.Name,
		Package: pkg.PkgPath,
		Pos:     relPos(dir, pkg.Fset, name.Pos()),
	}
	bad := false
	cfg, ok := configFields(dir, pkg, call.Args, "AuthConfig", errs)
	if !ok {
		bad = true
	}
	if boolField(dir, pkg, cfg, "Google", "AuthConfig", errs) {
		r.Providers = append(r.Providers, "google")
	}
	if boolField(dir, pkg, cfg, "Password", "AuthConfig", errs) {
		r.Providers = append(r.Providers, "email")
	}
	if len(r.Providers) == 0 && !bad {
		*errs = append(*errs, &Error{
			Pos:  relPos(dir, pkg.Fset, call.Pos()),
			Msg:  "tako.Auth needs at least one provider (AuthConfig{Google: true} or AuthConfig{Password: true})",
			Rule: "auth-provider",
		})
		bad = true
	}
	if bad {
		return nil
	}
	return r
}

// extractStatusPage validates one tako.StatusPage declaration. It
// carries no configuration — the whole declaration is the function —
// so what is validated is the shape: an exported var, because the
// generated service names it from package main.
func extractStatusPage(dir string, pkg *packages.Package, name *ast.Ident, call *ast.CallExpr, errs *ErrorList) *Resource {
	if name.Name == "_" {
		return nil
	}
	if !name.IsExported() {
		*errs = append(*errs, &Error{
			Pos: relPos(dir, pkg.Fset, name.Pos()),
			Msg: fmt.Sprintf("status page %s must be exported — the generated service registers it by name from outside the package; rename it %s",
				name.Name, strings.ToUpper(name.Name[:1])+name.Name[1:]),
			Rule: "unexported-status",
		})
		return nil
	}
	return &Resource{
		Kind:    "status",
		Name:    "status",
		Var:     pkg.Name + "." + name.Name,
		Package: pkg.PkgPath,
		Pos:     relPos(dir, pkg.Fset, name.Pos()),
	}
}
