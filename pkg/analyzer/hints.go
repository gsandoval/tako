package analyzer

import (
	"encoding/json"
	"fmt"
	"go/types"
	"reflect"
	"strings"
)

// RuleGuide holds the actionable fix hint and documentation command for
// an analysis rule ID.
type RuleGuide struct {
	Rule    string `json:"rule"`
	Summary string `json:"summary"`
	Hint    string `json:"hint"`
	Example string `json:"example,omitempty"`
	Doc     string `json:"doc"`
}

var ruleGuides = map[string]RuleGuide{
	"package-level": {
		Rule:    "package-level",
		Summary: "Handlers and resources must be declared as package-level vars; handlers must be exported.",
		Hint:    "Move the constructor call to a top-level `var` declaration in the package, and capitalize handler variable names (e.g. `var Greet = tako.API(...)`).",
		Example: "var Greet = tako.API(\"GET /greet/{name}\", greet)",
		Doc:     "tako docs api",
	},
	"const-route": {
		Rule:    "const-route",
		Summary: "Route patterns must be compile-time string constants.",
		Hint:    "Pass a literal string constant like `\"GET /items/{id}\"` directly to `tako.API` or `tako.Page` instead of a variable or function call.",
		Example: "var GetItem = tako.API(\"GET /items/{id}\", getItem)",
		Doc:     "tako docs api",
	},
	"const-name": {
		Rule:    "const-name",
		Summary: "Resource names must be compile-time string constants.",
		Hint:    "Pass a string literal (e.g. `\"users\"`) as the first argument to the resource constructor.",
		Example: "var users = tako.Entity[User](\"users\")",
		Doc:     "tako docs entity",
	},
	"const-query-field": {
		Rule:    "const-query-field",
		Summary: "Entity query field names in Where/OrderBy must be string constants.",
		Hint:    "Pass the Go struct field name as a string literal (e.g. `.Where(\"Owner\", \"==\", uid)`) so composite indexes can be derived at analysis time.",
		Example: "items.Where(\"Owner\", \"==\", uid).OrderBy(\"-Created\").All(ctx)",
		Doc:     "tako docs entity",
	},
	"const-role": {
		Rule:    "const-role",
		Summary: "Role names must be compile-time string constants.",
		Hint:    "Declare `var Editor = tako.Role(\"editor\")` using a string literal.",
		Example: "var Editor = tako.Role(\"editor\")",
		Doc:     "tako docs auth",
	},
	"unexported-req": {
		Rule:    "unexported-req",
		Summary: "Handler request structs with fields must be named, exported struct types.",
		Hint:    "Export the request struct type (start its name with a capital letter) or use `struct{}` when no input is needed.",
		Example: "type CreateReq struct {\n\tTitle string `json:\"title\"`\n}",
		Doc:     "tako docs api",
	},
	"resource-name": {
		Rule:    "resource-name",
		Summary: "Physical resource names (Bucket, Disk, Queue, Topic) must use lowercase letters, digits, and hyphens.",
		Hint:    "Rename the resource to match `[a-z0-9]([-a-z0-9]*[a-z0-9])?` (replace underscores or spaces with hyphens, lowercase all letters).",
		Example: "var uploads = tako.Bucket(\"photo-uploads\")",
		Doc:     "tako docs bucket",
	},
	"route-conflict": {
		Rule:    "route-conflict",
		Summary: "Two routes in the same service have overlapping patterns where neither is more specific.",
		Hint:    "Make one route pattern more specific with a literal path segment or distinct HTTP method, or move one route to another package.",
		Doc:     "tako docs api",
	},
	"reserved-prefix": {
		Rule:    "reserved-prefix",
		Summary: "Routes under `/_tako/` are reserved for framework endpoints.",
		Hint:    "Choose an application route path outside `/_tako/`.",
		Doc:     "tako docs api",
	},
	"route-params": {
		Rule:    "route-params",
		Summary: "Every `{param}` in the route pattern must match a `path:\"param\"` field on the request struct, and vice versa.",
		Hint:    "Add a matching `Param string `path:\"param\"`` field to the request struct for every `{param}` segment in the route string.",
		Example: "type ItemReq struct {\n\tID string `path:\"id\"`\n}",
		Doc:     "tako docs api",
	},
	"bind-type": {
		Rule:    "bind-type",
		Summary: "Fields tagged with `path:`, `query:`, or `form:` must use supported scalar types.",
		Hint:    "Use `string`, `bool`, `int`, `int64`, or `float64` for `path:` fields (and `[]string` only on `query:` or `form:` fields).",
		Doc:     "tako docs api",
	},
	"body-on-get": {
		Rule:    "body-on-get",
		Summary: "GET/HEAD routes cannot decode a JSON request body.",
		Hint:    "Tag every field on a GET/HEAD request struct with `path:\"...\"` or `query:\"...\"`, or change the route method to `POST`/`PUT`/`PATCH`.",
		Example: "type SearchReq struct {\n\tQuery string `query:\"q\"`\n}",
		Doc:     "tako docs api",
	},
	"blob-binding": {
		Rule:    "blob-binding",
		Summary: "`tako.Blob` fields require a non-GET route, a `form:\"...\"` tag, and no untagged sibling fields.",
		Hint:    "Use `POST`/`PUT`, tag the `tako.Blob` field with `form:\"file\"`, and tag any other fields on the struct with `path:`, `query:`, or `form:`.",
		Doc:     "tako docs bucket",
	},
	"page-binding": {
		Rule:    "page-binding",
		Summary: "`tako.Page` handlers receive path, query, or form parameters, never JSON bodies.",
		Hint:    "Add `path:\"name\"`, `query:\"name\"`, or `form:\"name\"` struct tags to every field of a `tako.Page` request struct, or use `tako.API` for JSON.",
		Doc:     "tako docs pages",
	},
	"tool-api": {
		Rule:    "tool-api",
		Summary: "`Route.Tool` is only supported on JSON `tako.API` endpoints.",
		Hint:    "Use `Route{Tool: \"...\"}` only on `tako.API` handlers that accept and return JSON (not `tako.Page` or `tako.Blob` endpoints).",
		Example: "var CreateOrder = tako.API(\"POST /orders\", createOrder, tako.Route{Tool: \"Create a customer order\"})",
		Doc:     "tako docs api",
	},
	"worker-binding": {
		Rule:    "worker-binding",
		Summary: "`tako.Worker` must bind a package-level `tako.Queue` variable.",
		Hint:    "Declare `var q = tako.Queue[Msg](\"name\")` at package level and pass `q` to `tako.Worker(q, fn)`.",
		Doc:     "tako docs queue",
	},
	"subscribe-binding": {
		Rule:    "subscribe-binding",
		Summary: "`tako.Subscribe` must bind a package-level `tako.Topic` or bucket `.Uploaded()` event.",
		Hint:    "Pass a declared `tako.Topic[E]` variable or `bucket.Uploaded()` to `tako.Subscribe`.",
		Doc:     "tako docs topic",
	},
	"job-binding": {
		Rule:    "job-binding",
		Summary: "Every `tako.Job` requires a corresponding `tako.Runner` handler.",
		Hint:    "Bind a runner to the job with `var RunName = tako.Runner(jobVar, runFn)`.",
		Doc:     "tako docs job",
	},
	"one-worker": {
		Rule:    "one-worker",
		Summary: "A `tako.Queue` may have only one `tako.Worker`.",
		Hint:    "Use `tako.Topic` with multiple `tako.Subscribe` handlers when you need fan-out to multiple consumers.",
		Doc:     "tako docs queue",
	},
	"one-job-handler": {
		Rule:    "one-job-handler",
		Summary: "A `tako.Job` may have only one `tako.Runner`.",
		Hint:    "Declare a second `tako.Job` if you need a second background job body.",
		Doc:     "tako docs job",
	},
	"raw-call": {
		Rule:    "raw-call",
		Summary: "Cross-service calls must go through the exported endpoint variable (`Endpoint.Call`), not the underlying Go function.",
		Hint:    "Replace `otherpkg.handlerFn(ctx, req)` with `otherpkg.HandlerVar.Call(ctx, req)`.",
		Example: "res, err := billing.Charge.Call(ctx, billing.ChargeReq{Amount: 500})",
		Doc:     "tako docs calls",
	},
	"one-service": {
		Rule:    "one-service",
		Summary: "A Go package maps to at most one `tako.Service` declaration.",
		Hint:    "Merge the `tako.Service` declarations in this package into one, or split the second service into a separate Go subpackage.",
		Doc:     "tako docs service",
	},
	"one-site": {
		Rule:    "one-site",
		Summary: "A package may declare at most one `tako.Site`.",
		Hint:    "Keep a single `tako.Site` declaration per package.",
		Doc:     "tako docs site",
	},
	"mixed-ingress": {
		Rule:    "mixed-ingress",
		Summary: "Public routes and `Route{Private: true}` routes cannot share the same service package.",
		Hint:    "Move `Route{Private: true}` endpoints into their own dedicated Go package so Cloud Run can enforce IAM ingress on that service.",
		Doc:     "tako docs calls",
	},
	"config-literal": {
		Rule:    "config-literal",
		Summary: "Configuration structs and their fields must be compile-time literals/constants.",
		Hint:    "Write the config struct inline as a composite literal (`tako.Route{SignIn: true}`) with constant field values.",
		Doc:     "tako docs errors",
	},
	"one-config": {
		Rule:    "one-config",
		Summary: "Constructors accept at most one configuration struct argument.",
		Hint:    "Combine all settings into a single config literal argument.",
		Doc:     "tako docs errors",
	},
	"auth-provider": {
		Rule:    "auth-provider",
		Summary: "`tako.Auth` requires at least one sign-in provider.",
		Hint:    "Enable `Google: true` and/or `Password: true` in `tako.AuthConfig{...}`.",
		Example: "var auth = tako.Auth(tako.AuthConfig{Password: true})",
		Doc:     "tako docs auth",
	},
	"cache-signin": {
		Rule:    "cache-signin",
		Summary: "`Route.MaxAge` cannot be combined with `SignIn` or `Allow`.",
		Hint:    "Remove `MaxAge` from authenticated routes so shared caches never serve one user's response to another.",
		Doc:     "tako docs pages",
	},
	"policy-private": {
		Rule:    "policy-private",
		Summary: "`Route.Allow` and `Route.Private` cannot both be set on the same route.",
		Hint:    "Drop either `Allow` (end-user session policy) or `Private` (service-to-service IAM ingress).",
		Doc:     "tako docs auth",
	},
	"entity-id": {
		Rule:    "entity-id",
		Summary: "Every `tako.Entity[T]` struct requires exactly one `string` field tagged `entity:\"id\"`.",
		Hint:    "Add `ID string `json:\"id\" entity:\"id\"`` to the entity struct and ensure no other field has `entity:\"id\"`.",
		Example: "type User struct {\n\tID   string `json:\"id\" entity:\"id\"`\n\tName string `json:\"name\"`\n}",
		Doc:     "tako docs entity",
	},
	"entity-ttl": {
		Rule:    "entity-ttl",
		Summary: "An entity may have at most one `entity:\"ttl\"` field, which must be `time.Time` and not `json:\"-\"`.",
		Hint:    "Change the `entity:\"ttl\"` field's type to `time.Time` and ensure it is serialized to JSON.",
		Example: "ExpiresAt time.Time `json:\"expires_at\" entity:\"ttl\"`",
		Doc:     "tako docs entity",
	},
	"entity-index": {
		Rule:    "entity-index",
		Summary: "A field tagged `entity:\"index\"` cannot be excluded from JSON storage via `json:\"-\"`.",
		Hint:    "Remove `json:\"-\"` from the indexed field or remove `entity:\"index\"`.",
		Doc:     "tako docs entity",
	},
	"unindexed-query": {
		Rule:    "unindexed-query",
		Summary: "Fields passed to `.Where(...)` or `.OrderBy(...)` must be tagged `entity:\"index\"` on the entity struct.",
		Hint:    "Add `entity:\"index\"` to the struct field's tag, and make sure the query uses the Go struct field name (not the JSON tag name).",
		Example: "Owner string `json:\"owner\" entity:\"index\"`",
		Doc:     "tako docs entity",
	},
	"query-shapes": {
		Rule:    "query-shapes",
		Summary: "A single function combines too many conditional `.Where` branches (>4), exploding composite indexes.",
		Hint:    "Split the conditional query branches into dedicated helper functions with unconditional filter shapes.",
		Doc:     "tako docs entity",
	},
	"query-escapes": {
		Rule:    "query-escapes",
		Summary: "An `*EntityQuery[T]` cannot be stored in a struct field, channel, or closure where static analysis cannot trace its filters.",
		Hint:    "Build and terminal-call (`.All`, `.First`, `.Page`, `.Count`) the query within one function or via helper functions that take and return `*tako.EntityQuery[T]`.",
		Doc:     "tako docs entity",
	},
}

