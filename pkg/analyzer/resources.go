package analyzer

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/types"
	"regexp"
	"strconv"
	"time"

	"golang.org/x/tools/go/packages"
)

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
		Kind:        "entity",
		Name:        resName,
		Var:         pkg.Name + "." + name.Name,
		Package:     pkg.PkgPath,
		Type:        et.display,
		TTLField:    et.ttlField,
		BlobBuckets: et.blobBuckets,
		Pos:         relPos(dir, pkg.Fset, name.Pos()),
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
	cfg, ok := configFields(dir, pkg, call.Args[1:], "BucketConfig", errs)
	if !ok {
		return nil
	}
	var cfgMap map[string]string
	if te, set := fieldOf(cfg, "TTL"); set {
		n, tok := constInt(pkg, te)
		if !tok || n < int(24*time.Hour) {
			*errs = append(*errs, &Error{
				Pos:  relPos(dir, pkg.Fset, te.Pos()),
				Msg:  fmt.Sprintf("bucket %q BucketConfig.TTL must be a constant duration of at least 24h (Cloud Storage object lifecycle rules have 1-day granularity)", resName),
				Rule: "config-literal",
			})
			return nil
		}
		days := (n + int(24*time.Hour) - 1) / int(24*time.Hour)
		cfgMap = map[string]string{"ttl_days": strconv.Itoa(days)}
	}
	return &Resource{
		Kind:    "bucket",
		Name:    resName,
		Var:     pkg.Name + "." + name.Name,
		Package: pkg.PkgPath,
		Config:  cfgMap,
		Pos:     relPos(dir, pkg.Fset, name.Pos()),
	}
}

