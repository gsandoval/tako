package analyzer

import (
	"fmt"
	"strings"
)

// Summary renders the graph the way `tako gen graph` and `tako dev` print
// it: services, then one line per endpoint.
func Summary(g *AppGraph) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %d service%s\n", g.Module, len(g.Services), plural(len(g.Services)))
	for _, svc := range g.Services {
		foot := ""
		if svc.Footprint != nil {
			foot = " + footprint (" + svc.Footprint.String() + ")"
		}
		domain := ""
		if svc.Domain != "" {
			domain = ", domain " + svc.Domain
		}
		fmt.Fprintf(&b, "  service %s (package %s, profile %s%s%s)\n", svc.Name, svc.Package, svc.Profile, foot, domain)
		for _, h := range g.Handlers {
			if h.Service != svc.Name {
				continue
			}
			fmt.Fprintf(&b, "    %s\n", HandlerLine(h))
		}
		for _, r := range g.Resources {
			// The service declaration is the LINE it annotates, not a
			// resource sitting under it — its domain and profile already
			// render above.
			if r.Package != svc.Package || r.Kind == "service" {
				continue
			}
			// Channels and alerts belong to the APP, not to a service:
			// they run nowhere. Printing them under each service of a
			// package makes five copies read as five alerts.
			if r.Kind == "channel" || r.Kind == "alert" {
				continue
			}
			fmt.Fprintf(&b, "    %s\n", ResourceLine(r))
		}
	}
	// App-scoped declarations, once, after the services they watch.
	var watching []Resource
	for _, r := range g.Resources {
		if r.Kind == "channel" || r.Kind == "alert" {
			watching = append(watching, r)
		}
	}
	if len(watching) > 0 {
		fmt.Fprintf(&b, "  watching\n")
		for _, r := range watching {
			fmt.Fprintf(&b, "    %s\n", ResourceLine(r))
		}
	}
	return b.String()
}

// HandlerLine renders one handler for summaries and the dev banner.
func HandlerLine(h Handler) string {
	lock := ""
	if h.RequiresAuth {
		lock = " 🔒"
	}
	switch h.Kind {
	case "job":
		par := ""
		if h.Parallel > 1 {
			par = fmt.Sprintf(" ×%d tasks", h.Parallel)
		}
		return fmt.Sprintf("job %s ← %s%s (%s)", ShortVar(h), h.Binding, par, ShortPos(h.Pos))
	case "singleton":
		// The binding IS the var now, so printing both stutters.
		return fmt.Sprintf("singleton %s — exactly one everywhere (%s)", ShortVar(h), ShortPos(h.Pos))
	case "worker":
		return fmt.Sprintf("worker %s ← queue %s (%s)", ShortVar(h), h.Binding, ShortPos(h.Pos))
	case "subscribe":
		return fmt.Sprintf("subscriber %s ← %s %s (%s)", ShortVar(h), h.BindingKind, h.Binding, ShortPos(h.Pos))
	case "cron":
		flight := ""
		if h.Singleflight {
			flight = " (singleflight)"
		}
		return fmt.Sprintf("cron %s — %s%s (%s)", ShortVar(h), h.Schedule, flight, ShortPos(h.Pos))
	}
	return fmt.Sprintf("%s → %s%s (%s)", h.Route, ShortVar(h), lock, ShortPos(h.Pos))
}

// ResourceLine renders one resource for summaries and the dev banner,
// e.g. `entity links (indexes: Owner, Created)`.
func ResourceLine(r Resource) string {
	line := r.Kind + " " + r.Name
	if len(r.Indexes) > 0 {
		line += " (indexes: " + strings.Join(r.Indexes, ", ") + ")"
	}
	if r.Kind == "auth" {
		return "auth (" + strings.Join(r.Providers, ", ") + ")"
	}
	if r.Kind == "channel" {
		return "channel " + r.Name + " → " + r.ChannelKind + " " + r.Target
	}
	if r.Kind == "alert" {
		// An alert nobody can see is an alert nobody trusts: name the
		// condition and every destination it will wake.
		return "alert " + r.Name + " — " + r.Condition + " → " + strings.Join(shortVars(r.Channels), ", ")
	}
	if r.TrustedHTML {
		// The markdown opt-out is a security posture — visible wherever
		// the site is described, never buried in the graph JSON.
		line += " (trusted-html: markdown renders raw)"
	}
	return line
}

// ShortVar is the handler var without its package qualifier, for display
// within its own service.
func ShortVar(h Handler) string {
	if i := strings.LastIndex(h.Var, "."); i >= 0 {
		return h.Var[i+1:]
	}
	return h.Var
}

// ShortPos drops the column from a path:line:col position for display.
func ShortPos(pos string) string {
	if i := strings.LastIndex(pos, ":"); i >= 0 {
		return pos[:i]
	}
	return pos
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// shortVars drops the package qualifier from declared var names for
// display: "gallery.oncall" reads as "oncall" beside its own service.
func shortVars(vars []string) []string {
	out := make([]string, len(vars))
	for i, v := range vars {
		if dot := strings.LastIndexByte(v, '.'); dot >= 0 {
			out[i] = v[dot+1:]
			continue
		}
		out[i] = v
	}
	return out
}
