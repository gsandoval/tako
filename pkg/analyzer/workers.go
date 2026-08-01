package analyzer

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/types"
	"strings"
	"time"

	"golang.org/x/tools/go/packages"

	"github.com/gsandoval/tako/internal/schedule"
)

// workerOptions is the option set tako.Worker accepts.
var workerOptions = map[string]bool{"WithProfile": true, "WithFootprint": true, "WithTraffic": true}

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
	var traffic *Traffic
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
		if isOpt && optName == "WithTraffic" {
			traffic = trafficArg(dir, pkg, opt, errs)
			continue
		}
		if isOpt && optName == "WithTraffic" {
			traffic = trafficArg(dir, pkg, opt, errs)
			continue
		}
		if isOpt && !workerOptions[optName] {
			fail(&Error{
				Pos: relPos(dir, pkg.Fset, opt.Pos()),
				Msg: fmt.Sprintf("tako.%s is not a tako.Worker option (tako.Worker accepts WithProfile, WithFootprint, WithTraffic)",
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
		Traffic:   traffic,
		Pos:       relPos(dir, pkg.Fset, name.Pos()),
	}
	workersByQueue[queueName] = h
	return h
}

// subscribeOptions is the option set tako.Subscribe accepts.
var subscribeOptions = map[string]bool{"WithProfile": true, "WithFootprint": true, "WithTraffic": true}

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
	var traffic *Traffic
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
		if isOpt && optName == "WithTraffic" {
			traffic = trafficArg(dir, pkg, opt, errs)
			continue
		}
		if isOpt && !subscribeOptions[optName] {
			fail(&Error{
				Pos: relPos(dir, pkg.Fset, opt.Pos()),
				Msg: fmt.Sprintf("tako.%s is not a tako.Subscribe option (tako.Subscribe accepts WithProfile, WithFootprint, WithTraffic)",
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
		Traffic:     traffic,
		Pos:         relPos(dir, pkg.Fset, name.Pos()),
	}
}

// cronOptions is the option set tako.Cron accepts. Profile/footprint
// (S6 amendment) shape the HOSTING service — the one a cron-only
// package would otherwise have no way to control.
var cronOptions = map[string]bool{"WithSingleflight": true, "WithProfile": true, "WithFootprint": true,
	"WithTimezone": true, "WithDeadline": true}

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
	profile := ""
	timezone := ""
	deadline := 0
	var footprint *Footprint
	var traffic *Traffic
	for _, opt := range call.Args[2:] {
		optName, isOpt := sdkOptionCall(pkg, opt)
		if isOpt && optName == "WithSingleflight" {
			singleflight = true
			continue
		}
		if isOpt && optName == "WithTimezone" {
			zone, ok := stringOptionArg(pkg, opt)
			// The tz database is right here at analysis time, so a
			// misspelled zone is a build error, not a 3am surprise.
			if !ok {
				fail(&Error{
					Pos:  relPos(dir, pkg.Fset, opt.Pos()),
					Msg:  "tako.WithTimezone takes a constant IANA zone name — schedules are read at analysis time, not run time",
					Rule: "const-name",
				})
			} else if _, err := time.LoadLocation(zone); err != nil {
				fail(&Error{
					Pos:  relPos(dir, pkg.Fset, opt.Pos()),
					Msg:  fmt.Sprintf("tako.WithTimezone does not know the zone %q — use an IANA name like \"America/Mexico_City\" or \"Europe/Madrid\"", zone),
					Rule: "timezone",
				})
			} else {
				timezone = zone
			}
			continue
		}
		if isOpt && optName == "WithDeadline" {
			n, ok := intOptionArg(pkg, opt)
			secs := n / int(time.Second)
			if !ok || secs < 15 || secs > 1800 {
				fail(&Error{
					Pos:  relPos(dir, pkg.Fset, opt.Pos()),
					Msg:  "tako.WithDeadline takes a constant 15s–30m — Cloud Scheduler's range for an HTTP target; a cron that needs longer wants a tako.Job",
					Rule: "deadline",
				})
			}
			deadline = secs
			continue
		}
		if isOpt && optName == "WithProfile" {
			profile = profileArg(pkg, opt)
			continue
		}
		if isOpt && optName == "WithFootprint" {
			footprint = footprintArg(dir, pkg, opt, errs)
			continue
		}
		if isOpt && !cronOptions[optName] {
			fail(&Error{
				Pos: relPos(dir, pkg.Fset, opt.Pos()),
				Msg: fmt.Sprintf("tako.%s is not a tako.Cron option (tako.Cron accepts WithSingleflight, WithTimezone, WithDeadline, WithProfile, WithFootprint)",
					optName),
				Rule: "invalid-option",
			})
		}
	}
	if bad {
		return nil
	}

	return &Handler{
		Kind:            "cron",
		Var:             pkg.Name + "." + name.Name,
		Package:         pkg.PkgPath,
		Service:         pkg.Name,
		Schedule:        sched,
		Singleflight:    singleflight,
		Timezone:        timezone,
		DeadlineSeconds: deadline,
		Profile:         profile,
		Footprint:       footprint,
		Traffic:         traffic,
		Pos:             relPos(dir, pkg.Fset, name.Pos()),
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
var jobOptions = map[string]bool{"WithParallel": true, "WithFootprint": true,
	"WithRetries": true, "WithProfile": true}

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
	retries := 0
	profile := ""
	var footprint *Footprint
	var traffic *Traffic
	for _, opt := range call.Args[2:] {
		optName, isOpt := sdkOptionCall(pkg, opt)
		if isOpt && optName == "WithProfile" {
			profile = profileArg(pkg, opt)
			continue
		}
		if isOpt && optName == "WithTraffic" {
			traffic = trafficArg(dir, pkg, opt, errs)
			// A job task HAS a timeout — 24h of it. What it does not have
			// is clients: nothing connects to an execution, so there is
			// nobody to keep affinity with and no connection to negotiate
			// a protocol over.
			if traffic != nil && (traffic.SessionAffinity != ToggleInherit || traffic.HTTP2 != ToggleInherit) {
				fail(&Error{
					Pos:  relPos(dir, pkg.Fset, opt.Pos()),
					Msg:  "a job's only traffic setting is Timeout — an execution serves nobody, so there are no clients to keep affinity with and no connections to negotiate HTTP/2 over",
					Rule: "traffic",
				})
			}
			continue
		}
		if isOpt && optName == "WithRetries" {
			n, ok := intOptionArg(pkg, opt)
			if !ok || n < 0 || n > 10 {
				fail(&Error{
					Pos:  relPos(dir, pkg.Fset, opt.Pos()),
					Msg:  "tako.WithRetries takes a constant 0–10 attempts after the first — a task that fails 10 times is not going to pass on the 11th, and jobs have no dead letter to catch it",
					Rule: "retries",
				})
			}
			retries = n
			continue
		}
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
			if footprint != nil && (footprint.Concurrency != 0 || footprint.MinInstances != 0 || footprint.MaxInstances != 0 || footprint.CPUAlwaysOn != ToggleInherit || footprint.StartupBoost != ToggleInherit) {
				fail(&Error{
					Pos:  relPos(dir, pkg.Fset, opt.Pos()),
					Msg:  "a job footprint sets CPU and Memory only — executions are tasks, not serving instances, so there is no concurrency, no scaling, no idle CPU to throttle and no cold start to boost; WithParallel sets the fan-out and WithRetries the attempts",
					Rule: "footprint",
				})
			}
			continue
		}
		if isOpt && !jobOptions[optName] {
			fail(&Error{
				Pos: relPos(dir, pkg.Fset, opt.Pos()),
				Msg: fmt.Sprintf("tako.%s is not a tako.JobHandler option (tako.JobHandler accepts WithParallel, WithRetries, WithProfile, WithFootprint)",
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
		Retries:   retries,
		Profile:   profile,
		Footprint: footprint,
		Traffic:   traffic,
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
	var traffic *Traffic
	for _, opt := range call.Args[2:] {
		if optName, isOpt := sdkOptionCall(pkg, opt); isOpt && optName == "WithTraffic" {
			traffic = trafficArg(dir, pkg, opt, errs)
			// One instance, and the only caller is its own health check:
			// affinity has nothing to choose between and there is no
			// client conversation to upgrade.
			if traffic != nil && (traffic.SessionAffinity != ToggleInherit || traffic.HTTP2 != ToggleInherit) {
				*errs = append(*errs, &Error{
					Pos:  relPos(dir, pkg.Fset, opt.Pos()),
					Msg:  "a singleton's only traffic setting is Timeout — exactly one instance serving nothing but its own health check has no clients to keep affinity with or negotiate HTTP/2 over",
					Rule: "traffic",
				})
				bad = true
			}
			continue
		}
		if optName, isOpt := sdkOptionCall(pkg, opt); isOpt && optName == "WithFootprint" {
			footprint = footprintArg(dir, pkg, opt, errs)
			if footprint != nil && footprint.MaxInstances != 0 {
				*errs = append(*errs, &Error{
					Pos:  relPos(dir, pkg.Fset, opt.Pos()),
					Msg:  "a singleton footprint cannot set MaxInstances — the lease is what makes a singleton exactly-one, so a max above 1 only buys instances that pay to lose the race; CPU, Memory and StartupBoost are yours",
					Rule: "singleton-max",
				})
				bad = true
			}
			if footprint != nil && (footprint.Concurrency != 0 || footprint.MinInstances != 0) {
				*errs = append(*errs, &Error{
					Pos:  relPos(dir, pkg.Fset, opt.Pos()),
					Msg:  "a singleton footprint sets CPU, Memory and StartupBoost only — min=1 and concurrency=1 are what keep the loop's host alive and alone",
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
		Traffic:   traffic,
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
