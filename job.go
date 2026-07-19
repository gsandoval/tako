package tako

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

// Run starts an execution of the job with the given parameters.
func (j *JobHandle[T]) Run(ctx Ctx, params T) error {
	panic("tako: not implemented")
}

// JobHandler binds fn as the body of job. Like every async handler it is
// func(Ctx, T) error; WithParallel fans the execution out across tasks.
func JobHandler[T any](job *JobHandle[T], fn func(Ctx, T) error, opts ...Option) Handler {
	return Handler{kind: "job"}
}
