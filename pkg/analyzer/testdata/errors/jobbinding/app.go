package jobbinding

import "github.com/gsandoval/tako"

// A job's body is bound to the job, and that binding is the edge the
// analyzer follows to derive the execution's IAM.
var (
	reindex = tako.Job[Params]("reindex")
	Reindex = tako.Runner(reindex, run)
	Again   = tako.Runner(reindex, run)

	// Bound to a job that is not a declared var: nothing can name it.
	Loose = tako.Runner(tako.Job[Params]("adhoc"), run)
)

type Params struct {
	Since string `json:"since"`
}

func run(ctx tako.Ctx, p Params) error { return nil }
