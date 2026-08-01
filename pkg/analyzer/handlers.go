package analyzer

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"reflect"
	"strings"

	"golang.org/x/tools/go/packages"
)

// apiOptions is the option set tako.API accepts; anything else is
// tako/invalid-option.
var apiOptions = map[string]bool{
	"WithAuth":      true,
	"WithPrivate":   true,
	"WithProfile":   true,
	"WithFootprint": true,
	"WithDomain":    true,
}

// bindableTypes are the field types path:/query:/form: tags may bind in
// v0.
var bindableTypes = map[string]string{
	"string":  "string",
	"bool":    "bool",
	"int":     "int",
	"int64":   "int64",
	"float64": "float64",
}

// extractAPI validates one tako.API declaration and returns its AppGraph
// entry. It appends every rule violation it finds to errs and returns nil
// when any occurred.
func extractAPI(dir string, pkg *packages.Package, name *ast.Ident, call *ast.CallExpr, errs *ErrorList) *Handler {
	return extractRoute("API", dir, pkg, name, call, errs)
}

// extractPage validates one tako.Page declaration — the same route +
// tagged-Req shape as API, except pages receive browser forms, never
// JSON bodies, and render HTML (the signature enforces the result).
func extractPage(dir string, pkg *packages.Package, name *ast.Ident, call *ast.CallExpr, errs *ErrorList) *Handler {
	return extractRoute("Page", dir, pkg, name, call, errs)
}

