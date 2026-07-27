// Package clientgen renders the typed TypeScript client `tako gen
// client ts` emits: one zero-dependency ES module with a method per
// browser-callable api endpoint, request/response interfaces mirrored
// from the Go signatures, and session auth pre-wired (same-origin
// cookie + CSRF header). It walks handler Req/Res type TREES, so it
// consumes analyzer.TypedGraph, not the JSON-shaped AppGraph.
package clientgen

import (
	"fmt"
	"go/token"
	"go/types"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gsandoval/tako/pkg/analyzer"
)

// Summary is what the CLI narrates about one generation run.
type Summary struct {
	// Endpoints is the count of generated client methods.
	Endpoints int
	// Types is the count of emitted interfaces.
	Types int
	// Excluded lists api endpoints the client cannot call, with the
	// reason — exclusion is visible, never silent.
	Excluded []Exclusion
}

// Exclusion is one api endpoint left out of the client.
type Exclusion struct {
	Var    string
	Route  string
	Reason string
}

// Generate renders the TypeScript client for the analyzed app. It
// returns an analyzer.ErrorList when a type cannot be represented, so
// failures carry file:line like every other analysis diagnostic.
func Generate(tg *analyzer.TypedGraph) ([]byte, Summary, error) {
	g := &generator{tg: tg, methodOwner: map[string]string{}, ifaceByName: map[string]*types.TypeName{}}
	var methods []method
	var sum Summary
	for _, h := range tg.Graph.Handlers {
		if h.Kind != "api" {
			continue
		}
		if reason := excluded(h); reason != "" {
			sum.Excluded = append(sum.Excluded, Exclusion{Var: h.Var, Route: h.Route, Reason: reason})
			continue
		}
		m, ok := g.method(h)
		if !ok {
			continue
		}
		methods = append(methods, m)
	}
	if len(g.errs) > 0 {
		sort.SliceStable(g.errs, func(i, j int) bool { return g.errs[i].Pos < g.errs[j].Pos })
		return nil, sum, g.errs
	}
	sum.Endpoints = len(methods)
	sum.Types = len(g.ifaces)
	return g.render(methods, sum), sum, nil
}

// excluded names why an api endpoint cannot appear in a browser JSON
// client, or returns "" when it can.
func excluded(h analyzer.Handler) string {
	if h.Private {
		return "private (IAM-only — browsers cannot mint OIDC)"
	}
	hasBlob, hasForm := false, false
	for _, b := range h.Bindings {
		hasBlob = hasBlob || b.Kind == "blob"
		hasForm = hasForm || b.Kind == "form"
	}
	if hasBlob {
		return "binds a multipart upload (the signed-URL flow, not the JSON client)"
	}
	if hasForm {
		return "form-bound (browsers post forms to it directly)"
	}
	return ""
}

// method is one generated client method.
type method struct {
	name     string
	doc      string
	args     []string // rendered "name: type" parameters, in order
	retType  string   // TS return type inside Promise<...>
	httpVerb string
	pathExpr string // TS template literal (backticks included)
	queryLit string // TS object literal for query params, "" for none
	bodyArg  string // name of the body parameter, "" for none
	resBlob  bool
}

type iface struct {
	name   string
	fields []string // rendered "  name: type;" lines
	doc    string
}

type generator struct {
	tg          *analyzer.TypedGraph
	errs        analyzer.ErrorList
	methodOwner map[string]string // method name → declaring handler var
	ifaces      []iface
	ifaceByName map[string]*types.TypeName
	ifaceDone   map[*types.TypeName]string // emitted shape: "full" or "body"
}

func (g *generator) fail(pos token.Pos, format string, args ...any) {
	g.errs = append(g.errs, &analyzer.Error{
		Pos:  g.relPos(pos),
		Msg:  fmt.Sprintf(format, args...),
		Rule: "client-type",
	})
}

func (g *generator) relPos(pos token.Pos) string {
	p := g.tg.Fset.Position(pos)
	file := p.Filename
	if rel, err := filepath.Rel(g.tg.Dir, file); err == nil && !strings.HasPrefix(rel, "..") {
		file = rel
	}
	return fmt.Sprintf("%s:%d:%d", file, p.Line, p.Column)
}

