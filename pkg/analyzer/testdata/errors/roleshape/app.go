// Package roleshape holds the ways a declared role is not one.
//
// A role's name is read at analysis time — printed beside the door in
// `tako gen graph`, handed to the dev sign-in page so a laptop can hold
// one. A name that is not there at analysis time is a door that opens
// for nobody, and it fails exactly like a rule that is merely strict.
package roleshape

import "github.com/gsandoval/tako"

// Not a constant: nothing can read this name before the program runs.
var Editor = tako.Role(roleName())

func roleName() string { return "editor" }

// Unexported: the generated service cannot name it.
var moderator = tako.Role("moderator")

// A door with no key. It refuses everyone, silently.
var Nobody = tako.RequireRole()

// A role that was never declared as a var.
var Desk = tako.RequireRole(tako.Role("admin"))

var (
	Moderate = tako.Page("GET /moderate", moderate, tako.Route{Allow: Nobody})
	Admin    = tako.Page("GET /admin", admin, tako.Route{Allow: Desk})
)

func moderate(ctx tako.Ctx, _ struct{}) (tako.HTML, error) { return tako.HTML{}, nil }
func admin(ctx tako.Ctx, _ struct{}) (tako.HTML, error)    { return tako.HTML{}, nil }
