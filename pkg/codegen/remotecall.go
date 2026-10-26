package codegen

import (
	"fmt"
	"sort"
	"strings"

	"github.com/gsandoval/tako/pkg/analyzer"
)

// remoteCallee is one cross-service call target this service's
// handlers reach (§2.12): its transport is registered at boot from the
// deploy-injected URL, so Call goes over the wire in the cloud and
// stays direct everywhere else.
type remoteCallee struct {
	handler analyzer.Handler
	service string // callee service name
}

// remoteCallees lists svc's cross-service call targets, in stable order.
func remoteCallees(g *analyzer.AppGraph, svc analyzer.Service) []remoteCallee {
	svcOf := map[string]string{}
	byVar := map[string]analyzer.Handler{}
	for _, h := range g.Handlers {
		svcOf[h.Var] = h.Service
		byVar[h.Var] = h
	}
	seen := map[string]bool{}
	var out []remoteCallee
	for _, e := range g.Edges {
		if e.Kind != "call" || svcOf[e.From] != svc.Name || svcOf[e.To] == svc.Name || seen[e.To] {
			continue
		}
		seen[e.To] = true
		out = append(out, remoteCallee{handler: byVar[e.To], service: svcOf[e.To]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].handler.Var < out[j].handler.Var })
	return out
}

// remoteCallBlock renders the transport registrations. Blob endpoints
// cannot ride the JSON wire and are skipped with a visible comment.
func remoteCallBlock(callees []remoteCallee) (string, bool) {
	var b strings.Builder
	usesNetURL := false
	b.WriteString("\t// Cross-service calls (§2.12): remote transports for this service's\n")
	b.WriteString("\t// callees, wired from deploy-injected URLs. Locally nothing is\n")
	b.WriteString("\t// injected — dev, tests, and co-located packages stay direct\n\t// in-process, breakpoints and all.\n")
	for _, c := range callees {
		h := c.handler
		if h.ResBlob || hasBlobBinding(h) {
			fmt.Fprintf(&b, "\t// %s is not remotely callable: blobs do not ride the JSON call wire.\n", h.Var)
			continue
		}
		envVar := "TAKO_CALLEE_" + strings.ToUpper(strings.ReplaceAll(c.service, "-", "_"))
		method, path, _ := strings.Cut(h.Route, " ")
		pathExpr, query, needsURL := remotePathExpr(path, h.Bindings)
		usesNetURL = usesNetURL || needsURL
		fmt.Fprintf(&b, "\tif base := os.Getenv(%q); base != \"\" {\n", envVar)
		fmt.Fprintf(&b, "\t\truntime.RegisterRemoteCall(%q, %q, func(ctx tako.Ctx, req %s) (%s, error) {\n",
			h.Package, h.Route, h.ReqRef, h.ResRef)
		fmt.Fprintf(&b, "\t\t\tpath := %s\n", pathExpr)
		b.WriteString(query)
		fmt.Fprintf(&b, "\t\t\treturn runtime.RemoteJSON[%s, %s](ctx, %q, base, %q, path, req)\n",
			h.ReqRef, h.ResRef, c.service, method)
		b.WriteString("\t\t})\n\t}\n")
	}
	b.WriteString("\n")
	return b.String(), usesNetURL
}

func hasBlobBinding(h analyzer.Handler) bool {
	for _, b := range h.Bindings {
		if b.Kind == "blob" {
			return true
		}
	}
	return false
}

// remotePathExpr renders the callee path as a Go expression with path
// params interpolated from req fields, plus query-string lines.
func remotePathExpr(path string, bindings []analyzer.Binding) (expr, queryLines string, usesNetURL bool) {
	fieldOfParam := map[string]string{}
	var queryBindings []analyzer.Binding
	for _, b := range bindings {
		switch b.Kind {
		case "path":
			fieldOfParam[b.Name] = b.Field
		case "query":
			queryBindings = append(queryBindings, b)
		}
	}
	var parts []string
	rest := path
	for {
		i := strings.IndexByte(rest, '{')
		if i < 0 {
			break
		}
		j := strings.IndexByte(rest[i:], '}')
		if j < 0 {
			break
		}
		if i > 0 {
			parts = append(parts, fmt.Sprintf("%q", rest[:i]))
		}
		param := rest[i+1 : i+j]
		field := fieldOfParam[strings.TrimSuffix(param, "...")]
		if strings.HasSuffix(param, "...") {
			// Wildcards span segments; their slashes must survive.
			parts = append(parts, fmt.Sprintf("fmt.Sprint(req.%s)", field))
		} else {
			parts = append(parts, fmt.Sprintf("neturl.PathEscape(fmt.Sprint(req.%s))", field))
			usesNetURL = true
		}
		rest = rest[i+j+1:]
	}
	if rest != "" || len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("%q", rest))
	}
	expr = strings.Join(parts, " + ")
	if len(queryBindings) > 0 {
		usesNetURL = true
		var q strings.Builder
		q.WriteString("\t\t\tqs := neturl.Values{}\n")
		for _, b := range queryBindings {
			if b.Type == "[]string" {
				// Add, not Set: the whole point of the field is that it
				// repeats, and Set would collapse it to one value.
				fmt.Fprintf(&q, "\t\t\tfor _, v := range req.%s {\n\t\t\t\tqs.Add(%q, v)\n\t\t\t}\n", b.Field, b.Name)
				continue
			}
			fmt.Fprintf(&q, "\t\t\tqs.Set(%q, fmt.Sprint(req.%s))\n", b.Name, b.Field)
		}
		q.WriteString("\t\t\tpath += \"?\" + qs.Encode()\n")
		queryLines = q.String()
	}
	return expr, queryLines, usesNetURL
}
