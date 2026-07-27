package lockopt

import (
	"time"

	"github.com/gsandoval/tako"
)

var relock = tako.Lock("reindex", tako.WithTTL(time.Minute))

var Ping = tako.API("GET /api/ping", ping)

func ping(ctx tako.Ctx, _ struct{}) (string, error) { _ = relock; return "pong", nil }
