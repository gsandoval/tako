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

// applyDomains projects WithDomain claims onto services and rejects
// conflicts, the way applyProfiles projects WithProfile. A domain is
// declared on any ONE of a service's handlers — or on its site — and
// answers for the whole service, because a tako service is one origin:
// its embedded static assets and its dynamic routes share a URL.
//
// Only API and Page accept the option (a worker or job that took a
// domain would be refused as tako/invalid-option before reaching here),
// so a handler claim always lands on a service that serves traffic. A
// SITE's claim is the case that can miss: the site mounts on its
// package's app service, and a package whose every handler is async — or
// that is a job — has no such service for the domain to address.
func applyDomains(graph *AppGraph, errs *ErrorList) {
	for i := range graph.Services {
		svc := &graph.Services[i]
		claim, claimedBy := "", ""
		for _, h := range graph.Handlers {
			if h.Service != svc.Name || h.Domain == "" {
				continue
			}
			if claim != "" && claim != h.Domain {
				*errs = append(*errs, &Error{
					Pos: h.Pos,
					Msg: fmt.Sprintf("service %s cannot answer on both %q (%s) and %q (%s) — one service, one domain; split the packages or align the domains",
						svc.Name, claim, claimedBy, h.Domain, ShortVar(h)),
					Rule: "domain-conflict",
				})
				continue
			}
			claim, claimedBy = h.Domain, ShortVar(h)
		}
		if claim != "" {
			svc.Domain = claim
		}
	}

	// Site claims, projected onto whichever service mounts the site —
	// the same choice codegen's siteOf makes.
	for _, r := range graph.Resources {
		if r.Kind != "site" || r.Domain == "" {
			continue
		}
		svc := mountingService(graph, r.Package)
		if svc == nil {
			*errs = append(*errs, &Error{
				Pos: r.Pos,
				Msg: fmt.Sprintf("the site declares the domain %q, but no service in this package serves public traffic — queue workers, topic subscribers, and jobs are reached over authenticated push or not at all, so the name would answer nothing; declare an API or Page route here, or move the domain to the package that has one",
					r.Domain),
				Rule: "domain-unreachable",
			})
			continue
		}
		if svc.Domain != "" && svc.Domain != r.Domain {
			*errs = append(*errs, &Error{
				Pos: r.Pos,
				Msg: fmt.Sprintf("service %s cannot answer on both %q (a handler) and %q (the site) — one service, one domain; split the packages or align the domains",
					svc.Name, svc.Domain, r.Domain),
				Rule: "domain-conflict",
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
