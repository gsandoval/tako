package absencewindow

import (
	"time"

	"github.com/gsandoval/tako"
)

// An explicit window past the ceiling. The threshold form of this check
// has always existed; the absence form did not, which is how a 48h
// lookback reached a deploy and came back as HTTP 400.
var (
	oncall = tako.AlertEmail("ops@example.com")

	Nightly = tako.Cron("every 24h", nightly)
	Silent  = tako.Alert(tako.CronMissed(Nightly, tako.Window{For: 48 * time.Hour}), oncall)
)

func nightly(ctx tako.Ctx) error { return nil }
