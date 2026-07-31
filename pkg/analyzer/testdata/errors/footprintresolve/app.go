package footprintresolve

import "github.com/gsandoval/tako"

// Each worker is its own service, so each resolved conflict reports
// independently: pinning one side of a pair inherits the other from
// the profile, and the RESOLVED combination is what must be legal.
var (
	qa = tako.Queue[string]("qa")
	qb = tako.Queue[string]("qb")
	qc = tako.Queue[string]("qc")

	// CPU 8 over the default profile's 512Mi.
	BigCPU = tako.Worker(qa, work, tako.WithFootprint(tako.Footprint{CPU: 8}))
	// 8Gi over the default profile's 1 vCPU.
	BigMem = tako.Worker(qb, work, tako.WithFootprint(tako.Footprint{Memory: "8Gi"}))
	// Min 30 over the default profile's max 20.
	BigMin = tako.Worker(qc, work, tako.WithFootprint(tako.Footprint{MinInstances: 30}))
)

func work(ctx tako.Ctx, s string) error { return nil }
