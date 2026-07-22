package cronschedule

import "github.com/gsandoval/tako"

var Nightly = tako.Cron("0 3 * * *", nightly)

func nightly(ctx tako.Ctx) error { return nil }

var Ping = tako.API("GET /api/ping", ping)

func ping(ctx tako.Ctx, _ struct{}) (string, error) { return "pong", nil }
