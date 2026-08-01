package drainrange

import (
	"time"

	"github.com/gsandoval/tako"
)

// Cloud Run kills the instance 10s after SIGTERM and that window is not
// configurable — there is no field for it in the v1 RevisionSpec or the
// v2 RevisionTemplate. So a drain longer than the window is refused
// rather than clamped: clamping would leave a declaration that reads
// like a promise tako keeps.
var Svc = tako.Service(tako.WithFootprint(tako.Footprint{Drain: 30 * time.Second}))

var Slow = tako.API("GET /slow", slow)

// A singleton MAY set Drain — it serves, so it has requests to finish.
// What it may not do is name the duration in the wrong unit: Drain is a
// time.Duration, and a sub-second value is the same mistake Timeout
// already guards against.
var Loop = tako.Singleton("loop", loop,
	tako.WithFootprint(tako.Footprint{Drain: 200 * time.Millisecond}))

type req struct{}
type res struct{}

func slow(ctx tako.Ctx, r req) (res, error) { return res{}, nil }

func loop(ctx tako.Ctx) error { return nil }
