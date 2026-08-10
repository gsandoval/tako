// Package routeconflict holds the pair Go's router cannot order.
//
// This is not hypothetical: it is the exact shape that panicked while
// AB10b's moderation screen was being written, and it was found by
// running the example's tests rather than by analysis. A service that
// does not boot is the most expensive way to learn about a route.
package routeconflict

import "github.com/gsandoval/tako"

// Neither is more specific than the other: a request for
// /moderate/people/retract matches both.
var (
	Retract = tako.Page("POST /moderate/{id}/retract", retract)
	SetRole = tako.Page("POST /moderate/people/{id}", setRole)
)

// IDReq binds the segment both routes carry.
type IDReq struct {
	ID string `path:"id"`
}

// SlugReq is the same for the pair below.
type SlugReq struct {
	Slug string `path:"slug"`
}

// The same trap one method apart is FINE — a literal beats a wildcard —
// and must not be reported, or the rule would refuse the precedence
// trick every app uses.
var (
	Edit = tako.Page("GET /admin/{slug}", edit)
	New  = tako.Page("GET /admin/new", newItem)
)

func retract(ctx tako.Ctx, req IDReq) (tako.HTML, error)  { return tako.HTML{}, nil }
func setRole(ctx tako.Ctx, req IDReq) (tako.HTML, error)  { return tako.HTML{}, nil }
func edit(ctx tako.Ctx, req SlugReq) (tako.HTML, error)   { return tako.HTML{}, nil }
func newItem(ctx tako.Ctx, _ struct{}) (tako.HTML, error) { return tako.HTML{}, nil }
