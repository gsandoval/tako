package oneservice

import "github.com/gsandoval/tako"

// One deployable, one declaration — the tako.Site rule, one kind over.
var (
	api   = tako.Service(tako.ServiceConfig{Domain: "api.example.com"})
	admin = tako.Service(tako.ServiceConfig{Profile: tako.ProfileLatency})
)

var Ping = tako.API("GET /ping", ping)

func ping(ctx tako.Ctx, _ struct{}) (string, error) { return "pong", nil }
