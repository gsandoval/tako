package codegen

import (
	"fmt"
	"go/token"
	"strings"

	"github.com/gsandoval/tako/internal/routes"
	"github.com/gsandoval/tako/pkg/analyzer"
)

// writeQueueConfigs emits runtime.SetQueueRate and runtime.SetQueueDelivery
// calls for queues matching pkgFilter.
func writeQueueConfigs(b *strings.Builder, g *analyzer.AppGraph, pkgFilter func(string) bool, indent string, commented bool) {
	for _, r := range g.Resources {
		if r.Kind != "queue" || !pkgFilter(r.Package) {
			continue
		}
		if r.RateLimit > 0 {
			if commented {
				fmt.Fprintf(b, "%s// queue %s: WithDelivery RateLimit %d — Cloud Tasks caps dispatch\n%s// in the cloud; locally the dispatcher paces to the same rate.\n", indent, r.Name, r.RateLimit, indent)
				fmt.Fprintf(b, "%sruntime.SetQueueRate(%q, %d)\n\n", indent, r.Name, r.RateLimit)
			} else {
				fmt.Fprintf(b, "%sruntime.SetQueueRate(%q, %d)\n", indent, r.Name, r.RateLimit)
			}
		}
		if r.MaxAttempts > 0 || r.MinBackoffSeconds > 0 || r.MaxBackoffSeconds > 0 {
			trail := "\n"
			if commented {
				trail = "\n\n"
			}
			fmt.Fprintf(b, "%sruntime.SetQueueDelivery(%q, %d, %d*time.Second, %d*time.Second)%s",
				indent, r.Name, r.MaxAttempts, r.MinBackoffSeconds, r.MaxBackoffSeconds, trail)
		}
	}
}

// writeBackgroundRegistrations emits runtime.Register* calls for worker,
// subscribe, cron, job, and singleton handlers.
func writeBackgroundRegistrations(b *strings.Builder, handlers []analyzer.Handler, aliasFor func(analyzer.Handler) string, indent string, commented bool) {
	for _, h := range handlers {
		regFn, commentSuffix := backgroundRegistrationSpec(h.Kind)
		if regFn == "" {
			continue
		}
		alias := aliasFor(h)
		if commented {
			fmt.Fprintf(b, "%s// %s — %s\n", indent, analyzer.HandlerLine(h), commentSuffix)
			fmt.Fprintf(b, "%sruntime.%s(%q, %s.%s)\n\n", indent, regFn, analyzer.ShortVar(h), alias, analyzer.ShortVar(h))
		} else {
			fmt.Fprintf(b, "%sruntime.%s(%q, %s.%s)\n", indent, regFn, analyzer.ShortVar(h), alias, analyzer.ShortVar(h))
		}
	}
}

func backgroundRegistrationSpec(kind string) (regFn, commentSuffix string) {
	switch kind {
	case "worker":
		return "RegisterWorker", "local mode runs the worker in-process"
	case "subscribe":
		return "RegisterSubscriber", "local mode runs the subscriber in-process"
	case "cron":
		return "RegisterCron", "runs in this service on its schedule"
	case "job":
		return "RegisterJob", "local mode runs executions in-process (a real Cloud Run Job in the cloud)"
	case "singleton":
		return "RegisterSingleton", "local mode runs the loop in-process (its own min=1 service in the cloud)"
	}
	return "", ""
}

