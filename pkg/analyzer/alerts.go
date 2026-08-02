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
	channelVars map[types.Object]string, handlersByVar map[string]*Handler, errs *ErrorList) *Resource {
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
	if bad {
		return nil
	}
	return &Resource{
		Kind:      "alert",
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