// method builds one client method from a handler; ok is false when a
// diagnostic was recorded instead.
func (g *generator) method(h analyzer.Handler) (method, bool) {
	fn := g.tg.Funcs[h.Var]
	if fn == nil {
		return method{}, false
	}
	sig := fn.Type().(*types.Signature)
	name := lowerCamel(h.Var[strings.LastIndex(h.Var, ".")+1:])
	if prior, taken := g.methodOwner[name]; taken {
		g.errs = append(g.errs, &analyzer.Error{
			Pos:  g.posOfHandler(h),
			Msg:  fmt.Sprintf("client method %s already generated from %s — endpoint vars must map to distinct method names", name, prior),
			Rule: "client-name",
		})
		return method{}, false
	}
	g.methodOwner[name] = h.Var

	m := method{name: name, httpVerb: routeMethod(h.Route)}
	m.doc = fmt.Sprintf("%s — %s", h.Var, h.Route)
	if h.RequiresAuth {
		m.doc += " (sign-in required: 401 ApiError without a session)"
	}

	// Path params become positional args, in ROUTE order.
	bindingsByParam := map[string]analyzer.Binding{}
	for _, b := range h.Bindings {
		if b.Kind == "path" {
			bindingsByParam[b.Name] = b
		}
	}
	m.pathExpr = "`" + routePath(h.Route)
	for _, param := range routeParams(h.Route) {
		b := bindingsByParam[param]
		arg := argName(param)
		m.args = append(m.args, arg+": "+scalarTS(b.Type))
		expr := "${encodeURIComponent(String(" + arg + "))}"
		if strings.HasSuffix(param, "...") {
			// A {p...} wildcard spans segments; its slashes must survive.
			expr = "${String(" + arg + ")}"
		}
		m.pathExpr = strings.Replace(m.pathExpr, "{"+param+"}", expr, 1)
	}
	m.pathExpr += "`"

	// The JSON body: the request struct's untagged fields, as an
	// interface carrying the user's own type name.
	if h.HasBody && sig.Params().Len() >= 2 {
		reqType := sig.Params().At(1).Type()
		bodyTS := g.bodyInterface(reqType, h)
		m.args = append(m.args, "req: "+bodyTS)
		m.bodyArg = "req"
	}

	// Query params: one trailing optional object.
	var queryFields, queryPairs []string
	for _, b := range h.Bindings {
		if b.Kind != "query" {
			continue
		}
		key := argName(b.Name)
		queryFields = append(queryFields, key+"?: "+scalarTS(b.Type))
		queryPairs = append(queryPairs, quoteKey(b.Name)+": query?."+key)
	}
	if len(queryFields) > 0 {
		m.args = append(m.args, "query?: { "+strings.Join(queryFields, "; ")+" }")
		m.queryLit = "{ " + strings.Join(queryPairs, ", ") + " }"
	}

	if h.ResBlob {
		m.resBlob = true
		m.retType = "Response"
		return m, len(g.errs) == 0
	}
	if sig.Results().Len() >= 1 {
		m.retType = g.tsType(sig.Results().At(0).Type(), g.posOfHandler(h), "full")
	} else {
		m.retType = "void"
	}
	return m, true
}

func (g *generator) posOfHandler(h analyzer.Handler) string { return h.Pos }

// bodyInterface renders the TS type for a request body: the named
// request struct restricted to its untagged (JSON) fields.
func (g *generator) bodyInterface(t types.Type, h analyzer.Handler) string {
	named, ok := t.(*types.Named)
	if !ok {
		g.errs = append(g.errs, &analyzer.Error{Pos: h.Pos,
			Msg:  "request bodies need a named struct type",
			Rule: "client-type"})
		return "unknown"
	}
	return g.collect(named, "body")
}

