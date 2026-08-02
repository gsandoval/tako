package analyzer

import (
	"fmt"
	"go/ast"
	"go/constant"
	"time"

	"golang.org/x/tools/go/packages"
)

// DeliveryDefaults is the contract every queue and topic gets unless it
// says otherwise: local's failure semantics, made explicit so the cloud
// can be held to them. One table, like ProfileKnobs.
var DeliveryDefaults = Delivery{
	MaxAttempts:       5,
	MinBackoffSeconds: 10,
	MaxBackoffSeconds: 300,
	RateLimit:         0, // no cap: plain Pub/Sub push, no Tasks queue
}

// Delivery is a carrier's declared delivery contract. Zero fields
// inherit DeliveryDefaults.
type Delivery struct {
	MaxAttempts       int
	MinBackoffSeconds int
	MaxBackoffSeconds int
	RateLimit         int
}

func (d *Delivery) attempts() int {
	if d == nil || d.MaxAttempts == 0 {
		return DeliveryDefaults.MaxAttempts
	}
	return d.MaxAttempts
}

func (d *Delivery) minBackoff() int {
	if d == nil || d.MinBackoffSeconds == 0 {
		return DeliveryDefaults.MinBackoffSeconds
	}
	return d.MinBackoffSeconds
}

func (d *Delivery) maxBackoff() int {
	if d == nil || d.MaxBackoffSeconds == 0 {
		return DeliveryDefaults.MaxBackoffSeconds
	}
	return d.MaxBackoffSeconds
}

func (d *Delivery) rate() int {
	if d == nil {
		return 0
	}
	return d.RateLimit
}

// deliveryArg reads and validates a WithDelivery literal. The bounds are
// the BACKENDS' bounds, stated as the framework's: a value tako would
// have to silently clamp is one the analyzer refuses instead.
func deliveryArg(dir string, pkg *packages.Package, opt ast.Expr, kind string, errs *ErrorList) *Delivery {
	bad := false
	fail := func(pos ast.Node, msg string) {
		*errs = append(*errs, &Error{Pos: relPos(dir, pkg.Fset, pos.Pos()), Msg: msg, Rule: "delivery"})
		bad = true
	}
	lit, ok := opt.(*ast.CompositeLit)
	if !ok {
		fail(opt, "tako.Delivery must be written as a literal with constant fields — the contract is read at analysis time, not run time")
		return nil
	}
	d := &Delivery{}
	for _, el := range lit.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			fail(el, "tako.Delivery fields must be named (MaxAttempts: 3, MinBackoff: time.Second, …) — positional literals hide which knob is which")
			return nil
		}
		key := kv.Key.(*ast.Ident).Name
		tv, ok := pkg.TypesInfo.Types[kv.Value]
		if !ok || tv.Value == nil {
			fail(kv.Value, fmt.Sprintf("delivery %s must be a constant — the contract is read at analysis time, not run time", key))
			continue
		}
		n, _ := constant.Int64Val(tv.Value)
		switch key {
		case "MaxAttempts":
			d.MaxAttempts = int(n)
		case "RateLimit":
			d.RateLimit = int(n)
		case "MinBackoff", "MaxBackoff":
			if n > 0 && n < int64(time.Second) {
				fail(kv.Value, fmt.Sprintf("delivery %s must be at least one second; got %s", key, time.Duration(n)))
			}
			if key == "MinBackoff" {
				d.MinBackoffSeconds = int(n / int64(time.Second))
			} else {
				d.MaxBackoffSeconds = int(n / int64(time.Second))
			}
		}
	}
	// Pub/Sub's dead-letter policy accepts 5–100 attempts and nothing
	// else. tako will not clamp a number the user wrote down.
	if d.MaxAttempts != 0 && (d.MaxAttempts < 5 || d.MaxAttempts > 100) {
		fail(lit, fmt.Sprintf("delivery MaxAttempts must be 5–100 — Pub/Sub's dead-letter policy accepts no other range, and tako will not quietly clamp yours; got %d", d.MaxAttempts))
	}
	if d.MinBackoffSeconds < 0 || d.MinBackoffSeconds > 600 {
		fail(lit, fmt.Sprintf("delivery MinBackoff must be between 1s and 10m; got %s", time.Duration(d.MinBackoffSeconds)*time.Second))
	}
	if d.MaxBackoffSeconds < 0 || d.MaxBackoffSeconds > 600 {
		fail(lit, fmt.Sprintf("delivery MaxBackoff must be between 1s and 10m; got %s", time.Duration(d.MaxBackoffSeconds)*time.Second))
	}
	if d.minBackoff() > d.maxBackoff() {
		fail(lit, fmt.Sprintf("delivery MinBackoff (%s) is longer than MaxBackoff (%s) — backoff grows, it does not shrink",
			time.Duration(d.minBackoff())*time.Second, time.Duration(d.maxBackoff())*time.Second))
	}
	if d.RateLimit != 0 && (d.RateLimit < 1 || d.RateLimit > 500) {
		fail(lit, fmt.Sprintf("delivery RateLimit must be 1–500 messages per second (Cloud Tasks' dispatch ceiling); got %d", d.RateLimit))
	}
	// A topic fans out to independent subscribers with independent
	// fates; a rate cap belongs to ONE consumer, so there is nothing for
	// it to mean here.
	if kind == "topic" && d.RateLimit != 0 {
		fail(lit, "delivery RateLimit is a queue setting — a topic fans out to subscribers with independent fates, so there is no single consumer to pace; cap the queue each subscriber feeds instead")
	}
	if bad {
		return nil
	}
	return d
}

// stringOptionArg reads a single constant string option argument.
func stringOptionArg(pkg *packages.Package, opt ast.Expr) (string, bool) {
	call, ok := opt.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return "", false
	}
	tv, ok := pkg.TypesInfo.Types[call.Args[0]]
	if !ok || tv.Value == nil || tv.Value.Kind() != constant.String {
		return "", false
	}
	return constant.StringVal(tv.Value), true
}
