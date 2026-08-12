package cronconfig

import (
	"time"

	"github.com/gsandoval/tako"
)

// CronConfig's two range checks. Both are Cloud Scheduler's limits,
// refused here rather than as a 400 at deploy time.
var (
	Nightly = tako.Cron("every 24h", nightly, tako.CronConfig{Timezone: "Mars/Olympus"})
	Slow    = tako.Cron("every 1h", slow, tako.CronConfig{Deadline: time.Hour})
)

func nightly(ctx tako.Ctx) error { return nil }
func slow(ctx tako.Ctx) error    { return nil }
