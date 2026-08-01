package jobretries

import "github.com/gsandoval/tako"

var work = tako.Job[string]("work")

var Work = tako.JobHandler(work, run, tako.WithRetries(50))

func run(ctx tako.Ctx, _ string) error { return nil }
