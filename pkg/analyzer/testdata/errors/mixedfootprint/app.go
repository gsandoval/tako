package mixedfootprint

import "github.com/gsandoval/tako"

// Crons run in their package's APP service alongside API and Page
// handlers, and they accept WithFootprint — so two of them are the way
// two different claims can still land on one service. (Workers,
// subscribers, jobs and singletons each own their service, and
// tako.Service is one per package, so those cannot collide.)
var Sweep = tako.Cron("every 1h", sweep, tako.CronConfig{Footprint: tako.Footprint{CPU: 2}})
var Purge = tako.Cron("every 2h", sweep, tako.CronConfig{Footprint: tako.Footprint{CPU: 4, Memory: "2Gi"}})

func sweep(ctx tako.Ctx) error { return nil }
