package tako

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/gsandoval/tako/internal/hooks"
	"github.com/gsandoval/tako/pkg/backends"
)

// Job declares long-running compute with parameters of type T, run outside
// request timeouts. The name must be a string constant (analyzer-enforced).
func Job[T any](name string, opts ...Option) *JobHandle[T] {
	return &JobHandle[T]{name: name, opts: opts}
}

// JobHandle is a declared job. Bind its body with tako.JobHandler; trigger
// it from any handler with Run.
type JobHandle[T any] struct {
	name string
	opts []Option
}

// Run starts an execution of the job with the given parameters and
// returns once the execution is HANDED OFF — queued locally, a Cloud
// Run Job execution created in the cloud. It never waits for
// completion; a job is compute you walk away from.
func (j *JobHandle[T]) Run(ctx Ctx, params T) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("job %s params do not marshal: %w", j.name, err)
	}
	if hooks.RunJob != nil { // the cloud path: a real Cloud Run Job execution
		return hooks.RunJob(ctx, j.name, raw)
	}
	if hooks.Queue == nil {
		return errors.New("tako: jobs are not wired — Run works in booted services (tako dev / deploy) and NewTestApp tests, not bare tests")
	}
	// Locally a job rides the queue dispatcher: DrainQueues/WaitIdle
	// cover it, retries follow the local queue policy, and a failed
	// run dead-letters inspectably — no invisible goroutines.
	return hooks.Queue.Publish(ctx, hooks.JobQueue(j.name), raw, backends.PublishOptions{})
}

// JobHandler binds fn as the body of job. Like every async handler it is
// func(Ctx, T) error; WithParallel fans the execution out across tasks.
func JobHandler[T any](job *JobHandle[T], fn func(Ctx, T) error, opts ...Option) Handler {
	parallel := 1
	for _, o := range opts {
		if o.name == "parallel" {
			if n, ok := o.args[0].(int); ok && n > 1 {
				parallel = n
			}
		}
	}
	return register(Handler{
		kind:     "job",
		id:       nextHandlerID(),
		source:   job.name,
		parallel: parallel,
		name:     fnName(fn),
		invoke: func(ctx Ctx, raw []byte) error {
			var params T
			if err := json.Unmarshal(raw, &params); err != nil {
				return Permanent(fmt.Errorf("job params do not decode: %w", err))
			}
			return fn(ctx, params)
		},
	})
}