// tsType maps a Go type to its TypeScript rendering, collecting named
// struct interfaces along the way. shape is "full" (every JSON field)
// or "body" (untagged fields only — request bodies).
func (g *generator) tsType(t types.Type, pos string, shape string) string {
	switch tt := t.(type) {
	case *types.Basic:
		switch {
		case tt.Info()&types.IsString != 0:
			return "string"
		case tt.Info()&types.IsBoolean != 0:
			return "boolean"
		case tt.Info()&types.IsNumeric != 0:
			return "number"
		}
	case *types.Pointer:
		return g.tsType(tt.Elem(), pos, shape) + " | null"
	case *types.Slice:
		if b, ok := tt.Elem().(*types.Basic); ok && b.Kind() == types.Byte {
			return "string" // encoding/json renders []byte as base64
		}
		elem := g.tsType(tt.Elem(), pos, shape)
		if strings.ContainsAny(elem, "|") {
			elem = "(" + elem + ")"
		}
		return elem + "[]"
	case *types.Map:
		if b, ok := tt.Key().(*types.Basic); !ok || b.Info()&types.IsString == 0 {
			g.errs = append(g.errs, &analyzer.Error{Pos: pos,
				Msg:  fmt.Sprintf("map key %s cannot ride JSON — client types need map[string]T", tt.Key()),
				Rule: "client-type"})
			return "unknown"
		}
		return "Record<string, " + g.tsType(tt.Elem(), pos, shape) + ">"
	case *types.Named:
		obj := tt.Obj()
		if obj.Pkg() != nil {
			switch obj.Pkg().Path() + "." + obj.Name() {
			case "time.Time":
				return "string" // RFC 3339, encoding/json's rendering
			case "encoding/json.RawMessage":
				return "unknown"
			}
		}
		if _, isStruct := tt.Underlying().(*types.Struct); isStruct {
			return g.collect(tt, shape)
		}
		return g.tsType(tt.Underlying(), pos, shape)
	}
	g.errs = append(g.errs, &analyzer.Error{Pos: pos,
		Msg:  fmt.Sprintf("%s cannot ride JSON — client types are strings, bools, numbers, time.Time, slices, string-keyed maps, and exported structs of those", t),
		Rule: "client-type"})
	return "unknown"
}

// collect ensures the named struct has an emitted interface and returns
// its TS name. A type asked for in both "body" and "full" shape with
// differing field sets is ambiguous and diagnosed.
func (g *generator) collect(named *types.Named, shape string) string {
	obj := named.Obj()
	if g.ifaceDone == nil {
		g.ifaceDone = map[*types.TypeName]string{}
	}
	if prior, ok := g.ifaceByName[obj.Name()]; ok && prior != obj {
		g.fail(obj.Pos(), "client type %s is also declared at %s — client type names must be unique across the app", obj.Name(), g.relPos(prior.Pos()))
		return obj.Name()
	}
	if done, ok := g.ifaceDone[obj]; ok {
		if done != shape && g.shapesDiffer(named) {
			g.fail(obj.Pos(), "%s binds path/query fields and also appears in a response — split the request type from the response type", obj.Name())
		}
		return obj.Name()
	}
	g.ifaceByName[obj.Name()] = obj
	g.ifaceDone[obj] = shape

	st := named.Underlying().(*types.Struct)
	var lines []string
	for i := range st.NumFields() {
		f := st.Field(i)
		if !f.Exported() {
			continue // encoding/json skips them; so does the client
		}
		if f.Embedded() {
			g.fail(f.Pos(), "embedded field %s is not supported in client types — flatten it", f.Name())
			continue
		}
		tag := parseTag(st.Tag(i))
		if shape == "body" && (tag.has("path") || tag.has("query") || tag.has("form")) {
			continue // bound elsewhere; method parameters carry these
		}
		name, opts := tag.json(f.Name())
		if name == "-" {
			continue
		}
		opt := ""
		if strings.Contains(opts, "omitempty") {
			opt = "?"
		}
		lines = append(lines, fmt.Sprintf("  %s%s: %s;", quoteKey(name), opt, g.tsType(f.Type(), g.relPos(f.Pos()), "full")))
	}
	doc := obj.Pkg().Name() + "." + obj.Name()
	if shape == "body" {
		doc += " (request body — path/query fields are method parameters)"
	}
	g.ifaces = append(g.ifaces, iface{name: obj.Name(), fields: lines, doc: doc})
	return obj.Name()
}