// extractDisk validates one tako.Disk declaration and its optional DiskConfig.
func extractDisk(dir string, pkg *packages.Package, name *ast.Ident, call *ast.CallExpr, errs *ErrorList) *Resource {
	if name.Name == "_" || len(call.Args) < 1 {
		return nil
	}
	resName, err := constString(pkg, call.Args[0])
	if err != nil {
		*errs = append(*errs, &Error{
			Pos: relPos(dir, pkg.Fset, call.Args[0].Pos()),
			Msg: fmt.Sprintf("tako.Disk name must be a string constant — names are read at analysis time, not run time; got %s",
				describeExpr(call.Args[0])),
			Rule: "const-name",
		})
		return nil
	}
	if !checkResourceName("disk", "tako.Disk", resName, dir, pkg, call.Args[0], errs) {
		return nil
	}
	cfg, ok := configFields(dir, pkg, call.Args[1:], "DiskConfig", errs)
	if !ok {
		return nil
	}
	readOnly := boolField(dir, pkg, cfg, "ReadOnly", "DiskConfig", errs)
	ephemeral := boolField(dir, pkg, cfg, "Ephemeral", "DiskConfig", errs)
	sizeLimit, hasSize := stringField(dir, pkg, cfg, "SizeLimit", "DiskConfig", errs)
	if hasSize && sizeLimit != "" {
		if !ephemeral {
			*errs = append(*errs, &Error{
				Pos:  relPos(dir, pkg.Fset, cfg["SizeLimit"].Pos()),
				Msg:  fmt.Sprintf("disk %q sets SizeLimit without Ephemeral: true — SizeLimit only caps in-memory ephemeral disks; persistent disks are backed by Cloud Storage", resName),
				Rule: "footprint",
			})
			return nil
		}
		if mi, ok := memMi(sizeLimit); !ok || mi < 1 || mi > 32*1024 {
			*errs = append(*errs, &Error{
				Pos:  relPos(dir, pkg.Fset, cfg["SizeLimit"].Pos()),
				Msg:  fmt.Sprintf("disk %q SizeLimit must be a memory size like \"512Mi\" or \"2Gi\" (up to 32Gi); got %q", resName, sizeLimit),
				Rule: "footprint",
			})
			return nil
		}
	}
	var cfgMap map[string]string
	if readOnly || ephemeral || sizeLimit != "" {
		cfgMap = map[string]string{}
		if readOnly {
			cfgMap["readonly"] = "true"
		}
		if ephemeral {
			cfgMap["ephemeral"] = "true"
		}
		if sizeLimit != "" {
			cfgMap["size_limit"] = sizeLimit
		}
	}
	return &Resource{
		Kind:    "disk",
		Name:    resName,
		Var:     pkg.Name + "." + name.Name,
		Package: pkg.PkgPath,
		Config:  cfgMap,
		Pos:     relPos(dir, pkg.Fset, name.Pos()),
	}
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
				ta := named.TypeArgs().At(0)
				if p, ok := ta.(*types.Named); ok {
					res.Type = p.Obj().Name()
				} else if kind == "flag" {
					if b, ok := ta.(*types.Basic); ok {
						res.Type = b.Name()
					}
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

// checkResourceName reports whether the name is usable, appending the
// error if not.
func checkResourceName(kind, ctor, name, dir string, pkg *packages.Package, arg ast.Expr, errs *ErrorList) bool {
	if validResourceName(kind, name) {
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

// validResourceName reports whether name is valid for a resource of kind.
func validResourceName(kind, name string) bool {
	return !ByKind[kind].PhysicalName || physicalName.MatchString(name)
}

// AllDocsSlices lists the 7 project documentation slices in presentation order.
var AllDocsSlices = []string{
	"architecture",
	"apis",
	"schedules",
	"resources",
	"roles",
	"footprints",
	"permissions",
}

// ResolveDocsSlices computes the enabled documentation slices from a security
// preset level ("public", "internal", "full", or "" when omitted) and
// explicit per-slice boolean flags.
func ResolveDocsSlices(level string, hasExplicitLevel bool, cherry map[string]bool) ([]string, string) {
	set := map[string]bool{}
	anyCherry := false
	for k, v := range cherry {
		if v {
			set[k] = true
			anyCherry = true
		}
	}
	effectiveLevel := level
	if !hasExplicitLevel {
		if anyCherry {
			effectiveLevel = "custom"
		} else {
			effectiveLevel = "public"
			set["apis"] = true
		}
	} else {
		switch level {
		case "public", "":
			effectiveLevel = "public"
			set["apis"] = true
		case "internal":
			set["architecture"] = true
			set["apis"] = true
			set["schedules"] = true
			set["resources"] = true
		case "full":
			for _, s := range AllDocsSlices {
				set[s] = true
			}
		}
	}
	var out []string
	for _, s := range AllDocsSlices {
		if set[s] {
			out = append(out, s)
		}
	}
	return out, effectiveLevel
}

// extractDocs validates one tako.Docs declaration and its optional DocsConfig.
func extractDocs(dir string, pkg *packages.Package, name *ast.Ident, call *ast.CallExpr, errs *ErrorList) *Resource {
	if name.Name == "_" || len(call.Args) < 1 {
		return nil
	}
	resName, err := constString(pkg, call.Args[0])
	if err != nil {
		*errs = append(*errs, &Error{
			Pos: relPos(dir, pkg.Fset, call.Args[0].Pos()),
			Msg: fmt.Sprintf("tako.Docs name must be a string constant — names are read at analysis time, not run time; got %s",
				describeExpr(call.Args[0])),
			Rule: "const-name",
		})
		return nil
	}
	if !checkResourceName("docs", "tako.Docs", resName, dir, pkg, call.Args[0], errs) {
		return nil
	}
	cfg, ok := configFields(dir, pkg, call.Args[1:], "DocsConfig", errs)
	if !ok {
		return nil
	}
	bad := false
	public := boolField(dir, pkg, cfg, "Public", "DocsConfig", errs)
	_, hasLevel := fieldOf(cfg, "Level")
	level := ""
	if hasLevel {
		lv, lok := stringField(dir, pkg, cfg, "Level", "DocsConfig", errs)
		if !lok {
			bad = true
		} else if lv != "public" && lv != "internal" && lv != "full" {
			*errs = append(*errs, &Error{
				Pos:  relPos(dir, pkg.Fset, cfg["Level"].Pos()),
				Msg:  fmt.Sprintf("tako.DocsConfig.Level must be tako.DocsPublic, tako.DocsInternal, or tako.DocsFull; got %q", lv),
				Rule: "config-literal",
			})
			bad = true
		} else {
			level = lv
		}
	}
	cherry := map[string]bool{
		"apis":         boolField(dir, pkg, cfg, "APIs", "DocsConfig", errs),
		"schedules":    boolField(dir, pkg, cfg, "Schedules", "DocsConfig", errs),
		"architecture": boolField(dir, pkg, cfg, "Architecture", "DocsConfig", errs),
		"resources":    boolField(dir, pkg, cfg, "Resources", "DocsConfig", errs),
		"roles":        boolField(dir, pkg, cfg, "Roles", "DocsConfig", errs),
		"footprints":   boolField(dir, pkg, cfg, "Footprints", "DocsConfig", errs),
		"permissions":  boolField(dir, pkg, cfg, "Permissions", "DocsConfig", errs),
	}
	if bad {
		return nil
	}
	slices, effectiveLevel := ResolveDocsSlices(level, hasLevel, cherry)
	cfgMap := map[string]string{
		"level": effectiveLevel,
	}
	if public {
		cfgMap["public"] = "true"
	}
	return &Resource{
		Kind:       "docs",
		Name:       resName,
		Var:        pkg.Name + "." + name.Name,
		Package:    pkg.PkgPath,
		Config:     cfgMap,
		DocsSlices: slices,
		Pos:        relPos(dir, pkg.Fset, name.Pos()),
	}
}
