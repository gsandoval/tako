package analyzer

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/types"
	"strings"

	"golang.org/x/tools/go/packages"

	"github.com/gsandoval/tako/internal/schedule"
)

// workerOptions is the option set tako.Worker accepts.
var workerOptions = map[string]bool{"WithProfile": true, "WithFootprint": true}

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

	profile := ""
	var footprint *Footprint
	for _, opt := range call.Args[2:] {
		optName, isOpt := sdkOptionCall(pkg, opt)
		if isOpt && optName == "WithProfile" {
			profile = profileArg(pkg, opt)
			continue
		}
		if isOpt && optName == "WithFootprint" {
			footprint = footprintArg(dir, pkg, opt, errs)
			continue
		}
		if isOpt && !workerOptions[optName] {
			fail(&Error{
				Pos: relPos(dir, pkg.Fset, opt.Pos()),
				Msg: fmt.Sprintf("tako.%s is not a tako.Worker option (tako.Worker accepts WithProfile, WithFootprint)",
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
		Kind:      "worker",
		Var:       pkg.Name + "." + name.Name,
		Package:   pkg.PkgPath,
		Service:   pkg.Name + "-" + strings.ToLower(name.Name),
		Binding:   queueName,
		Profile:   profile,
		Footprint: footprint,
		Pos:       relPos(dir, pkg.Fset, name.Pos()),
	}
	workersByQueue[queueName] = h
	return h
}

// subscribeOptions is the option set tako.Subscribe accepts.
var subscribeOptions = map[string]bool{"WithProfile": true, "WithFootprint": true}

// extractSubscribe validates one tako.Subscribe declaration. Subscribers
// partition into their own service, and any number may bind one source —
// fan-out with independent retries is the point (contrast one-worker).
func extractSubscribe(dir string, pkg *packages.Package, name *ast.Ident, call *ast.CallExpr, topicVars, bucketVars map[types.Object]string, errs *ErrorList) *Handler {
	if name.Name == "_" || len(call.Args) < 2 {
		return nil
	}
	bad := false
	fail := func(e *Error) { *errs = append(*errs, e); bad = true }

	binding, bindKind, ok := resolveEventSource(pkg, call.Args[0], topicVars, bucketVars)
	if !ok {
		fail(&Error{
			Pos:  relPos(dir, pkg.Fset, call.Args[0].Pos()),
			Msg:  "tako.Subscribe must bind a topic or bucket event declared as a package-level var in this module",
			Rule: "subscribe-binding",
		})
		return nil
	}

	profile := ""
	var footprint *Footprint
	for _, opt := range call.Args[2:] {
		optName, isOpt := sdkOptionCall(pkg, opt)
		if isOpt && optName == "WithProfile" {
			profile = profileArg(pkg, opt)
			continue
		}
		if isOpt && optName == "WithFootprint" {
			footprint = footprintArg(dir, pkg, opt, errs)
			continue
		}
		if isOpt && !subscribeOptions[optName] {
			fail(&Error{
				Pos: relPos(dir, pkg.Fset, opt.Pos()),
				Msg: fmt.Sprintf("tako.%s is not a tako.Subscribe option (tako.Subscribe accepts WithProfile, WithFootprint)",
					optName),
				Rule: "invalid-option",
			})
		}
	}
	if bad {
		return nil
	}

	return &Handler{
		Kind:        "subscribe",
		Var:         pkg.Name + "." + name.Name,
		Package:     pkg.PkgPath,
		Service:     pkg.Name + "-" + strings.ToLower(name.Name),
		Binding:     binding,
		BindingKind: bindKind,
		Profile:     profile,
		Footprint:   footprint,
		Pos:         relPos(dir, pkg.Fset, name.Pos()),
	}
}

// cronOptions is the option set tako.Cron accepts.
var cronOptions = map[string]bool{"WithSingleflight": true}

// extractCron validates one tako.Cron declaration. Cron handlers run in
// the package's app service (spec §4.1 partitioning), not their own.
func extractCron(dir string, pkg *packages.Package, name *ast.Ident, call *ast.CallExpr, errs *ErrorList) *Handler {
	if name.Name == "_" || len(call.Args) < 2 {
		return nil
	}
	bad := false
	fail := func(e *Error) { *errs = append(*errs, e); bad = true }

	sched, err := constString(pkg, call.Args[0])
	if err != nil {
		fail(&Error{
			Pos: relPos(dir, pkg.Fset, call.Args[0].Pos()),
			Msg: fmt.Sprintf("tako.Cron schedule must be a string constant — schedules are read at analysis time, not run time; got %s",
				describeExpr(call.Args[0])),
			Rule: "const-name",
		})
		return nil
	}
	if _, perr := schedule.Parse(sched); perr != nil {
		fail(&Error{
			Pos:  relPos(dir, pkg.Fset, call.Args[0].Pos()),
			Msg:  perr.Error(),
			Rule: "cron-schedule",
		})
	}

	singleflight := false
	for _, opt := range call.Args[2:] {
		optName, isOpt := sdkOptionCall(pkg, opt)
		if isOpt && optName == "WithSingleflight" {
			singleflight = true
			continue
		}
		if isOpt && !cronOptions[optName] {
			fail(&Error{
				Pos: relPos(dir, pkg.Fset, opt.Pos()),
				Msg: fmt.Sprintf("tako.%s is not a tako.Cron option (tako.Cron accepts WithSingleflight)",
					optName),
				Rule: "invalid-option",
			})
		}
	}
	if bad {
		return nil
	}

	return &Handler{
		Kind:         "cron",
		Var:          pkg.Name + "." + name.Name,
		Package:      pkg.PkgPath,
		Service:      pkg.Name,
		Schedule:     sched,
		Singleflight: singleflight,
		Pos:          relPos(dir, pkg.Fset, name.Pos()),
	}
}

// resolveEventSource maps a Subscribe binding argument back to the topic
// var or bucket-event field it references, returning the source name and
// its kind ("topic" or "bucket").
func resolveEventSource(pkg *packages.Package, arg ast.Expr, topicVars, bucketVars map[types.Object]string) (string, string, bool) {
	switch e := arg.(type) {
	case *ast.Ident:
		if topic, ok := topicVars[pkg.TypesInfo.Uses[e]]; ok {
			return topic, "topic", true
		}
	case *ast.SelectorExpr:
		// A qualified topic var (pkg.Topic) …
		if topic, ok := topicVars[pkg.TypesInfo.Uses[e.Sel]]; ok {
			return topic, "topic", true
		}
		// … or a bucket event: <bucketVar>.Uploaded.
		if e.Sel.Name == "Uploaded" {
			if x, ok := e.X.(*ast.Ident); ok {
				if bucket, ok := bucketVars[pkg.TypesInfo.Uses[x]]; ok {
					return bucket + ".Uploaded", "bucket", true
				}
			}
		}
	}
	return "", "", false
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

// jobOptions is the option set tako.JobHandler accepts.
var jobOptions = map[string]bool{"WithParallel": true, "WithFootprint": true}

// extractJobHandler validates one tako.JobHandler declaration: its own
// deployable (a Cloud Run JOB — no routes, no request timeout), bound
// to exactly one declared tako.Job.
func extractJobHandler(dir string, pkg *packages.Package, name *ast.Ident, call *ast.CallExpr, jobVars map[types.Object]string, jobsByName map[string]*Handler, errs *ErrorList) *Handler {
	if name.Name == "_" || len(call.Args) < 2 {
		return nil
	}
	bad := false
	fail := func(e *Error) { *errs = append(*errs, e); bad = true }

	jobName, ok := resolveQueueVar(pkg, call.Args[0], jobVars)
	if !ok {
		fail(&Error{
			Pos:  relPos(dir, pkg.Fset, call.Args[0].Pos()),
			Msg:  "tako.JobHandler must bind a job declared as a package-level var in this module",
			Rule: "job-binding",
		})
		return nil
	}
	parallel := 0
	var footprint *Footprint
	for _, opt := range call.Args[2:] {
		optName, isOpt := sdkOptionCall(pkg, opt)
		if isOpt && optName == "WithParallel" {
			if optCall, ok := opt.(*ast.CallExpr); ok && len(optCall.Args) == 1 {
				if tv, ok := pkg.TypesInfo.Types[optCall.Args[0]]; ok && tv.Value != nil {
					if n, ok := constant.Int64Val(tv.Value); ok {
						parallel = int(n)
					}
				}
			}
			continue
		}
		if isOpt && optName == "WithFootprint" {
			footprint = footprintArg(dir, pkg, opt, errs)
			if footprint != nil && (footprint.Concurrency != 0 || footprint.MinInstances != 0 || footprint.MaxInstances != 0) {
				fail(&Error{
					Pos:  relPos(dir, pkg.Fset, opt.Pos()),
					Msg:  "a job footprint sets CPU and Memory only — executions are tasks, not serving instances; WithParallel sets the fan-out",
					Rule: "footprint",
				})
			}
			continue
		}
		if isOpt && !jobOptions[optName] {
			fail(&Error{
				Pos: relPos(dir, pkg.Fset, opt.Pos()),
				Msg: fmt.Sprintf("tako.%s is not a tako.JobHandler option (tako.JobHandler accepts WithParallel, WithFootprint)",
					optName),
				Rule: "invalid-option",
			})
		}
	}
	if prior := jobsByName[jobName]; prior != nil {
		fail(&Error{
			Pos: relPos(dir, pkg.Fset, call.Args[0].Pos()),
			Msg: fmt.Sprintf("job %q already has handler %s (%s) — a job has exactly one body",
				jobName, ShortVar(*prior), ShortPos(prior.Pos)),
			Rule: "one-job-handler",
		})
	}
	if bad {
		return nil
	}
	h := &Handler{
		Kind:      "job",
		Var:       pkg.Name + "." + name.Name,
		Package:   pkg.PkgPath,
		Service:   pkg.Name + "-" + strings.ToLower(name.Name),
		Binding:   jobName,
		Parallel:  parallel,
		Footprint: footprint,
		Pos:       relPos(dir, pkg.Fset, name.Pos()),
	}
	jobsByName[jobName] = h
	return h
}

// extractSingleton validates one tako.Singleton declaration: a min=1
// service whose generated main runs the lease-guarded loop.
func extractSingleton(dir string, pkg *packages.Package, name *ast.Ident, call *ast.CallExpr, errs *ErrorList) *Handler {
	if name.Name == "_" || len(call.Args) < 2 {
		return nil
	}
	singletonName, err := constString(pkg, call.Args[0])
	if err != nil {
		*errs = append(*errs, &Error{
			Pos: relPos(dir, pkg.Fset, call.Args[0].Pos()),
			Msg: fmt.Sprintf("tako.Singleton name must be a string constant — names are read at analysis time, not run time; got %s",
				describeExpr(call.Args[0])),
			Rule: "const-name",
		})
		return nil
	}
	bad := false
	var footprint *Footprint
	for _, opt := range call.Args[2:] {
		if optName, isOpt := sdkOptionCall(pkg, opt); isOpt && optName == "WithFootprint" {
			footprint = footprintArg(dir, pkg, opt, errs)
			if footprint != nil && (footprint.Concurrency != 0 || footprint.MinInstances != 0 || footprint.MaxInstances != 0) {
				*errs = append(*errs, &Error{
					Pos:  relPos(dir, pkg.Fset, opt.Pos()),
					Msg:  "a singleton footprint sets CPU and Memory only — min=1/max=1/concurrency=1 plus the lease is what makes it exactly-one",
					Rule: "footprint",
				})
				bad = true
			}
			continue
		}
		*errs = append(*errs, &Error{
			Pos:  relPos(dir, pkg.Fset, opt.Pos()),
			Msg:  "tako.Singleton accepts only WithFootprint — min=1/max=1 plus the lease is the rest of the contract",
			Rule: "invalid-option",
		})
		bad = true
	}
	if bad {
		return nil
	}
	return &Handler{
		Kind:      "singleton",
		Var:       pkg.Name + "." + name.Name,
		Package:   pkg.PkgPath,
		Service:   pkg.Name + "-" + strings.ToLower(name.Name),
		Binding:   singletonName,
		Footprint: footprint,
		Pos:       relPos(dir, pkg.Fset, name.Pos()),
	}
}

// profileArg reads WithProfile's constant argument ("", "latency",
// "batch") — tako.Default, tako.Latency, tako.Batch are typed int
// constants in the SDK.
func profileArg(pkg *packages.Package, opt ast.Expr) string {
	call, ok := opt.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return ""
	}
	tv, ok := pkg.TypesInfo.Types[call.Args[0]]
	if !ok || tv.Value == nil {
		return ""
	}
	if n, ok := constant.Int64Val(tv.Value); ok {
		switch n {
		case 1:
			return "latency"
		case 2:
			return "batch"
		}
	}
	return ""
}