// shapesDiffer reports whether a struct's body shape (untagged fields)
// differs from its full JSON shape.
func (g *generator) shapesDiffer(named *types.Named) bool {
	st := named.Underlying().(*types.Struct)
	for i := range st.NumFields() {
		tag := parseTag(st.Tag(i))
		if st.Field(i).Exported() && (tag.has("path") || tag.has("query") || tag.has("form")) {
			return true
		}
	}
	return false
}

// render emits the whole api.ts.
func (g *generator) render(methods []method, sum Summary) []byte {
	var b strings.Builder
	b.WriteString("// Code generated by tako gen client ts. DO NOT EDIT.\n")
	b.WriteString("//\n")
	fmt.Fprintf(&b, "// Typed client for the api endpoints of %s: plain fetch, zero\n", g.tg.Graph.Module)
	b.WriteString("// dependencies. The session cookie rides every call (same-origin);\n")
	b.WriteString("// non-GET requests attach X-CSRF-Token from the page's meta tag.\n")
	b.WriteString("// int64 fields ride as number — values past 2^53 lose precision.\n")
	if len(sum.Excluded) > 0 {
		b.WriteString("//\n// Excluded (not browser-callable):\n")
		for _, e := range sum.Excluded {
			fmt.Fprintf(&b, "//   %s %s — %s\n", e.Var, e.Route, e.Reason)
		}
	}
	b.WriteString("\n")

	for _, i := range g.ifaces {
		fmt.Fprintf(&b, "/** %s */\nexport interface %s {\n", i.doc, i.name)
		for _, f := range i.fields {
			b.WriteString(f + "\n")
		}
		b.WriteString("}\n\n")
	}

	b.WriteString(`/** A non-2xx response, message parsed from tako's {"error": ...} body. */
export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message);
    this.name = "ApiError";
  }
}

export interface ClientOptions {
  /** Origin of the deployed service; defaults to same-origin. */
  baseUrl?: string;
  /** Extra headers on every call — non-browser callers pass the session cookie here. */
  headers?: Record<string, string>;
}

type Query = Record<string, string | number | boolean | undefined>;

export function createClient(opts: ClientOptions = {}) {
  const base = opts.baseUrl ?? "";
  async function call(method: string, path: string, query: Query | null, body: unknown): Promise<Response> {
    let url = base + path;
    if (query) {
      const q = new URLSearchParams();
      for (const [k, v] of Object.entries(query)) {
        if (v !== undefined) q.set(k, String(v));
      }
      const s = q.toString();
      if (s) url += "?" + s;
    }
    const headers: Record<string, string> = { ...opts.headers };
    if (body !== undefined) headers["Content-Type"] = "application/json";
    if (method !== "GET" && method !== "HEAD" && typeof document !== "undefined") {
      const token = document.querySelector('meta[name="csrf-token"]')?.getAttribute("content");
      if (token) headers["X-CSRF-Token"] = token;
    }
    const res = await fetch(url, {
      method,
      headers,
      credentials: "same-origin",
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    if (!res.ok) {
      let message = res.statusText;
      try {
        const data: unknown = await res.json();
        if (data && typeof data === "object" && typeof (data as { error?: unknown }).error === "string") {
          message = (data as { error: string }).error;
        }
      } catch {
        // non-JSON error body: statusText stands
      }
      throw new ApiError(res.status, message);
    }
    return res;
  }
  return {
`)
	for _, m := range methods {
		fmt.Fprintf(&b, "    /** %s */\n", m.doc)
		query := "null"
		if m.queryLit != "" {
			query = m.queryLit
		}
		body := "undefined"
		if m.bodyArg != "" {
			body = m.bodyArg
		}
		callExpr := fmt.Sprintf("call(%q, %s, %s, %s)", m.httpVerb, m.pathExpr, query, body)
		if m.resBlob {
			fmt.Fprintf(&b, "    async %s(%s): Promise<Response> {\n      return %s;\n    },\n", m.name, strings.Join(m.args, ", "), callExpr)
			continue
		}
		fmt.Fprintf(&b, "    async %s(%s): Promise<%s> {\n      return (await %s).json();\n    },\n", m.name, strings.Join(m.args, ", "), m.retType, callExpr)
	}
	b.WriteString("  };\n}\n")
	return []byte(b.String())
}

