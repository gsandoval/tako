package carrieropt

import (
	"time"

	"github.com/gsandoval/tako"
)

var (
	slow = tako.Queue[string]("slow", tako.Delivery{RateLimit: 0, MaxAttempts: 2})
	fan  = tako.Topic[string]("fan", tako.Delivery{RateLimit: 3})
	back = tako.Queue[string]("back", tako.Delivery{
		MinBackoff: 5 * time.Minute, MaxBackoff: time.Minute})
)

var _ = slow
var _ = fan
var _ = back
