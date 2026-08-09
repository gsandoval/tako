package tako

import "errors"

// Authorization is declared, like everything else.
//
// tako authenticates: Route.SignIn answers 401 before a handler runs and
// guarantees ctx.User past it. Who, among the people who signed in, may
// use THIS route was the app's problem, and every app solved it the same
// way — the same four lines at the top of every handler. The first app to
// carry an admin wrote them 49 times across 12 files, which is 49 chances
// to forget, and no tool could answer "which of my routes can a stranger
// reach" because the answer lived in function bodies.
//
// A Policy moves that decision onto the declaration, where the analyzer
// can read it, `tako gen graph` can print it, and a status page can say
// which door refused. The RULE stays the app's, all of it: tako never
// learns what a role is.

// Policy declares an authorization rule: a function that decides whether
// a signed-in user may proceed.
//
//	var Editors = tako.Policy(editorAllowed)
//
//	func editorAllowed(ctx tako.Ctx, u tako.User) error {
//		if !cfg.IsEditor(u.Email) {
//			return tako.Deny("that address is not on the editor list for this site")
//		}
//		return nil
//	}
//
// Routes opt in with Route.Allow, and one policy serves as many routes
// as share a door. Like every tako constructor this is a side-effect-free
// record, so declaration order never matters.
//
// The function returns nil to admit, tako.Deny to refuse, and any other
// error to say it could not decide — a store read that failed while
// deciding is a 500, not a refusal (see Deny).
//
// It sees the USER, not the request. A policy answers "who may reach this
// route"; whether this user may touch THAT record is a question about the
// record, and it belongs in the handler that loaded it. Splitting them is
// what keeps one policy reusable across 50 routes.
func Policy(fn func(Ctx, User) error) *PolicyHandle {
	return &PolicyHandle{fn: fn}
}

// PolicyHandle is a declared authorization rule, the value tako.Policy
// returns. Assign it to an exported package-level var; routes name it in
// Route.Allow.
type PolicyHandle struct {
	fn func(Ctx, User) error
	// roles is set by RequireRole and read by nothing at runtime: the
	// rule is already in fn. It is here so a policy built from roles can
	// SAY so — the analyzer prints the names beside the door, and a
	// reader of `tako gen graph` learns which role opens it without
	// opening the source.
	roles []string
}

// Roles are the role names a RequireRole policy admits, in declaration
// order; nil for a policy with a hand-written body.
func (p *PolicyHandle) Roles() []string {
	if p == nil {
		return nil
	}
	return append([]string(nil), p.roles...)
}

// Check evaluates the policy. The runtime and Endpoint.Call are the
// callers; user code never needs this.
//
// A handle with no function refuses. That state is refused at build time
// (tako/policy-var), so this is the answer if one ever reaches a request:
// a door whose rule is missing is shut, not open.
func (p *PolicyHandle) Check(ctx Ctx, u User) error {
	if p == nil || p.fn == nil {
		return errors.New("tako: this route names a policy that was never declared")
	}
	return p.fn(ctx, u)
}

// denial is a policy's refusal: a 403 whose message is public by
// declaration. It is a distinct type rather than an Errorf(403, …) so
// that a refusal from YOUR rule and a 403 tako answers on its own
// account — a stale CSRF token, above all — can never be mistaken for
// each other. They are different problems with different remediations,
// and the app that first hit this had to sniff a sentinel string to tell
// them apart.
type denial struct{ msg string }

func (d *denial) Error() string { return d.msg }

// HTTPStatus is how the runtime reads the status of any handler error.
func (d *denial) HTTPStatus() int { return 403 }

// PolicyDenial marks this as a rule's refusal rather than any other 403
// (duck-typed, so pkg/runtime reads it without a type dependency).
func (d *denial) PolicyDenial() bool { return true }

// Deny is what a policy returns to refuse a request: a 403 carrying text
// the visitor may read.
//
//	return tako.Deny("editors sign in with Google; this session came from a password")
//
// The message is PUBLIC by declaration — it reaches the status page as
// Status.Message and, for an API route, the JSON body. That is the whole
// reason it exists as its own constructor: any other error a policy
// returns is treated as a failure to decide, answered 500, and logged
// against the request id rather than rendered. So the only way to say
// something to the caller is to say it deliberately, and an internal
// error can never leak through the door.
//
// Say the MECHANISM when there is one. "Not authorized" is true and
// useless; "that address is not on the editor list — ask the site owner
// to add it" tells someone what to do next, and an editor refused for
// arriving by the wrong provider must not be told to ask for access they
// already have.
func Deny(msg string) error { return &denial{msg: msg} }

// IsDenied reports whether err is a policy's refusal — the caller-side
// twin of Status.Policy.
//
// A cross-package Endpoint.Call returns the callee's denial as an error,
// and a caller usually wants to treat "you may not" differently from
// "it broke".
func IsDenied(err error) bool {
	var d interface{ PolicyDenial() bool }
	return errors.As(err, &d) && d.PolicyDenial()
}

// authorize enforces a route's declared gates for a direct call:
// Route.SignIn and Route.Allow. It is the Call-side of what the
// generated mount does with runtime.RequireAuth and
// runtime.RequirePolicy.
//
// Only for IN-PROCESS calls. A remote hop deliberately does not evaluate
// the callee's policy here: the rule may read the callee's entity store,
// which this service does not have, so a caller-side verdict could be
// confidently wrong in both directions. The session rides the hop and
// the callee's own mount decides.
func authorize(ctx Ctx, cfg []Route) error {
	var allow *PolicyHandle
	needsUser := false
	for _, c := range cfg {
		if c.SignIn {
			needsUser = true
		}
		if c.Allow != nil {
			allow, needsUser = c.Allow, true
		}
	}
	if !needsUser {
		return nil
	}
	u, ok := ctx.MaybeUser()
	if !ok {
		// Before this, an anonymous call reached ctx.User() and panicked.
		// A missing session is an answer, not a crash.
		return Errorf(401, "%s", "sign in to continue")
	}
	if allow == nil {
		return nil
	}
	return allow.Check(ctx, u)
}