// writeReservedMux emits the /_tako/ reserved endpoints mux when the
// service uses auth, buckets, or crons.
func writeReservedMux(b *strings.Builder, g *analyzer.AppGraph, svc analyzer.Service, commented bool) {
	hasReserved := hasAuth(g) || hasBucket(g, svc) || hasCron(g, svc)
	if !hasReserved {
		if !commented {
			b.WriteString("\ttakoMux := http.NewServeMux()\n\n")
		}
		return
	}
	if commented {
		b.WriteString("\t// tako's own endpoints, on a mux of their own. They are answered\n")
		b.WriteString("\t// before the app's router below is consulted, so nothing under\n")
		b.WriteString("\t// /_tako/ can be shadowed by a route this app declares — and no\n")
		b.WriteString("\t// framework pattern can collide with a wildcard page, which on one\n")
		b.WriteString("\t// shared mux is a panic at startup rather than a bad response.\n")
	}
	b.WriteString("\ttakoMux := http.NewServeMux()\n")
	if hasAuth(g) {
		if commented {
			b.WriteString("\n\t// Auth (tako.Auth): the session endpoint — sign-in posts the\n\t// Identity Platform ID token here once; sessions are revocable\n\t// records in the entity store. The signout form is the dev bar's.\n")
			b.WriteString("\t//\n\t// The declared providers ride along: a session may only come from\n\t// one of them, whatever else the Identity Platform project has\n\t// enabled.\n")
		}
		fmt.Fprintf(b, "\ttakoMux.Handle(%q, runtime.SessionHandler(%s))\n", routes.PatternSession, providerArgs(g))
		fmt.Fprintf(b, "\ttakoMux.Handle(%q, runtime.SignOutHandler())\n", routes.PatternSignOut)
		fmt.Fprintf(b, "\ttakoMux.Handle(%q, runtime.SignInPage(%s))\n", routes.PatternSignIn, providerArgs(g))
		if len(g.Roles) > 0 {
			if commented {
				fmt.Fprintf(b, "\truntime.RegisterRoles(%s) // tako.Role declarations; the dev sign-in page offers them\n", quotedList(g.Roles))
			} else {
				fmt.Fprintf(b, "\truntime.RegisterRoles(%s)\n", quotedList(g.Roles))
			}
		}
	}
	if hasBucket(g, svc) {
		if commented {
			b.WriteString("\n\t// Signed upload/download URLs land here (SignedPut/SignedGet).\n")
		}
		fmt.Fprintf(b, "\ttakoMux.Handle(%q, runtime.SignedURLHandler())\n", routes.PatternSigned)
	}
	if hasCron(g, svc) {
		if commented {
			b.WriteString("\n\t// Cloud Scheduler triggers crons here (T2); the request proves\n\t// itself with a Google-signed OIDC token — locally the in-process\n\t// ticker drives them and this route just refuses strangers.\n")
		}
		fmt.Fprintf(b, "\ttakoMux.Handle(%q, runtime.CronDispatch())\n", routes.PatternCron)
	}
	b.WriteString("\n")
}

// quotedList renders strings as a Go argument list: "a", "b".
func quotedList(items []string) string {
	out := make([]string, len(items))
	for i, s := range items {
		out[i] = fmt.Sprintf("%q", s)
	}
	return strings.Join(out, ", ")
}

// providerArgs renders the declared providers as SignInPage arguments.
func providerArgs(g *analyzer.AppGraph) string {
	for _, r := range g.Resources {
		if r.Kind == "auth" {
			quoted := make([]string, len(r.Providers))
			for i, p := range r.Providers {
				quoted[i] = fmt.Sprintf("%q", p)
			}
			return strings.Join(quoted, ", ")
		}
	}
	return ""
}

// hasAuth reports whether the app declares tako.Auth.
func hasAuth(g *analyzer.AppGraph) bool {
	for _, r := range g.Resources {
		if r.Kind == "auth" {
			return true
		}
	}
	return false
}

// hasCron reports whether any cron handler runs in this service — it
// then carries the /_tako/cron dispatch route Cloud Scheduler targets.
func hasCron(g *analyzer.AppGraph, svc analyzer.Service) bool {
	for _, h := range g.Handlers {
		if h.Service == svc.Name && h.Kind == "cron" {
			return true
		}
	}
	return false
}

