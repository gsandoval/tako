package analyzer

import (
	"fmt"
	"go/ast"
	"strings"

	"golang.org/x/tools/go/packages"
)

// domainProblem reports why d is not a bare fully-qualified domain name,
// or "" when it is one. The analyzer validates SHAPE only — that the
// string is a domain name at all. Backend limits (Cloud Run's 64-char
// cap and its region list) are deploy's to enforce, because they belong
// to one backend and the analyzer serves both.
func domainProblem(d string) string {
	switch {
	case d == "":
		return "needs a domain name — got an empty string"
	case strings.Contains(d, "://"), strings.Contains(d, "/"),
		strings.Contains(d, ":"), strings.HasSuffix(d, "."),
		strings.ContainsAny(d, " \t"):
		return fmt.Sprintf("needs a bare domain name — no scheme, port, path, or trailing dot; got %q", d)
	case strings.Contains(d, "*"):
		return fmt.Sprintf("does not support wildcards — got %q; declare each subdomain on its own service", d)
	case !strings.Contains(d, "."):
		return fmt.Sprintf("needs a fully-qualified domain — %q has no dot", d)
	case d != strings.ToLower(d):
		return fmt.Sprintf("needs a lowercase domain — DNS is case-insensitive, so %q would hash differently than the same name in lowercase", d)
	case len(d) > 253:
		return fmt.Sprintf("needs a domain of at most 253 characters — %q is %d", d, len(d))
	}
	for _, label := range strings.Split(d, ".") {
		if label == "" || len(label) > 63 ||
			strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return fmt.Sprintf("needs a valid domain name — %q has an empty or malformed label (%q)", d, label)
		}
		for _, r := range label {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				return fmt.Sprintf("needs a valid domain name — %q contains %q, which DNS labels do not allow", d, r)
			}
		}
	}
	return ""
}

// domainArg reads and validates a WithDomain option's argument. It
// returns "" (and appends the positioned error) when the argument is not
// a valid constant domain.
func domainArg(dir string, pkg *packages.Package, opt ast.Expr, errs *ErrorList) string {
	call, ok := opt.(*ast.CallExpr)
	if !ok || len(call.Args) < 1 {
		return ""
	}
	d, err := constString(pkg, call.Args[0])
	if err != nil {
		*errs = append(*errs, &Error{
			Pos: relPos(dir, pkg.Fset, call.Args[0].Pos()),
			Msg: fmt.Sprintf("tako.WithDomain argument must be a string constant — it is read at analysis time; got %s",
				describeExpr(call.Args[0])),
			Rule: "const-name",
		})
		return ""
	}
	if why := domainProblem(d); why != "" {
		*errs = append(*errs, &Error{
			Pos:  relPos(dir, pkg.Fset, opt.Pos()),
			Msg:  "tako.WithDomain " + why,
			Rule: "domain",
		})
		return ""
	}
	return d
}

// applyDomains projects each package's tako.Service domain claim onto
// its app service.
//
// There is no conflict rule and there cannot be one: WithDomain is
// accepted only on tako.Service, and a package declares at most one
// (tako/one-service). The single failure left is a domain in front of a
// package where nothing serves public traffic.
func applyDomains(graph *AppGraph, errs *ErrorList) {
	for _, r := range graph.Resources {
		if r.Kind != "service" || r.Domain == "" {
			continue
		}
		svc := mountingService(graph, r.Package)
		if svc == nil {
			*errs = append(*errs, &Error{
				Pos: r.Pos,
				Msg: fmt.Sprintf("this package declares the domain %q, but nothing here serves public traffic — queue workers, topic subscribers, and jobs are reached over authenticated push or not at all, so the name would answer nothing; declare an API, Page or Site route here, or move the domain to the package that has one",
					r.Domain),
				Rule: "domain-unreachable",
			})
			continue
		}
		svc.Domain = r.Domain
	}
}

