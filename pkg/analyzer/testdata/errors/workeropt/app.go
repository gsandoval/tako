package workeropt

import (
	"time"

	"github.com/gsandoval/tako"
)

var jobs = tako.Queue[Job]("jobs")

type Job struct{ N int }

var W = tako.Worker(jobs, work, tako.WithTTL(time.Minute))

func work(ctx tako.Ctx, j Job) error { return nil }
