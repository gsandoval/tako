package cronmissedlong

import "github.com/gsandoval/tako"

// A weekly backup is exactly the cron whose silence is invisible, and it
// is the one absence Cloud Monitoring cannot express: the ceiling on a
// lookback over a custom metric is 1d1h, and any window it accepts is
// shorter than the cron's own interval.
var (
	oncall = tako.AlertEmail("ops@example.com")

	Backup       = tako.Cron("0 0 * * 0", backup)
	BackupSilent = tako.Alert(tako.CronMissed(Backup), oncall)
)

func backup(ctx tako.Ctx) error { return nil }
