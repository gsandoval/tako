package oneconfig

import "github.com/gsandoval/tako"

// The config parameter is variadic so the common case stays
// tako.Worker(q, fn). That leaves exactly one mistake the type system
// cannot catch: passing two, where the second would be silently
// dropped. Silently dropped configuration is the class X2 existed to
// end, so it is an error.

var (
	q = tako.Queue[string]("work")

	Handle = tako.Worker(q, handle,
		tako.Compute{Profile: tako.ProfileBatch},
		tako.Compute{Profile: tako.ProfileLatency})

	Slow = tako.Queue[string]("slow",
		tako.Delivery{MaxAttempts: 10},
		tako.Delivery{MaxAttempts: 20})
)

func handle(ctx tako.Ctx, s string) error { return nil }

var Ping = tako.API("GET /ping", ping)

func ping(ctx tako.Ctx, _ struct{}) (string, error) { return "pong", nil }
