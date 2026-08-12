package mixedprofile

import "github.com/gsandoval/tako"

// Two crons in one package land on one app service, and a service scales
// one way — so two different profile claims cannot both win. (Workers,
// subscribers, jobs and singletons each own a service, so only handlers
// that SHARE one can collide.)
var Web = tako.Cron("every 1h", sweep, tako.CronConfig{Profile: tako.ProfileLatency})
var Sweep = tako.Cron("every 2h", sweep, tako.CronConfig{Profile: tako.ProfileBatch})

func sweep(ctx tako.Ctx) error { return nil }
