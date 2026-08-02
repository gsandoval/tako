package configliteral

import "github.com/gsandoval/tako"

// Configuration is read from SOURCE at analysis time. A config held in
// a variable compiles perfectly and would then be invisible to the
// deploy — the one thing about the config structs a type cannot check.

var shared = tako.Compute{Profile: tako.ProfileBatch}

var (
	q = tako.Queue[string]("work")

	Handle = tako.Worker(q, handle, shared)

	// A computed field is the same problem one level down.
	Nightly = tako.Cron("every 24h", tick, tako.CronConfig{Timezone: zone()})
)

func zone() string { return "America/Mexico_City" }

func handle(ctx tako.Ctx, s string) error { return nil }
func tick(ctx tako.Ctx) error             { return nil }

var Ping = tako.API("GET /ping", ping)

func ping(ctx tako.Ctx, _ struct{}) (string, error) { return "pong", nil }
