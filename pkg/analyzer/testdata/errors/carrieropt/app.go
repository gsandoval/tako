package carrieropt

import (
	"time"

	"github.com/gsandoval/tako"
)

var (
	q    = tako.Queue[string]("jobs", tako.WithTTL(time.Minute))
	slow = tako.Queue[string]("slow", tako.WithRateLimit(0))
	tp   = tako.Topic[string]("events", tako.WithDelay(time.Second))
	ups  = tako.Counter("ups", tako.WithTTL(time.Minute))
)

var _ = q
var _ = slow
var _ = tp
var _ = ups
