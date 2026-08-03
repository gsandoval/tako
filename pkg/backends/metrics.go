package backends

import "context"

// Metrics is the sink behind tako.Counter and tako.Histogram. Recording
// is fire-and-forget: implementations never fail and never block the
// caller on I/O.
type Metrics interface {
	// Add increments the named counter by delta. Labels may be nil;
	// tako.Counter always passes nil, because user-declared metrics
	// have no dimensions (observability.md). The parameter exists for
	// the metrics TAKO itself records — per-route request counts and
	// latencies — where the route has to be a label tako writes,
	// because Cloud Run's own `route` label is never populated.
	Add(ctx context.Context, name string, labels map[string]string, delta int64)
	// Observe records one sample of the named distribution.
	Observe(ctx context.Context, name string, labels map[string]string, value float64)
}
