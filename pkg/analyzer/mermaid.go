package analyzer

import (
	"fmt"
	"strings"
)

// Mermaid renders the AppGraph as a mermaid flowchart: one subgraph per
// service with its handlers, resource nodes shaped by family (stores as
// cylinders, carriers as hexagons, the rest as stadiums), and an edge
// per async binding (queue → worker, topic/bucket event → subscriber).
// Publish/read edges join once the analyzer derives usage edges.
func Mermaid(g *AppGraph) string {
	var b strings.Builder
	b.WriteString("flowchart LR\n")

	for _, svc := range g.Services {
		fmt.Fprintf(&b, "  subgraph %s[\"service %s\"]\n", nodeID("svc", svc.Name), svc.Name)
		for _, h := range g.Handlers {
			if h.Service != svc.Name {
				continue
			}
			fmt.Fprintf(&b, "    %s[\"%s\"]\n", nodeID("h", h.Var), handlerLabel(h))
		}
		b.WriteString("  end\n")
	}

	for _, r := range g.Resources {
		open, close := "([\"", "\"])"
		switch r.Kind {
		case "entity", "bucket", "cache", "disk":
			open, close = "[(\"", "\")]"
		case "queue", "topic":
			open, close = "{{\"", "\"}}"
		}
		fmt.Fprintf(&b, "  %s%s%s %s%s\n", nodeID("r", r.Kind+"_"+r.Name), open, r.Kind, r.Name, close)
	}

	for _, h := range g.Handlers {
		switch h.Kind {
		case "worker":
			fmt.Fprintf(&b, "  %s --> %s\n", nodeID("r", "queue_"+h.Binding), nodeID("h", h.Var))
		case "subscribe":
			source := h.Binding
			if h.BindingKind == "bucket" {
				// photos.Uploaded rides the bucket's node.
				source = "bucket_" + strings.TrimSuffix(h.Binding, ".Uploaded")
				fmt.Fprintf(&b, "  %s -. \"%s\" .-> %s\n", nodeID("r", source), h.Binding, nodeID("h", h.Var))
				continue
			}
			fmt.Fprintf(&b, "  %s --> %s\n", nodeID("r", "topic_"+source), nodeID("h", h.Var))
		}
	}
	return b.String()
}

func handlerLabel(h Handler) string {
	switch h.Kind {
	case "worker":
		return "worker " + ShortVar(h)
	case "subscribe":
		return "subscriber " + ShortVar(h)
	case "cron":
		return "cron " + ShortVar(h) + " — " + h.Schedule
	}
	return h.Route + " → " + ShortVar(h)
}

// nodeID makes a mermaid-safe node identifier.
func nodeID(prefix, raw string) string {
	clean := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		default:
			return '_'
		}
	}, raw)
	return prefix + "_" + clean
}