// PopulateErrorHints enriches every Error in errs with its Hint and Doc
// metadata when not already set.
func PopulateErrorHints(errs ErrorList) {
	for _, e := range errs {
		if e == nil {
			continue
		}
		if g, ok := RuleInfo(e.Rule); ok {
			if e.Hint == "" {
				e.Hint = g.Hint
			}
			if e.Doc == "" {
				e.Doc = g.Doc
			}
		} else if e.Doc == "" && e.Rule != "" {
			e.Doc = "tako docs errors"
		}
	}
}

// RuleInfo looks up the structured guide for a rule ID (with or without
// the "tako/" prefix).
func RuleInfo(rule string) (RuleGuide, bool) {
	clean := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(rule)), "tako/")
	g, ok := ruleGuides[clean]
	return g, ok
}

// ExplainRule formats a human- and agent-readable explanation for a rule.
func ExplainRule(rule string) (string, bool) {
	g, ok := RuleInfo(rule)
	if !ok {
		return "", false
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Rule: tako/%s\n", g.Rule)
	fmt.Fprintf(&b, "Summary: %s\n", g.Summary)
	fmt.Fprintf(&b, "How to fix: %s\n", g.Hint)
	if g.Example != "" {
		fmt.Fprintf(&b, "\nExample:\n  %s\n", strings.ReplaceAll(g.Example, "\n", "\n  "))
	}
	fmt.Fprintf(&b, "\nRead more: %s\n", g.Doc)
	return b.String(), true
}

// buildToolSchema constructs a compact Draft 2020-12 JSON Schema object
// string for an API handler's request struct, combining path, query, and
// JSON body fields.
func buildToolSchema(req reqInfo) string {
	type propSchema struct {
		Type        string               `json:"type,omitempty"`
		Description string               `json:"description,omitempty"`
		Items       *propSchema          `json:"items,omitempty"`
		Properties  map[string]propSchema `json:"properties,omitempty"`
		Required    []string             `json:"required,omitempty"`
	}
	props := map[string]propSchema{}
	var required []string

	var typeToSchema func(t types.Type, depth int) propSchema
	typeToSchema = func(t types.Type, depth int) propSchema {
		if depth > 6 || t == nil {
			return propSchema{Type: "object"}
		}
		switch tt := t.(type) {
		case *types.Basic:
			switch {
			case tt.Info()&types.IsString != 0:
				return propSchema{Type: "string"}
			case tt.Info()&types.IsBoolean != 0:
				return propSchema{Type: "boolean"}
			case tt.Info()&types.IsInteger != 0:
				return propSchema{Type: "integer"}
			case tt.Info()&types.IsNumeric != 0:
				return propSchema{Type: "number"}
			}
		case *types.Pointer:
			return typeToSchema(tt.Elem(), depth+1)
		case *types.Slice:
			if b, ok := tt.Elem().(*types.Basic); ok && b.Kind() == types.Byte {
				return propSchema{Type: "string"}
			}
			elem := typeToSchema(tt.Elem(), depth+1)
			return propSchema{Type: "array", Items: &elem}
		case *types.Map:
			return propSchema{Type: "object"}
		case *types.Named:
			obj := tt.Obj()
			if obj != nil && obj.Pkg() != nil && obj.Pkg().Path() == "time" && obj.Name() == "Time" {
				return propSchema{Type: "string", Description: "RFC 3339 timestamp"}
			}
			return typeToSchema(tt.Underlying(), depth+1)
		case *types.Struct:
			stProps := map[string]propSchema{}
			for i := range tt.NumFields() {
				fv := tt.Field(i)
				if !fv.Exported() {
					continue
				}
				tag := reflect.StructTag(tt.Tag(i))
				jsonTag := tag.Get("json")
				if jsonTag == "-" {
					continue
				}
				name := fv.Name()
				if jsonName, _, _ := strings.Cut(jsonTag, ","); jsonName != "" {
					name = jsonName
				}
				stProps[name] = typeToSchema(fv.Type(), depth+1)
			}
			return propSchema{Type: "object", Properties: stProps}
		}
		return propSchema{Type: "string"}
	}

	for _, f := range req.fields {
		switch {
		case f.tag("path") != "":
			name := f.tag("path")
			ps := typeToSchema(f.typ, 0)
			ps.Description = fmt.Sprintf("Path parameter {%s}", name)
			props[name] = ps
			required = append(required, name)
		case f.tag("query") != "":
			name := f.tag("query")
			ps := typeToSchema(f.typ, 0)
			ps.Description = fmt.Sprintf("Query parameter %q", name)
			props[name] = ps
		case f.tag("form") != "":
			name := f.tag("form")
			ps := typeToSchema(f.typ, 0)
			props[name] = ps
		default:
			jsonTag := f.tag("json")
			if jsonTag == "-" {
				continue
			}
			name := f.name
			omitEmpty := strings.Contains(jsonTag, "omitempty")
			if jsonName, _, _ := strings.Cut(jsonTag, ","); jsonName != "" {
				name = jsonName
			}
			ps := typeToSchema(f.typ, 0)
			props[name] = ps
			if !omitEmpty {
				if _, isPtr := f.typ.(*types.Pointer); !isPtr {
					required = append(required, name)
				}
			}
		}
	}

	root := propSchema{
		Type:       "object",
		Properties: props,
		Required:   required,
	}
	raw, err := json.Marshal(root)
	if err != nil {
		return `{"type":"object"}`
	}
	return string(raw)
}
