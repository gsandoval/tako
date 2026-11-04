// Package schemagen derives Draft 2020-12 JSON Schemas, Model Context
// Protocol (MCP) tool definitions, and OpenAPI 3.1 specifications
// directly from analyzer.TypedGraph at analysis time — with zero
// runtime reflection.
package schemagen

import (
	"encoding/json"
	"fmt"
	"go/types"
	"reflect"
	"sort"
	"strings"

	"github.com/gsandoval/tako/pkg/analyzer"
)

// Schema is a Draft 2020-12 / OpenAPI 3.1 JSON Schema node.
type Schema struct {
	Type                 string             `json:"type,omitempty"`
	Format               string             `json:"format,omitempty"`
	Description          string             `json:"description,omitempty"`
	Ref                  string             `json:"$ref,omitempty"`
	Properties           map[string]*Schema `json:"properties,omitempty"`
	Required             []string           `json:"required,omitempty"`
	Items                *Schema            `json:"items,omitempty"`
	AdditionalProperties *Schema            `json:"additionalProperties,omitempty"`
	Nullable             bool               `json:"nullable,omitempty"`
}

// ToolSpec describes one MCP tool exposed by an application via
// tako.Route{Tool: "..."}.
type ToolSpec struct {
	Name         string   `json:"name"`
	HandlerVar   string   `json:"handler_var"`
	Service      string   `json:"service"`
	Route        string   `json:"route"`
	Method       string   `json:"method"`
	Path         string   `json:"path"`
	Description  string   `json:"description"`
	RequiresAuth bool     `json:"requires_auth,omitempty"`
	Policy       string   `json:"policy,omitempty"`
	PolicyRoles  []string `json:"policy_roles,omitempty"`
	Idempotent   bool     `json:"idempotent,omitempty"`
	InputSchema  *Schema  `json:"inputSchema"`
	OutputSchema *Schema  `json:"outputSchema,omitempty"`
}

// ToolName derives the stable MCP tool name from a handler var:
// single-service apps use snake_case of the ShortVar ("CreateOrder" -> "create_order");
// multi-service apps prefix the service when needed for uniqueness.
func ToolName(h analyzer.Handler, multiService bool) string {
	short := analyzer.ShortVar(h)
	snake := toSnake(short)
	if multiService && h.Service != "" {
		return strings.ReplaceAll(h.Service, "-", "_") + "_" + snake
	}
	return snake
}

