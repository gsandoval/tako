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
	"strings"
	"time"

	"github.com/gsandoval/tako/internal/metrics"

	"golang.org/x/tools/go/packages"
)

// channelKinds maps the SDK constructor to the Cloud Monitoring channel
// type. The values are Google's, verified against the notification
// channel descriptors.
var channelKinds = map[string]string{
	"AlertEmail":   "email",
	"AlertWebhook": "webhook_tokenauth",
	"AlertTopic":   "pubsub",
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
	if ctor == "AlertTopic" {
		topic, ok := resolveTopicVar(pkg, call.Args[0], topicVars)
		if !ok {
			*errs = append(*errs, &Error{
				Pos:  relPos(dir, pkg.Fset, call.Args[0].Pos()),
				Msg:  "tako.AlertTopic must name a topic declared as a package-level var in this module",
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
		fail(call.Args[0], "tako.Alert takes a condition — tako.WhenAbove, tako.WhenBelow, tako.WhenAbsent, or a preset like tako.QueueBacklogged", "alert-condition")
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
			fail(arg, "tako.Alert destinations must be channels declared as package-level vars in this module (tako.AlertEmail, tako.AlertWebhook, tako.AlertTopic)", "channel-binding")
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
	// queue marks the two conditions whose last argument is a
	// QueueWatch rather than a Window: they carry a source as well as a
	// window, and their For is required rather than defaulted.
	queue bool
}{
	"QueueBacklogged":   {reading: "queue_depth", hasValue: true, valueArg: 1, quantArg: -1, op: "atleast", queue: true},
	"QueueStalled":      {reading: "queue_age", unit: time.Second, hasValue: true, valueArg: 1, quantArg: -1, op: "above", queue: true},
	"CronFailed":        {reading: "cron_failed", subject: "cron", hasValue: true, valueArg: 1, quantArg: -1, op: "atleast"},
	"CronMissed":        {reading: "cron_missed", subject: "cron", quantArg: -1, op: "absent"},
	"JobFailed":         {reading: "job_failed", subject: "job", hasValue: true, valueArg: 1, quantArg: -1, op: "atleast"},
	"RequestsErroring":  {reading: "error_rate", hasValue: true, valueArg: 1, quantArg: -1, op: "above"},
	"RequestsSlow":      {reading: "latency", unit: time.Millisecond, hasValue: true, valueArg: 1, quantArg: 2, op: "above"},
	"ServiceAtCapacity": {reading: "at_capacity", quantArg: -1, op: "above"},
	"SingletonDown":     {reading: "singleton_down", subject: "singleton", quantArg: -1, op: "below"},
}

// readingMethods are the reading methods on a Counter or Histogram
// handle. Readings moved off the tako namespace and onto the handle
// they read (spec rule 6), so the analyzer resolves a METHOD call here,
// not a constructor.
var readingMethods = map[string]string{
	"Rate": "rate", "Count": "count", "Sum": "sum",
	"Mean": "mean", "Percentile": "percentile",
}

// scopedMetrics are the constructors that read a Google metric about a
// tako object. The object supplies the resource matcher, which is the
// half a user could not write correctly by hand.
// The layout is data rather than a switch so a new one cannot be added
// without saying where its arguments are — the completeness habit that
// the preset table already has.
var scopedMetrics = map[string]struct {
	reading string
	scoped  bool // takes a tako object as its first argument
	hasType bool // takes a Google metric type
	// quantArg is the index of a quantile argument, -1 for none.
	quantArg int
	// supplies are the labels the deploy writes into the selector
	// itself. A user matcher on one of them is REFUSED rather than
	// merged: PromQL takes both, so `{subscription_id="x",
	// subscription_id="x-dead"}` is a selector that matches nothing and
	// reads as a healthy queue. Listed per constructor so a new one
	// cannot be added without saying which labels it owns.
	supplies []string
}{
	"ServiceMetric":     {reading: "service_metric", scoped: true, hasType: true, quantArg: -1, supplies: []string{"service_name"}},
	"QueueMetric":       {reading: "queue_metric", scoped: true, hasType: true, quantArg: -1, supplies: []string{"subscription_id"}},
	"ServicePercentile": {reading: "service_percentile", scoped: true, hasType: true, quantArg: 2, supplies: []string{"service_name"}},
	"ServiceInstances":  {reading: "service_instances", scoped: true, quantArg: -1, supplies: []string{"service_name", "state"}},
	"PlatformMetric":    {reading: "platform", hasType: true, quantArg: -1},
}

// monitoredResource is supplied by every selector tako builds, scoped or
// not — a custom-metric selector is refused without it, so it is never
// the user's to write.
const monitoredResource = "monitored_resource"

// scopedLabelMessage explains why a matcher on a tako-supplied label is
// refused, and points at the thing that DOES ask the question — a
// refusal that names no alternative just moves the dead end.
func scopedLabelMessage(ctor, label string) string {
	switch label {
	case monitoredResource:
		return fmt.Sprintf("tako.%s already matches %s — it is derived from the metric type, and a second matcher on the same label matches nothing; drop it",
			ctor, label)
	case "subscription_id":
		return fmt.Sprintf("tako.%s already matches %s for the consumer you named — a second matcher on the same label matches nothing, and the deployed subscription is env-prefixed so it cannot be written by hand; for the dead-letter subscription use tako.QueueBacklogged or tako.QueueStalled with tako.QueueWatch{DeadLetters: true}",
			ctor, label)
	case "state":
		return fmt.Sprintf("tako.%s already matches %s=\"active\" — an instance that is not active is not one you are paying to serve with, so there is one right answer and tako supplies it",
			ctor, label)
	}
	return fmt.Sprintf("tako.%s already matches %s for the target you named — a second matcher on the same label matches nothing, and the deployed name is env-prefixed so it cannot be written by hand; drop it",
		ctor, label)
}

// matcherLabel is the label a raw PromQL matcher constrains:
// `route="GET /x"` → route. Anything unparseable is left to the deploy,
// which is where a malformed matcher becomes a 400 with Google's own
// explanation of the syntax.
func matcherLabel(m string) string {
	i := strings.IndexAny(m, "=!~")
	if i <= 0 {
		return ""
	}
	return strings.TrimSpace(m[:i])
}

// conditionSpec reads a condition call into its logical parameters.
func conditionSpec(dir string, pkg *packages.Package, cond *ast.CallExpr, ctor string,
	handlersByVar map[string]*Handler, resVars map[types.Object]resRef, endpointVars map[types.Object]string,
	errs *ErrorList) *AlertCondition {

	spec := &AlertCondition{Ctor: ctor}
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
		if p.queue {
			readQueueWatch(dir, pkg, ctor, windowArgs, spec, errs)
			return spec
		}
		readWindow(dir, pkg, windowArgs, spec, errs)
		return spec
	}

	// Raw: WhenAbove(m, threshold, window) / WhenBelow / WhenAbsent.
	switch ctor {
	case "WhenAbove":
		spec.Op = "above"
	case "WhenBelow":
		spec.Op = "below"
	case "WhenAtLeast":
		spec.Op = "atleast"
	case "WhenAbsent":
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
			Msg:  "a condition's metric must be a tako reading — a method on a Counter or Histogram (uploads.Rate(), thumbMs.Percentile(0.99)) or tako.ServiceMetric / tako.QueueMetric / tako.PlatformMetric",
			Rule: "alert-condition",
		})
		return nil
	}
	if !readMetric(dir, pkg, metric, spec, handlersByVar, resVars, endpointVars, errs) {
		return nil
	}
	rest := cond.Args[1:]
	if spec.Op != "absent" && len(rest) > 0 {
		spec.Threshold = constFloat(pkg, rest[0])
		rest = rest[1:]
	}
	readWindow(dir, pkg, rest, spec, errs)
	return spec
}

// readMetric resolves the thing a raw condition is written about: a
// reading METHOD on a declared handle, or one of the scoped-metric
// constructors.
func readMetric(dir string, pkg *packages.Package, metric *ast.CallExpr, spec *AlertCondition,
	handlersByVar map[string]*Handler, resVars map[types.Object]resRef,
	endpointVars map[types.Object]string, errs *ErrorList) bool {

	// uploads.Rate() / thumbMs.Percentile(0.99): the receiver is the
	// declaration, the method is the reading.
	if sel, ok := metric.Fun.(*ast.SelectorExpr); ok {
		if reading, isReading := readingMethods[sel.Sel.Name]; isReading {
			if kind, name, v := resolveSubject(pkg, sel.X, handlersByVar, resVars, endpointVars); kind == "counter" || kind == "histogram" {
				spec.Reading, spec.SubjectKind, spec.SubjectName, spec.SubjectVar = reading, kind, name, v
				if sel.Sel.Name == "Percentile" && len(metric.Args) > 0 {
					spec.Quantile = constFloat(pkg, metric.Args[0])
				}
				return true
			}
		}
	}

	// tako.Requests(Boom).Status("5xx").Rate(): a reading method whose
	// receiver is a CHAIN back to a framework-metric constructor rather
	// than a declared handle.
	if readFrameworkMetric(dir, pkg, metric, spec, handlersByVar, resVars, endpointVars, errs) {
		return spec.Framework != ""
	}

	ctor := sdkConstructor(pkg, metric)
	m, isScoped := scopedMetrics[ctor]
	if !isScoped {
		*errs = append(*errs, &Error{
			Pos:  relPos(dir, pkg.Fset, metric.Pos()),
			Msg:  "a condition's metric must be a tako reading — a method on a Counter or Histogram (uploads.Rate(), thumbMs.Percentile(0.99)), on tako.Requests / tako.RequestMs, or tako.ServiceMetric / tako.ServicePercentile / tako.ServiceInstances / tako.QueueMetric / tako.PlatformMetric",
			Rule: "alert-condition",
		})
		return false
	}
	spec.Reading = "platform"
	typeArg := 0
	if m.scoped {
		if len(metric.Args) == 0 {
			return false
		}
		spec.SubjectKind, spec.SubjectName, spec.SubjectVar = resolveSubject(pkg, metric.Args[0], handlersByVar, resVars, endpointVars)
		spec.ScopedTo = m.reading
		typeArg = 1
	} else {
		spec.SubjectKind = "platform"
	}
	// ServiceInstances names no metric type: there is exactly one right
	// answer and tako supplies it.
	if !m.hasType {
		return true
	}
	if len(metric.Args) <= typeArg {
		return false
	}
	name, err := constString(pkg, metric.Args[typeArg])
	if err != nil {
		*errs = append(*errs, &Error{
			Pos:  relPos(dir, pkg.Fset, metric.Args[typeArg].Pos()),
			Msg:  fmt.Sprintf("tako.%s takes a string constant metric type — it is read at analysis time; find one with `tako metrics <search>`", ctor),
			Rule: "const-name",
		})
		return false
	}
	if m.scoped {
		spec.MetricType = name
	} else {
		spec.SubjectName = name
	}
	rest := typeArg + 1
	if m.quantArg >= 0 {
		if len(metric.Args) > m.quantArg {
			spec.Quantile = constFloat(pkg, metric.Args[m.quantArg])
		}
		rest = m.quantArg + 1
	}
	for _, f := range metric.Args[min(rest, len(metric.Args)):] {
		mm, err := constString(pkg, f)
		if err != nil {
			continue
		}
		if label := matcherLabel(mm); label != "" {
			owned := label == monitoredResource
			for _, s := range m.supplies {
				owned = owned || label == s
			}
			if owned {
				*errs = append(*errs, &Error{
					Pos:  relPos(dir, pkg.Fset, f.Pos()),
					Msg:  scopedLabelMessage(ctor, label),
					Rule: "scoped-label",
				})
				continue
			}
		}
		spec.Matchers = append(spec.Matchers, mm)
	}
	return true
}

// frameworkReadings are the reading methods each request handle carries.
// Data rather than a switch, so a method cannot be added to the SDK
// without saying which metric it reads and whether it is legal there —
// RequestMs has no Fraction and Requests has no Percentile, and both
// facts belong in one place.
var frameworkReadings = map[string]map[string]string{
	"Requests": {
		"Rate": "rate", "Count": "count", "Fraction": "fraction",
	},
	"RequestMs": {
		"Rate": "rate", "Count": "count", "Sum": "sum",
		"Mean": "mean", "Percentile": "percentile",
	},
}

// frameworkMetricNames maps the SDK constructor to the metric it reads.
var frameworkMetricNames = map[string]string{
	"Requests":  metrics.RequestsName,
	"RequestMs": metrics.RequestMsName,
}

// readFrameworkMetric resolves tako.Requests(x)[.Status(c)].Reading().
//
// It reports whether the expression WAS one of these — not whether it
// was valid — so a malformed chain produces an error here rather than
// falling through to "that is not a tako reading", which would name the
// wrong problem.
func readFrameworkMetric(dir string, pkg *packages.Package, metric *ast.CallExpr, spec *AlertCondition,
	handlersByVar map[string]*Handler, resVars map[types.Object]resRef,
	endpointVars map[types.Object]string, errs *ErrorList) bool {

	sel, ok := metric.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	method := sel.Sel.Name
	// Unwind the chain to its constructor, collecting the narrowing on
	// the way down.
	status := ""
	cur, _ := sel.X.(*ast.CallExpr)
	var ctor string
	for cur != nil {
		c := sdkConstructor(pkg, cur)
		if _, isFramework := frameworkMetricNames[c]; isFramework {
			ctor = c
			break
		}
		if c == "" {
			// A method on something else entirely.
			inner, isCall := cur.Fun.(*ast.SelectorExpr)
			if !isCall {
				return false
			}
			if inner.Sel.Name != "Status" {
				return false
			}
			if len(cur.Args) > 0 {
				if v, err := constString(pkg, cur.Args[0]); err == nil {
					status = v
				}
			}
			cur, _ = inner.X.(*ast.CallExpr)
			continue
		}
		return false
	}
	if ctor == "" {
		return false
	}

	// A method the handle does not have cannot reach here: RequestsHandle
	// and RequestMsHandle are distinct types, so the compiler refuses
	// Requests(x).Percentile(…) and RequestMs(x).Fraction() before the
	// analyzer loads the package. The table maps names to readings; it
	// does not need to police them.
	reading, isReading := frameworkReadings[ctor][method]
	if !isReading {
		return false
	}
	if len(cur.Args) == 0 {
		return true
	}
	spec.SubjectKind, spec.SubjectName, spec.SubjectVar = resolveSubject(pkg, cur.Args[0], handlersByVar, resVars, endpointVars)
	spec.Reading = reading
	spec.Framework = frameworkMetricNames[ctor]
	spec.Status = status
	if method == "Percentile" && len(metric.Args) > 0 {
		spec.Quantile = constFloat(pkg, metric.Args[0])
	}
	if status != "" && !validStatusClass(status) {
		*errs = append(*errs, &Error{
			Pos: relPos(dir, pkg.Fset, metric.Pos()),
			Msg: fmt.Sprintf("tako records a status CLASS, not a code — Status(%q) matches nothing; use one of %s",
				status, orList(metrics.StatusClasses)),
			Rule: "status-class",
		})
	}
	if reading == "fraction" && status == "" {
		*errs = append(*errs, &Error{
			Pos:  relPos(dir, pkg.Fset, sel.Sel.Pos()),
			Msg:  "tako.Requests(…).Fraction() with no Status is every request over every request, which is always 1 — narrow it, e.g. Requests(x).Status(\"5xx\").Fraction()",
			Rule: "alert-condition",
		})
	}
	return true
}

func validStatusClass(s string) bool {
	for _, v := range metrics.StatusClasses {
		if v == s {
			return true
		}
	}
	return false
}

func orList(vs []string) string {
	if len(vs) < 2 {
		return strings.Join(vs, "")
	}
	return strings.Join(vs[:len(vs)-1], ", ") + " or " + vs[len(vs)-1]
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

// readQueueWatch reads a QueueWatch: which queue, and the REQUIRED
// sustain window.
//
// For is required because a queue crosses any depth for an instant every
// time a burst arrives — how long it stays there IS the alert. Same
// reasoning as a Count's period, and the same refusal to guess.
func readQueueWatch(dir string, pkg *packages.Package, ctor string, args []ast.Expr, spec *AlertCondition, errs *ErrorList) {
	spec.On = "live"
	var pos ast.Node
	if len(args) > 0 {
		pos = args[0]
		if fields, ok := configLit(dir, pkg, args[0], "QueueWatch", errs); ok {
			if e, set := fieldOf(fields, "DeadLetters"); set {
				if b, ok := constBool(pkg, e); ok && b {
					spec.On = "dead"
				}
			}
			if e, set := fieldOf(fields, "For"); set {
				spec.ForSeconds = int(constFloat(pkg, e) / 1e9)
			}
		}
	}
	if spec.ForSeconds == 0 && pos != nil {
		*errs = append(*errs, &Error{
			Pos:  relPos(dir, pkg.Fset, pos.Pos()),
			Msg:  fmt.Sprintf("tako.%s needs a sustain window — write tako.QueueWatch{For: …}; a queue crosses any depth for an instant, so how long it stays there IS the alert", ctor),
			Rule: "window",
		})
	}
	checkWindow(dir, pkg, args, spec, errs)
}

func constBool(pkg *packages.Package, e ast.Expr) (bool, bool) {
	tv, ok := pkg.TypesInfo.Types[e]
	if !ok || tv.Value == nil {
		return false, false
	}
	return constant.BoolVal(tv.Value), true
}

// readWindow reads the optional trailing tako.Window and validates it
// against the ceilings measured on the live API.
func readWindow(dir string, pkg *packages.Package, args []ast.Expr, spec *AlertCondition, errs *ErrorList) {
	for _, a := range args {
		call, ok := a.(*ast.CallExpr)
		_ = call
		_ = ok
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
		fail("a Count reading needs a period — write tako.Window{Every: …}; how long you are counting over IS the count, so there is no default worth guessing", "window")
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
