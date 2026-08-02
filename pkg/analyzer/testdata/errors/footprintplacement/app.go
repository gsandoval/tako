package footprintplacement

import "github.com/gsandoval/tako"

var (
	crunch = tako.Job[string]("crunch")

	Crunch = tako.Runner(crunch, run,
		tako.RunnerConfig{Footprint: tako.Footprint{CPU: 4, Memory: "4Gi", Concurrency: 10}})
	Loop = tako.Singleton(loop,
		tako.SingletonConfig{Footprint: tako.Footprint{Memory: "1Gi", MinInstances: 2}})
)

func run(ctx tako.Ctx, s string) error { return nil }

func loop(ctx tako.Ctx) error { return nil }
