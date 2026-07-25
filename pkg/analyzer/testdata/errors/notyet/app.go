package notyet

import "github.com/gsandoval/tako"

var Home = tako.Page("GET /", home)

func home(ctx tako.Ctx, _ struct{}) (tako.HTML, error) { return tako.HTML{}, nil }

var reindex = tako.Job[Params]("reindex")

type Params struct{ Since string }

var Ping = tako.API("GET /api/ping", ping)

func ping(ctx tako.Ctx, _ struct{}) (string, error) { return "pong", nil }
