package tako

import (
	"errors"
	"fmt"
)

// apiError is an error with an explicit HTTP status. It stays unexported:
// handlers construct one via Errorf or NotFound, and the runtime reads the
// status through the HTTPStatus method.
type apiError struct {
	status int
	msg    string
}

func (e *apiError) Error() string { return e.msg }

// HTTPStatus reports the status an API handler error surfaces as.
func (e *apiError) HTTPStatus() int { return e.status }

var errNotFound = &apiError{status: 404, msg: "not found"}

// Errorf returns an error that an API handler surfaces with the given HTTP
// status and body {"error":"<message>"}.
func Errorf(status int, format string, args ...any) error {
	return &apiError{status: status, msg: fmt.Sprintf(format, args...)}
}

// NotFound returns the error an API or Page handler surfaces as a 404.
func NotFound() error {
	return errNotFound
}

// IsNotFound reports whether err marks an absent value — a handler's
// NotFound, and (from F2 on) misses from Entity.Get, Cache.Get,
// Bucket.Get, and a query's First.
func IsNotFound(err error) bool {
	return errors.Is(err, errNotFound)
}

// Permanent marks an async handler's error as not worth retrying: the
// message is acked and dead-lettered instead of redelivered.
func Permanent(err error) error {
	panic("tako: not implemented")
}
