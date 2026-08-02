package analyzer

// Reading declaration configuration.
//
// Every constructor takes at most one config struct — tako.Route,
// tako.Compute, tako.CronConfig and friends (X2). That replaced a flat
// Option namespace where the analyzer had to say which options each
// constructor accepted; the compiler says it now, so the two rules that
// existed for it (invalid-option, service-option) are gone.
//
// What survives here is the part a type cannot check: the value must be
// written AS A LITERAL, because configuration is read from source at
// analysis time. A config held in a variable would compile and then be
// invisible to the deploy.

import (
	"fmt"
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/packages"
)

// configFields returns the named fields of one config-struct argument.
// A missing argument is the zero config, which is legal everywhere and
// returns a nil map — callers read fields with fieldOf, which handles it.
func configFields(dir string, pkg *packages.Package, args []ast.Expr, typeName string, errs *ErrorList) (map[string]ast.Expr, bool) {
	if len(args) == 0 {
		return nil, true
	}
	if len(args) > 1 {
		*errs = append(*errs, &Error{
			Pos: relPos(dir, pkg.Fset, args[1].Pos()),
			Msg: fmt.Sprintf("tako.%s takes one configuration value, got %d — merge them into a single literal",
				typeName, len(args)),
			Rule: "one-config",
		})
		return nil, false
	}
	return configLit(dir, pkg, args[0], typeName, errs)
}

// configLit reads one config literal in place — used for nested structs
// (Footprint inside CronConfig) as well as top-level arguments.
func configLit(dir string, pkg *packages.Package, arg ast.Expr, typeName string, errs *ErrorList) (map[string]ast.Expr, bool) {
	fail := func(pos ast.Node, msg string) {
		*errs = append(*errs, &Error{Pos: relPos(dir, pkg.Fset, pos.Pos()), Msg: msg, Rule: "config-literal"})
	}
	lit, ok := arg.(*ast.CompositeLit)
	if !ok {
		fail(arg, fmt.Sprintf("tako.%s must be written as a literal with constant fields — configuration is read at analysis time, not run time; got %s",
			typeName, describeExpr(arg)))
		return nil, false
	}
	fields := map[string]ast.Expr{}
	for _, el := range lit.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			fail(el, fmt.Sprintf("tako.%s fields must be named — positional literals hide which knob is which", typeName))
			return nil, false
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			fail(kv, fmt.Sprintf("tako.%s fields must be named", typeName))
			return nil, false
		}
		fields[key.Name] = kv.Value
	}
	return fields, true
}

// fieldOf returns one field's expression, if it was set.
func fieldOf(fields map[string]ast.Expr, name string) (ast.Expr, bool) {
	if fields == nil {
		return nil, false
	}
	e, ok := fields[name]
	return e, ok
}

// boolField reads a bool field. Only a literal true counts as set —
// the value is read at analysis time, so it cannot be computed.
func boolField(dir string, pkg *packages.Package, fields map[string]ast.Expr, name, typeName string, errs *ErrorList) bool {
	e, ok := fieldOf(fields, name)
	if !ok {
		return false
	}
	id, isIdent := e.(*ast.Ident)
	if isIdent && (id.Name == "true" || id.Name == "false") {
		return id.Name == "true"
	}
	*errs = append(*errs, &Error{
		Pos: relPos(dir, pkg.Fset, e.Pos()),
		Msg: fmt.Sprintf("tako.%s.%s must be a literal true or false — it is read at analysis time, not run time; got %s",
			typeName, name, describeExpr(e)),
		Rule: "config-literal",
	})
	return false
}

// stringField reads a constant string field.
func stringField(dir string, pkg *packages.Package, fields map[string]ast.Expr, name, typeName string, errs *ErrorList) (string, bool) {
	e, ok := fieldOf(fields, name)
	if !ok {
		return "", false
	}
	s, err := constString(pkg, e)
	if err != nil {
		*errs = append(*errs, &Error{
			Pos: relPos(dir, pkg.Fset, e.Pos()),
			Msg: fmt.Sprintf("tako.%s.%s must be a string constant — it is read at analysis time, not run time; got %s",
				typeName, name, describeExpr(e)),
			Rule: "config-literal",
		})
		return "", false
	}
	return s, true
}

// nestedLit reads a struct-valued field (Footprint, Traffic, Build).
func nestedLit(dir string, pkg *packages.Package, fields map[string]ast.Expr, name, typeName string, errs *ErrorList) (map[string]ast.Expr, bool) {
	e, ok := fieldOf(fields, name)
	if !ok {
		return nil, false
	}
	got, ok := configLit(dir, pkg, e, typeName, errs)
	return got, ok
}

// sdkConfigType reports the tako type a composite literal constructs,
// for call sites that must tell one config apart from another.
func sdkConfigType(pkg *packages.Package, arg ast.Expr) string {
	lit, ok := arg.(*ast.CompositeLit)
	if !ok {
		return ""
	}
	tv, ok := pkg.TypesInfo.Types[lit]
	if !ok {
		return ""
	}
	named, ok := tv.Type.(*types.Named)
	if !ok {
		return ""
	}
	obj := named.Obj()
	if obj.Pkg() == nil || obj.Pkg().Path() != sdkPath {
		return ""
	}
	return obj.Name()
}
