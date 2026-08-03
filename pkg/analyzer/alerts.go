package analyzer

// Alerting declarations (Y1b).
//
// Channels and alerts are app-scoped: they run nowhere, so they are
// never partitioned into a service. What the analyzer must do is resolve
// each condition's SUBJECT back to the declaration it names — a counter,
// a queue, a cron var, a route — because every query tako generates is
// written from that declaration, and a subject that resolves to nothing
// would deploy as a policy watching a metric no one emits.

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/types"
	"time"

	"golang.org/x/tools/go/packages"
)

// channelKinds maps the SDK constructor to the Cloud Monitoring channel
// type. The values are Google's, verified against the notification
// channel descriptors.
var channelKinds = map[string]string{
	"Email":     "email",
	"ChatSpace": "google_chat",
	"Webhook":   "webhook_tokenauth",
	"PubSub":    "pubsub",
}

// extractChannel validates one alert destination. The declaring VARIABLE
// is the identity (X1); the argument is the address.
func extractChannel(ctor, dir string, pkg *packages.Package, name *ast.Ident, call *ast.CallExpr, topicVars map[types.Object]string, errs *ErrorList) *Resource {
	if name.Name == "_" || len(call.Args) < 1 {
		return nil
	}
	r := &Resource{
		Kind:        "channel",
		Name:        name.Name,
		ChannelKind: channelKinds[ctor],
		Var:         pkg.Name + "." + name.Name,
		Package:     pkg.PkgPath,
		Pos:         relPos(dir, pkg.Fset, name.Pos()),
	}

	// A Pub/Sub channel names a declared topic, not a string: the topic
	// has to exist for the deploy to grant the notification service
	// agent publisher on it.
	if ctor == "PubSub" {
		topic, ok := resolveTopicVar(pkg, call.Args[0], topicVars)
		if !ok {
			*errs = append(*errs, &Error{
				Pos:  relPos(dir, pkg.Fset, call.Args[0].Pos()),
				Msg:  "tako.PubSub must name a topic declared as a package-level var in this module",
				Rule: "channel-binding",
			})
			return nil
		}
		r.Target = topic
		return r
	}

	target, err := constString(pkg, call.Args[0])
	if err != nil {
		*errs = append(*errs, &Error{
			Pos: relPos(dir, pkg.Fset, call.Args[0].Pos()),
			Msg: fmt.Sprintf("tako.%s takes a string constant — alert destinations are read at analysis time, not run time; got %s",
				ctor, describeExpr(call.Args[0])),
			Rule: "const-name",
		})
		return nil
	}
	r.Target = target
	return r
}

func resolveTopicVar(pkg *packages.Package, arg ast.Expr, topicVars map[types.Object]string) (string, bool) {
	switch e := arg.(type) {
	case *ast.Ident:
		n, ok := topicVars[pkg.TypesInfo.Uses[e]]
		return n, ok
	case *ast.SelectorExpr:
		n, ok := topicVars[pkg.TypesInfo.Uses[e.Sel]]
		return n, ok
	}
	return "", false
}

// alertSubjects is the set of condition constructors and what each one's
// first argument must resolve to. Presets name a SITUATION, so the
// analyzer checks the subject is the right kind of thing: CronMissed on
// a worker is a mistake the type system cannot catch, because every
// async handler is a tako.Handler.
var alertSubjects = map[string]string{
	"CronFailed":    "cron",
	"CronMissed":    "cron",
	"SingletonDown": "singleton",
}