func toSnake(s string) string {
	var b strings.Builder
	runes := []rune(s)
	for i, r := range runes {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				prev := runes[i-1]
				nextLower := i+1 < len(runes) && runes[i+1] >= 'a' && runes[i+1] <= 'z'
				if (prev >= 'a' && prev <= 'z') || (prev >= '0' && prev <= '9') || nextLower {
					b.WriteByte('_')
				}
			}
			b.WriteRune(r + ('a' - 'A'))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Tools returns every MCP tool declared via Route{Tool: "..."} in tg.
func Tools(tg *analyzer.TypedGraph) ([]ToolSpec, error) {
	if tg == nil || tg.Graph == nil {
		return nil, nil
	}
	multi := len(tg.Graph.Services) > 1
	var out []ToolSpec
	for _, h := range tg.Graph.Handlers {
		if h.Kind != "api" || h.Tool == "" {
			continue
		}
		inSchema, outSchema := HandlerSchemas(tg, h)
		method, path := splitRoute(h.Route)
		out = append(out, ToolSpec{
			Name:         ToolName(h, multi),
			HandlerVar:   h.Var,
			Service:      h.Service,
			Route:        h.Route,
			Method:       method,
			Path:         path,
			Description:  h.Tool,
			RequiresAuth: h.RequiresAuth,
			Policy:       h.Policy,
			PolicyRoles:  h.PolicyRoles,
			Idempotent:   h.Idempotent,
			InputSchema:  inSchema,
			OutputSchema: outSchema,
		})
	}
	return out, nil
}

// HandlerSchemas derives the combined input Schema (path, query, form,
// and JSON body fields) and output Schema for a handler.
func HandlerSchemas(tg *analyzer.TypedGraph, h analyzer.Handler) (input *Schema, output *Schema) {
	input = &Schema{
		Type:       "object",
		Properties: map[string]*Schema{},
	}
	if tg == nil {
		return input, nil
	}
	fn := tg.Funcs[h.Var]
	if fn == nil {
		if h.ToolSchema != "" {
			var parsed Schema
			if json.Unmarshal([]byte(h.ToolSchema), &parsed) == nil {
				return &parsed, nil
			}
		}
		return input, nil
	}
	sig, ok := fn.Type().(*types.Signature)
	if !ok {
		return input, nil
	}
	if sig.Params().Len() >= 2 {
		reqType := sig.Params().At(1).Type()
		input = requestSchema(reqType, h)
	}
	if !h.ResBlob && sig.Results().Len() >= 1 {
		resType := sig.Results().At(0).Type()
		output = goTypeSchema(resType, 0, nil)
	}
	return input, output
}

func requestSchema(reqType types.Type, h analyzer.Handler) *Schema {
	out := &Schema{
		Type:       "object",
		Properties: map[string]*Schema{},
	}
	st, ok := reqType.Underlying().(*types.Struct)
	if !ok {
		return out
	}
	for i := range st.NumFields() {
		fv := st.Field(i)
		if !fv.Exported() {
			continue
		}
		tag := reflect.StructTag(st.Tag(i))
		switch {
		case tag.Get("path") != "":
			name := tag.Get("path")
			s := goTypeSchema(fv.Type(), 0, nil)
			s.Description = fmt.Sprintf("Path parameter {%s}", name)
			out.Properties[name] = s
			out.Required = append(out.Required, name)
		case tag.Get("query") != "":
			name := tag.Get("query")
			s := goTypeSchema(fv.Type(), 0, nil)
			s.Description = fmt.Sprintf("Query parameter %q", name)
			out.Properties[name] = s
		case tag.Get("form") != "":
			name := tag.Get("form")
			s := goTypeSchema(fv.Type(), 0, nil)
			s.Description = fmt.Sprintf("Form parameter %q", name)
			out.Properties[name] = s
		default:
			jsonTag := tag.Get("json")
			if jsonTag == "-" {
				continue
			}
			name := fv.Name()
			omitEmpty := strings.Contains(jsonTag, "omitempty")
			if jsonName, _, _ := strings.Cut(jsonTag, ","); jsonName != "" {
				name = jsonName
			}
			s := goTypeSchema(fv.Type(), 0, nil)
			out.Properties[name] = s
			if !omitEmpty {
				if _, isPtr := fv.Type().(*types.Pointer); !isPtr {
					out.Required = append(out.Required, name)
				}
			}
		}
	}
	if len(out.Properties) == 0 {
		out.Properties = nil
	}
	return out
}

func goTypeSchema(t types.Type, depth int, components map[string]*Schema) *Schema {
	if depth > 8 || t == nil {
		return &Schema{Type: "object"}
	}
	switch tt := t.(type) {
	case *types.Basic:
		switch {
		case tt.Info()&types.IsString != 0:
			return &Schema{Type: "string"}
		case tt.Info()&types.IsBoolean != 0:
			return &Schema{Type: "boolean"}
		case tt.Info()&types.IsInteger != 0:
			return &Schema{Type: "integer"}
		case tt.Info()&types.IsNumeric != 0:
			return &Schema{Type: "number"}
		}
	case *types.Pointer:
		s := goTypeSchema(tt.Elem(), depth+1, components)
		cp := *s
		cp.Nullable = true
		return &cp
	case *types.Slice:
		if b, ok := tt.Elem().(*types.Basic); ok && b.Kind() == types.Byte {
			return &Schema{Type: "string", Format: "byte"}
		}
		return &Schema{
			Type:  "array",
			Items: goTypeSchema(tt.Elem(), depth+1, components),
		}
	case *types.Map:
		return &Schema{
			Type:                 "object",
			AdditionalProperties: goTypeSchema(tt.Elem(), depth+1, components),
		}
	case *types.Named:
		obj := tt.Obj()
		if obj != nil && obj.Pkg() != nil {
			switch obj.Pkg().Path() + "." + obj.Name() {
			case "time.Time":
				return &Schema{Type: "string", Format: "date-time"}
			case "encoding/json.RawMessage":
				return &Schema{Type: "object"}
			}
		}
		if st, isStruct := tt.Underlying().(*types.Struct); isStruct {
			if st.NumFields() == 0 {
				return &Schema{Type: "object"}
			}
			if components != nil && obj != nil {
				typeName := obj.Name()
				if _, exists := components[typeName]; !exists {
					// Insert placeholder first to break recursive cycles.
					components[typeName] = &Schema{Type: "object"}
					components[typeName] = structBodySchema(st, depth+1, components)
				}
				return &Schema{Ref: "#/components/schemas/" + typeName}
			}
			return structBodySchema(st, depth+1, components)
		}
		return goTypeSchema(tt.Underlying(), depth+1, components)
	case *types.Struct:
		return structBodySchema(tt, depth+1, components)
	}
	return &Schema{Type: "string"}
}

func structBodySchema(st *types.Struct, depth int, components map[string]*Schema) *Schema {
	props := map[string]*Schema{}
	var req []string
	for i := range st.NumFields() {
		fv := st.Field(i)
		if !fv.Exported() {
			continue
		}
		tag := reflect.StructTag(st.Tag(i))
		if tag.Get("path") != "" || tag.Get("query") != "" || tag.Get("form") != "" {
			continue
		}
		jsonTag := tag.Get("json")
		if jsonTag == "-" {
			continue
		}
		name := fv.Name()
		omitEmpty := strings.Contains(jsonTag, "omitempty")
		if jsonName, _, _ := strings.Cut(jsonTag, ","); jsonName != "" {
			name = jsonName
		}
		props[name] = goTypeSchema(fv.Type(), depth, components)
		if !omitEmpty {
			if _, isPtr := fv.Type().(*types.Pointer); !isPtr {
				req = append(req, name)
			}
		}
	}
	if len(props) == 0 {
		props = nil
	}
	return &Schema{
		Type:       "object",
		Properties: props,
		Required:   req,
	}
}

// OpenAPISummary reports counts from one OpenAPI generation pass.
type OpenAPISummary struct {
	Operations int
	Schemas    int
	Tools      int
}

// OpenAPI renders an OpenAPI 3.1.0 JSON document from the analyzed
// application's API handlers.
func OpenAPI(tg *analyzer.TypedGraph) ([]byte, OpenAPISummary, error) {
	if tg == nil || tg.Graph == nil {
		return nil, OpenAPISummary{}, fmt.Errorf("nil typed graph")
	}

	type oaParam struct {
		Name        string  `json:"name"`
		In          string  `json:"in"`
		Required    bool    `json:"required,omitempty"`
		Description string  `json:"description,omitempty"`
		Schema      *Schema `json:"schema"`
	}
	type oaMedia struct {
		Schema *Schema `json:"schema"`
	}
	type oaBody struct {
		Required bool               `json:"required"`
		Content  map[string]oaMedia `json:"content"`
	}
	type oaResponse struct {
		Description string             `json:"description"`
		Content     map[string]oaMedia `json:"content,omitempty"`
	}
	type oaRateLimit struct {
		Requests      int `json:"requests"`
		WindowSeconds int `json:"window_seconds"`
	}
	type oaOperation struct {
		OperationID string                `json:"operationId"`
		Summary     string                `json:"summary"`
		Description string                `json:"description,omitempty"`
		Tags        []string              `json:"tags,omitempty"`
		Parameters  []oaParam             `json:"parameters,omitempty"`
		RequestBody *oaBody               `json:"requestBody,omitempty"`
		Responses   map[string]oaResponse `json:"responses"`
		Security    []map[string][]string `json:"security,omitempty"`
		XTakoVar    string                `json:"x-tako-var"`
		XTakoTool   string                `json:"x-tako-tool,omitempty"`
		XTakoPolicy string                `json:"x-tako-policy,omitempty"`
		XTakoRoles  []string              `json:"x-tako-roles,omitempty"`
		XTakoLimit  *oaRateLimit          `json:"x-tako-rate-limit,omitempty"`
	}

	paths := map[string]map[string]oaOperation{}
	components := map[string]*Schema{}
	var sum OpenAPISummary
	hasAuthRoutes := false
	multi := len(tg.Graph.Services) > 1

	for _, h := range tg.Graph.Handlers {
		if h.Kind != "api" {
			continue
		}
		sum.Operations++
		method, rawPath := splitRoute(h.Route)
		oaPath := normalizeOpenAPIPath(rawPath)
		lowerMethod := strings.ToLower(method)

		op := oaOperation{
			OperationID: ToolName(h, multi),
			Summary:     fmt.Sprintf("%s (%s)", h.Var, h.Route),
			Tags:        []string{h.Service},
			Responses:   map[string]oaResponse{},
			XTakoVar:    h.Var,
		}
		if h.Tool != "" {
			op.Description = h.Tool
			op.XTakoTool = ToolName(h, multi)
			sum.Tools++
		}
		if h.RequiresAuth {
			hasAuthRoutes = true
			op.Security = []map[string][]string{{"sessionCookie": {}}, {"bearerAuth": {}}}
		}
		if h.Policy != "" {
			op.XTakoPolicy = h.Policy
			op.XTakoRoles = h.PolicyRoles
		}
		if h.RateLimitRequests > 0 && h.RateLimitWindowSeconds > 0 {
			op.XTakoLimit = &oaRateLimit{
				Requests:      h.RateLimitRequests,
				WindowSeconds: h.RateLimitWindowSeconds,
			}
		}

		for _, b := range h.Bindings {
			if b.Kind != "path" && b.Kind != "query" {
				continue
			}
			op.Parameters = append(op.Parameters, oaParam{
				Name:     b.Name,
				In:       b.Kind,
				Required: b.Kind == "path",
				Schema:   bindingSchema(b.Type),
			})
		}
		if h.Idempotent {
			op.Parameters = append(op.Parameters, oaParam{
				Name:        "Idempotency-Key",
				In:          "header",
				Required:    false,
				Description: "Client-supplied key to deduplicate concurrent or retried mutations across instances.",
				Schema:      &Schema{Type: "string"},
			})
		}

		if fn := tg.Funcs[h.Var]; fn != nil {
			if sig, ok := fn.Type().(*types.Signature); ok {
				if h.HasBody && sig.Params().Len() >= 2 {
					reqType := sig.Params().At(1).Type()
					bodySchema := goTypeSchema(reqType, 0, components)
					op.RequestBody = &oaBody{
						Required: true,
						Content: map[string]oaMedia{
							"application/json": {Schema: bodySchema},
						},
					}
				}
				if h.ResBlob {
					op.Responses["200"] = oaResponse{
						Description: "Binary response stream",
						Content: map[string]oaMedia{
							"application/octet-stream": {Schema: &Schema{Type: "string", Format: "binary"}},
						},
					}
				} else if sig.Results().Len() >= 1 {
					resType := sig.Results().At(0).Type()
					resSchema := goTypeSchema(resType, 0, components)
					op.Responses["200"] = oaResponse{
						Description: "Successful JSON response",
						Content: map[string]oaMedia{
							"application/json": {Schema: resSchema},
						},
					}
				}
			}
		}
		if len(op.Responses) == 0 {
			op.Responses["200"] = oaResponse{Description: "OK"}
		}

		if paths[oaPath] == nil {
			paths[oaPath] = map[string]oaOperation{}
		}
		paths[oaPath][lowerMethod] = op
	}

	sum.Schemas = len(components)

	type oaDoc struct {
		OpenAPI    string                            `json:"openapi"`
		Info       map[string]string                 `json:"info"`
		Paths      map[string]map[string]oaOperation `json:"paths"`
		Components map[string]any                    `json:"components,omitempty"`
	}
	doc := oaDoc{
		OpenAPI: "3.1.0",
		Info: map[string]string{
			"title":   tg.Graph.Module,
			"version": "1.0.0",
		},
		Paths: paths,
	}
	if len(components) > 0 || hasAuthRoutes {
		doc.Components = map[string]any{}
		if len(components) > 0 {
			// Ensure deterministic map keys via json.MarshalIndent.
			keys := make([]string, 0, len(components))
			for k := range components {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			ordered := make(map[string]*Schema, len(keys))
			for _, k := range keys {
				ordered[k] = components[k]
			}
			doc.Components["schemas"] = ordered
		}
		if hasAuthRoutes {
			doc.Components["securitySchemes"] = map[string]any{
				"sessionCookie": map[string]any{
					"type": "apiKey",
					"in":   "cookie",
					"name": "tako_session",
				},
				"bearerAuth": map[string]any{
					"type":   "http",
					"scheme": "bearer",
				},
			}
		}
	}

	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, sum, err
	}
	return append(raw, '\n'), sum, nil
}

func splitRoute(route string) (method, path string) {
	method = "GET"
	path = route
	if i := strings.IndexByte(route, ' '); i >= 0 {
		method = strings.TrimSpace(route[:i])
		path = strings.TrimSpace(route[i+1:])
	}
	return method, path
}

func normalizeOpenAPIPath(p string) string {
	p = strings.ReplaceAll(p, "{$}", "")
	for {
		i := strings.Index(p, "...}")
		if i < 0 {
			break
		}
		p = p[:i] + "}" + p[i+4:]
	}
	if p == "" {
		return "/"
	}
	return p
}

func bindingSchema(typ string) *Schema {
	switch typ {
	case "bool":
		return &Schema{Type: "boolean"}
	case "int", "int64":
		return &Schema{Type: "integer"}
	case "float64":
		return &Schema{Type: "number"}
	case "[]string":
		return &Schema{Type: "array", Items: &Schema{Type: "string"}}
	default:
		return &Schema{Type: "string"}
	}
}
