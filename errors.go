package tako

// Errorf returns an error that an API handler surfaces with the given HTTP
// status.
func Errorf(status int, format string, args ...any) error {
	panic("tako: not implemented")
}

// NotFound returns the error an API or Page handler surfaces as a 404.
func NotFound() error {
	panic("tako: not implemented")
}

// IsNotFound reports whether err marks an absent value — from Entity.Get,
// Cache.Get, Bucket.Get, or a query's First.
func IsNotFound(err error) bool {
	panic("tako: not implemented")
}

// Permanent marks an async handler's error as not worth retrying: the
// message is acked and dead-lettered instead of redelivered.
func Permanent(err error) error {
	panic("tako: not implemented")
}
