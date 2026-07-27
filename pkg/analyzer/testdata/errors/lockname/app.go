package lockname

import "github.com/gsandoval/tako"

var name = "reindex"
var relock = tako.Lock(name)

var Ping = tako.API("GET /api/ping", ping)

func ping(ctx tako.Ctx, _ struct{}) (string, error) { _ = relock; return "pong", nil }
