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
		fmt.Fprintf(&b, "  service %s (package %s, profile %s)\n", svc.Name, svc.Package, svc.Profile)
		for _, h := range g.Handlers {
			if h.Service != svc.Name {
				continue
			}
			fmt.Fprintf(&b, "    %s\n", HandlerLine(h))
		}
		for _, r := range g.Resources {
			if r.Package != svc.Package {
				continue
			}
			fmt.Fprintf(&b, "    %s\n", ResourceLine(r))
		}
	}
	return b.String()
}

// HandlerLine renders one handler for summaries and the dev banner.
func HandlerLine(h Handler) string {
	if h.Kind == "worker" {
		return fmt.Sprintf("worker %s ← queue %s (%s)", ShortVar(h), h.Binding, ShortPos(h.Pos))
	}
	return fmt.Sprintf("%s → %s (%s)", h.Route, ShortVar(h), ShortPos(h.Pos))
}

// ResourceLine renders one resource for summaries and the dev banner,
// e.g. `entity links (indexes: Owner, Created)`.
func ResourceLine(r Resource) string {
	line := r.Kind + " " + r.Name
	if len(r.Indexes) > 0 {
		line += " (indexes: " + strings.Join(r.Indexes, ", ") + ")"
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
