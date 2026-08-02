package footprintvalue

import "github.com/gsandoval/tako"

// Footprint VALUE validation. Before X2 this fixture declared its
// footprints on tako.API routes, so every line it asserted was
// service-option and not one footprint value was ever checked — the
// coverage evaporated when V1 moved footprints off routes and nobody
// noticed, because the golden still had six lines in it.
//
// The footprints now sit where they are legal, so the value rules
// actually fire.

var mem = "1Gi"

var (
	q      = tako.Queue[string]("work")
	events = tako.Topic[string]("events")

	BadCPU   = tako.Worker(q, handle, tako.Compute{Footprint: tako.Footprint{CPU: 3}})
	BadMem   = tako.Cron("every 1h", tick, tako.CronConfig{Footprint: tako.Footprint{Memory: "1GB"}})
	BadConc  = tako.Subscribe(events, onEvent, tako.Compute{Footprint: tako.Footprint{Concurrency: 2000}})
	NotConst = tako.Singleton(loop, tako.SingletonConfig{Footprint: tako.Footprint{Memory: mem}})

	svc = tako.Service(tako.ServiceConfig{
		Footprint: tako.Footprint{MinInstances: 5, MaxInstances: 2},
	})

	Empty = tako.API("GET /api/f", h)
)

func handle(ctx tako.Ctx, s string) error        { return nil }
func onEvent(ctx tako.Ctx, s string) error       { return nil }
func tick(ctx tako.Ctx) error                    { return nil }
func loop(ctx tako.Ctx) error                    { return nil }
func h(ctx tako.Ctx, _ struct{}) (string, error) { return "", nil }

var _ = svc
