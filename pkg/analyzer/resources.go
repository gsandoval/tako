package analyzer

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"regexp"
	"sort"
	"strings"

	"github.com/gsandoval/tako/internal/entitytag"
	"golang.org/x/tools/go/packages"
)

// entityType is the analyzer's view of one entity payload struct: which
// fields are indexed (and may appear in Where/OrderBy) and which were
// actually used by queries — the derived index set.
type entityType struct {
	display     string            // short type name, e.g. "Link"
	declared    []string          // entity:"index" fields, struct order
	indexed     map[string]bool   // membership set of declared
	wire        map[string]string // Go field → marshaled property name (json tag rule)
	wireToField map[string]string // marshaled property name → Go field (when json tag differs)
	used        map[string]bool   // fields seen in Where/OrderBy call sites
	// chains are the query shapes derived per function (R0), one per
	// combination of branches that function can take (RC1). A function
	// whose filters are all unconditional contributes exactly one.
	//
	// The pre-RC1 rule was "one tuple per function", justified as
	// over-indexing a function with two separate queries but never
	// under-indexing one. That was false for a query assembled ACROSS a
	// branch: gallery's readyPhotos adds its owner filter inside an if,
	// which folded into the single tuple and left the without-owner
	// shape unindexed. It answered 500 in production — Datastore asked
	// for exactly the index nobody derived — while every local test
	// passed, because the local backend is bbolt and has no indexes to
	// be wrong about.
	chains []queryChain
}

// queryChain is one function's query shape on an entity.
type queryChain struct {
	filters map[string]bool
	orders  []orderBy // in call order
	// source is the query this shape came from, "app.go:385 readyPhotos".
	// It rides to the deploy plan as an annotation (RC1).
	source string
}

// queryMod is one Where or OrderBy call, in the order it appears, and
// whether a branch guards it. optional modifiers are what make a
// function produce more than one shape (RC1); dir is "" for a filter.
//
// pos is what "in the order it appears" MEANS, and it has to be
// recorded rather than inferred from traversal order: ast.Inspect
// visits a chained call outermost-first, so
// OrderBy("A").OrderBy("-B") is walked as [-B, A] and derived a
// composite whose sort suffix was backwards — an index Datastore will
// not use, which is the same failure as a missing one. Modifiers
// spliced in from a fragment carry the position of the CALL, so they
// land where the fragment was invoked (FL10).
type queryMod struct {
	field    string
	dir      string
	optional bool
	pos      token.Pos
}

