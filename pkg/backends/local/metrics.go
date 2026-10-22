package local

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"sync"
)

// MetricsLog is the local metrics sink: every Add/Observe becomes one
// structured log line, with running totals kept in-process so the dev
// console can show them. Cloud Monitoring export is a later backend
// behind the same call sites.
type MetricsLog struct {
	mu             sync.Mutex
	counters       map[string]int64
	counts         map[string]int64 // histogram sample counts
	onMetric       func(kind, name string, value float64, total int64)
	onMetricLabels func(kind, name string, labels map[string]string, value float64, total int64)
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

// OnMetricWithLabels subscribes to recordings with their label map.
func (m *MetricsLog) OnMetricWithLabels(fn func(kind, name string, labels map[string]string, value float64, total int64)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onMetricLabels = fn
}

// FormatMetricLabels formats a metric label map as "{k=v, ...}" in
// deterministic key order, or "" when labels is empty.
func FormatMetricLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(labels[k])
	}
	b.WriteByte('}')
	return b.String()
}

func (m *MetricsLog) Add(ctx context.Context, name string, labels map[string]string, delta int64) {
	m.mu.Lock()
	m.counters[name] += delta
	total := m.counters[name]
	fn := m.onMetric
	fnLabels := m.onMetricLabels
	m.mu.Unlock()
	args := []any{"kind", "counter", "name", name, "delta", delta, "total", total}
	if len(labels) > 0 {
		args = append(args, "labels", labels)
	}
	slog.InfoContext(ctx, "metric", args...)
	if fn != nil {
		fn("counter", name, float64(delta), total)
	}
	if fnLabels != nil {
		fnLabels("counter", name, labels, float64(delta), total)
	}
}

func (m *MetricsLog) Observe(ctx context.Context, name string, labels map[string]string, value float64) {
	m.mu.Lock()
	m.counts[name]++
	count := m.counts[name]
	fn := m.onMetric
	fnLabels := m.onMetricLabels
	m.mu.Unlock()
	args := []any{"kind", "histogram", "name", name, "value", value, "count", count}
	if len(labels) > 0 {
		args = append(args, "labels", labels)
	}
	slog.InfoContext(ctx, "metric", args...)
	if fn != nil {
		fn("histogram", name, value, count)
	}
	if fnLabels != nil {
		fnLabels("histogram", name, labels, value, count)
	}
}
