package tako

import "strings"

// Roles: the one thing tako knows about who somebody is, beyond that
// they signed in.
//
// A role is a NAME an identity carries into the request. In the cloud it
// rides in an Identity Platform custom claim; on a laptop it lives on the
// local user record. What the name means — who may hold it, what it
// unlocks — is the app's, and tako never learns it.
//
// The declaration exists so the name is not a string typed twice. A
// misspelled role in a Route.Allow is a door that never opens, and it
// opens no differently from a door whose rule is simply strict: nothing
// fails, nobody gets in, and the log says the request was refused
// exactly as designed.

// Role declares a role name. The name must be a string constant
// (analyzer-enforced) — it is read at analysis time so a route's door can
// be named in the graph.
//
//	var Editor = tako.Role("editor")
//
// Assign it to an exported package-level var like every other tako
// declaration; the generated service names it from outside the package.
func Role(name string) RoleHandle { return RoleHandle{name: name} }

// RoleHandle is a declared role, the value tako.Role returns.
type RoleHandle struct{ name string }

// Name is the role as it appears in a user's claim.
func (r RoleHandle) Name() string { return r.name }

// RequireRole is the policy for the commonest door there is: hold one of
// these roles and you may pass.
//
//	var Editor  = tako.Role("editor")
//	var Editors = tako.RequireRole(Editor)
//
//	var AdminHome = tako.Page("GET /admin", adminHome, tako.Route{Allow: Editors})
//
// It returns an ordinary tako.Policy, so it goes exactly where a
// hand-written one goes and is enforced at exactly the same point
// (Endpoint.Call, so a cross-service call cannot walk past it).
//
// ANY of the roles admits, because that is what a door with two keys is.
// A rule that needs two roles at once, or a role plus something else, is
// a tako.Policy with a body — RequireRole is the shorthand, not the
// ceiling.
func RequireRole(roles ...RoleHandle) *PolicyHandle {
	names := make([]string, 0, len(roles))
	for _, r := range roles {
		names = append(names, r.name)
	}
	return &PolicyHandle{
		roles: names,
		fn: func(_ Ctx, u User) error {
			for _, want := range names {
				if u.hasRole(want) {
					return nil
				}
			}
			// The refusal names the role. A role name is not a secret,
			// and "not authorized" is the message that sends somebody to
			// ask a question nobody can answer.
			return Deny("this page needs the " + orList(names) + " role")
		},
	}
}

// orList renders role names for a refusal: "editor", or "editor or
// moderator", or "editor, moderator or admin".
func orList(names []string) string {
	switch len(names) {
	case 0:
		return "(none)"
	case 1:
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
}

// HasRole reports whether the user carries the declared role.
//
//	if ctx.User().HasRole(Editor) { … }
//
// For the common case — a whole route behind a role — prefer
// Route{Allow: tako.RequireRole(Editor)}, which keeps the check out of
// the handler and puts it in the graph. This is for the rest: a page
// that shows one extra button.
func (u User) HasRole(role RoleHandle) bool { return u.hasRole(role.name) }

func (u User) hasRole(name string) bool {
	for _, have := range u.Roles {
		if have == name {
			return true
		}
	}
	return false
}
