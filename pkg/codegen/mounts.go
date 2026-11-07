package codegen

import (
	"fmt"
	"strings"

	"github.com/gsandoval/tako/internal/routes"
	"github.com/gsandoval/tako/pkg/analyzer"
	"github.com/gsandoval/tako/pkg/schemagen"
)

// qual qualifies a symbol with the app package's alias. The harness
// (AB2) is generated INTO the app package, so its alias is empty and
// every symbol is unqualified — the one difference between the two
// files that mount the same routes.
func qual(alias, name string) string {
	if alias == "" || strings.HasPrefix(name, "tako.") {
		return name
	}
	return alias + "." + name
}

// writeMounts emits the declared routes of one service onto `mux`.
//
// Shared by the generated main and the generated TEST HARNESS, and that
// sharing is the point: a harness that re-typed these lines is exactly
// what AB2 exists to end. Apps hand-mirrored them, drifted three times
// in two milestones, and had four write routes that no test drove
// because nobody noticed they were never mounted.
func writeMounts(b *strings.Builder, g *analyzer.AppGraph, svc analyzer.Service, alias string) []analyzer.Handler {
	var decoders []analyzer.Handler
	for _, h := range g.Handlers {
		// Positive filter: routes only. The exclusion list this replaces
		// missed "singleton" and "job", which serviceMain never saw
		// (Generate routes those services elsewhere) but the harness
		// generator does — it tried to mount a singleton as a JSON
		// endpoint, and the generated file did not compile.
		if h.Service != svc.Name || (h.Kind != "api" && h.Kind != "page") {
			continue
		}
		decode := "nil"
		if needsDecode(h) {
			decode = decodeName(h)
			decoders = append(decoders, h)
		}
		wrapper := "JSONHandler"
		if h.ResBlob {
			wrapper = "BlobHandler"
		}
		if h.Kind == "page" {
			wrapper = "PageHandler"
		}
		opts := ""
		if h.RequiresAuth {
			opts = ", runtime.RequireAuth()"
		}
		// The policy's NAME, not the handle: Endpoint.Call evaluates the
		// rule (so a cross-service call cannot walk past it), and what
		// the mount adds is the name a status page needs to tell this
		// refusal from a stale CSRF token's.
		if h.Policy != "" {
			opts += fmt.Sprintf(", runtime.RequirePolicy(%q)", h.Policy)
		}
		// Pages and blobs read MaxAge; a JSON handler does not yet, and
		// passing it there would generate a line that says something
		// the runtime ignores.
		if h.MaxAge > 0 && (h.Kind == "page" || h.ResBlob) {
			opts += fmt.Sprintf(", runtime.CacheFor(%d)", h.MaxAge)
		}
		if h.Idempotent {
			opts += ", runtime.Idempotent()"
		}
		if h.RateLimitRequests > 0 && h.RateLimitWindowSeconds > 0 {
			opts += fmt.Sprintf(", runtime.WithRateLimit(%d, %d)", h.RateLimitRequests, h.RateLimitWindowSeconds)
		}
		fmt.Fprintf(b, "\t// %s\n", analyzer.HandlerLine(h))
		fmt.Fprintf(b, "\tmux.Handle(%q, runtime.%s(%s, %s%s))\n\n",
			muxPattern(h.Route), wrapper, qual(alias, analyzer.ShortVar(h)), decode, opts)
	}
	writeMCPMount(b, g, svc)
	return decoders
}

func writeMCPMount(b *strings.Builder, g *analyzer.AppGraph, svc analyzer.Service) {
	var tools []analyzer.Handler
	for _, h := range g.Handlers {
		if h.Service == svc.Name && h.Kind == "api" && h.Tool != "" {
			tools = append(tools, h)
		}
	}
	if len(tools) == 0 {
		return
	}
	multi := len(g.Services) > 1
	fmt.Fprintf(b, "\t// Model Context Protocol (MCP) tools declared via Route{Tool: ...}\n")
	fmt.Fprintf(b, "\ttakoMux.Handle(%q, runtime.MCPHandler(mux,\n", routes.PatternMCP)
	for _, h := range tools {
		var pathParams, queryParams []string
		for _, bind := range h.Bindings {
			switch bind.Kind {
			case "path":
				pathParams = append(pathParams, fmt.Sprintf("%q", bind.Name))
			case "query":
				queryParams = append(queryParams, fmt.Sprintf("%q", bind.Name))
			}
		}
		fmt.Fprintf(b, "\t\truntime.MCPToolSpec{\n")
		fmt.Fprintf(b, "\t\t\tName:        %q,\n", schemagen.ToolName(h, multi))
		fmt.Fprintf(b, "\t\t\tDescription: %q,\n", h.Tool)
		fmt.Fprintf(b, "\t\t\tRoute:       %q,\n", h.Route)
		if h.ToolSchema != "" {
			fmt.Fprintf(b, "\t\t\tInputSchema: %q,\n", h.ToolSchema)
		}
		if len(pathParams) > 0 {
			fmt.Fprintf(b, "\t\t\tPathParams:  []string{%s},\n", strings.Join(pathParams, ", "))
		}
		if len(queryParams) > 0 {
			fmt.Fprintf(b, "\t\t\tQueryParams: []string{%s},\n", strings.Join(queryParams, ", "))
		}
		if h.HasBody {
			fmt.Fprintf(b, "\t\t\tHasBody:     true,\n")
		}
		fmt.Fprintf(b, "\t\t},\n")
	}
	fmt.Fprintf(b, "\t))\n\n")
}

