package carrieropt

import (
	"time"

	"github.com/gsandoval/tako"
)

var (
	q    = tako.Queue[string]("jobs", tako.WithTTL(time.Minute))
	slow = tako.Queue[string]("slow", tako.WithDelivery(tako.Delivery{RateLimit: 0, MaxAttempts: 2}))
	fan  = tako.Topic[string]("fan", tako.WithDelivery(tako.Delivery{RateLimit: 3}))
	back = tako.Queue[string]("back", tako.WithDelivery(tako.Delivery{
		MinBackoff: 5 * time.Minute, MaxBackoff: time.Minute}))
	tp   = tako.Topic[string]("events", tako.WithDelay(time.Second))
	ups  = tako.Counter("ups", tako.WithTTL(time.Minute))
)

var _ = q
var _ = slow
var _ = tp
var _ = ups
var _ = fan
var _ = back
