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

// computeConfig reads a tako.Compute argument — the sizing every async
// handler that IS its own service shares.
func computeConfig(dir string, pkg *packages.Package, args []ast.Expr, errs *ErrorList) (string, *Footprint, *Traffic) {
	cfg, ok := configFields(dir, pkg, args, "Compute", errs)
	if !ok {
		return "", nil, nil
	}
	profile := ""
	if e, set := fieldOf(cfg, "Profile"); set {
		profile = profileArg(pkg, e)
	}
	var footprint *Footprint
	if e, set := fieldOf(cfg, "Footprint"); set {
		footprint = footprintArg(dir, pkg, e, errs)
	}
	var traffic *Traffic
	if e, set := fieldOf(cfg, "Traffic"); set {
		traffic = trafficArg(dir, pkg, e, errs)
	}
	return profile, footprint, traffic
}

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

	profile, footprint, traffic := computeConfig(dir, pkg, call.Args[2:], errs)

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

	profile, footprint, traffic := computeConfig(dir, pkg, call.Args[2:], errs)
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

	cfg, ok := configFields(dir, pkg, call.Args[2:], "CronConfig", errs)
	if !ok {
		bad = true
	}
	singleflight := boolField(dir, pkg, cfg, "Singleflight", "CronConfig", errs)
	timezone := ""
	if zone, set := stringField(dir, pkg, cfg, "Timezone", "CronConfig", errs); set {
		// The tz database is right here at analysis time, so a misspelled
		// zone is a build error, not a 3am surprise.
		if _, err := time.LoadLocation(zone); err != nil {
			fail(&Error{
				Pos:  relPos(dir, pkg.Fset, cfg["Timezone"].Pos()),
				Msg:  fmt.Sprintf("tako.CronConfig.Timezone does not know the zone %q — use an IANA name like \"America/Mexico_City\" or \"Europe/Madrid\"", zone),
				Rule: "timezone",
			})
		} else {
			timezone = zone
		}
	}
	deadline := 0
	if e, set := fieldOf(cfg, "Deadline"); set {
		n, dok := constInt(pkg, e)
		secs := n / int(time.Second)
		if !dok || secs < 15 || secs > 1800 {
			fail(&Error{
				Pos:  relPos(dir, pkg.Fset, e.Pos()),
				Msg:  "tako.CronConfig.Deadline takes a constant 15s–30m — Cloud Scheduler's range for an HTTP target; a cron that needs longer wants a tako.Job",
				Rule: "deadline",
			})
		}
		deadline = secs
	}
	profile := ""
	if e, set := fieldOf(cfg, "Profile"); set {
		profile = profileArg(pkg, e)
	}
	var footprint *Footprint
	if e, set := fieldOf(cfg, "Footprint"); set {
		footprint = footprintArg(dir, pkg, e, errs)
	}
	var traffic *Traffic
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

