package workerbinding

import "github.com/gsandoval/tako"

type Job struct{ N int }

// The binding is a constructor call, not a declared package-level var —
// the analyzer cannot name the queue.
var W = tako.Worker(tako.Queue[Job]("inline"), work)

func work(ctx tako.Ctx, j Job) error { return nil }
