package oneservice

import "github.com/gsandoval/tako"

// One deployable, one declaration — the tako.Site rule, one kind over.
var (
	api   = tako.Service(tako.WithDomain("api.example.com"))
	admin = tako.Service(tako.WithProfile(tako.Latency))
)

var Ping = tako.API("GET /ping", ping)

func ping(ctx tako.Ctx, _ struct{}) (string, error) { return "pong", nil }
