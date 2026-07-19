package tako

// Counter declares a monotonically increasing metric. The name must be a
// string constant (analyzer-enforced).
func Counter(name string, opts ...Option) *CounterHandle {
	return &CounterHandle{name: name, opts: opts}
}

// CounterHandle is a declared counter metric.
type CounterHandle struct {
	name string
	opts []Option
}

// Add increments the counter by delta.
func (c *CounterHandle) Add(ctx Ctx, delta int64) {
	panic("tako: not implemented")
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

// Observe records one sample.
func (h *HistogramHandle) Observe(ctx Ctx, value float64) {
	panic("tako: not implemented")
}
