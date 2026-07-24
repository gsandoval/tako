package notyet

import "github.com/gsandoval/tako"

var site = tako.Site("./web", tako.SPA())

var reindex = tako.Job[Params]("reindex")

type Params struct{ Since string }

var Ping = tako.API("GET /api/ping", ping)

func ping(ctx tako.Ctx, _ struct{}) (string, error) { return "pong", nil }
