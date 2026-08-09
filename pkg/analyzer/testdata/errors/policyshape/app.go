// Package policyshape holds the three ways a declared door is not one.
//
// A policy is what makes authorization visible to the analyzer, to
// `tako gen graph` and to a status page. Each rule below exists because
// the alternative is authorization tako cannot see or codegen cannot
// name — which is where the app was before Route.Allow.
package policyshape

import "github.com/gsandoval/tako"

// A literal has no name, so nothing can print it, two routes cannot
// share it, and the graph cannot say what guards this route.
var Inline = tako.Page("GET /inline", inline, tako.Route{
	Allow: tako.Policy(func(ctx tako.Ctx, u tako.User) error { return nil }),
})

// An unexported policy cannot be named from the generated service, for
// the same reason an unexported status page cannot.
var editors = tako.Policy(editorAllowed)

var Admin = tako.Page("GET /admin", admin, tako.Route{Allow: editors})

// A policy needs a user; an IAM-only route has none, so this pair can
// only ever refuse every call.
var Editors = tako.Policy(editorAllowed)

var Internal = tako.API("POST /internal/reindex", reindex, tako.Route{
	Allow:   Editors,
	Private: true,
})

func editorAllowed(ctx tako.Ctx, u tako.User) error {
	if u.Email == "" {
		return tako.Deny("that address is not on the editor list for this site")
	}
	return nil
}

func inline(ctx tako.Ctx, _ struct{}) (tako.HTML, error) { return tako.HTML{}, nil }
func admin(ctx tako.Ctx, _ struct{}) (tako.HTML, error)  { return tako.HTML{}, nil }
func reindex(ctx tako.Ctx, _ struct{}) (string, error)   { return "", nil }
