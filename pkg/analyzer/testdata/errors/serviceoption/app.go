package serviceoption

import (
	"time"

	"github.com/gsandoval/tako"
)

// Service-level options on a route: a service has many endpoints, so
// none of them is an honest place to describe the whole thing.
var (
	Create = tako.API("POST /links", ping, tako.WithDomain("api.example.com"))
	Read   = tako.API("GET /links", ping, tako.WithProfile(tako.Latency))
	Update = tako.Page("POST /p", page, tako.WithFootprint(tako.Footprint{Memory: "1Gi"}))
	Slow   = tako.API("GET /slow", ping, tako.WithTraffic(tako.Traffic{Timeout: 30 * time.Second}))
)

func ping(ctx tako.Ctx, _ struct{}) (string, error) { return "pong", nil }

func page(ctx tako.Ctx, _ struct{}) (tako.HTML, error) { return tako.HTML{}, nil }