// writeDecoder renders the static request-binding function for one
// endpoint.
func writeDecoder(b *strings.Builder, alias string, h analyzer.Handler) {
	reqType := qual(alias, h.ReqType)
	fmt.Fprintf(b, "\n// %s binds %s requests to %s.\n", decodeName(h), h.Route, reqType)
	fmt.Fprintf(b, "func %s(r *http.Request) (%s, error) {\n", decodeName(h), reqType)
	fmt.Fprintf(b, "\tvar req %s\n", reqType)
	if fallible(h) {
		b.WriteString("\tvar err error\n")
	}
	if h.HasBody {
		b.WriteString("\tif err := runtime.DecodeJSON(r, &req); err != nil {\n\t\treturn req, err\n\t}\n")
	}
	for _, bind := range h.Bindings {
		helper := helperName(bind)
		if infallibleBinding(bind) {
			fmt.Fprintf(b, "\treq.%s = runtime.%s(r, %q)\n", bind.Field, helper, bind.Name)
			continue
		}
		fmt.Fprintf(b, "\tif req.%s, err = runtime.%s(r, %q); err != nil {\n\t\treturn req, err\n\t}\n",
			bind.Field, helper, bind.Name)
	}
	b.WriteString("\treturn req, nil\n}\n")
}

// helperName maps a binding to its pkg/runtime helper.
func helperName(b analyzer.Binding) string {
	if b.Kind == "blob" {
		return "FormBlob"
	}
	kind := map[string]string{"path": "Path", "query": "Query", "form": "Form"}[b.Kind]
	typ := map[string]string{
		"string":   "String",
		"[]string": "Strings",
		"bool":     "Bool",
		"int":      "Int",
		"int64":    "Int64",
		"float64":  "Float64",
	}[b.Type]
	return kind + typ
}

// muxPattern maps a declared route to its ServeMux pattern. A declared
// "GET /" means THE home page, but to the mux "GET /" is the method
// catch-all — it would swallow every GET (assets, templates/) away
// from the site. "{$}" is the mux's exact-root marker.
// muxPattern is analyzer.MuxPattern: the route as it is registered.
// One function, because the conflict check and the mount have to be
// looking at the same string — checking one and mounting another is a
// check of nothing.
func muxPattern(route string) string { return analyzer.MuxPattern(route) }

// siteOptionsLiteral renders the SiteOptions struct fields for the
// generated mount — readable, like everything in this file.
func siteOptionsLiteral(site *analyzer.Resource) string {
	var parts []string
	if site.SPA {
		parts = append(parts, "SPA: true")
	}
	if site.DevProxy != "" {
		parts = append(parts, fmt.Sprintf("DevProxy: %q", site.DevProxy))
	}
	if len(site.Redirects) > 0 {
		var pairs []string
		for _, r := range site.Redirects {
			pairs = append(pairs, fmt.Sprintf("%q: %q", r.From, r.To))
		}
		parts = append(parts, "Redirects: map[string]string{"+strings.Join(pairs, ", ")+"}")
	}
	if site.TrustedHTML {
		parts = append(parts, "TrustedHTML: true")
	}
	if site.NotFound != "" {
		parts = append(parts, fmt.Sprintf("NotFound: %q", site.NotFound))
	}
	return strings.Join(parts, ", ")
}

func needsDecode(h analyzer.Handler) bool {
	return h.ReqType != "" && (h.HasBody || len(h.Bindings) > 0)
}

// fallible reports whether any non-body binding can fail (string and
// []string bindings cannot; blob and parsed bindings can).
func fallible(h analyzer.Handler) bool {
	for _, b := range h.Bindings {
		if !infallibleBinding(b) {
			return true
		}
	}
	return false
}

// infallibleBinding reports whether a binding assigns without an error
// check. Text arrives as text: there is nothing to parse and so nothing
// that can fail to parse.
func infallibleBinding(b analyzer.Binding) bool {
	return b.Kind != "blob" && (b.Type == "string" || b.Type == "[]string")
}

func decodeName(h analyzer.Handler) string {
	return "decode" + analyzer.ShortVar(h)
}