func extractRoute(ctor, dir string, pkg *packages.Package, name *ast.Ident, call *ast.CallExpr, errs *ErrorList) *Handler {
	if name.Name == "_" || len(call.Args) < 2 {
		return nil
	}
	bad := false
	fail := func(e *Error) { *errs = append(*errs, e); bad = true }

	route, err := constString(pkg, call.Args[0])
	if err != nil {
		fail(&Error{
			Pos: relPos(dir, pkg.Fset, call.Args[0].Pos()),
			Msg: fmt.Sprintf("tako.%s route must be a string constant — routes are read at analysis time, not run time; got %s",
				ctor, describeExpr(call.Args[0])),
			Rule: "const-route",
		})
		return nil
	}
	method, params := parseRoute(route)

	requiresAuth := false
	private := false
	profile := ""
	domain := ""
	var footprint *Footprint
	for _, opt := range call.Args[2:] {
		optName, ok := sdkOptionCall(pkg, opt)
		if ok && optName == "WithAuth" {
			requiresAuth = true
			continue
		}
		if ok && optName == "WithPrivate" {
			private = true
			continue
		}
		if ok && optName == "WithProfile" {
			profile = profileArg(pkg, opt)
			continue
		}
		if ok && optName == "WithFootprint" {
			footprint = footprintArg(dir, pkg, opt, errs)
			continue
		}
		if ok && optName == "WithDomain" {
			domain = domainArg(dir, pkg, opt, errs)
			continue
		}
		if ok && !apiOptions[optName] {
			fail(&Error{
				Pos: relPos(dir, pkg.Fset, opt.Pos()),
				Msg: fmt.Sprintf("tako.%s is not a tako.%s option (tako.%s accepts WithAuth, WithPrivate, WithProfile, WithFootprint, WithDomain)",
					optName, ctor, ctor),
				Rule: "invalid-option",
			})
		}
	}

	req, reqErr := reqStruct(pkg, call.Args[1])
	if reqErr != "" {
		fail(&Error{
			Pos:  relPos(dir, pkg.Fset, call.Args[1].Pos()),
			Msg:  reqErr,
			Rule: "unexported-req",
		})
		return nil
	}

	hasBlob := false
	for _, f := range req.fields {
		if f.isBlob() {
			hasBlob = true
		}
	}
	if hasBlob && (method == "GET" || method == "HEAD") {
		fail(&Error{
			Pos: relPos(dir, pkg.Fset, call.Args[0].Pos()),
			Msg: fmt.Sprintf("%s route %q cannot receive an upload — requests with tako.Blob fields need a non-GET route",
				method, route),
			Rule: "blob-binding",
		})
	}

	var bindings []Binding
	hasBody := false
	seenPath := map[string]bool{}
	for _, f := range req.fields {
		switch {
		case f.isBlob():
			formName := f.tag("form")
			if formName == "" {
				fail(&Error{
					Pos: relPos(dir, pkg.Fset, f.pos),
					Msg: fmt.Sprintf("tako.Blob field %s needs a form:\"…\" tag — blobs bind from multipart form fields",
						f.name),
					Rule: "blob-binding",
				})
				continue
			}
			bindings = append(bindings, Binding{Field: f.name, Kind: "blob", Name: formName, Type: "blob"})
		case f.tag("path") != "":
			param := f.tag("path")
			seenPath[param] = true
			if !contains(params, param) {
				fail(&Error{
					Pos: relPos(dir, pkg.Fset, f.pos),
					Msg: fmt.Sprintf("%s field %s is tagged path:%q but route %q has no {%s} parameter",
						req.name, f.name, param, route, param),
					Rule: "route-params",
				})
				continue
			}
			bindings = append(bindings, f.binding(dir, pkg, "path", param, fail))
		case f.tag("query") != "":
			bindings = append(bindings, f.binding(dir, pkg, "query", f.tag("query"), fail))
		case f.tag("form") != "":
			bindings = append(bindings, f.binding(dir, pkg, "form", f.tag("form"), fail))
		default:
			if hasBlob {
				fail(&Error{
					Pos: relPos(dir, pkg.Fset, f.pos),
					Msg: fmt.Sprintf("%s mixes a tako.Blob field with untagged field %s — when a blob is present every other field needs a path:/query:/form: tag",
						req.name, f.name),
					Rule: "blob-binding",
				})
				continue
			}
			if method == "GET" || method == "HEAD" {
				fail(&Error{
					Pos: relPos(dir, pkg.Fset, f.pos),
					Msg: fmt.Sprintf("%s route %q cannot bind a JSON body, but %s field %s has no path:/query: tag",
						method, route, req.name, f.name),
					Rule: "body-on-get",
				})
				continue
			}
			if ctor == "Page" {
				// Pages receive browser forms, never JSON bodies.
				fail(&Error{
					Pos: relPos(dir, pkg.Fset, f.pos),
					Msg: fmt.Sprintf("%s field %s has no path:/query:/form: tag — pages receive browser forms, not JSON bodies",
						req.name, f.name),
					Rule: "page-binding",
				})
				continue
			}
			hasBody = true
		}
	}
	for _, param := range params {
		if !seenPath[param] {
			fail(&Error{
				Pos: relPos(dir, pkg.Fset, call.Args[0].Pos()),
				Msg: fmt.Sprintf("route %q has path parameter {%s} but %s has no field tagged path:%q",
					route, param, req.name, param),
				Rule: "route-params",
			})
		}
	}
	if bad {
		return nil
	}

	kind := "api"
	if ctor == "Page" {
		kind = "page"
	}
	reqRef, resRef := qualifiedSignature(pkg, call.Args[1])
	return &Handler{
		Kind:         kind,
		Var:          pkg.Name + "." + name.Name,
		Package:      pkg.PkgPath,
		Service:      pkg.Name,
		Route:        route,
		ReqType:      req.ref,
		ReqRef:       reqRef,
		ResRef:       resRef,
		Bindings:     bindings,
		HasBody:      hasBody,
		ResBlob:      resIsBlob(pkg, call.Args[1]),
		RequiresAuth: requiresAuth,
		Private:      private,
		Profile:      profile,
		Footprint:    footprint,
		Domain:       domain,
		Pos:          relPos(dir, pkg.Fset, name.Pos()),
	}
}

// checkPackageLevel reports handler constructors called inside function
// bodies — declarations the analyzer would never see.
func checkPackageLevel(dir string, pkg *packages.Package, errs *ErrorList) {
	for _, file := range pkg.Syntax {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if sdkConstructor(pkg, call) == "API" {
					*errs = append(*errs, &Error{
						Pos:  relPos(dir, pkg.Fset, call.Pos()),
						Msg:  "tako.API must initialize a package-level var — handlers declared inside functions are invisible to analysis",
						Rule: "package-level",
					})
				}
				return true
			})
		}
	}
}

// reqInfo describes a handler's request struct.
type reqInfo struct {
	name   string  // display name, e.g. "GreetReq"
	ref    string  // how generated code references it, e.g. "GreetReq"; "" for struct{}
	fields []field // direct fields, source order
}

type field struct {
	name   string
	typ    types.Type
	rawTag reflect.StructTag
	pos    token.Pos
}

func (f field) tag(key string) string { return f.rawTag.Get(key) }

func (f field) isBlob() bool { return f.typ.String() == sdkPath+".Blob" }

