package trafficopt

import (
	"time"

	"github.com/gsandoval/tako"
)

// Two cases this fixture used to carry — a job setting SessionAffinity
// and a singleton setting HTTP2 — are gone, deliberately: after X2
// neither RunnerConfig nor SingletonConfig has those fields, so the
// mistake no longer compiles and needs no rule. Both keep a Timeout,
// which is the one traffic setting either of them ever had.
//
// What remains here are the value judgments a type cannot make.

var (
	// Nanoseconds are the unit trap: a sub-second timeout is a mistake
	// worth naming rather than truncating to zero.
	Quick = tako.Worker(work, handle,
		tako.Compute{Traffic: tako.Traffic{Timeout: 500 * time.Millisecond, SessionAffinity: 3}})

	// A setting that sets nothing is noise the reader has to decode.
	Empty = tako.Worker(other, handle,
		tako.Compute{Traffic: tako.Traffic{}})

	work  = tako.Queue[string]("work")
	other = tako.Queue[string]("other")
)

func handle(ctx tako.Ctx, s string) error { return nil }
