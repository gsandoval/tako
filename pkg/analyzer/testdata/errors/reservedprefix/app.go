package reservedprefix

import "github.com/gsandoval/tako"

// Every shape a user might reach for inside tako's namespace: a page at
// the root of it, an API in the api room, one in the reserved z room,
// and the bare namespace itself.
var (
	Sneaky    = tako.Page("GET /_tako/sneaky", sneaky)
	Shadow    = tako.API("POST /_tako/api/session", shadow)
	Dashboard = tako.Page("GET /_tako/z/dashboard", sneaky)
	Root      = tako.Page("GET /_tako", sneaky)
)

// A route that merely starts with the same letters is the app's own.
var Fine = tako.Page("GET /_takoish/page", sneaky)

func sneaky(ctx tako.Ctx, _ struct{}) (tako.HTML, error) { return tako.HTML{}, nil }

func shadow(ctx tako.Ctx, _ struct{}) (string, error) { return "", nil }
