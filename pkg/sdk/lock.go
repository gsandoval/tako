package tako

// Lock declares a distributed mutual-exclusion lock. Most apps never touch
// Lock directly — Cron's WithSingleflight and Singleton are built on the
// same mechanism. The name must be a string constant (analyzer-enforced).
func Lock(name string, opts ...Option) *LockHandle {
	return &LockHandle{name: name, opts: opts}
}

// LockHandle is a declared distributed lock.
type LockHandle struct {
	name string
	opts []Option
}

// Do blocks until the lock is held, runs fn while holding it, and releases
// it on return. fn's Ctx is cancelled if the lock is lost mid-flight.
func (l *LockHandle) Do(ctx Ctx, fn func(Ctx) error) error {
	panic("tako: not implemented")
}

// TryDo runs fn only if the lock is immediately available, reporting
// whether it ran; it returns (false, nil) when someone else holds the lock.
func (l *LockHandle) TryDo(ctx Ctx, fn func(Ctx) error) (bool, error) {
	panic("tako: not implemented")
}
