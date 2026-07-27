package analyzer

import (
	"fmt"
	"go/ast"
	"go/types"
	"strings"

	"golang.org/x/tools/go/packages"
)

// entityType is the analyzer's view of one entity payload struct: which
// fields are indexed (and may appear in Where/OrderBy) and which were
// actually used by queries — the derived index set.
type entityType struct {
	display    string            // short type name, e.g. "Link"
	declared   []string          // entity:"index" fields, struct order
	indexed    map[string]bool   // membership set of declared
	wire       map[string]string // Go field → marshaled property name (json tag rule)
	used       map[string]bool   // fields seen in Where/OrderBy call sites
	usedFilter map[string]bool   // fields seen in Where call sites
	usedOrder  map[string]string // OrderBy fields → "asc" | "desc"
}

// extractEntity validates one tako.Entity declaration and returns its
// AppGraph entry plus the payload type's schema for the query scan.
func extractEntity(dir string, pkg *packages.Package, name *ast.Ident, call *ast.CallExpr, errs *ErrorList) (*Resource, string, *entityType) {
	if name.Name == "_" || len(call.Args) < 1 {
		return nil, "", nil
	}
	resName, err := constString(pkg, call.Args[0])
	if err != nil {
		*errs = append(*errs, &Error{
			Pos: relPos(dir, pkg.Fset, call.Args[0].Pos()),
			Msg: fmt.Sprintf("tako.Entity name must be a string constant — names are read at analysis time, not run time; got %s",
				describeExpr(call.Args[0])),
			Rule: "const-name",
		})
		return nil, "", nil
	}

	tv, ok := pkg.TypesInfo.Types[call]
	if !ok {
		return nil, "", nil
	}
	ptr, ok := tv.Type.(*types.Pointer)
	if !ok {
		return nil, "", nil
	}
	named, ok := ptr.Elem().(*types.Named)
	if !ok || named.TypeArgs().Len() != 1 {
		return nil, "", nil
	}
	payload := named.TypeArgs().At(0)

	et, ok2 := entitySchemaOf(dir, pkg, payload, errs)
	if !ok2 {
		return nil, "", nil
	}
	res := &Resource{
		Kind:    "entity",
		Name:    resName,
		Var:     pkg.Name + "." + name.Name,
		Package: pkg.PkgPath,
		Type:    et.display,
		Pos:     relPos(dir, pkg.Fset, name.Pos()),
	}
	return res, payload.String(), et
}

// extractBucket validates one tako.Bucket declaration.
func extractBucket(dir string, pkg *packages.Package, name *ast.Ident, call *ast.CallExpr, errs *ErrorList) *Resource {
	if name.Name == "_" || len(call.Args) < 1 {
		return nil
	}
	resName, err := constString(pkg, call.Args[0])
	if err != nil {
		*errs = append(*errs, &Error{
			Pos: relPos(dir, pkg.Fset, call.Args[0].Pos()),
			Msg: fmt.Sprintf("tako.Bucket name must be a string constant — names are read at analysis time, not run time; got %s",
				describeExpr(call.Args[0])),
			Rule: "const-name",
		})
		return nil
	}
	return &Resource{
		Kind:    "bucket",
		Name:    resName,
		Var:     pkg.Name + "." + name.Name,
		Package: pkg.PkgPath,
		Pos:     relPos(dir, pkg.Fset, name.Pos()),
	}
}