// extractAlert validates one tako.Alert declaration: the condition's
// subject resolves, and every channel is a declared channel var.
func extractAlert(dir string, pkg *packages.Package, name *ast.Ident, call *ast.CallExpr,
	channelVars map[types.Object]string, handlersByVar map[string]*Handler,
	resVars map[types.Object]resRef, endpointVars map[types.Object]string, errs *ErrorList) *Resource {
	if name.Name == "_" || len(call.Args) < 2 {
		return nil
	}
	bad := false
	fail := func(pos ast.Node, msg, rule string) {
		*errs = append(*errs, &Error{Pos: relPos(dir, pkg.Fset, pos.Pos()), Msg: msg, Rule: rule})
		bad = true
	}

	cond, ok := call.Args[0].(*ast.CallExpr)
	if !ok {
		fail(call.Args[0], "tako.Alert takes a condition — tako.Above, tako.Below, tako.Absent, or a preset like tako.DeadLettered", "alert-condition")
		return nil
	}
	condCtor := sdkConstructor(pkg, cond)
	if condCtor == "" {
		fail(call.Args[0], "tako.Alert takes a condition built by the tako package", "alert-condition")
		return nil
	}

	// A preset's subject must be the KIND of thing the situation is
	// about. Every async handler is a tako.Handler, so the compiler
	// cannot tell a cron from a worker here — this is the check that can.
	if want, isPreset := alertSubjects[condCtor]; isPreset && len(cond.Args) > 0 {
		if got, v, ok := resolveHandlerKind(pkg, cond.Args[0], handlersByVar); ok && got != want {
			fail(cond.Args[0], fmt.Sprintf("tako.%s watches a %s, but %s is a %s", condCtor, want, v, got), "alert-subject")
		}
	}

	// Every destination must be a declared channel var: an alert whose
	// channel tako cannot see is a policy with nowhere to deliver.
	var channels []string
	for _, arg := range call.Args[1:] {
		ch, ok := resolveChannelVar(pkg, arg, channelVars)
		if !ok {
			fail(arg, "tako.Alert destinations must be channels declared as package-level vars in this module (tako.Email, tako.ChatSpace, tako.Webhook, tako.PubSub)", "channel-binding")
			continue
		}
		channels = append(channels, ch)
	}
	spec := conditionSpec(dir, pkg, cond, condCtor, handlersByVar, resVars, endpointVars, errs)
	if bad {
		return nil
	}
	return &Resource{
		Kind:      "alert",
		Alert:     spec,
		Name:      name.Name,
		Var:       pkg.Name + "." + name.Name,
		Package:   pkg.PkgPath,
		Condition: condCtor,
		Channels:  channels,
		Pos:       relPos(dir, pkg.Fset, name.Pos()),
	}
}

func resolveChannelVar(pkg *packages.Package, arg ast.Expr, channelVars map[types.Object]string) (string, bool) {
	switch e := arg.(type) {
	case *ast.Ident:
		n, ok := channelVars[pkg.TypesInfo.Uses[e]]
		return n, ok
	case *ast.SelectorExpr:
		n, ok := channelVars[pkg.TypesInfo.Uses[e.Sel]]
		return n, ok
	}
	return "", false
}

// resolveHandlerKind reports the kind ("cron", "worker", …) of the
// handler var an expression names.
func resolveHandlerKind(pkg *packages.Package, arg ast.Expr, handlersByVar map[string]*Handler) (kind, v string, ok bool) {
	var obj types.Object
	switch e := arg.(type) {
	case *ast.Ident:
		obj = pkg.TypesInfo.Uses[e]
	case *ast.SelectorExpr:
		obj = pkg.TypesInfo.Uses[e.Sel]
	}
	if obj == nil {
		return "", "", false
	}
	name := pkg.Name + "." + obj.Name()
	if h, found := handlersByVar[name]; found {
		return h.Kind, name, true
	}
	return "", "", false
}

// checkQuantile validates a percentile argument at its call site: a
// quantile outside (0,1) is a policy that can never fire, and 99 for
// "the 99th" is the mistake everyone makes once.
func checkQuantile(dir string, pkg *packages.Package, e ast.Expr, ctor string, errs *ErrorList) {
	tv, ok := pkg.TypesInfo.Types[e]
	if !ok || tv.Value == nil {
		*errs = append(*errs, &Error{
			Pos:  relPos(dir, pkg.Fset, e.Pos()),
			Msg:  fmt.Sprintf("tako.%s quantile must be a constant — it is read at analysis time, not run time", ctor),
			Rule: "quantile",
		})
		return
	}
	f, _ := constant.Float64Val(constant.ToFloat(tv.Value))
	if f <= 0 || f >= 1 {
		*errs = append(*errs, &Error{
			Pos: relPos(dir, pkg.Fset, e.Pos()),
			Msg: fmt.Sprintf("tako.%s quantile must be between 0 and 1 exclusive — 0.99 is the 99th percentile, not 99; got %v",
				ctor, f),
			Rule: "quantile",
		})
	}
}

