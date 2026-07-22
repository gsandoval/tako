package backends

import "context"

// Metrics is the sink behind tako.Counter and tako.Histogram. Recording
// is fire-and-forget: implementations never fail and never block the
// caller on I/O.
type Metrics interface {
	// Add increments the named counter by delta.
	Add(ctx context.Context, name string, delta int64)
	// Observe records one sample of the named distribution.
	Observe(ctx context.Context, name string, value float64)
}
