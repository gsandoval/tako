package jobretries

import "github.com/gsandoval/tako"

var work = tako.Job[string]("work")

var Work = tako.Runner(work, run, tako.RunnerConfig{Attempts: 50})

func run(ctx tako.Ctx, _ string) error { return nil }
