package analyzer

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

// sdkPath is the import path of the user-facing SDK package.
const sdkPath = "github.com/gsandoval/tako"

// Config controls one analysis run.
type Config struct {
	// Dir is the app's module directory.
	Dir string
	// Env is extra environment for package loading (tests use GOWORK=off).
	Env []string
}

// Error is one analysis diagnostic. Analysis errors are product UX: their
// text is golden-tested and formatted path:line:col: message (tako/rule).
type Error struct {
	// Pos is the diagnostic position as path:line:col, relative to the
	// module directory.
	Pos string
	// Msg is the human message.
	Msg string
	// Rule is the lower-kebab rule ID within the tako/ namespace.
	Rule string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s (tako/%s)", e.Pos, e.Msg, e.Rule)
}

// ErrorList is every diagnostic from one run, in source order.
type ErrorList []*Error

func (l ErrorList) Error() string {
	msgs := make([]string, len(l))
	for i, e := range l {
		msgs[i] = e.Error()
	}
	return strings.Join(msgs, "\n")
}

// Analyze loads the module at cfg.Dir and produces its AppGraph. It
// returns an ErrorList when the app violates analysis rules, or another
// error when the module doesn't load (e.g. it doesn't compile).
func Analyze(cfg Config) (*AppGraph, error) {
	if abs, err := filepath.Abs(cfg.Dir); err == nil {
		cfg.Dir = abs
	}
	pkgs, err := load(cfg)
	if err != nil {
		return nil, err
	}

	graph := &AppGraph{
		Services:  []Service{},
		Resources: []Resource{},
		Handlers:  []Handler{},
		Edges:     []Edge{},
	}
	var errs ErrorList
	entities := map[string]*entityType{}    // payload type key → schema
	entityRes := map[string]*Resource{}     // payload type key → resource entry
	queueVars := map[types.Object]string{}  // queue var object → queue name
	workersByQueue := map[string]*Handler{} // queue name → its worker
	type varDecl struct {
		name *ast.Ident
		call *ast.CallExpr
		ctor string
	}
	for _, pkg := range pkgs {
		if pkg.Module != nil && graph.Module == "" {
			graph.Module = pkg.Module.Path
		}
		var decls []varDecl
		for _, file := range pkg.Syntax {
			for _, decl := range file.Decls {
				gen, ok := decl.(*ast.GenDecl)
				if !ok || gen.Tok != token.VAR {
					continue
				}
				for _, spec := range gen.Specs {
					vs := spec.(*ast.ValueSpec)
					for i, name := range vs.Names {
						if i >= len(vs.Values) {
							continue
						}
						if call, ok := vs.Values[i].(*ast.CallExpr); ok {
							decls = append(decls, varDecl{name: name, call: call, ctor: sdkConstructor(pkg, call)})
						}
					}
				}
			}
		}
		// Resources first, so handler bindings resolve regardless of
		// declaration order within the package.
		for _, d := range decls {
			switch d.ctor {
			case "Entity":
				if r, key, et := extractEntity(cfg.Dir, pkg, d.name, d.call, &errs); r != nil {
					graph.Resources = append(graph.Resources, *r)
					entities[key] = et
					entityRes[key] = &graph.Resources[len(graph.Resources)-1]
				}
			case "Bucket":
				if r := extractBucket(cfg.Dir, pkg, d.name, d.call, &errs); r != nil {
					graph.Resources = append(graph.Resources, *r)
				}
			case "Queue":
				if r := extractQueue(cfg.Dir, pkg, d.name, d.call, &errs); r != nil {
					graph.Resources = append(graph.Resources, *r)
					queueVars[pkg.TypesInfo.Defs[d.name]] = r.Name
				}
			}
		}
		for _, d := range decls {
			switch d.ctor {
			case "API":
				if h := extractAPI(cfg.Dir, pkg, d.name, d.call, &errs); h != nil {
					graph.Handlers = append(graph.Handlers, *h)
				}
			case "Worker":
				if h := extractWorker(cfg.Dir, pkg, d.name, d.call, queueVars, workersByQueue, &errs); h != nil {
					graph.Handlers = append(graph.Handlers, *h)
				}
			}
		}
		checkPackageLevel(cfg.Dir, pkg, &errs)
	}
	for _, pkg := range pkgs {
		scanQueries(cfg.Dir, pkg, entities, &errs)
	}
	for key, et := range entities {
		res := entityRes[key]
		for _, f := range et.declared { // derived indexes, struct order
			if et.used[f] {
				res.Indexes = append(res.Indexes, f)
			}
		}
	}
	if len(errs) > 0 {
		sort.SliceStable(errs, func(i, j int) bool { return posLess(errs[i].Pos, errs[j].Pos) })
		return nil, errs
	}

	sort.SliceStable(graph.Handlers, func(i, j int) bool {
		return posLess(graph.Handlers[i].Pos, graph.Handlers[j].Pos)
	})
	sort.SliceStable(graph.Resources, func(i, j int) bool {
		return posLess(graph.Resources[i].Pos, graph.Resources[j].Pos)
	})
	graph.Services = partition(graph.Handlers)
	return graph, nil
}

