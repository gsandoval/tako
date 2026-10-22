package analyzer

import (
	"fmt"
	"go/ast"
	"go/types"
	"sort"

	"golang.org/x/tools/go/packages"
)

// resRef identifies a declared resource var for the usage-edge scan.
type resRef struct {
	name string // Resource.Name (the declared constant name)
	kind string // Resource.Kind ("entity", "bucket", "queue", "topic", "secret")
}

// verbOf maps an SDK method call on a resource var to its IAM verb.
// Only kinds that derive cloud grants (spec §4.6's table) produce edges;
// unknown methods produce none.
func verbOf(kind, method string) string {
	switch kind {
	case "entity":
		switch method {
		case "Get", "Exists", "Query":
			return "read"
		case "Put", "Delete", "Update", "Tx":
			return "write"
		}
	case "bucket":
		switch method {
		case "Get", "Exists", "List":
			return "read"
		case "Put", "Delete":
			return "write"
		case "SignedPut", "SignedGet":
			return "sign"
		}
	case "disk":
		switch method {
		case "ReadFile", "Open", "Stat", "FS", "Path":
			return "read"
		case "WriteFile", "Create", "OpenFile", "Remove", "MkdirAll":
			return "write"
		}
	case "queue", "topic":
		if method == "Publish" || method == "PublishAfter" {
			return "publish"
		}
	case "secret":
		return "read"
	case "lock":
		switch method {
		case "Do", "TryDo":
			return "lease"
		}
	case "job":
		if method == "Run" {
			return "run"
		}
	case "auth":
		// Accounts (AB10b). The split is the whole point: a service that
		// only reads the account list is not a service that can mint one,
		// and Identity Platform has separate roles for exactly that.
		switch method {
		case "Get", "ByEmail", "List", "Page":
			return "read"
		case "Create", "Grant", "Revoke", "Disable", "Enable", "Delete":
			return "write"
		}
	}
	return ""
}

// usersHandleType is the receiver type whose methods derive account
// grants. It is matched by TYPE rather than by variable, because
// `users := auth.Users()` puts a local between the declaration and the
// call and no dataflow is needed to see what that local is.
const usersHandleType = "*github.com/gsandoval/tako.UsersHandle"

// accountVerb classifies a method call on a *tako.UsersHandle (or on the
// query its List returns), or returns "" for anything else.
func accountVerb(pkg *packages.Package, sel *ast.SelectorExpr) string {
	tv, ok := pkg.TypesInfo.Types[sel.X]
	if !ok || tv.Type == nil {
		return ""
	}
	switch tv.Type.String() {
	case usersHandleType:
		return verbOf("auth", sel.Sel.Name)
	case "*github.com/gsandoval/tako.UserQuery":
		// List(...).Limit(n).Page("") — the read is the whole chain.
		return verbOf("auth", "Page")
	}
	return ""
}

// funcArg resolves a handler constructor's function argument to its
// *types.Func and (when written inline as a function literal) its
// *ast.FuncLit so the edge and query scans can walk closure bodies
// identically to named functions.
func funcArg(pkg *packages.Package, name *ast.Ident, call *ast.CallExpr, idx int) (*types.Func, *ast.FuncLit) {
	if idx >= len(call.Args) {
		return nil, nil
	}
	switch e := call.Args[idx].(type) {
	case *ast.Ident:
		if fn, ok := pkg.TypesInfo.Uses[e].(*types.Func); ok {
			return fn, nil
		}
	case *ast.SelectorExpr:
		if fn, ok := pkg.TypesInfo.Uses[e.Sel].(*types.Func); ok {
			return fn, nil
		}
	case *ast.FuncLit:
		if tv, ok := pkg.TypesInfo.Types[e]; ok {
			if sig, ok := tv.Type.(*types.Signature); ok {
				return types.NewFunc(e.Pos(), pkg.Types, name.Name, sig), e
			}
		}
	}
	return nil, nil
}

