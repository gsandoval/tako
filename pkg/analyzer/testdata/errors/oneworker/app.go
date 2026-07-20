package oneworker

import "github.com/gsandoval/tako"

var jobs = tako.Queue[Job]("jobs")

type Job struct{ N int }

var First = tako.Worker(jobs, work)

var Second = tako.Worker(jobs, work)

func work(ctx tako.Ctx, j Job) error { return nil }
