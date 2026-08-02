package alerts

import (
	"time"

	"github.com/gsandoval/tako"
)

var (
	thumbs = tako.Queue[string]("thumbs")
	lat    = tako.Histogram("thumb_ms")

	oncall = tako.Email("ops@example.com")
	// Not a constant: destinations are read at analysis time.
	loose = tako.Email(address())

	Work    = tako.Worker(thumbs, work)
	Cleanup = tako.Cron("every 24h", tick)
)

func address() string                   { return "x@y.z" }
func work(ctx tako.Ctx, s string) error { return nil }
func tick(ctx tako.Ctx) error           { return nil }

var (
	// CronMissed watches a cron; Work is a worker. Every async handler
	// is a tako.Handler, so only the analyzer can catch this.
	WrongKind = tako.Alert(tako.CronMissed(Work), oncall)

	// A quantile is a fraction, not a percentage.
	BadQuantile = tako.Alert(tako.SlowerThan(Photos, time.Second, 99), oncall)
	BadPct      = tako.Alert(tako.Above(tako.Percentile(lat, 99), 10, tako.For(time.Minute)), oncall)

	// A destination that is not a declared channel var.
	Undeclared = tako.Alert(tako.CronMissed(Cleanup), tako.Email("adhoc@example.com"))
)

var Photos = tako.API("GET /photos", list)

func list(ctx tako.Ctx, _ struct{}) (string, error) { return "", nil }

var _ = loose