// deriveEdges computes resource-usage and service-call edges: for each
// handler, an AST reference scan of its function, transitive through
// same-module calls (v0 per spec §4.4 — slightly over-grants, never
// under-reports). Verbs come from the SDK method invoked on the
// resource var; a `.Call` on another handler's Endpoint var is a call
// edge (§2.12). Raw cross-package handler-function calls — the hidden
// RPC boundary — are diagnosed here, where the scan already looks.
func deriveEdges(dir string, pkgs []*packages.Package, vars map[types.Object]resRef, endpoints map[types.Object]string, handlerFuncs map[string]*types.Func, pkgHandlerLits map[*packages.Package][]*ast.FuncDecl, handlers []Handler) ([]Edge, map[string]bool, ErrorList) {
	// The declared auth resource, if this app has one: account usage
	// attaches its edges to it, because auth.Users() is how accounts are
	// reached and tako.Auth is what declares them.
	authRes := ""
	for _, r := range vars {
		if r.kind == "auth" {
			authRes = r.name
		}
	}
	type ref struct {
		res  resRef
		verb string
	}
	refs := map[*types.Func][]ref{}          // direct resource touches per function
	epRefs := map[*types.Func][]string{}     // Endpoint .Call targets per function
	calls := map[*types.Func][]*types.Func{} // same-module call graph
	delayed := map[string]bool{}             // queues with WithDelay publish sites (T3: Tasks-routed)
	var errs ErrorList

	// Handler root functions, for the raw-cross-package-call error.
	rootVarOf := map[*types.Func]string{}
	for v, fn := range handlerFuncs {
		if fn != nil {
			rootVarOf[fn] = v
		}
	}

	scanBody := func(pkg *packages.Package, fn *types.Func, body *ast.BlockStmt) {
		ast.Inspect(body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
				// endpoint.Call / pkg.Endpoint.Call → a typed
				// service call (§2.12).
				if sel.Sel.Name == "Call" {
					var obj types.Object
					switch x := sel.X.(type) {
					case *ast.Ident:
						obj = pkg.TypesInfo.Uses[x]
					case *ast.SelectorExpr:
						obj = pkg.TypesInfo.Uses[x.Sel]
					}
					if callee, ok := endpoints[obj]; ok {
						epRefs[fn] = append(epRefs[fn], callee)
						return true
					}
				}
				// Accounts are reached through a handle, not
				// through the declared var, so they are matched
				// on the receiver's TYPE.
				if v := accountVerb(pkg, sel); v != "" && authRes != "" {
					refs[fn] = append(refs[fn], ref{resRef{authRes, "auth"}, v})
					return true
				}
				if id, ok := sel.X.(*ast.Ident); ok {
					if r, ok := vars[pkg.TypesInfo.Uses[id]]; ok {
						if v := verbOf(r.kind, sel.Sel.Name); v != "" {
							refs[fn] = append(refs[fn], ref{r, v})
							// A delayed publish routes the queue
							// through Cloud Tasks in the cloud. It
							// is its own method now, so the call
							// site names it outright.
							if r.kind == "queue" && sel.Sel.Name == "PublishAfter" {
								delayed[r.name] = true
							}
						}
						return true
					}
				}
				if callee, ok := pkg.TypesInfo.Uses[sel.Sel].(*types.Func); ok {
					if callee.Pkg() == pkg.Types {
						calls[fn] = append(calls[fn], callee)
					} else if v, isRoot := rootVarOf[callee]; isRoot {
						errs = append(errs, &Error{
							Pos:  relPos(dir, pkg.Fset, call.Pos()),
							Msg:  fmt.Sprintf("%s is a handler function — cross-service calls go through the Endpoint var (%s.Call(ctx, req)) so the RPC boundary stays visible", callee.Name(), v),
							Rule: "raw-call",
						})
					}
				}
				return true
			}
			if id, ok := call.Fun.(*ast.Ident); ok {
				if callee, ok := pkg.TypesInfo.Uses[id].(*types.Func); ok && callee.Pkg() == pkg.Types {
					calls[fn] = append(calls[fn], callee)
				}
			}
			return true
		})
	}

	for _, pkg := range pkgs {
		for _, file := range pkg.Syntax {
			for _, decl := range file.Decls {
				fd, ok := decl.(*ast.FuncDecl)
				if !ok || fd.Body == nil {
					continue
				}
				fn, ok := pkg.TypesInfo.Defs[fd.Name].(*types.Func)
				if !ok {
					continue
				}
				scanBody(pkg, fn, fd.Body)
			}
		}
		for _, fd := range pkgHandlerLits[pkg] {
			if fn := handlerFuncs[pkg.Name+"."+fd.Name.Name]; fn != nil && fd.Body != nil {
				scanBody(pkg, fn, fd.Body)
			}
		}
	}

	// reach unions a function's own touches with everything reachable
	// through the call graph.
	memo := map[*types.Func]map[ref]bool{}
	var reach func(fn *types.Func, seen map[*types.Func]bool) map[ref]bool
	reach = func(fn *types.Func, seen map[*types.Func]bool) map[ref]bool {
		if got, ok := memo[fn]; ok {
			return got
		}
		if seen[fn] {
			return nil
		}
		seen[fn] = true
		out := map[ref]bool{}
		for _, r := range refs[fn] {
			out[r] = true
		}
		for _, callee := range calls[fn] {
			for r := range reach(callee, seen) {
				out[r] = true
			}
		}
		memo[fn] = out
		return out
	}

	// The same transitive union, for call targets.
	epMemo := map[*types.Func]map[string]bool{}
	var epReach func(fn *types.Func, seen map[*types.Func]bool) map[string]bool
	epReach = func(fn *types.Func, seen map[*types.Func]bool) map[string]bool {
		if got, ok := epMemo[fn]; ok {
			return got
		}
		if seen[fn] {
			return nil
		}
		seen[fn] = true
		out := map[string]bool{}
		for _, t := range epRefs[fn] {
			out[t] = true
		}
		for _, callee := range calls[fn] {
			for t := range epReach(callee, seen) {
				out[t] = true
			}
		}
		epMemo[fn] = out
		return out
	}

	edges := []Edge{}
	for _, h := range handlers {
		fn := handlerFuncs[h.Var]
		if fn == nil {
			continue
		}
		set := reach(fn, map[*types.Func]bool{})
		hs := make([]Edge, 0, len(set))
		for r := range set {
			hs = append(hs, Edge{From: h.Var, To: r.res.name, ToKind: r.res.kind, Kind: "resource", Verb: r.verb})
		}
		for t := range epReach(fn, map[*types.Func]bool{}) {
			if t == h.Var {
				continue // self-calls are just recursion
			}
			hs = append(hs, Edge{From: h.Var, To: t, Kind: "call", Verb: "call"})
		}
		sort.Slice(hs, func(i, j int) bool {
			if hs[i].To != hs[j].To {
				return hs[i].To < hs[j].To
			}
			if hs[i].ToKind != hs[j].ToKind {
				return hs[i].ToKind < hs[j].ToKind
			}
			return hs[i].Verb < hs[j].Verb
		})
		edges = append(edges, hs...)
	}
	return edges, delayed, errs
}
