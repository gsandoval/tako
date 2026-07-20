package analyzer

import (
	"fmt"
	"go/ast"
	"go/types"
	"strings"

	"golang.org/x/tools/go/packages"
)

// workerOptions is the option set tako.Worker accepts.
var workerOptions = map[string]bool{"WithProfile": true}

// extractWorker validates one tako.Worker declaration. Workers partition
// into their own service (package = service, but each worker scales and
// grants independently).
func extractWorker(dir string, pkg *packages.Package, name *ast.Ident, call *ast.CallExpr, queueVars map[types.Object]string, workersByQueue map[string]*Handler, errs *ErrorList) *Handler {
	if name.Name == "_" || len(call.Args) < 2 {
		return nil
	}
	bad := false
	fail := func(e *Error) { *errs = append(*errs, e); bad = true }

	queueName, ok := resolveQueueVar(pkg, call.Args[0], queueVars)
	if !ok {
		fail(&Error{
			Pos:  relPos(dir, pkg.Fset, call.Args[0].Pos()),
			Msg:  "tako.Worker must bind a queue declared as a package-level var in this module",
			Rule: "worker-binding",
		})
		return nil
	}

	for _, opt := range call.Args[2:] {
		if optName, isOpt := sdkOptionCall(pkg, opt); isOpt && !workerOptions[optName] {
			fail(&Error{
				Pos: relPos(dir, pkg.Fset, opt.Pos()),
				Msg: fmt.Sprintf("tako.%s is not a tako.Worker option (tako.Worker accepts WithProfile)",
					optName),
				Rule: "invalid-option",
			})
		}
	}

	if prior := workersByQueue[queueName]; prior != nil {
		fail(&Error{
			Pos: relPos(dir, pkg.Fset, call.Args[0].Pos()),
			Msg: fmt.Sprintf("queue %q already has worker %s (%s) — a queue has exactly one worker; use a Topic for fan-out",
				queueName, ShortVar(*prior), ShortPos(prior.Pos)),
			Rule: "one-worker",
		})
	}
	if bad {
		return nil
	}

	h := &Handler{
		Kind:    "worker",
		Var:     pkg.Name + "." + name.Name,
		Package: pkg.PkgPath,
		Service: pkg.Name + "-" + strings.ToLower(name.Name),
		Binding: queueName,
		Pos:     relPos(dir, pkg.Fset, name.Pos()),
	}
	workersByQueue[queueName] = h
	return h
}

// resolveQueueVar maps a Worker's binding argument back to the declared
// queue var it references.
func resolveQueueVar(pkg *packages.Package, arg ast.Expr, queueVars map[types.Object]string) (string, bool) {
	var obj types.Object
	switch e := arg.(type) {
	case *ast.Ident:
		obj = pkg.TypesInfo.Uses[e]
	case *ast.SelectorExpr:
		obj = pkg.TypesInfo.Uses[e.Sel]
	default:
		return "", false
	}
	name, ok := queueVars[obj]
	return name, ok
}