// presetReadings maps each preset to the reading the provisioner will
// build, and to how its arguments are laid out. Keeping this as data
// rather than a switch means a new preset cannot be added without an
// entry — the completeness habit the retry allowlist had to learn.
var presetReadings = map[string]struct {
	reading string
	subject string // subject kind when the preset fixes it
	// unit converts a duration-valued argument out of nanoseconds. A
	// Go duration constant reaches the analyzer as raw nanos, so a
	// 10-minute backlog would otherwise be stored as 6e11 and compared
	// against seconds — an alert that can never fire.
	unit     time.Duration
	hasValue bool // takes a threshold argument
	quantArg int  // index of a quantile argument, -1 for none
	valueArg int
	op       string
}{
	"DeadLettered":  {reading: "dead_letters", hasValue: true, valueArg: 1, quantArg: -1, op: "atleast"},
	"Backlogged":    {reading: "backlog_age", unit: time.Second, hasValue: true, valueArg: 1, quantArg: -1, op: "above"},
	"CronFailed":    {reading: "cron_failed", subject: "cron", hasValue: true, valueArg: 1, quantArg: -1, op: "atleast"},
	"CronMissed":    {reading: "cron_missed", subject: "cron", quantArg: -1, op: "absent"},
	"JobFailed":     {reading: "job_failed", subject: "job", hasValue: true, valueArg: 1, quantArg: -1, op: "atleast"},
	"Erroring":      {reading: "error_rate", hasValue: true, valueArg: 1, quantArg: -1, op: "above"},
	"SlowerThan":    {reading: "latency", unit: time.Millisecond, hasValue: true, valueArg: 1, quantArg: 2, op: "above"},
	"AtCapacity":    {reading: "at_capacity", quantArg: -1, op: "above"},
	"SingletonDown": {reading: "singleton_down", subject: "singleton", quantArg: -1, op: "below"},
}

// readingOf maps a raw metric constructor to its reading.
var readingOf = map[string]string{
	"Rate": "rate", "Count": "count", "Sum": "sum",
	"Mean": "mean", "Percentile": "percentile", "Platform": "platform",
}

// conditionSpec reads a condition call into its logical parameters.
func conditionSpec(dir string, pkg *packages.Package, cond *ast.CallExpr, ctor string,
	handlersByVar map[string]*Handler, resVars map[types.Object]resRef, endpointVars map[types.Object]string,
	errs *ErrorList) *AlertCondition {

	spec := &AlertCondition{}
	var windowArgs []ast.Expr

	if p, isPreset := presetReadings[ctor]; isPreset {
		spec.Op, spec.Reading = p.op, p.reading
		if len(cond.Args) > 0 {
			spec.SubjectKind, spec.SubjectName, spec.SubjectVar = resolveSubject(pkg, cond.Args[0], handlersByVar, resVars, endpointVars)
			if p.subject != "" {
				spec.SubjectKind = p.subject
			}
		}
		if p.hasValue && len(cond.Args) > p.valueArg {
			spec.Threshold = constFloat(pkg, cond.Args[p.valueArg])
			if p.unit > 0 {
				spec.Threshold /= float64(p.unit)
			}
		}
		if p.quantArg >= 0 && len(cond.Args) > p.quantArg {
			spec.Quantile = constFloat(pkg, cond.Args[p.quantArg])
		}
		// Presets take a trailing optional Window.
		first := p.valueArg + 1
		if p.quantArg >= 0 {
			first = p.quantArg + 1
		}
		if !p.hasValue && p.quantArg < 0 {
			first = 1
		}
		if len(cond.Args) > first {
			windowArgs = cond.Args[first:]
		}
		readWindow(dir, pkg, windowArgs, spec, errs)
		return spec
	}

	// Raw: Above(m, threshold, window) / Below(...) / Absent(m, window).
	switch ctor {
	case "Above":
		spec.Op = "above"
	case "Below":
		spec.Op = "below"
	case "Absent":
		spec.Op = "absent"
	default:
		return nil
	}
	if len(cond.Args) < 2 {
		return spec
	}
	metric, ok := cond.Args[0].(*ast.CallExpr)
	if !ok {
		*errs = append(*errs, &Error{
			Pos:  relPos(dir, pkg.Fset, cond.Args[0].Pos()),
			Msg:  "a condition's metric must be a tako reading — tako.Rate, tako.Count, tako.Sum, tako.Mean, tako.Percentile or tako.Platform",
			Rule: "alert-condition",
		})
		return nil
	}
	mCtor := sdkConstructor(pkg, metric)
	spec.Reading = readingOf[mCtor]
	if spec.Reading == "" {
		*errs = append(*errs, &Error{
			Pos:  relPos(dir, pkg.Fset, cond.Args[0].Pos()),
			Msg:  fmt.Sprintf("tako.%s is not a reading a condition can be written about", mCtor),
			Rule: "alert-condition",
		})
		return nil
	}
	if mCtor == "Platform" && len(metric.Args) > 0 {
		spec.SubjectKind = "platform"
		if name, err := constString(pkg, metric.Args[0]); err == nil {
			spec.SubjectName = name
		} else {
			*errs = append(*errs, &Error{
				Pos:  relPos(dir, pkg.Fset, metric.Args[0].Pos()),
				Msg:  "tako.Platform takes a string constant metric type — it is read at analysis time; find one with `tako metrics <search>`",
				Rule: "const-name",
			})
		}
		for _, f := range metric.Args[1:] {
			if m, err := constString(pkg, f); err == nil {
				spec.Matchers = append(spec.Matchers, m)
			}
		}
	} else if len(metric.Args) > 0 {
		spec.SubjectKind, spec.SubjectName, spec.SubjectVar = resolveSubject(pkg, metric.Args[0], handlersByVar, resVars, endpointVars)
		if mCtor == "Percentile" && len(metric.Args) > 1 {
			spec.Quantile = constFloat(pkg, metric.Args[1])
		}
	}
	rest := cond.Args[1:]
	if spec.Op != "absent" && len(rest) > 0 {
		spec.Threshold = constFloat(pkg, rest[0])
		rest = rest[1:]
	}
	readWindow(dir, pkg, rest, spec, errs)
	return spec
}