// entitySchemaOf validates the payload struct's entity tags: exactly one
// entity:"id" field, of type string.
func entitySchemaOf(dir string, pkg *packages.Package, payload types.Type, errs *ErrorList) (*entityType, bool) {
	named, ok := payload.(*types.Named)
	if !ok {
		return nil, false
	}
	obj := named.Obj()
	st, ok := named.Underlying().(*types.Struct)
	if !ok {
		*errs = append(*errs, &Error{
			Pos:  relPos(dir, pkg.Fset, obj.Pos()),
			Msg:  fmt.Sprintf("entity type %s must be a struct", obj.Name()),
			Rule: "entity-id",
		})
		return nil, false
	}

	et := &entityType{display: obj.Name(), indexed: map[string]bool{}, wire: map[string]string{},
		used: map[string]bool{}, usedFilter: map[string]bool{}, usedOrder: map[string]string{}}
	idCount := 0
	bad := false
	for i := range st.NumFields() {
		f := st.Field(i)
		switch tagValue(st.Tag(i), "entity") {
		case "id":
			idCount++
			if f.Type().String() != "string" {
				*errs = append(*errs, &Error{
					Pos: relPos(dir, pkg.Fset, f.Pos()),
					Msg: fmt.Sprintf("entity id field %s must be a string; got %s",
						f.Name(), types.TypeString(f.Type(), types.RelativeTo(pkg.Types))),
					Rule: "entity-id",
				})
				bad = true
			}
		case "index":
			et.declared = append(et.declared, f.Name())
			et.indexed[f.Name()] = true
			// Datastore property names are the marshaled (json) names —
			// the same rule the SDK's jsonName applies at runtime.
			et.wire[f.Name()] = f.Name()
			if tag := tagValue(st.Tag(i), "json"); tag != "" && tag != "-" {
				if i := strings.IndexByte(tag, ','); i >= 0 {
					tag = tag[:i]
				}
				if tag != "" {
					et.wire[f.Name()] = tag
				}
			}
		}
	}
	if idCount != 1 {
		*errs = append(*errs, &Error{
			Pos:  relPos(dir, pkg.Fset, obj.Pos()),
			Msg:  fmt.Sprintf("entity type %s must have exactly one field tagged entity:\"id\"", obj.Name()),
			Rule: "entity-id",
		})
		bad = true
	}
	return et, !bad
}

// scanQueries walks the package for Where/OrderBy calls on entity
// queries, enforces constant + indexed fields, and records each store's
// used fields — the derived index set.
func scanQueries(dir string, pkg *packages.Package, entities map[string]*entityType, errs *ErrorList) {
	for _, file := range pkg.Syntax {
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) < 1 {
				return true
			}
			method, payloadKey := entityQueryMethod(pkg, call)
			if method != "Where" && method != "OrderBy" {
				return true
			}
			et := entities[payloadKey]
			if et == nil {
				return true
			}

			fieldArg := call.Args[0]
			field, err := constString(pkg, fieldArg)
			if err != nil {
				*errs = append(*errs, &Error{
					Pos: relPos(dir, pkg.Fset, fieldArg.Pos()),
					Msg: fmt.Sprintf("query field must be a string constant — indexes are derived at analysis time; got %s",
						describeExpr(fieldArg)),
					Rule: "const-query-field",
				})
				return true
			}
			verb, suffix, direction := "filters on", "queryable", ""
			if method == "OrderBy" {
				verb, suffix, direction = "orders by", "orderable", "asc"
				if len(field) > 0 && field[0] == '-' {
					field = field[1:]
					direction = "desc"
				}
			}
			if !et.indexed[field] {
				*errs = append(*errs, &Error{
					Pos: relPos(dir, pkg.Fset, fieldArg.Pos()),
					Msg: fmt.Sprintf("query %s %s.%s, but %s is not indexed — tag it entity:\"index\" to make it %s",
						verb, et.display, field, field, suffix),
					Rule: "unindexed-query",
				})
				return true
			}
			et.used[field] = true
			if direction == "" {
				et.usedFilter[field] = true
			} else {
				et.usedOrder[field] = direction
			}
			return true
		})
	}
}

// entityQueryMethod reports the EntityQuery method a call invokes and the
// query's payload type key, or ("", "") for unrelated calls.
func entityQueryMethod(pkg *packages.Package, call *ast.CallExpr) (string, string) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", ""
	}
	fn, ok := pkg.TypesInfo.Uses[sel.Sel].(*types.Func)
	if !ok {
		return "", ""
	}
	sig, ok := fn.Type().(*types.Signature)
	if !ok || sig.Recv() == nil {
		return "", ""
	}
	ptr, ok := sig.Recv().Type().(*types.Pointer)
	if !ok {
		return "", ""
	}
	named, ok := ptr.Elem().(*types.Named)
	if !ok || named.Obj().Name() != "EntityQuery" ||
		named.Obj().Pkg() == nil || named.Obj().Pkg().Path() != sdkPath ||
		named.TypeArgs().Len() != 1 {
		return "", ""
	}
	return fn.Name(), named.TypeArgs().At(0).String()
}