// sdkConstructor returns the SDK constructor name a call invokes ("API",
// "Worker", ...), or "" when the call is not into the SDK package.
func sdkConstructor(pkg *packages.Package, call *ast.CallExpr) string {
	fun := call.Fun
	switch f := fun.(type) { // unwrap explicit instantiation tako.API[Req, Res](...)
	case *ast.IndexExpr:
		fun = f.X
	case *ast.IndexListExpr:
		fun = f.X
	}
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok {
		return ""
	}
	pn, ok := pkg.TypesInfo.Uses[id].(*types.PkgName)
	if !ok || pn.Imported().Path() != sdkPath {
		return ""
	}
	return sel.Sel.Name
}

// constString returns the compile-time string value of expr.
func constString(pkg *packages.Package, expr ast.Expr) (string, error) {
	tv, ok := pkg.TypesInfo.Types[expr]
	if !ok || tv.Value == nil || tv.Value.Kind() != constant.String {
		return "", fmt.Errorf("not a string constant")
	}
	return constant.StringVal(tv.Value), nil
}

// describeExpr names a non-constant expression the way the error texts
// talk about it.
func describeExpr(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return fmt.Sprintf("variable %q", e.Name)
	case *ast.SelectorExpr:
		return fmt.Sprintf("variable %q", exprString(e))
	case *ast.CallExpr:
		return "a call expression"
	default:
		return "a non-constant expression"
	}
}

func exprString(e *ast.SelectorExpr) string {
	if x, ok := e.X.(*ast.Ident); ok {
		return x.Name + "." + e.Sel.Name
	}
	return e.Sel.Name
}

// partition derives services from handlers: package = service; API, Page,
// and Cron handlers deploy together as their package's service.
func partition(handlers []Handler) []Service {
	byService := map[string]*Service{}
	var order []string
	for _, h := range handlers {
		svc, ok := byService[h.Service]
		if !ok {
			svc = &Service{Name: h.Service, Package: h.Package, Profile: "default"}
			byService[h.Service] = svc
			order = append(order, h.Service)
		}
		svc.Handlers = append(svc.Handlers, h.Var)
	}
	services := make([]Service, 0, len(order))
	for _, name := range order {
		services = append(services, *byService[name])
	}
	return services
}

func load(cfg Config) ([]*packages.Package, error) {
	lc := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax |
			packages.NeedTypes | packages.NeedTypesInfo | packages.NeedModule |
			packages.NeedImports | packages.NeedDeps,
		Dir: cfg.Dir,
	}
	if len(cfg.Env) > 0 {
		lc.Env = append(os.Environ(), cfg.Env...)
	}
	pkgs, err := packages.Load(lc, "./...")
	if err != nil {
		return nil, fmt.Errorf("loading %s: %w", cfg.Dir, err)
	}
	var loadErrs []string
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		for _, e := range p.Errors {
			loadErrs = append(loadErrs, e.Error())
		}
	})
	if len(loadErrs) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(loadErrs, "\n"))
	}
	if len(pkgs) == 0 {
		return nil, fmt.Errorf("no Go packages found in %s", cfg.Dir)
	}
	return pkgs, nil
}

func relPos(dir string, fset *token.FileSet, pos token.Pos) string {
	p := fset.Position(pos)
	file := p.Filename
	if rel, err := filepath.Rel(dir, file); err == nil && !strings.HasPrefix(rel, "..") {
		file = rel
	}
	return fmt.Sprintf("%s:%d:%d", file, p.Line, p.Column)
}

// posLess orders path:line:col strings by path, then numeric line and
// column.
func posLess(a, b string) bool {
	af, al, ac := splitPos(a)
	bf, bl, bc := splitPos(b)
	if af != bf {
		return af < bf
	}
	if al != bl {
		return al < bl
	}
	return ac < bc
}

func splitPos(pos string) (file string, line, col int) {
	i := strings.LastIndex(pos, ":")
	j := strings.LastIndex(pos[:i], ":")
	fmt.Sscanf(pos[j+1:], "%d:%d", &line, &col)
	return pos[:j], line, col
}
