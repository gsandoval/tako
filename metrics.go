package tako

import (
	"github.com/gsandoval/tako/internal/hooks"
	"github.com/gsandoval/tako/pkg/backends"
)

// Counter declares a monotonically increasing metric. The name must be a
// string constant (analyzer-enforced). Locally every Add is one
// structured log line (and a 📈 line in the dev console); Cloud
// Monitoring export is a later backend behind the same call sites.
func Counter(name string, opts ...Option) *CounterHandle {
	return &CounterHandle{name: name, opts: opts}
}

// CounterHandle is a declared counter metric.
type CounterHandle struct {
	name string
	opts []Option
}

// Add increments the counter by delta. Fire-and-forget: recording never
// fails and never blocks the handler.
func (c *CounterHandle) Add(ctx Ctx, delta int64) {
	metricsBackend(ctx).Add(ctx, c.name, delta)
}

// Histogram declares a distribution metric. The name must be a string
// constant (analyzer-enforced).
func Histogram(name string, opts ...Option) *HistogramHandle {
	return &HistogramHandle{name: name, opts: opts}
}

// HistogramHandle is a declared histogram metric.
type HistogramHandle struct {
	name string
	opts []Option
}

// Observe records one sample. Fire-and-forget, like Counter.Add.
func (h *HistogramHandle) Observe(ctx Ctx, value float64) {
	metricsBackend(ctx).Observe(ctx, h.name, value)
}

// metricsBackend returns the wired metrics sink (see queueBackend).
func metricsBackend(ctx Ctx) backends.Metrics {
	if a := appFrom(ctx); a != nil {
		return a.Metrics
	}
	if hooks.Metrics == nil {
		panic("tako: no metrics sink wired — run via tako dev, a generated service, or tako.NewTestApp")
	}
	return hooks.Metrics
}
