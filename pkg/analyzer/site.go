package analyzer

import (
	"fmt"
	"go/ast"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/tools/go/packages"
)

// siteOptions is the option set tako.Site accepts in W1; the rest of
// §2.11's surface names its future slice instead of failing blind.
var siteNotYet = map[string]string{
	"WithDomain": "custom domains (mapping + TLS + HSTS) are their own slice",
	"WithCDN":    "bucket + Cloud CDN serving is its own when-scale-justifies slice",
}

// extractSite validates one tako.Site declaration: the served directory
// (a string constant), and the W1 options — SPA(), WithBuild(cmd, out),
// WithDevProxy(url), WithRedirect(from, to).
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
	for _, opt := range call.Args[1:] {
		optName, isOpt := sdkOptionCall(pkg, opt)
		optCall, _ := opt.(*ast.CallExpr)
		if !isOpt || optCall == nil {
			fail(&Error{
				Pos:  relPos(dir, pkg.Fset, opt.Pos()),
				Msg:  fmt.Sprintf("tako.Site accepts SPA, WithBuild, WithDevProxy, WithRedirect, WithTrustedHTML; got %s", describeExpr(opt)),
				Rule: "invalid-option",
			})
			continue
		}
		if why, ok := siteNotYet[optName]; ok {
			fail(&Error{
				Pos:  relPos(dir, pkg.Fset, opt.Pos()),
				Msg:  fmt.Sprintf("tako.%s is not available yet — %s", optName, why),
				Rule: "not-yet",
			})
			continue
		}
		constArg := func(i int) (string, bool) {
			if i >= len(optCall.Args) {
				return "", false
			}
			s, err := constString(pkg, optCall.Args[i])
			if err != nil {
				fail(&Error{
					Pos: relPos(dir, pkg.Fset, optCall.Args[i].Pos()),
					Msg: fmt.Sprintf("tako.%s arguments must be string constants — they are read at analysis time; got %s",
						optName, describeExpr(optCall.Args[i])),
					Rule: "const-name",
				})
				return "", false
			}
			return s, true
		}
		switch optName {
		case "SPA":
			r.SPA = true
		case "WithTrustedHTML":
			// The markdown opt-out: raw HTML passes through. Recorded on
			// the graph so the posture is visible in review.
			r.TrustedHTML = true
		case "WithBuild":
			cmd, ok1 := constArg(0)
			out, ok2 := constArg(1)
			if ok1 && ok2 {
				r.Build, r.BuildOut = cmd, moduleRel(dir, pkg, out)
			}
		case "WithDevProxy":
			if u, ok := constArg(0); ok {
				r.DevProxy = u
			}
		case "WithRedirect":
			from, ok1 := constArg(0)
			to, ok2 := constArg(1)
			if ok1 && ok2 {
				r.Redirects = append(r.Redirects, SiteRedirect{From: from, To: to})
			}
		default:
			fail(&Error{
				Pos:  relPos(dir, pkg.Fset, opt.Pos()),
				Msg:  fmt.Sprintf("tako.%s is not a tako.Site option (tako.Site accepts SPA, WithBuild, WithDevProxy, WithRedirect, WithTrustedHTML)", optName),
				Rule: "invalid-option",
			})
		}
	}

	// The directory must exist at analysis time — unless a build creates
	// it, in which case the build step owns the check.
	if r.Build == "" {
		if abs := filepath.Join(pkgDir(pkg), siteDir); !dirExists(abs) {
			fail(&Error{
				Pos:  relPos(dir, pkg.Fset, call.Args[0].Pos()),
				Msg:  fmt.Sprintf("tako.Site directory %s does not exist (add it, or use WithBuild if a generator creates it)", siteDir),
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
