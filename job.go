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

// Gather records this parallel job task's shard result under key and reports
// whether all tasks (0..count-1) of the current job execution have now
// reported. Exactly one task receives complete == true along with the full
// slice of shards in task-index order (0..count-1); all earlier tasks receive
// complete == false. When count <= 1, Gather immediately returns
// ([]S{shard}, true, nil).
func Gather[S any](ctx Ctx, key string, shard S) (all []S, complete bool, err error) {
	idx, count := ctx.Task()
	if count <= 1 {
		return []S{shard}, true, nil
	}
	doc, err := json.Marshal(shard)
	if err != nil {
		return nil, false, fmt.Errorf("tako.Gather: marshal shard: %w", err)
	}
	eb := entityBackend(ctx)
	shardID := fmt.Sprintf("%s#%d", key, idx)
	if err := eb.Put(ctx, "_tako_shard", shardID, doc, nil); err != nil {
		return nil, false, err
	}
	won := false
	err = eb.Tx(ctx, "_tako_shard", nil, func(ops backends.EntityTxOps) error {
		won = false
		candidate := make([]S, count)
		for i := range count {
			raw, gerr := ops.Get(fmt.Sprintf("%s#%d", key, i))
			if gerr == backends.ErrNotFound {
				return nil
			}
			if gerr != nil {
				return gerr
			}
			if uerr := json.Unmarshal(raw, &candidate[i]); uerr != nil {
				return uerr
			}
		}
		for i := range count {
			if derr := ops.Delete(fmt.Sprintf("%s#%d", key, i)); derr != nil {
				return derr
			}
		}
		all = candidate
		won = true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	if !won {
		return nil, false, nil
	}
	return all, true, nil
}
