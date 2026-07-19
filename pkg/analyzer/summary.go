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
			fmt.Fprintf(&b, "    %s → %s (%s)\n", h.Route, ShortVar(h), ShortPos(h.Pos))
		}
	}
	return b.String()
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
