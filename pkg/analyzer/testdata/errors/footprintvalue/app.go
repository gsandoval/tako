package footprintvalue

import "github.com/gsandoval/tako"

var mem = "1Gi"

var (
	BadCPU   = tako.API("GET /api/a", h, tako.WithFootprint(tako.Footprint{CPU: 3}))
	BadMem   = tako.API("GET /api/b", h, tako.WithFootprint(tako.Footprint{Memory: "1GB"}))
	BadConc  = tako.API("GET /api/c", h, tako.WithFootprint(tako.Footprint{Concurrency: 2000}))
	BadMax   = tako.API("GET /api/d", h, tako.WithFootprint(tako.Footprint{MinInstances: 5, MaxInstances: 2}))
	NotConst = tako.API("GET /api/e", h, tako.WithFootprint(tako.Footprint{Memory: mem}))
	Empty    = tako.API("GET /api/f", h, tako.WithFootprint(tako.Footprint{}))
)

func h(ctx tako.Ctx, _ struct{}) (string, error) { return "", nil }