// hasBucket reports whether the service's package declares a bucket —
// the signal to mount the local signed-URL endpoint.
func hasBucket(g *analyzer.AppGraph, svc analyzer.Service) bool {
	for _, r := range g.Resources {
		if r.Package == svc.Package && r.Kind == "bucket" {
			return true
		}
	}
	return false
}

// hasResources reports whether the service's package declares resources
// needing a booted backend.
func hasResources(g *analyzer.AppGraph, svc analyzer.Service) bool {
	for _, r := range g.Resources {
		if r.Package == svc.Package {
			return true
		}
	}
	return false
}

// asyncOnly reports whether every handler of the service is async (worker
// or subscriber) — no main is generated for it yet.
func asyncOnly(g *analyzer.AppGraph, svc analyzer.Service) bool {
	any := false
	for _, h := range g.Handlers {
		if h.Service != svc.Name {
			continue
		}
		any = true
		if h.Kind != "worker" && h.Kind != "subscribe" {
			return false
		}
	}
	return any
}

// pkgAlias is the import alias for the service's user package in
// generated code: the package's OWN name, which is what the rest of
// the graph already calls it (Handler.Var is "<package>.<Var>") and
// which is an identifier by definition.
//
// It is not the last segment of the import path. That segment is not
// necessarily a Go identifier — `github.com/org/my-service` is the
// ordinary way to name a repository, and `my-service
// "github.com/org/my-service"` is a parse error at the hyphen — and
// even when it parses, spelling the module path through every
// generated reference (`tako_example_hello.GreetReq`) makes the output
// harder to read than the app it was generated from.
func pkgAlias(g *analyzer.AppGraph, svc analyzer.Service) string {
	return pkgAliasForPath(g, svc.Package)
}

func pkgAliasForPath(g *analyzer.AppGraph, pkgPath string) string {
	for _, h := range g.Handlers {
		if h.Package != pkgPath {
			continue
		}
		if i := strings.Index(h.Var, "."); i > 0 {
			if name := h.Var[:i]; token.IsIdentifier(name) {
				return name
			}
		}
	}
	return aliasFromPath(pkgPath)
}

func isPrimaryService(g *analyzer.AppGraph, svc analyzer.Service) bool {
	for _, s := range g.Services {
		if !s.Job && !s.Singleton && s.Package == g.Module {
			return svc.Name == s.Name
		}
	}
	for _, s := range g.Services {
		if !s.Job && !s.Singleton && !asyncOnly(g, s) {
			return svc.Name == s.Name
		}
	}
	return false
}

func foreignBackgroundHandlers(g *analyzer.AppGraph, svc analyzer.Service) []analyzer.Handler {
	if !isPrimaryService(g, svc) {
		return nil
	}
	var out []analyzer.Handler
	for _, h := range g.Handlers {
		if h.Package == svc.Package {
			continue
		}
		switch h.Kind {
		case "worker", "subscribe", "cron", "job", "singleton":
			out = append(out, h)
		}
	}
	return out
}

// aliasFromPath is the fallback for a service with no handler to read a
// package name from: the last path segment, forced into an identifier.
func aliasFromPath(path string) string {
	seg := path
	if i := strings.LastIndex(seg, "/"); i >= 0 {
		seg = seg[i+1:]
	}
	var b strings.Builder
	for _, r := range seg {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	alias := b.String()
	if alias == "" || alias[0] >= '0' && alias[0] <= '9' {
		alias = "pkg" + alias
	}
	if token.IsKeyword(alias) {
		alias += "_"
	}
	return alias
}

// statusOf finds the status page the service's package declares, if
// any. Like the site, it belongs to the package, so an async service
// of the same package registers it too — a worker can answer a 404 on
// its push endpoint like anything else.
func statusOf(g *analyzer.AppGraph, svc analyzer.Service) *analyzer.Resource {
	for i, r := range g.Resources {
		if r.Kind == "status" && r.Package == svc.Package {
			return &g.Resources[i]
		}
	}
	return nil
}