// mountingService is the service in pkgPath that mounts the package's
// site and carries its public ingress: the app service, never a
// single-handler async service and never a job. Nil when the package has
// none.
func mountingService(graph *AppGraph, pkgPath string) *Service {
	for i := range graph.Services {
		svc := &graph.Services[i]
		if svc.Package != pkgPath || svc.Job {
			continue
		}
		public := false
		for _, h := range graph.Handlers {
			if h.Service == svc.Name && h.Kind != "worker" && h.Kind != "subscribe" {
				public = true
				break
			}
		}
		if public {
			return svc
		}
	}
	return nil
}

// extractService validates one tako.Service declaration: the app
// service's own record, and the home for every setting that describes a
// SERVICE rather than a route.
func extractService(dir string, pkg *packages.Package, name *ast.Ident, call *ast.CallExpr, errs *ErrorList) *Resource {
	if name.Name == "_" {
		return nil
	}
	r := &Resource{
		Kind:    "service",
		Name:    name.Name,
		Var:     pkg.Name + "." + name.Name,
		Package: pkg.PkgPath,
		Pos:     relPos(dir, pkg.Fset, name.Pos()),
	}
	bad := false
	for _, opt := range call.Args {
		optName, isOpt := sdkOptionCall(pkg, opt)
		if !isOpt {
			*errs = append(*errs, &Error{
				Pos:  relPos(dir, pkg.Fset, opt.Pos()),
				Msg:  fmt.Sprintf("tako.Service accepts WithDomain, WithProfile, WithFootprint; got %s", describeExpr(opt)),
				Rule: "invalid-option",
			})
			bad = true
			continue
		}
		switch optName {
		case "WithDomain":
			if d := domainArg(dir, pkg, opt, errs); d != "" {
				r.Domain = d
			} else {
				bad = true
			}
		case "WithProfile":
			r.Profile = profileArg(pkg, opt)
		case "WithFootprint":
			r.Footprint = footprintArg(dir, pkg, opt, errs)
		default:
			*errs = append(*errs, &Error{
				Pos:  relPos(dir, pkg.Fset, opt.Pos()),
				Msg:  fmt.Sprintf("tako.%s is not a tako.Service option (tako.Service accepts WithDomain, WithProfile, WithFootprint)", optName),
				Rule: "invalid-option",
			})
			bad = true
		}
	}
	if bad {
		return nil
	}
	return r
}

// renameAppServices applies a declared tako.Service's name to the
// package's APP service — the one running API, Page and Cron handlers.
// Worker, subscriber, job and singleton services are already named after
// the handler that spawned them and are left alone.
func renameAppServices(graph *AppGraph, serviceNames, sitePackages map[string]string) {
	for pkgPath, newName := range serviceNames {
		// The app service's old name is the package name; satellites
		// carry a suffix and must not be caught by the rename.
		old := ""
		for _, h := range graph.Handlers {
			if h.Package == pkgPath && h.Service == packageNameOf(h.Var) {
				old = h.Service
				break
			}
		}
		if old == "" {
			// No app handlers: a site-only package still deploys, and
			// its service takes the declared name too.
			if _, ok := sitePackages[pkgPath]; ok {
				sitePackages[pkgPath] = newName
			}
			continue
		}
		for i := range graph.Handlers {
			if graph.Handlers[i].Package == pkgPath && graph.Handlers[i].Service == old {
				graph.Handlers[i].Service = newName
			}
		}
		if _, ok := sitePackages[pkgPath]; ok {
			sitePackages[pkgPath] = newName
		}
	}
}

// packageNameOf is the package half of a package-qualified var
// ("blog.Home" → "blog") — which is exactly what an app service is
// named before any tako.Service declaration renames it.
func packageNameOf(qualifiedVar string) string {
	if i := strings.Index(qualifiedVar, "."); i >= 0 {
		return qualifiedVar[:i]
	}
	return qualifiedVar
}

// isAppService reports whether svc is its package's APP service — the
// one running API, Page and Cron handlers — as opposed to a worker,
// subscriber, job or singleton carved out of the same package. A
// tako.Service declaration describes only the former.
func isAppService(graph *AppGraph, svc Service) bool {
	if svc.Job || svc.Singleton {
		return false
	}
	for _, h := range graph.Handlers {
		if h.Service == svc.Name && (h.Kind == "worker" || h.Kind == "subscribe") {
			return false
		}
	}
	return true
}
