package cronschedule

import "github.com/gsandoval/tako"

// A field out of range, and an interval unix-cron cannot express —
// both refused at analysis time, both naming the judgment.
var Nightly = tako.Cron("0 25 * * *", nightly)

func nightly(ctx tako.Ctx) error { return nil }

var Odd = tako.Cron("every 7m", odd)

func odd(ctx tako.Ctx) error { return nil }
