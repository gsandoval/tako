package mixedtraffic

import (
	"time"

	"github.com/gsandoval/tako"
)

// One service answers one way. tako.Service configures this package's
// service, and a worker's tako.Compute configures the service that
// worker is alone in — but both are claims on a service in this
// package, so two different ones cannot both hold.
var Svc = tako.Service(tako.ServiceConfig{
	Traffic: tako.Traffic{Timeout: 45 * time.Second},
})

var (
	jobs = tako.Queue[string]("jobs")
	Work = tako.Worker(jobs, work, tako.Compute{Traffic: tako.Traffic{HTTP2: tako.ToggleOn}})
)

func work(ctx tako.Ctx, s string) error { return nil }