type orderBy struct {
	field, dir string
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
	if !checkResourceName("entity", "tako.Entity", resName, dir, pkg, call.Args[0], errs) {
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
	if !checkResourceName("bucket", "tako.Bucket", resName, dir, pkg, call.Args[0], errs) {
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
		wireToField: map[string]string{}, used: map[string]bool{}}
	idCount := 0
	bad := false
	for i := range st.NumFields() {
		f := st.Field(i)
		if !entitytag.Omitted(st.Tag(i)) {
			if w := entitytag.Name(st.Tag(i), f.Name()); w != "" && w != f.Name() {
				et.wireToField[w] = f.Name()
			}
		}
		switch entitytag.Kind(st.Tag(i)) {
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
			// json:"-" keeps the field out of the document, so an index on
			// it matches nothing — silently. The SDK refuses this when it
			// reads the tags at first use; refused here too, because an
			// analyzer error names the file and line and arrives before a
			// deploy provisions a composite index on a property no
			// document has (item 43).
			if entitytag.Omitted(st.Tag(i)) {
				*errs = append(*errs, &Error{
					Pos: relPos(dir, pkg.Fset, f.Pos()),
					Msg: fmt.Sprintf("field %s is tagged entity:\"index\" but json:\"-\" keeps it "+
						"out of the stored document — an index on a field nothing writes matches "+
						"nothing, silently; drop one of the two tags", f.Name()),
					Rule: "entity-index",
				})
				bad = true
				continue
			}
			et.declared = append(et.declared, f.Name())
			et.indexed[f.Name()] = true
			// Datastore property names are the MARSHALED names, and which
			// name that is comes from internal/entitytag — the same
			// reading the SDK's write path uses, so the index this
			// provisions and the property the document carries cannot
			// drift apart (item 43).
			et.wire[f.Name()] = entitytag.Name(st.Tag(i), f.Name())
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
//
// Filters are classified by the block they sit in (RC1). One assembled
// across a branch produces MORE THAN ONE runtime shape:
//
//	q := photos.Query(ctx).Where("Ready", "=", true)
//	if owner != "" {
//	    q = q.Where("Owner", "=", owner)
//	}
//
// runs as {ready} for an anonymous caller and {ready, owner} otherwise,
// and Datastore needs an index for each — a composite on
// (owner, ready, -created) does not serve a query that never constrains
// owner. Folding both Where calls into one shape, which is what this
// did before RC1, left the narrower one unindexed and answered 500 in
// production while every local test passed.
// A modifier can also arrive from a FRAGMENT — a function taking or
// returning an *EntityQuery, whose modifiers belong to its callers
// (FL10). Fragments derive nothing themselves; a query tako cannot
// follow is refused.
func scanQueries(dir string, pkg *packages.Package, handlerLits []*ast.FuncDecl, entities map[string]*entityType, errs *ErrorList) {
	s := newQueryScan(dir, pkg, entities, errs)
	// Every declaration is collected, so its own errors are reported
	// whether or not anything calls it; only NON-fragments expand into
	// shapes.
	for _, file := range pkg.Syntax {
		for _, decl := range file.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			mods := s.modsOf(fd)
			if s.isFragmentDecl(fd) {
				continue
			}
			for et, m := range mods {
				expandShapes(dir, pkg, fd, et, m, errs)
			}
		}
	}
	for _, fd := range handlerLits {
		if fd.Body == nil {
			continue
		}
		mods := s.modsOf(fd)
		for et, m := range mods {
			expandShapes(dir, pkg, fd, et, m, errs)
		}
	}
}

// isFragmentDecl reports whether this declaration is a query fragment.
func (s *queryScan) isFragmentDecl(fd *ast.FuncDecl) bool {
	fn, ok := s.pkg.TypesInfo.Defs[fd.Name].(*types.Func)
	return ok && s.fragments[fn] != nil
}

// modsOf collects one declaration's query modifiers per entity, in the
// order they run: its own Where/OrderBy calls, plus the modifiers of
// every fragment it calls, spliced in at the call site. Memoized, so a
// fragment called five times is analyzed once and reports its errors
// once.
func (s *queryScan) modsOf(fd *ast.FuncDecl) map[*entityType][]queryMod {
	if done, ok := s.mods[fd]; ok {
		return done
	}
	if s.busy[fd] {
		// A fragment that (indirectly) calls itself builds a query of
		// unbounded shape; there is nothing to derive.
		return nil
	}
	s.busy[fd] = true
	defer func() { s.busy[fd] = false }()

	dir, pkg, entities, errs := s.dir, s.pkg, s.entities, s.errs
	fnMods := map[*entityType]*[]queryMod{}
	modsFor := func(et *entityType) *[]queryMod {
		if fnMods[et] == nil {
			fnMods[et] = &[]queryMod{}
		}
		return fnMods[et]
	}
	{
		// ast.Inspect visits a node, then its children, then nil —
		// so pushing on entry and popping on nil keeps an accurate
		// ancestor stack, which is what tells a guarded call from an
		// unconditional one.
		var stack []ast.Node
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			if n == nil {
				stack = stack[:len(stack)-1]
				return true
			}
			stack = append(stack, n)
			s.escapes(n)
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			// A call to a fragment contributes the fragment's own
			// modifiers, at this position, under this branch context.
			if frag := s.fragmentCall(call); frag != nil && frag != fd {
				for et, fm := range s.modsOf(frag) {
					dst := modsFor(et)
					for _, m := range fm {
						m.pos = call.Rparen
						m.optional = m.optional || guarded(stack)
						*dst = append(*dst, m)
					}
				}
				return true
			}
			if len(call.Args) < 1 {
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
				msg := fmt.Sprintf("query %s %s.%s, but %s is not indexed — tag it entity:\"index\" to make it %s",
					verb, et.display, field, field, suffix)
				if goField, ok := et.wireToField[field]; ok {
					msg = fmt.Sprintf("query %s %q (the json tag of %s.%s) — Where and OrderBy take the Go struct field name %q, not the json tag",
						verb, field, et.display, goField, goField)
				}
				*errs = append(*errs, &Error{
					Pos:  relPos(dir, pkg.Fset, fieldArg.Pos()),
					Msg:  msg,
					Rule: "unindexed-query",
				})
				return true
			}
			et.used[field] = true
			mods := modsFor(et)
			*mods = append(*mods, queryMod{
				field:    field,
				dir:      direction,
				optional: guarded(stack),
				pos:      fieldArg.Pos(),
			})
			return true
		})
	}
	out := map[*entityType][]queryMod{}
	for et, mods := range fnMods {
		// Source order, not traversal order. Stable, so modifiers
		// spliced from one fragment call keep their order among
		// themselves.
		m := *mods
		sort.SliceStable(m, func(i, j int) bool { return m[i].pos < m[j].pos })
		out[et] = m
	}
	s.mods[fd] = out
	return out
}

// branchStmts are the statements whose bodies a query modifier can hide
// inside. A modifier under any of them may or may not run, which is the
// whole of RC1.
func guarded(stack []ast.Node) bool {
	for _, n := range stack {
		switch n.(type) {
		case *ast.IfStmt, *ast.ForStmt, *ast.RangeStmt, *ast.SwitchStmt,
			*ast.TypeSwitchStmt, *ast.SelectStmt, *ast.CaseClause, *ast.CommClause:
			return true
		}
	}
	return false
}

// maxOptionalMods caps the powerset. k optional modifiers in one
// function are 2^k shapes and so up to 2^k composite indexes; Datastore
// allows 200 per project and each takes minutes to build. Real code sits
// at one to three. Past this tako refuses at analysis time rather than
// deriving an index set nobody can deploy.
const maxOptionalMods = 4

// expandShapes turns one function's modifiers into one chain per
// runtime shape: the mandatory ones, plus every subset of the guarded
// ones. Call order is preserved within each shape, which is what makes
// the OrderBy suffix of a tuple correct.
func expandShapes(dir string, pkg *packages.Package, fd *ast.FuncDecl, et *entityType, mods []queryMod, errs *ErrorList) {
	var optional []int
	for i, m := range mods {
		if m.optional {
			optional = append(optional, i)
		}
	}
	if len(optional) > maxOptionalMods {
		*errs = append(*errs, &Error{
			Pos: relPos(dir, pkg.Fset, fd.Name.Pos()),
			Msg: fmt.Sprintf("%s assembles one query from %d conditional filters — that is %d composite indexes for a single function, and Datastore allows 200 in a project; split it into separate queries whose filters are unconditional",
				fd.Name.Name, len(optional), 1<<len(optional)),
			Rule: "query-shapes",
		})
		return
	}
	// "app.go:385 readyPhotos" — the column is dropped: this points a
	// reader at a query, it is not a compiler diagnostic to jump to.
	pos := relPos(dir, pkg.Fset, fd.Name.Pos())
	if i := strings.LastIndex(pos, ":"); i >= 0 {
		pos = pos[:i]
	}
	shapes := shapesOf(mods)
	for i := range shapes {
		shapes[i].source = pos + " " + fd.Name.Name
	}
	et.chains = append(et.chains, shapes...)
}

// shapesOf enumerates the runtime shapes of one function's modifiers:
// the mandatory ones, plus every subset of the guarded ones. Pure, so
// the enumeration is testable without an AST.
//
// The mandatory-only shape is always among them — index 0 of the
// powerset — and it is the one the pre-RC1 scheme dropped, because it
// is what runs when every branch is skipped.
//
// Deliberate over-indexing: if/else arms are treated as independently
// optional, so a combination that cannot occur together still derives
// an index. Over-indexing is the safe direction, and ruling it out
// needs path sensitivity this does not have.
func shapesOf(mods []queryMod) []queryChain {
	var optional []int
	for i, m := range mods {
		if m.optional {
			optional = append(optional, i)
		}
	}
	out := make([]queryChain, 0, 1<<len(optional))
	for mask := 0; mask < 1<<len(optional); mask++ {
		on := map[int]bool{}
		for bit, idx := range optional {
			if mask&(1<<bit) != 0 {
				on[idx] = true
			}
		}
		c := queryChain{filters: map[string]bool{}}
		for i, m := range mods {
			if m.optional && !on[i] {
				continue
			}
			if m.dir == "" {
				c.filters[m.field] = true
			} else {
				// Call order is preserved, which is what makes the
				// OrderBy suffix of the derived tuple correct.
				c.orders = append(c.orders, orderBy{field: m.field, dir: m.dir})
			}
		}
		out = append(out, c)
	}
	return out
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

// extractQueue validates one tako.Queue declaration.
func extractQueue(dir string, pkg *packages.Package, name *ast.Ident, call *ast.CallExpr, errs *ErrorList) *Resource {
	return extractCarrier("queue", "tako.Queue", dir, pkg, name, call, errs)
}

// extractTopic validates one tako.Topic declaration.
func extractTopic(dir string, pkg *packages.Package, name *ast.Ident, call *ast.CallExpr, errs *ErrorList) *Resource {
	return extractCarrier("topic", "tako.Topic", dir, pkg, name, call, errs)
}

// intOptionArg reads a one-int-argument option's constant value.
func intOptionArg(pkg *packages.Package, opt ast.Expr) (int, bool) {
	call, ok := opt.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return 0, false
	}
	tv, ok := pkg.TypesInfo.Types[call.Args[0]]
	if !ok || tv.Value == nil {
		return 0, false
	}
	n, ok := constant.Int64Val(tv.Value)
	return int(n), ok
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
	if !checkResourceName(kind, ctor, resName, dir, pkg, call.Args[0], errs) {
		return nil
	}
	// Only the carriers take configuration, and the type says which:
	// Queue and Topic take a tako.Delivery, every other constructor
	// through here has no config parameter at all. The swallowed-option
	// class this code used to police (S6's WithRateLimit, and the Bucket
	// and Entity leaks api-review-3 found) cannot be written down now.
	bad := false
	var delivery *Delivery
	if (kind == "queue" || kind == "topic") && len(call.Args) > 1 {
		if len(call.Args) > 2 {
			*errs = append(*errs, &Error{
				Pos:  relPos(dir, pkg.Fset, call.Args[2].Pos()),
				Msg:  fmt.Sprintf("%s takes one tako.Delivery, got %d — merge them into a single literal", ctor, len(call.Args)-1),
				Rule: "one-config",
			})
			bad = true
		} else if d := deliveryArg(dir, pkg, call.Args[1], kind, errs); d != nil {
			delivery = d
		} else {
			bad = true
		}
	}
	if bad {
		return nil
	}
	res := &Resource{
		Kind:              kind,
		Name:              resName,
		Var:               pkg.Name + "." + name.Name,
		Package:           pkg.PkgPath,
		RateLimit:         delivery.rate(),
		MaxAttempts:       delivery.attempts(),
		MinBackoffSeconds: delivery.minBackoff(),
		MaxBackoffSeconds: delivery.maxBackoff(),
		Pos:               relPos(dir, pkg.Fset, name.Pos()),
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
//
// "No options" is the SIGNATURE's job, not a rule's: tako.Lock takes one
// string and nothing else, so extra arguments never reach analysis —
// they are a compile error first. This used to also refuse them as
// tako/invalid-option, which could not fire for any input that compiles
// and could not be given a golden fixture for the same reason. Removed
// with the last live use of that rule id, which errors.md had already
// described as gone.
func extractLock(dir string, pkg *packages.Package, name *ast.Ident, call *ast.CallExpr, errs *ErrorList) *Resource {
	if name.Name == "_" || len(call.Args) < 1 {
		return nil
	}
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
	return &Resource{
		Kind:    "lock",
		Name:    lockName,
		Var:     pkg.Name + "." + name.Name,
		Package: pkg.PkgPath,
		Pos:     relPos(dir, pkg.Fset, name.Pos()),
	}
}

// physicalName is the shape a name may take when it becomes a GCS
// bucket, a Pub/Sub topic or a Cloud Tasks queue (CR6).
//
// It is their intersection, and Cloud Tasks is the strict one: buckets
// and topics allow underscores, a Tasks queue allows letters, digits
// and hyphens and nothing else. A queue only becomes a Tasks queue when
// it carries a rate limit or a delay, so an underscore in a queue name
// is a deploy that works until the day someone adds WithRateLimit.
//
// Before this, the only name check anywhere was CheckNames' LENGTH
// check — whose own comment says "AppID guarantees the characters are
// legal", which is true of the app id and not of the declared name
// beside it. So tako.Bucket("My Photos") planned cleanly and failed in
// the middle of an apply with a Google 400 naming a string that appears
// nowhere in the user's source: the failure CheckNames exists to move
// forward, on the axis it did not cover.
var physicalName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// physicallyNamed are the kinds whose declared name IS a Google
// resource name. Deliberately not all of them: a Counter or Histogram
// name becomes a Cloud Monitoring metric type, where underscores are
// the convention (thumb_ms), a Secret Manager name accepts underscores
// and capitals, and a Cache never leaves the process. Applying one
// charset to all of them would refuse names that are perfectly legal
// where they actually land.
var physicallyNamed = map[string]bool{"bucket": true, "queue": true, "topic": true}

// checkResourceName reports whether the name is usable, appending the
// error if not.
func checkResourceName(kind, ctor, name, dir string, pkg *packages.Package, arg ast.Expr, errs *ErrorList) bool {
	if !physicallyNamed[kind] || physicalName.MatchString(name) {
		return true
	}
	*errs = append(*errs, &Error{
		Pos: relPos(dir, pkg.Fset, arg.Pos()),
		Msg: fmt.Sprintf("%s name %q is not usable as a physical name — use lowercase letters, "+
			"digits and hyphens (a-z, 0-9, -), starting and ending with a letter or digit",
			ctor, name),
		Rule: "resource-name",
	})
	return false
}