// tagValue reads one key from a raw struct tag.
func tagValue(rawTag, key string) string {
	return tagOf(rawTag).Get(key)
}

// extractQueue validates one tako.Queue declaration.
func extractQueue(dir string, pkg *packages.Package, name *ast.Ident, call *ast.CallExpr, errs *ErrorList) *Resource {
	return extractCarrier("queue", "tako.Queue", dir, pkg, name, call, errs)
}

// extractTopic validates one tako.Topic declaration.
func extractTopic(dir string, pkg *packages.Package, name *ast.Ident, call *ast.CallExpr, errs *ErrorList) *Resource {
	return extractCarrier("topic", "tako.Topic", dir, pkg, name, call, errs)
}

// extractCarrier validates one simple named-resource declaration — the
// message carriers (Queue, Topic) and the F8 primitives (Cache, Secret,
// Counter, Histogram). Same shape: constant name, payload type recorded
// when the constructor is generic.
func extractCarrier(kind, ctor, dir string, pkg *packages.Package, name *ast.Ident, call *ast.CallExpr, errs *ErrorList) *Resource {
	if name.Name == "_" || len(call.Args) < 1 {
		return nil
	}
	resName, err := constString(pkg, call.Args[0])
	if err != nil {
		*errs = append(*errs, &Error{
			Pos: relPos(dir, pkg.Fset, call.Args[0].Pos()),
			Msg: fmt.Sprintf("%s name must be a string constant — names are read at analysis time, not run time; got %s",
				ctor, describeExpr(call.Args[0])),
			Rule: "const-name",
		})
		return nil
	}
	res := &Resource{
		Kind:    kind,
		Name:    resName,
		Var:     pkg.Name + "." + name.Name,
		Package: pkg.PkgPath,
		Pos:     relPos(dir, pkg.Fset, name.Pos()),
	}
	if tv, ok := pkg.TypesInfo.Types[call]; ok {
		if ptr, ok := tv.Type.(*types.Pointer); ok {
			if named, ok := ptr.Elem().(*types.Named); ok && named.TypeArgs().Len() == 1 {
				if p, ok := named.TypeArgs().At(0).(*types.Named); ok {
					res.Type = p.Obj().Name()
				}
			}
		}
	}
	return res
}

// extractLock validates one tako.Lock declaration (W8): a constant
// name and — deliberately — no options: the lease policy (30s TTL,
// ttl/3 heartbeats, 2s campaigns) is framework-owned in v0.
func extractLock(dir string, pkg *packages.Package, name *ast.Ident, call *ast.CallExpr, errs *ErrorList) *Resource {
	if name.Name == "_" || len(call.Args) < 1 {
		return nil
	}
	bad := false
	lockName, err := constString(pkg, call.Args[0])
	if err != nil {
		*errs = append(*errs, &Error{
			Pos: relPos(dir, pkg.Fset, call.Args[0].Pos()),
			Msg: fmt.Sprintf("tako.Lock name must be a string constant — names are read at analysis time, not run time; got %s",
				describeExpr(call.Args[0])),
			Rule: "const-name",
		})
		return nil
	}
	for _, opt := range call.Args[1:] {
		*errs = append(*errs, &Error{
			Pos:  relPos(dir, pkg.Fset, opt.Pos()),
			Msg:  "tako.Lock accepts no options in v0 — the lease policy (30s TTL, heartbeats, campaign cadence) is framework-owned",
			Rule: "invalid-option",
		})
		bad = true
	}
	if bad {
		return nil
	}
	return &Resource{
		Kind:    "lock",
		Name:    lockName,
		Var:     pkg.Name + "." + name.Name,
		Package: pkg.PkgPath,
		Pos:     relPos(dir, pkg.Fset, name.Pos()),
	}
}
