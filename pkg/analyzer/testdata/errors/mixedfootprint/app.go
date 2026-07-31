package mixedfootprint

import "github.com/gsandoval/tako"

var Fast = tako.API("GET /api/fast", h, tako.WithFootprint(tako.Footprint{CPU: 2}))
var Slow = tako.API("GET /api/slow", h, tako.WithFootprint(tako.Footprint{CPU: 4, Memory: "2Gi"}))

func h(ctx tako.Ctx, _ struct{}) (string, error) { return "", nil }
