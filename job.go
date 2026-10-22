package tako

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gsandoval/tako/internal/hooks"
	"github.com/gsandoval/tako/pkg/backends"
)

// Job declares long-running compute with parameters of type T, run outside
// request timeouts. The name must be a string constant (analyzer-enforced).
func Job[T any](name string) *JobHandle[T] {
	return &JobHandle[T]{name: name}
}

// JobHandle is a declared job. Bind its body with tako.Runner; trigger
// it from any handler with Run.
type JobHandle[T any] struct {
	name string
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
	if a := appFrom(ctx); a != nil && a.Dispatcher != nil {
		return a.Dispatcher.Publish(ctx, hooks.JobQueue(j.name), raw, backends.PublishOptions{})
	}
	if hooks.Queue == nil {
		panic(unwiredPanic("queue"))
	}
	// Locally a job rides the queue dispatcher: DrainQueues/WaitIdle
	// cover it, retries follow the local queue policy, and a failed
	// run dead-letters inspectably — no invisible goroutines.
	return hooks.Queue.Publish(ctx, hooks.JobQueue(j.name), raw, backends.PublishOptions{})
}

// Runner binds fn as the body of job. Like every async handler it is
// func(Ctx, T) error; RunnerConfig.Parallel fans the execution out
// across tasks.
func Runner[T any](job *JobHandle[T], fn func(Ctx, T) error, cfg ...RunnerConfig) Handler {
	parallel := 1
	attempts := 0
	var timeout time.Duration
	for _, c := range cfg {
		if c.Parallel > 1 {
			parallel = c.Parallel
		}
		if c.Attempts > 0 {
			attempts = c.Attempts
		}
		if c.Timeout > 0 {
			timeout = c.Timeout
		}
	}
	return register(Handler{
		kind:     "job",
		id:       nextHandlerID(),
		source:   job.name,
		parallel: parallel,
		attempts: attempts,
		timeout:  timeout,
		name:     fnName(fn),
		invoke: func(ctx Ctx, raw []byte) error {
			var params T
			if err := json.Unmarshal(raw, &params); err != nil {
				return Permanent(fmt.Errorf("job params do not decode: %w", err))
			}
			if timeout > 0 {
				c, cancel := context.WithTimeout(ctx, timeout)
				defer cancel()
				ctx = reroot(ctx, c)
			}
			return fn(ctx, params)
		},
	})
}