// resolveSubject reports what a condition argument names.
func resolveSubject(pkg *packages.Package, arg ast.Expr, handlersByVar map[string]*Handler,
	resVars map[types.Object]resRef, endpointVars map[types.Object]string) (kind, name, v string) {
	var obj types.Object
	switch e := arg.(type) {
	case *ast.Ident:
		obj = pkg.TypesInfo.Uses[e]
	case *ast.SelectorExpr:
		obj = pkg.TypesInfo.Uses[e.Sel]
	}
	if obj == nil {
		return "", "", ""
	}
	if r, ok := resVars[obj]; ok {
		return r.kind, r.name, pkg.Name + "." + obj.Name()
	}
	qualified := pkg.Name + "." + obj.Name()
	if h, ok := handlersByVar[qualified]; ok {
		switch h.Kind {
		case "subscribe":
			return "subscriber", h.Binding, qualified
		case "api", "page":
			// The ROUTE is the matcher: request metrics carry a route
			// label, which is what makes per-endpoint alerting possible.
			return "route", h.Route, qualified
		}
		return h.Kind, h.Binding, qualified
	}
	if ep, ok := endpointVars[obj]; ok {
		return "route", "", ep
	}
	return "service", "", qualified
}

// readWindow reads the optional trailing tako.Window and validates it
// against the ceilings measured on the live API.
func readWindow(dir string, pkg *packages.Package, args []ast.Expr, spec *AlertCondition, errs *ErrorList) {
	for _, a := range args {
		call, ok := a.(*ast.CallExpr)
		if ok && sdkConstructor(pkg, call) == "For" && len(call.Args) == 1 {
			spec.ForSeconds = int(constFloat(pkg, call.Args[0]) / 1e9)
			continue
		}
		if fields, ok := configLit(dir, pkg, a, "Window", errs); ok {
			if e, set := fieldOf(fields, "For"); set {
				spec.ForSeconds = int(constFloat(pkg, e) / 1e9)
			}
			if e, set := fieldOf(fields, "Every"); set {
				spec.EverySeconds = int(constFloat(pkg, e) / 1e9)
			}
		}
	}
	checkWindow(dir, pkg, args, spec, errs)
}

// checkWindow enforces what the API refuses, at analysis time, with the
// real ceiling in the message — never as a deploy-time 400.
func checkWindow(dir string, pkg *packages.Package, args []ast.Expr, spec *AlertCondition, errs *ErrorList) {
	pos := func() ast.Node {
		if len(args) > 0 {
			return args[0]
		}
		return nil
	}
	fail := func(msg, rule string) {
		n := pos()
		if n == nil {
			return
		}
		*errs = append(*errs, &Error{Pos: relPos(dir, pkg.Fset, n.Pos()), Msg: msg, Rule: rule})
	}
	if spec.EverySeconds != 0 && (spec.EverySeconds < 60 || spec.EverySeconds > 90000) {
		fail(fmt.Sprintf("a Window's Every must be between 1m and 25h — Cloud Monitoring refuses anything else; got %ds", spec.EverySeconds), "window")
	}
	if spec.Op != "absent" && spec.ForSeconds > 86400 {
		fail(fmt.Sprintf("a threshold Window's For cannot exceed 24h — Cloud Monitoring refuses longer; got %ds", spec.ForSeconds), "window")
	}
	// Count has no defensible default period: the period IS the count.
	if spec.Reading == "count" && spec.EverySeconds == 0 {
		fail("tako.Count needs a period — write tako.Window{Every: …}; how long you are counting over IS the count, so there is no default worth guessing", "window")
	}
}

func constFloat(pkg *packages.Package, e ast.Expr) float64 {
	tv, ok := pkg.TypesInfo.Types[e]
	if !ok || tv.Value == nil {
		return 0
	}
	f, _ := constant.Float64Val(constant.ToFloat(tv.Value))
	return f
}