// resIsBlob reports whether the handler's result type is tako.Blob.
func resIsBlob(pkg *packages.Package, fnExpr ast.Expr) bool {
	tv, ok := pkg.TypesInfo.Types[fnExpr]
	if !ok {
		return false
	}
	sig, ok := tv.Type.(*types.Signature)
	if !ok || sig.Results().Len() < 1 {
		return false
	}
	return sig.Results().At(0).Type().String() == sdkPath+".Blob"
}

// tagOf parses a raw struct-tag string.
func tagOf(raw string) reflect.StructTag { return reflect.StructTag(raw) }

// qualifiedSignature renders the handler's Req and Res types the way
// ANOTHER package's generated code references them — package-name
// qualified ("billing.ChargeReq"). Remote call stubs (§2.12) need both.
func qualifiedSignature(pkg *packages.Package, fnExpr ast.Expr) (reqRef, resRef string) {
	tv, ok := pkg.TypesInfo.Types[fnExpr]
	if !ok {
		return "", ""
	}
	sig, ok := tv.Type.(*types.Signature)
	if !ok || sig.Params().Len() < 2 || sig.Results().Len() < 1 {
		return "", ""
	}
	qual := func(p *types.Package) string { return p.Name() }
	return types.TypeString(sig.Params().At(1).Type(), qual),
		types.TypeString(sig.Results().At(0).Type(), qual)
}

// binding builds one Binding, checking the field's type is bindable.
func (f field) binding(dir string, pkg *packages.Package, kind, name string, fail func(*Error)) Binding {
	typ, ok := bindableTypes[f.typ.String()]
	if !ok {
		fail(&Error{
			Pos: relPos(dir, pkg.Fset, f.pos),
			Msg: fmt.Sprintf("field %s: %s:-tagged fields must be string, bool, int, int64, or float64; got %s",
				f.name, kind, types.TypeString(f.typ, types.RelativeTo(pkg.Types))),
			Rule: "bind-type",
		})
		typ = "string"
	}
	return Binding{Field: f.name, Kind: kind, Name: name, Type: typ}
}

// reqStruct resolves the handler function's Req parameter. The error
// string is non-empty when the type can't be used by generated code.
func reqStruct(pkg *packages.Package, fnExpr ast.Expr) (reqInfo, string) {
	tv, ok := pkg.TypesInfo.Types[fnExpr]
	if !ok {
		return reqInfo{name: "the request struct"}, ""
	}
	sig, ok := tv.Type.(*types.Signature)
	if !ok || sig.Params().Len() < 2 {
		return reqInfo{name: "the request struct"}, ""
	}
	reqType := sig.Params().At(1).Type()

	info := reqInfo{name: "the request struct"}
	st, isStruct := reqType.Underlying().(*types.Struct)
	if named, ok := reqType.(*types.Named); ok {
		obj := named.Obj()
		info.name = obj.Name()
		info.ref = obj.Name()
		if isStruct && st.NumFields() > 0 && !obj.Exported() {
			return info, fmt.Sprintf(
				"request type %s must be exported — generated services construct it outside the package",
				obj.Name())
		}
	} else if isStruct && st.NumFields() > 0 {
		return info, "request type must be a named, exported struct (or struct{}) — generated services construct it outside the package"
	}
	if !isStruct {
		return info, ""
	}
	for i := range st.NumFields() {
		fv := st.Field(i)
		info.fields = append(info.fields, field{
			name:   fv.Name(),
			typ:    fv.Type(),
			rawTag: reflect.StructTag(st.Tag(i)),
			pos:    fv.Pos(),
		})
	}
	return info, ""
}

// sdkOptionCall reports the SDK option a constructor argument invokes
// (e.g. "WithTTL"), when it is one.
func sdkOptionCall(pkg *packages.Package, arg ast.Expr) (string, bool) {
	call, ok := arg.(*ast.CallExpr)
	if !ok {
		return "", false
	}
	name := sdkConstructor(pkg, call)
	if name == "" {
		return "", false
	}
	return name, strings.HasPrefix(name, "With") || name == "SPA"
}

// parseRoute splits a net/http pattern into its method and path
// parameters.
func parseRoute(route string) (method string, params []string) {
	method = "GET"
	path := route
	if i := strings.IndexByte(route, ' '); i >= 0 {
		method = route[:i]
		path = strings.TrimSpace(route[i+1:])
	}
	for _, seg := range strings.Split(path, "/") {
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			name := strings.TrimSuffix(strings.TrimPrefix(seg, "{"), "}")
			name = strings.TrimSuffix(name, "...")
			if name != "" && name != "$" {
				params = append(params, name)
			}
		}
	}
	return method, params
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
