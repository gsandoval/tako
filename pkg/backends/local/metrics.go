package local

import (
	"context"
	"log/slog"
	"sync"
)

// MetricsLog is the local metrics sink: every Add/Observe becomes one
// structured log line, with running totals kept in-process so the dev
// console can show them. Cloud Monitoring export is a later backend
// behind the same call sites.
type MetricsLog struct {
	mu       sync.Mutex
	counters map[string]int64
	counts   map[string]int64 // histogram sample counts
	onMetric func(kind, name string, value float64, total int64)
}

// NewMetricsLog builds an empty sink.
func NewMetricsLog() *MetricsLog {
	return &MetricsLog{counters: map[string]int64{}, counts: map[string]int64{}}
}

// OnMetric subscribes to recordings (the dev console's 📈 lines). total
// is the counter's running total, or the histogram's sample count.
func (m *MetricsLog) OnMetric(fn func(kind, name string, value float64, total int64)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onMetric = fn
}

func (m *MetricsLog) Add(ctx context.Context, name string, delta int64) {
	m.mu.Lock()
	m.counters[name] += delta
	total := m.counters[name]
	fn := m.onMetric
	m.mu.Unlock()
	slog.InfoContext(ctx, "metric", "kind", "counter", "name", name, "delta", delta, "total", total)
	if fn != nil {
		fn("counter", name, float64(delta), total)
	}
}

func (m *MetricsLog) Observe(ctx context.Context, name string, value float64) {
	m.mu.Lock()
	m.counts[name]++
	count := m.counts[name]
	fn := m.onMetric
	m.mu.Unlock()
	slog.InfoContext(ctx, "metric", "kind", "histogram", "name", name, "value", value, "count", count)
	if fn != nil {
		fn("histogram", name, value, count)
	}
}
