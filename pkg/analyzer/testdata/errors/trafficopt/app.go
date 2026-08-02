package trafficopt

import (
	"time"

	"github.com/gsandoval/tako"
)

var (
	crunch = tako.Job[string]("crunch")

	// A job task HAS a timeout, so Timeout is fine here — what an
	// execution does not have is clients.
	Crunch = tako.JobHandler(crunch, run,
		tako.WithTraffic(tako.Traffic{Timeout: time.Hour, SessionAffinity: tako.On}))

	// Exactly one instance, answering nothing but its own health check.
	Loop = tako.Singleton(loop,
		tako.WithTraffic(tako.Traffic{HTTP2: tako.On}))

	// Nanoseconds are the unit trap: a sub-second timeout is a mistake
	// worth naming rather than truncating to zero.
	Quick = tako.Worker(work, handle,
		tako.WithTraffic(tako.Traffic{Timeout: 500 * time.Millisecond, SessionAffinity: 3}))

	// An option that sets nothing is noise the reader has to decode.
	Empty = tako.Worker(other, handle,
		tako.WithTraffic(tako.Traffic{}))

	work  = tako.Queue[string]("work")
	other = tako.Queue[string]("other")
)

func run(ctx tako.Ctx, s string) error { return nil }

func loop(ctx tako.Ctx) error { return nil }

func handle(ctx tako.Ctx, s string) error { return nil }