// scalarTS maps a Binding's scalar Go type name.
func scalarTS(goType string) string {
	switch goType {
	case "string":
		return "string"
	case "bool":
		return "boolean"
	default:
		return "number"
	}
}

// routeMethod and routePath split a net/http "METHOD /path" pattern.
func routeMethod(route string) string {
	if i := strings.IndexByte(route, ' '); i >= 0 {
		return route[:i]
	}
	return "GET"
}

func routePath(route string) string {
	if i := strings.IndexByte(route, ' '); i >= 0 {
		return route[i+1:]
	}
	return route
}

// routeParams lists {param} names in route order.
func routeParams(route string) []string {
	var params []string
	path := routePath(route)
	for {
		i := strings.IndexByte(path, '{')
		if i < 0 {
			return params
		}
		j := strings.IndexByte(path[i:], '}')
		if j < 0 {
			return params
		}
		name := path[i+1 : i+j]
		if name != "$" {
			params = append(params, name)
		}
		path = path[i+j:]
	}
}

// argName renders a route/query parameter as a TS identifier.
func argName(param string) string {
	name := strings.TrimSuffix(param, "...")
	name = lowerCamel(name)
	if !validIdent(name) || jsReserved[name] {
		name = name + "_"
	}
	return name
}

// quoteKey renders an object key, quoting when it isn't an identifier.
func quoteKey(name string) string {
	if validIdent(name) {
		return name
	}
	return fmt.Sprintf("%q", name)
}

func validIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r == '_' || r == '$', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

var jsReserved = map[string]bool{
	"await": true, "break": true, "case": true, "catch": true, "class": true,
	"const": true, "continue": true, "debugger": true, "default": true,
	"delete": true, "do": true, "else": true, "enum": true, "export": true,
	"extends": true, "false": true, "finally": true, "for": true,
	"function": true, "if": true, "import": true, "in": true,
	"instanceof": true, "interface": true, "let": true, "new": true,
	"null": true, "package": true, "private": true, "protected": true,
	"public": true, "return": true, "static": true, "super": true,
	"switch": true, "this": true, "throw": true, "true": true, "try": true,
	"typeof": true, "var": true, "void": true, "while": true, "with": true,
	"yield": true,
}

// lowerCamel lowercases a leading initialism run: ListProducts →
// listProducts, URLFor → urlFor, ID → id.
func lowerCamel(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	upper := 0
	for upper < len(runes) && runes[upper] >= 'A' && runes[upper] <= 'Z' {
		upper++
	}
	switch {
	case upper == 0:
		return s
	case upper == len(runes):
		return strings.ToLower(s)
	case upper == 1:
		runes[0] += 'a' - 'A'
	default:
		// URLFor: lowercase the initialism except the rune starting the
		// next word.
		for i := range upper - 1 {
			runes[i] += 'a' - 'A'
		}
	}
	return string(runes)
}

// structTag is a parsed struct tag.
type structTag struct{ raw string }

func parseTag(raw string) structTag { return structTag{raw} }

func (t structTag) get(key string) string {
	// reflect.StructTag.Get without importing reflect for one call.
	tag := t.raw
	for tag != "" {
		i := 0
		for i < len(tag) && tag[i] == ' ' {
			i++
		}
		tag = tag[i:]
		i = strings.IndexByte(tag, ':')
		if i < 0 {
			break
		}
		name := tag[:i]
		rest := tag[i+1:]
		if len(rest) == 0 || rest[0] != '"' {
			break
		}
		j := 1
		for j < len(rest) && rest[j] != '"' {
			if rest[j] == '\\' {
				j++
			}
			j++
		}
		if j >= len(rest) {
			break
		}
		value := rest[1:j]
		if name == key {
			return value
		}
		tag = rest[j+1:]
	}
	return ""
}

func (t structTag) has(key string) bool { return t.get(key) != "" }

// json returns the marshaled field name and options.
func (t structTag) json(fieldName string) (name, opts string) {
	v := t.get("json")
	if v == "" {
		return fieldName, ""
	}
	if i := strings.IndexByte(v, ','); i >= 0 {
		name, opts = v[:i], v[i+1:]
		if name == "" {
			name = fieldName
		}
		return name, opts
	}
	return v, ""
}
