package subscribeopt

import (
	"time"

	"github.com/gsandoval/tako"
)

var events = tako.Topic[Evt]("events")

type Evt struct{ N int }

var S = tako.Subscribe(events, handle, tako.WithTTL(time.Minute))

func handle(ctx tako.Ctx, e Evt) error { return nil }
