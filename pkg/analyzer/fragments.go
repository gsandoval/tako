package analyzer

// Query fragments (FL10).
//
// A query is assembled by calling methods on an *EntityQuery, and Go
// programmers factor that assembly into helpers — which until FL10
// broke derivation in both directions. The caller's chain stopped at
// the call, so the shape that actually ran had no index (RC1's
// production 500, one call frame away); and the helper formed a chain
// of its own, so tako derived an index for a query that never runs.
//
// The saving grace is that a query value has a KNOWN TYPE. A function
// with an *EntityQuery[T] parameter or result is a FRAGMENT of a
// query, not a query, and go/types finds every one of them without any
// dataflow analysis. Its modifiers belong to whoever calls it, at the
// position it is called, carrying the caller's branch context.
//
// What tako cannot follow — a query stored in a struct field, handed
// to a function value, sent on a channel — is refused rather than
// silently under-derived, because under-deriving is the failure that
// only shows up in production.

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/packages"
)

// queryScan collects one package's query modifiers, resolving fragment
// calls as it goes.
type queryScan struct {
	dir      string
	pkg      *packages.Package
	entities map[string]*entityType
	errs     *ErrorList
	// fragments maps a function object to its declaration, for every
	// function in this package that takes or returns a query.
	fragments map[*types.Func]*ast.FuncDecl
	// mods memoizes each declaration's own modifiers, so a fragment
	// called five times is analyzed once and its errors reported once.
	mods map[*ast.FuncDecl]map[*entityType][]queryMod
	// busy is the cycle guard: a fragment that (indirectly) calls
	// itself has no derivable shape.
	busy map[*ast.FuncDecl]bool
}

// newQueryScan indexes the package's fragments before anything is
// collected — a fragment may be declared after its caller.
func newQueryScan(dir string, pkg *packages.Package, entities map[string]*entityType, errs *ErrorList) *queryScan {
	s := &queryScan{
		dir: dir, pkg: pkg, entities: entities, errs: errs,
		fragments: map[*types.Func]*ast.FuncDecl{},
		mods:      map[*ast.FuncDecl]map[*entityType][]queryMod{},
		busy:      map[*ast.FuncDecl]bool{},
	}
	for _, file := range pkg.Syntax {
		for _, decl := range file.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			if fn, ok := pkg.TypesInfo.Defs[fd.Name].(*types.Func); ok && isFragment(fn) {
				s.fragments[fn] = fd
			}
		}
	}
	return s
}

// isFragment reports whether a function takes or returns an
// *EntityQuery — the signature that makes it part of someone else's
// query rather than a query of its own.
func isFragment(fn *types.Func) bool {
	sig, ok := fn.Type().(*types.Signature)
	if !ok {
		return false
	}
	for _, tup := range []*types.Tuple{sig.Params(), sig.Results()} {
		for i := 0; i < tup.Len(); i++ {
			if _, ok := queryPayload(tup.At(i).Type()); ok {
				return true
			}
		}
	}
	return false
}

// queryPayload reports the payload type of an *EntityQuery[T], and
// whether the type is one at all.
func queryPayload(t types.Type) (string, bool) {
	ptr, ok := t.(*types.Pointer)
	if !ok {
		return "", false
	}
	named, ok := ptr.Elem().(*types.Named)
	if !ok || named.Obj().Name() != "EntityQuery" ||
		named.Obj().Pkg() == nil || named.Obj().Pkg().Path() != sdkPath ||
		named.TypeArgs().Len() != 1 {
		return "", false
	}
	return named.TypeArgs().At(0).String(), true
}

// fragmentCall resolves a call to a fragment declared in this package,
// or nil for anything else.
func (s *queryScan) fragmentCall(call *ast.CallExpr) *ast.FuncDecl {
	var id *ast.Ident
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		id = fun
	case *ast.SelectorExpr:
		id = fun.Sel // a method, or a qualified name
	default:
		return nil
	}
	fn, ok := s.pkg.TypesInfo.Uses[id].(*types.Func)
	if !ok {
		return nil
	}
	return s.fragments[fn]
}

// escapes reports the query values in this node that tako cannot
// follow, and refuses them. Under-deriving is the failure that reaches
// production, so an unfollowable query is an error rather than a
// best-effort guess.
func (s *queryScan) escapes(n ast.Node) {
	switch v := n.(type) {
	case *ast.CallExpr:
		if s.fragmentCall(v) != nil {
			return // followed
		}
		for _, arg := range v.Args {
			if payload, ok := s.queryTypeOf(arg); ok {
				s.refuse(arg.Pos(), payload,
					"is passed to something tako cannot resolve statically (a function value, a method on an interface, or another package)",
					"pass it to a helper in this package that takes and returns")
			}
		}
	case *ast.AssignStmt:
		for i, rhs := range v.Rhs {
			payload, ok := s.queryTypeOf(rhs)
			if !ok || i >= len(v.Lhs) {
				continue
			}
			if where, escaped := s.storedIn(v.Lhs[i]); escaped {
				s.refuse(v.Lhs[i].Pos(), payload, "is stored in "+where,
					"assemble it in one function, or in helpers that take and return")
			}
		}
	case *ast.SendStmt:
		if payload, ok := s.queryTypeOf(v.Value); ok {
			s.refuse(v.Value.Pos(), payload, "is sent on a channel",
				"assemble it in one function, or in helpers that take and return")
		}
	case *ast.CompositeLit:
		for _, elt := range v.Elts {
			e := elt
			if kv, ok := elt.(*ast.KeyValueExpr); ok {
				e = kv.Value
			}
			if payload, ok := s.queryTypeOf(e); ok {
				s.refuse(e.Pos(), payload, "is stored in a composite literal",
					"assemble it in one function, or in helpers that take and return")
			}
		}
	}
}

// storedIn names the destination when an assignment puts a query
// somewhere tako cannot follow, and reports whether it did. A plain
// local variable is followable and is the normal case.
func (s *queryScan) storedIn(lhs ast.Expr) (string, bool) {
	switch v := lhs.(type) {
	case *ast.Ident:
		if v.Name == "_" {
			return "", false
		}
		obj := s.pkg.TypesInfo.ObjectOf(v)
		if pv, ok := obj.(*types.Var); ok && pv.Parent() == s.pkg.Types.Scope() {
			return "a package-level variable", true
		}
		return "", false
	case *ast.SelectorExpr:
		return "a struct field", true
	case *ast.IndexExpr:
		return "a map or slice element", true
	case *ast.StarExpr:
		return "a pointer's target", true
	}
	return "", false
}

// queryTypeOf reports the payload of an expression whose type is a
// query, and whether it is one.
func (s *queryScan) queryTypeOf(e ast.Expr) (string, bool) {
	tv, ok := s.pkg.TypesInfo.Types[e]
	if !ok {
		return "", false
	}
	return queryPayload(tv.Type)
}

// refuse emits the one escape error, in three wordings.
func (s *queryScan) refuse(pos token.Pos, payload, what, remedy string) {
	et := s.entities[payload]
	display := payload
	if et != nil {
		display = et.display
	}
	*s.errs = append(*s.errs, &Error{
		Pos: relPos(s.dir, s.pkg.Fset, pos),
		Msg: fmt.Sprintf("this query %s, so the composite indexes it needs cannot be derived — %s *tako.EntityQuery[%s]",
			what, remedy, display),
		Rule: "query-escapes",
	})
}
