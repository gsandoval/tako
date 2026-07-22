package cronopt

import "github.com/gsandoval/tako"

var Nightly = tako.Cron("every 24h", nightly, tako.WithParallel(10))

func nightly(ctx tako.Ctx) error { return nil }

var Ping = tako.API("GET /api/ping", ping)

func ping(ctx tako.Ctx, _ struct{}) (string, error) { return "pong", nil }
