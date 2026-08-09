package analyzer

// Route.Allow — authorization the analyzer can see.
//
// Before it, an app's authorization lived in handler bodies: 49 copies of
// the same four lines in the app that drove this, and no way for any tool
// to answer "which of my routes can a stranger reach". A policy is a
// declared var, so the graph carries it per route and the generated
// service can name it.

import (
	"fmt"
	"go/ast"
	"go/types"
	"strings"

	"golang.org/x/tools/go/packages"
)

// extractPolicy validates one tako.Policy declaration. Like
// tako.StatusPage it carries no configuration — the declaration IS the
// function — so what is checked is the shape: an exported package-level
// var, because the generated service names it from outside the package.
func extractPolicy(dir string, pkg *packages.Package, name *ast.Ident, errs *ErrorList) bool {
	if name.Name == "_" {
		return false
	}
	if !name.IsExported() {
		*errs = append(*errs, &Error{
			Pos: relPos(dir, pkg.Fset, name.Pos()),
			Msg: fmt.Sprintf("policy %s must be exported — the generated service names it from outside the package; rename it %s",
				name.Name, strings.ToUpper(name.Name[:1])+name.Name[1:]),
			Rule: "unexported-policy",
		})
	}
	// Recorded either way, deliberately. An unexported policy has ALREADY
	// been named accurately above; letting the routes that reference it
	// resolve keeps them from each adding a second, misleading "not a
	// declared var" on top of it. Analysis fails on the first error, so
	// nothing generates against the name.
	return true
}

// resolvePolicy maps a Route.Allow expression back to the declared policy
// var it references, and refuses everything else.
//
// A function literal type-checks perfectly well and is refused anyway: a
// door with no name cannot be printed by `tako gen graph`, cannot be
// shared by two routes, and cannot reach a status page as
// Status.Policy — which is the field that tells an app's own refusal
// from a stale CSRF token's.
func resolvePolicy(dir string, pkg *packages.Package, route string, arg ast.Expr,
	policyVars map[types.Object]string, errs *ErrorList) (string, bool) {

	var obj types.Object
	switch e := arg.(type) {
	case *ast.Ident:
		obj = pkg.TypesInfo.Uses[e]
	case *ast.SelectorExpr:
		obj = pkg.TypesInfo.Uses[e.Sel]
	}
	if obj != nil {
		if name, ok := policyVars[obj]; ok {
			return name, true
		}
	}
	*errs = append(*errs, &Error{
		Pos: relPos(dir, pkg.Fset, arg.Pos()),
		Msg: fmt.Sprintf("%s names a policy that is not a declared var in this package — write `var Editors = tako.Policy(editorAllowed)` and set Allow: Editors; %s cannot be named by the generated service, printed by `tako gen graph`, or shared by a second route",
			route, describeExpr(arg)),
		Rule: "policy-var",
	})
	return "", false
}

// --- roles (AB10a) ------------------------------------------------------

// extractRole validates one tako.Role declaration and returns the role
// name. Two rules, and both are about a name that has to survive leaving
// the package: it is read at analysis time (so it can be printed beside
// the door in the graph and handed to the dev sign-in page), and the
// generated service references the var.
func extractRole(dir string, pkg *packages.Package, name *ast.Ident, call *ast.CallExpr, errs *ErrorList) (string, bool) {
	if name.Name == "_" || len(call.Args) < 1 {
		return "", false
	}
	role, err := constString(pkg, call.Args[0])
	if err != nil {
		*errs = append(*errs, &Error{
			Pos: relPos(dir, pkg.Fset, call.Args[0].Pos()),
			Msg: fmt.Sprintf("tako.Role takes a string constant — the role name is read at analysis time so a route's door can be named in the graph; got %s",
				describeExpr(call.Args[0])),
			Rule: "const-role",
		})
		return "", false
	}
	if !name.IsExported() {
		*errs = append(*errs, &Error{
			Pos: relPos(dir, pkg.Fset, name.Pos()),
			Msg: fmt.Sprintf("role %s must be exported — the generated service names it from outside the package; rename it %s",
				name.Name, strings.ToUpper(name.Name[:1])+name.Name[1:]),
			Rule: "unexported-role",
		})
	}
	return role, true
}

// resolveRequireRole reads the roles out of a `tako.RequireRole(Editor,
// Moderator)` declaration, so the graph can print them beside the policy
// that admits them.
//
// A RequireRole with no roles is refused: it is a door with no key, which
// admits nobody and looks exactly like a rule that is merely strict —
// nothing fails, nobody gets in, and the log says the request was refused
// as designed.
func resolveRequireRole(dir string, pkg *packages.Package, varName string, call *ast.CallExpr,
	roleVars map[types.Object]string, errs *ErrorList) []string {

	if len(call.Args) == 0 {
		*errs = append(*errs, &Error{
			Pos: relPos(dir, pkg.Fset, call.Pos()),
			Msg: fmt.Sprintf("%s = tako.RequireRole() needs at least one role — a door with no key refuses everyone, and does it silently",
				varName),
			Rule: "empty-role",
		})
		return nil
	}
	var out []string
	for _, arg := range call.Args {
		var obj types.Object
		switch e := arg.(type) {
		case *ast.Ident:
			obj = pkg.TypesInfo.Uses[e]
		case *ast.SelectorExpr:
			obj = pkg.TypesInfo.Uses[e.Sel]
		}
		role, ok := roleVars[obj]
		if !ok {
			*errs = append(*errs, &Error{
				Pos: relPos(dir, pkg.Fset, arg.Pos()),
				Msg: fmt.Sprintf("%s names a role that is not a declared var in this package — write `var Editor = tako.Role(\"editor\")` and pass Editor; %s cannot be printed beside the door or offered by the dev sign-in page",
					varName, describeExpr(arg)),
				Rule: "role-var",
			})
			continue
		}
		out = append(out, role)
	}
	return out
}
