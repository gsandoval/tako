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
	tg, err := AnalyzeTyped(cfg)
	if err != nil {
		return nil, err
	}
	return tg.Graph, nil
}

// TypedGraph is Analyze's output plus the go/types view that generators
// beyond codegen need: clientgen walks handler Req/Res type TREES, which
// the JSON-shaped AppGraph deliberately does not carry.
type TypedGraph struct {
	// Graph is the ordinary analysis output.
	Graph *AppGraph
	// Funcs maps a handler's Var to its root function; the signature
	// yields the Req (param 1) and Res (result 0) types.
	Funcs map[string]*types.Func
	// Fset renders positions of objects reached through Funcs.
	Fset *token.FileSet
	// Dir is the analyzed module directory (positions render relative
	// to it).
	Dir string
}

// AnalyzeTyped is Analyze, keeping the typed view alive for generators
// that walk beyond the AppGraph.
func AnalyzeTyped(cfg Config) (*TypedGraph, error) {
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
	entities := map[string]*entityType{} // payload type key → schema
	// payload type key → index into graph.Resources (an index, not a
	// pointer: later appends may reallocate the slice).
	entityRes := map[string]int{}
	queueVars := map[types.Object]string{}    // queue var object → queue name
	topicVars := map[types.Object]string{}    // topic var object → topic name
	bucketVars := map[types.Object]string{}   // bucket var object → bucket name
	resourceVars := map[types.Object]resRef{} // grant-relevant vars for the edge scan
	endpointVars := map[types.Object]string{} // Endpoint vars (API/Page) → Var, for call edges
	channelVars := map[types.Object]string{}  // alert destination vars → Var
	handlerFuncs := map[string]*types.Func{}  // handler var → its root function
	siteOfPackage := map[string]string{}      // package → declaring site var (one per package)
	sitePackages := map[string]string{}       // package path → package name (service-from-site)
	serviceOfPackage := map[string]string{}   // package path → declaring var (one per package)
	serviceNames := map[string]string{}       // package path → declared app-service name
	workersByQueue := map[string]*Handler{}   // queue name → its worker
	jobVars := map[types.Object]string{}      // job var object → job name
	jobsByName := map[string]*Handler{}       // job name → its handler
	for _, pkg := range pkgs {
		if pkg.Module != nil && graph.Module == "" {
			graph.Module = pkg.Module.Path
		}
		decls := declsOf(pkg)
		// Resources first, so handler bindings resolve regardless of
		// declaration order within the package.
		for _, d := range decls {
			switch d.ctor {
			case "Entity":
				if r, key, et := extractEntity(cfg.Dir, pkg, d.name, d.call, &errs); r != nil {
					graph.Resources = append(graph.Resources, *r)
					entities[key] = et
					entityRes[key] = len(graph.Resources) - 1
					resourceVars[pkg.TypesInfo.Defs[d.name]] = resRef{r.Name, "entity"}
				}
			case "Bucket":
				if r := extractBucket(cfg.Dir, pkg, d.name, d.call, &errs); r != nil {
					graph.Resources = append(graph.Resources, *r)
					bucketVars[pkg.TypesInfo.Defs[d.name]] = r.Name
					resourceVars[pkg.TypesInfo.Defs[d.name]] = resRef{r.Name, "bucket"}
				}
			case "Queue":
				if r := extractQueue(cfg.Dir, pkg, d.name, d.call, &errs); r != nil {
					graph.Resources = append(graph.Resources, *r)
					queueVars[pkg.TypesInfo.Defs[d.name]] = r.Name
					resourceVars[pkg.TypesInfo.Defs[d.name]] = resRef{r.Name, "queue"}
				}
			case "Topic":
				if r := extractTopic(cfg.Dir, pkg, d.name, d.call, &errs); r != nil {
					graph.Resources = append(graph.Resources, *r)
					topicVars[pkg.TypesInfo.Defs[d.name]] = r.Name
					resourceVars[pkg.TypesInfo.Defs[d.name]] = resRef{r.Name, "topic"}
				}
			case "Cache", "Secret", "Counter", "Histogram":
				if r := extractCarrier(strings.ToLower(d.ctor), "tako."+d.ctor, cfg.Dir, pkg, d.name, d.call, &errs); r != nil {
					graph.Resources = append(graph.Resources, *r)
					// All four register, not just Secret. Counters and
					// histograms were left out, so a reading on one
					// resolved to no subject at all and would have
					// generated custom.googleapis.com/tako/ with an
					// empty metric name — a query matching nothing.
					// Nothing asserted the resolved subject, so it went
					// unseen through the whole of Y1.
					resourceVars[pkg.TypesInfo.Defs[d.name]] = resRef{r.Name, strings.ToLower(d.ctor)}
				}
			case "Site":
				if r := extractSite(cfg.Dir, pkg, d.name, d.call, &errs); r != nil {
					if prior, exists := siteOfPackage[pkg.PkgPath]; exists {
						errs = append(errs, &Error{
							Pos:  relPos(cfg.Dir, pkg.Fset, d.call.Pos()),
							Msg:  fmt.Sprintf("a package serves one site — %s is already declared; remove %s or merge the two", prior, r.Var),
							Rule: "one-site",
						})
						continue
					}
					siteOfPackage[pkg.PkgPath] = r.Var
					sitePackages[pkg.PkgPath] = pkg.Name
					graph.Resources = append(graph.Resources, *r)
				}
			case "Service":
				if r := extractService(cfg.Dir, pkg, d.name, d.call, &errs); r != nil {
					if prior, exists := serviceOfPackage[pkg.PkgPath]; exists {
						errs = append(errs, &Error{
							Pos:  relPos(cfg.Dir, pkg.Fset, d.call.Pos()),
							Msg:  fmt.Sprintf("a package declares one service — %s is already declared; remove %s or merge the two", prior, r.Var),
							Rule: "one-service",
						})
						continue
					}
					serviceOfPackage[pkg.PkgPath] = r.Var
					serviceNames[pkg.PkgPath] = pkg.Name + "-" + strings.ToLower(r.Name)
					graph.Resources = append(graph.Resources, *r)
				}
			case "Auth":
				if r := extractAuth(cfg.Dir, pkg, d.name, d.call, &errs); r != nil {
					graph.Resources = append(graph.Resources, *r)
				}
			case "Job":
				if r := extractCarrier("job", "tako.Job", cfg.Dir, pkg, d.name, d.call, &errs); r != nil {
					graph.Resources = append(graph.Resources, *r)
					jobVars[pkg.TypesInfo.Defs[d.name]] = r.Name
					resourceVars[pkg.TypesInfo.Defs[d.name]] = resRef{r.Name, "job"}
				}
			case "Lock":
				if r := extractLock(cfg.Dir, pkg, d.name, d.call, &errs); r != nil {
					graph.Resources = append(graph.Resources, *r)
					resourceVars[pkg.TypesInfo.Defs[d.name]] = resRef{r.Name, "lock"}
				}
			}
		}
		// Channels come after every resource, because an AlertTopic names
		// a declared topic and declaration order in the file is not the
		// author's problem: a channel written above its topic resolved to
		// nothing when this shared the loop above.
		//
		// The dispatch reads channelKinds rather than a case list, so
		// adding a channel cannot leave it uncollected — a hand-copied
		// list here is what made the renamed constructors silently stop
		// being collected.
		for _, d := range decls {
			if _, isChannel := channelKinds[d.ctor]; !isChannel {
				continue
			}
			if r := extractChannel(d.ctor, cfg.Dir, pkg, d.name, d.call, topicVars, &errs); r != nil {
				graph.Resources = append(graph.Resources, *r)
				channelVars[pkg.TypesInfo.Defs[d.name]] = r.Var
			}
		}
		for _, d := range decls {
			switch d.ctor {
			case "API":
				if h := extractAPI(cfg.Dir, pkg, d.name, d.call, &errs); h != nil {
					graph.Handlers = append(graph.Handlers, *h)
					handlerFuncs[h.Var] = funcArg(pkg, d.call, 1)
					endpointVars[pkg.TypesInfo.Defs[d.name]] = h.Var
				}
			case "Page":
				if h := extractPage(cfg.Dir, pkg, d.name, d.call, &errs); h != nil {
					graph.Handlers = append(graph.Handlers, *h)
					handlerFuncs[h.Var] = funcArg(pkg, d.call, 1)
					endpointVars[pkg.TypesInfo.Defs[d.name]] = h.Var
				}
			case "Worker":
				if h := extractWorker(cfg.Dir, pkg, d.name, d.call, queueVars, workersByQueue, &errs); h != nil {
					graph.Handlers = append(graph.Handlers, *h)
					handlerFuncs[h.Var] = funcArg(pkg, d.call, 1)
				}
			case "Subscribe":
				if h := extractSubscribe(cfg.Dir, pkg, d.name, d.call, topicVars, bucketVars, &errs); h != nil {
					graph.Handlers = append(graph.Handlers, *h)
					handlerFuncs[h.Var] = funcArg(pkg, d.call, 1)
				}
			case "Cron":
				if h := extractCron(cfg.Dir, pkg, d.name, d.call, &errs); h != nil {
					graph.Handlers = append(graph.Handlers, *h)
					handlerFuncs[h.Var] = funcArg(pkg, d.call, 1)
				}
			case "Runner":
				if h := extractRunner(cfg.Dir, pkg, d.name, d.call, jobVars, jobsByName, &errs); h != nil {
					graph.Handlers = append(graph.Handlers, *h)
					handlerFuncs[h.Var] = funcArg(pkg, d.call, 1)
				}
			case "Singleton":
				if h := extractSingleton(cfg.Dir, pkg, d.name, d.call, &errs); h != nil {
					graph.Handlers = append(graph.Handlers, *h)
					// Arg 0: Singleton takes only its loop now. Reading
					// arg 1 here silently produced NO resource edges,
					// which means no IAM grants — the singleton would
					// deploy and then be denied its own datastore.
					handlerFuncs[h.Var] = funcArg(pkg, d.call, 0)
				}
			}
		}
		checkPackageLevel(cfg.Dir, pkg, &errs)
	}
	// Alerts resolve LAST: a condition names handlers and routes, so
	// every handler must already be in the graph before a subject can be
	// checked against it.
	handlersByVar := map[string]*Handler{}
	for i := range graph.Handlers {
		handlersByVar[graph.Handlers[i].Var] = &graph.Handlers[i]
	}
	for _, pkg := range pkgs {
		for _, d := range declsOf(pkg) {
			if d.ctor != "Alert" {
				continue
			}
			if r := extractAlert(cfg.Dir, pkg, d.name, d.call, channelVars, handlersByVar, resourceVars, endpointVars, &errs); r != nil {
				graph.Resources = append(graph.Resources, *r)
			}
		}
		checkQuantiles(cfg.Dir, pkg, &errs)
	}
	for _, pkg := range pkgs {
		scanQueries(cfg.Dir, pkg, entities, &errs)
	}
	for key, et := range entities {
		res := &graph.Resources[entityRes[key]]
		fields := 0
		for _, f := range et.declared { // derived indexes, struct order
			if et.used[f] {
				res.Indexes = append(res.Indexes, f)
				fields++
			}
		}
		_ = fields
		// Composite indexes derive PER QUERY SHAPE (R0): one tuple per
		// function that queries this entity — filters in struct order,
		// then OrderBys in call order — deduped; single-field shapes
		// need no composite (built-in indexes serve them). Property
		// names are the MARSHALED names (json tag rule) — that is what
		// the wire and Datastore see, not the Go field names.
		seenTuple := map[string]bool{}
		for _, c := range et.chains {
			distinct := map[string]bool{}
			for f := range c.filters {
				distinct[f] = true
			}
			var tuple []string
			for _, f := range et.declared {
				if c.filters[f] {
					ordered := false
					for _, o := range c.orders {
						if o.field == f {
							ordered = true
						}
					}
					if !ordered {
						tuple = append(tuple, et.wire[f])
					}
				}
			}
			for _, o := range c.orders {
				distinct[o.field] = true
				prop := et.wire[o.field]
				if o.dir == "desc" {
					prop = "-" + prop
				}
				tuple = append(tuple, prop)
			}
			if len(distinct) < 2 {
				continue
			}
			key := strings.Join(tuple, ",")
			if !seenTuple[key] {
				seenTuple[key] = true
				res.Composites = append(res.Composites, tuple)
			}
		}
		sort.Slice(res.Composites, func(i, j int) bool {
			return strings.Join(res.Composites[i], ",") < strings.Join(res.Composites[j], ",")
		})
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
	// A declared tako.Service names the package's app service
	// <package>-<var> (V4) — the shape workers and subscribers already
	// use. Undeclared packages keep the bare package name: that IS the
	// honest name for a service nobody bothered to name. Renaming here,
	// before partition, is what makes one pass enough.
	renameAppServices(graph, serviceNames, sitePackages)
	graph.Services = partition(graph.Handlers)
	// A package with a Site but no handlers is still an app: the pure-
	// static site deploys as its package's service.
	for path, name := range sitePackages {
		found := false
		for _, svc := range graph.Services {
			if svc.Package == path && svc.Name == name {
				found = true
			}
		}
		if !found {
			graph.Services = append(graph.Services, Service{Name: name, Package: path, Profile: "default"})
		}
	}
	for _, r := range graph.Resources {
		if r.Kind == "job" && jobsByName[r.Name] == nil {
			errs = append(errs, &Error{
				Pos:  r.Pos,
				Msg:  fmt.Sprintf("job %q has no body — bind one with tako.Runner", r.Name),
				Rule: "job-binding",
			})
		}
	}
	// A service's ingress is one Cloud Run setting: a package cannot
	// half-close its front door.
	checkMixedIngress(graph.Handlers, &errs)
	// WithProfile is honored (R0): a service inherits its handlers'
	// claim; two different claims in one service cannot both win.
	applyProfiles(graph, &errs)
	// WithFootprint layers on the profile — projected the same way,
	// then the RESOLVED knobs are validated (runs after applyProfiles
	// because the base comes from the service's profile).
	applyFootprints(graph, &errs)
	applyTraffic(graph, &errs)
	// WithDomain is projected the same way (U1): declared on any one of
	// a service's handlers, or on its site, it addresses the whole
	// service — which is why a service that nothing public can reach
	// refuses the claim rather than provisioning a dead name.
	applyDomains(graph, &errs)
	var edgeErrs ErrorList
	var delayedQueues map[string]bool
	graph.Edges, delayedQueues, edgeErrs = deriveEdges(cfg.Dir, pkgs, resourceVars, endpointVars, handlerFuncs, graph.Handlers)
	for i := range graph.Resources {
		if graph.Resources[i].Kind == "queue" && delayedQueues[graph.Resources[i].Name] {
			graph.Resources[i].Delayed = true
		}
	}
	errs = append(errs, edgeErrs...)
	// Call CYCLES need no rule: a service cycle is an import cycle
	// (calling b.Step requires importing b; package = service), which
	// Go itself refuses before analysis ever runs.
	if len(errs) > 0 {
		sort.SliceStable(errs, func(i, j int) bool { return posLess(errs[i].Pos, errs[j].Pos) })
		return nil, errs
	}
	// go/packages shares one FileSet across the load when none is given.
	return &TypedGraph{Graph: graph, Funcs: handlerFuncs, Fset: pkgs[0].Fset, Dir: cfg.Dir}, nil
}

// checkMixedIngress rejects a service mixing WithPrivate and public
// routes: Cloud Run enforces ingress per SERVICE, so a private route in
// a public service would be publicly reachable — silently.
func checkMixedIngress(handlers []Handler, errs *ErrorList) {
	private := map[string]*Handler{}
	public := map[string]bool{}
	for i, h := range handlers {
		if h.Kind != "api" && h.Kind != "page" {
			continue
		}
		if h.Private {
			private[h.Service] = &handlers[i]
		} else {
			public[h.Service] = true
		}
	}
	for svc, h := range private {
		if public[svc] {
			*errs = append(*errs, &Error{
				Pos:  h.Pos,
				Msg:  fmt.Sprintf("service %s mixes private and public routes — Cloud Run enforces Route.Private at the service level, so private endpoints need their own package", svc),
				Rule: "mixed-ingress",
			})
		}
	}
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
		svc := *byService[name]
		// A deployable whose one handler is a Runner provisions as
		// a Cloud Run JOB; a Singleton's provisions min=1/max=1.
		// Their profiles are DEFAULTS here rather than overwrites at
		// provision time, so WithProfile can still move them (V2).
		kinds := map[string]bool{}
		for _, h := range handlers {
			if h.Service == svc.Name {
				kinds[h.Kind] = true
			}
		}
		svc.Job = len(kinds) == 1 && kinds["job"]
		svc.Singleton = len(kinds) == 1 && kinds["singleton"]
		switch {
		case svc.Job:
			svc.Profile = "batch"
		case svc.Singleton:
			svc.Profile = "singleton"
		}
		services = append(services, svc)
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

// applyProfiles projects handler WithProfile claims onto services and
// rejects conflicts (one service, one scaling profile).
func applyProfiles(graph *AppGraph, errs *ErrorList) {
	for i := range graph.Services {
		svc := &graph.Services[i]
		claim, claimedBy := "", ""
		for _, h := range graph.Handlers {
			if h.Service != svc.Name || h.Profile == "" {
				continue
			}
			if claim != "" && claim != h.Profile {
				*errs = append(*errs, &Error{
					Pos:  h.Pos,
					Msg:  fmt.Sprintf("service %s cannot be both %s (%s) and %s (%s) — one service scales one way; split the packages or align the profiles", svc.Name, claim, claimedBy, h.Profile, ShortVar(h)),
					Rule: "mixed-profile",
				})
				continue
			}
			claim, claimedBy = h.Profile, ShortVar(h)
		}
		for _, r := range graph.Resources {
			if r.Kind != "service" || r.Package != svc.Package || r.Profile == "" {
				continue
			}
			if claim != "" && claim != r.Profile {
				*errs = append(*errs, &Error{
					Pos:  r.Pos,
					Msg:  fmt.Sprintf("service %s cannot be both %s (%s) and %s (%s) — one service scales one way; split the packages or align the profiles", svc.Name, claim, claimedBy, r.Profile, r.Var),
					Rule: "mixed-profile",
				})
				continue
			}
			if isAppService(graph, *svc) {
				claim = r.Profile
			}
		}
		if claim != "" {
			svc.Profile = claim
		}
	}
}

// varDecl is one package-level var initialised by a constructor call.
type varDecl struct {
	name *ast.Ident
	call *ast.CallExpr
	ctor string
}

// declsOf lists every package-level var initialised by a call, with the
// SDK constructor it names. Alerts need a second walk over these after
// handlers are in the graph, so the scan is a function rather than a
// loop body.
func declsOf(pkg *packages.Package) []varDecl {
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
	return decls
}

// checkQuantiles validates every quantile argument in the package,
// wherever it appears: RequestsSlow's third argument, and the argument
// to a histogram's Percentile method.
//
// Percentile is a METHOD now, so it is matched on the selector rather
// than through sdkConstructor — which only ever resolves a call on the
// tako package itself and would silently stop checking.
func checkQuantiles(dir string, pkg *packages.Package, errs *ErrorList) {
	for _, file := range pkg.Syntax {
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if sdkConstructor(pkg, call) == "RequestsSlow" && len(call.Args) >= 3 {
				checkQuantile(dir, pkg, call.Args[2], "RequestsSlow", errs)
				return true
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Percentile" && len(call.Args) == 1 {
				if sig, ok := pkg.TypesInfo.Uses[sel.Sel].(*types.Func); ok && isSDKMethod(sig) {
					checkQuantile(dir, pkg, call.Args[0], "Percentile", errs)
				}
			}
			return true
		})
	}
}

// isSDKMethod reports whether a method belongs to the tako package, so a
// user's own Percentile(0.5) is never mistaken for a reading.
func isSDKMethod(fn *types.Func) bool {
	return fn.Pkg() != nil && fn.Pkg().Path() == sdkPath
}