// extractRunner validates one tako.Runner declaration: its own
// deployable (a Cloud Run JOB — no routes, no request timeout), bound
// to exactly one declared tako.Job.
func extractRunner(dir string, pkg *packages.Package, name *ast.Ident, call *ast.CallExpr, jobVars map[types.Object]string, jobsByName map[string]*Handler, errs *ErrorList) *Handler {
	if name.Name == "_" || len(call.Args) < 2 {
		return nil
	}
	bad := false
	fail := func(e *Error) { *errs = append(*errs, e); bad = true }

	jobName, ok := resolveQueueVar(pkg, call.Args[0], jobVars)
	if !ok {
		fail(&Error{
			Pos:  relPos(dir, pkg.Fset, call.Args[0].Pos()),
			Msg:  "tako.Runner must bind a job declared as a package-level var in this module",
			Rule: "job-binding",
		})
		return nil
	}
	cfg, ok := configFields(dir, pkg, call.Args[2:], "RunnerConfig", errs)
	if !ok {
		bad = true
	}
	parallel := 0
	if e, set := fieldOf(cfg, "Parallel"); set {
		if n, nok := constInt(pkg, e); nok {
			parallel = n
		}
	}
	retries := 0
	if e, set := fieldOf(cfg, "Attempts"); set {
		n, nok := constInt(pkg, e)
		if !nok || n < 0 || n > 10 {
			fail(&Error{
				Pos:  relPos(dir, pkg.Fset, e.Pos()),
				Msg:  "tako.RunnerConfig.Attempts takes a constant 0–10 attempts after the first — a task that fails 10 times is not going to pass on the 11th, and jobs have no dead letter to catch it",
				Rule: "retries",
			})
		}
		retries = n
	}
	profile := ""
	if e, set := fieldOf(cfg, "Profile"); set {
		profile = profileArg(pkg, e)
	}
	var footprint *Footprint
	if e, set := fieldOf(cfg, "Footprint"); set {
		footprint = footprintArg(dir, pkg, e, errs)
		if footprint != nil && (footprint.Concurrency != 0 || footprint.MinInstances != 0 || footprint.MaxInstances != 0 || footprint.CPUAlwaysOn != ToggleInherit || footprint.StartupBoost != ToggleInherit) {
			fail(&Error{
				Pos:  relPos(dir, pkg.Fset, e.Pos()),
				Msg:  "a job footprint sets CPU and Memory only — executions are tasks, not serving instances, so there is no concurrency, no scaling, no idle CPU to throttle and no cold start to boost; RunnerConfig.Parallel sets the fan-out and RunnerConfig.Attempts the attempts",
				Rule: "footprint",
			})
		}
	}
	// A job task HAS a timeout — 24h of it. What it does not have is
	// clients: nothing connects to an execution, so there is nobody to
	// keep affinity with and no connection to negotiate a protocol over.
	// RunnerConfig.Timeout is that one setting; the rest of tako.Traffic
	// is not offered, which is why this needs no rule.
	var traffic *Traffic
	if e, set := fieldOf(cfg, "Timeout"); set {
		n, nok := constInt(pkg, e)
		if !nok {
			fail(&Error{
				Pos:  relPos(dir, pkg.Fset, e.Pos()),
				Msg:  "tako.RunnerConfig.Timeout must be a constant duration — it is read at analysis time, not run time",
				Rule: "config-literal",
			})
		} else {
			traffic = &Traffic{TimeoutSeconds: n / int(time.Second)}
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
	// tako.Singleton(fn, opts...) — no name argument. The declaring
	// variable is the identity, the same rule every other service tako
	// deploys already follows.
	if name.Name == "_" || len(call.Args) < 1 {
		return nil
	}
	bad := false
	// tako.Singleton takes a tako.Footprint directly: min=1/max=1 plus
	// the lease is the rest of the contract, so there is nothing else to
	// configure. Traffic is not offered — exactly one instance serving
	// nothing but its own health check has no clients to keep affinity
	// with or negotiate HTTP/2 over.
	cfg, cok := configFields(dir, pkg, call.Args[1:], "SingletonConfig", errs)
	if !cok {
		bad = true
	}
	var traffic *Traffic
	if e, set := fieldOf(cfg, "Timeout"); set {
		if n, nok := constInt(pkg, e); nok {
			traffic = &Traffic{TimeoutSeconds: n / int(time.Second)}
		} else {
			*errs = append(*errs, &Error{
				Pos:  relPos(dir, pkg.Fset, e.Pos()),
				Msg:  "tako.SingletonConfig.Timeout must be a constant duration — it is read at analysis time, not run time",
				Rule: "config-literal",
			})
			bad = true
		}
	}
	var footprint *Footprint
	if fe, set := fieldOf(cfg, "Footprint"); set {
		footprint = footprintArg(dir, pkg, fe, errs)
		if footprint != nil && footprint.MaxInstances != 0 {
			*errs = append(*errs, &Error{
				Pos:  relPos(dir, pkg.Fset, fe.Pos()),
				Msg:  "a singleton footprint cannot set MaxInstances — the lease is what makes a singleton exactly-one, so a max above 1 only buys instances that pay to lose the race; CPU, Memory and StartupBoost are yours",
				Rule: "singleton-max",
			})
			bad = true
		}
		if footprint != nil && (footprint.Concurrency != 0 || footprint.MinInstances != 0) {
			*errs = append(*errs, &Error{
				Pos:  relPos(dir, pkg.Fset, fe.Pos()),
				Msg:  "a singleton footprint sets CPU, Memory and StartupBoost only — min=1 and concurrency=1 are what keep the loop's host alive and alone",
				Rule: "footprint",
			})
			bad = true
		}
	}
	if bad {
		return nil
	}
	return &Handler{
		Kind:      "singleton",
		Var:       pkg.Name + "." + name.Name,
		Package:   pkg.PkgPath,
		Service:   pkg.Name + "-" + strings.ToLower(name.Name),
		Binding:   name.Name,
		Footprint: footprint,
		Traffic:   traffic,
		Pos:       relPos(dir, pkg.Fset, name.Pos()),
	}
}

// constInt reads a constant integer expression.
func constInt(pkg *packages.Package, e ast.Expr) (int, bool) {
	tv, ok := pkg.TypesInfo.Types[e]
	if !ok || tv.Value == nil {
		return 0, false
	}
	n, ok := constant.Int64Val(tv.Value)
	return int(n), ok
}

// profileArg reads a Profile field's constant value ("", "latency",
// "batch") — tako.ProfileDefault/Latency/Batch are typed int constants
// in the SDK.
func profileArg(pkg *packages.Package, e ast.Expr) string {
	tv, ok := pkg.TypesInfo.Types[e]
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
