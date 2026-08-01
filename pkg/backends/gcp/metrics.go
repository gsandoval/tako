package gcp

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// MetricsStore exports tako.Counter and tako.Histogram to Cloud
// Monitoring as user-defined metrics under custom.googleapis.com/tako/.
//
// Recording stays what the contract promises — a mutex and a map, never
// I/O — and a background flush ships what accumulated. The wire format
// is CUMULATIVE for both kinds, which is what makes dropped flushes
// harmless: the running total lives in this process, so the next flush
// carries the truth rather than a gap. Only the flush at shutdown is
// unrecoverable, which is why Serve drains before it runs.
//
// The monitored resource is generic_task, NOT cloud_run_revision.
// User-defined metrics may only be written against a specific set of
// resource types, and Cloud Run's own is not among them — writing there
// is refused outright:
//
//	metric cannot be written to cloud_run_revision, see
//	.../creating-metrics#which-resource for a list of writable types
//
// generic_task is the type Google points serverless writers at, and its
// schema happens to fit exactly: namespace scopes the deployment, job
// names the service, and TASK_ID identifies the instance. That last one
// matters for correctness, not tidiness — every instance of a revision
// would otherwise share one cumulative series, and concurrent writers to
// a single series is either a rejection or nonsense. The revision rides
// along as a metric label, which costs no extra series (task_id is
// already finer-grained) and makes a metric shift traceable to a deploy.
type MetricsStore struct {
	c    *Client
	who  Identity
	base string // overridable in tests; the real host by default

	// start is this process's birth, and the startTime of every
	// cumulative point it ever writes. Cloud Monitoring reads a
	// cumulative series as "total since startTime", so this must not
	// move once a point has been written under it.
	start time.Time

	mu       sync.Mutex
	counters map[string]int64
	dists    map[string]*distribution
	dirty    map[string]bool

	// flushMu serializes flushes so the ticker and the shutdown Close
	// cannot interleave. They genuinely can overlap: Close stops the
	// ticker but cannot un-start a flush already in flight, and two
	// concurrent writes to one series are what the per-series rate limit
	// rejects.
	flushMu   sync.Mutex
	lastFlush time.Time

	stop     chan struct{}
	stopOnce sync.Once
}

// Identity is who is writing: generic_task's four scoping labels plus
// the revision, which rides as a metric label. BootGCP assembles it from
// the injected env and the metadata server; tests pass one in.
type Identity struct {
	// Namespace scopes the deployment, "<app>-<env>", so prod and a
	// preview environment never merge into one series.
	Namespace string
	// Job is the tako service name — the name the user wrote, not the
	// env-prefixed deployed one.
	Job string
	// TaskID is this instance. Cloud Run's instance id when the metadata
	// server answers, hostname-pid otherwise.
	TaskID string
	// Location is the region.
	Location string
	// Revision is K_REVISION, carried as a metric label.
	Revision string
}

// Bucket layout for every histogram: exponential, doubling, starting at
// 1. One layout for all of them because tako.Histogram carries no unit —
// the SDK takes a name and a float64 — so the only honest choice is a
// layout with enough dynamic range to hold whatever the caller meant.
// Finite coverage is [1, 2^18); anything smaller or larger lands in the
// under/overflow buckets rather than being lost.
const (
	histBuckets = 18
	histGrowth  = 2.0
	histScale   = 1.0
)

// minWriteGap is Cloud Monitoring's per-series write limit: one point
// per 10 seconds. A flush that arrives sooner is rejected for the whole
// batch, so the shutdown flush waits out the remainder when it can.
const minWriteGap = 10 * time.Second

// maxSeriesPerCall is the API's batch ceiling.
const maxSeriesPerCall = 200

// NewMetricsStore builds the exporter. It does not start flushing —
// Start does — so a caller can construct one in a test without a
// goroutine appearing behind it.
func NewMetricsStore(c *Client, who Identity) *MetricsStore {
	return &MetricsStore{
		c:        c,
		who:      who,
		base:     "https://monitoring.googleapis.com",
		start:    time.Now(),
		counters: map[string]int64{},
		dists:    map[string]*distribution{},
		dirty:    map[string]bool{},
		stop:     make(chan struct{}),
	}
}

// Add increments a counter. Fire-and-forget, per the contract: it takes
// a lock and returns, and can neither fail nor block on I/O.
func (m *MetricsStore) Add(ctx context.Context, name string, delta int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counters[name] += delta
	m.dirty["c/"+name] = true
}

// Observe records one sample into the named distribution.
func (m *MetricsStore) Observe(ctx context.Context, name string, value float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.dists[name]
	if d == nil {
		d = &distribution{buckets: make([]int64, histBuckets+2)}
		m.dists[name] = d
	}
	d.record(value)
	m.dirty["h/"+name] = true
}

// Start begins flushing every interval until Close. The ticker is the
// only goroutine this type owns.
func (m *MetricsStore) Start(interval time.Duration) {
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				ctx, cancel := context.WithTimeout(context.Background(), interval)
				m.flushLogging(ctx, false)
				cancel()
			case <-m.stop:
				return
			}
		}
	}()
}

// Close stops the ticker and flushes what is left. It is what the
// runtime's shutdown hook calls, with whatever remains of Cloud Run's
// window — so it respects the ctx deadline rather than the flush's
// natural pace.
func (m *MetricsStore) Close(ctx context.Context) {
	m.stopOnce.Do(func() { close(m.stop) })
	m.flushLogging(ctx, true)
}

// flushLogging is the fire-and-forget face: the metrics contract has no
// error return, so a failure can only be logged. final marks the
// shutdown flush, which may wait out the per-series write limit because
// there is no later flush to correct for it.
func (m *MetricsStore) flushLogging(ctx context.Context, final bool) {
	if err := m.flush(ctx, final); err != nil {
		slog.WarnContext(ctx, "tako: metrics flush failed — the totals are cumulative, so a later flush recovers unless this was the last one",
			"err", err, "final", final)
	}
}

func (m *MetricsStore) flush(ctx context.Context, final bool) error {
	m.flushMu.Lock()
	defer m.flushMu.Unlock()
	series := m.drain()
	if len(series) == 0 {
		return nil
	}
	if final {
		// Writing twice inside the limit rejects the batch outright, and
		// at shutdown there is no next flush to fix it. Wait it out if
		// the budget allows; give up honestly if it does not.
		if wait := minWriteGap - time.Since(m.lastFlush); wait > 0 && !m.lastFlush.IsZero() {
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) > wait {
				time.Sleep(wait)
			} else {
				return fmt.Errorf("last flush was %s ago and Cloud Monitoring accepts one point per %s per series — %d series dropped rather than rejected",
					time.Since(m.lastFlush).Round(time.Second), minWriteGap, len(series))
			}
		}
	}
	m.lastFlush = time.Now()
	url := m.base + "/v3/projects/" + m.c.Opts.Project + "/timeSeries"
	for start := 0; start < len(series); start += maxSeriesPerCall {
		end := min(start+maxSeriesPerCall, len(series))
		body := map[string]any{"timeSeries": series[start:end]}
		if err := m.post(ctx, url, body); err != nil {
			return err
		}
	}
	return nil
}

// post writes one batch, retrying once on a 5xx. A 4xx is not retried:
// the batch is malformed or refused, and sending it again just spends
// the shutdown budget on the same answer.
func (m *MetricsStore) post(ctx context.Context, url string, body map[string]any) error {
	err := m.c.do(ctx, http.MethodPost, url, body, nil)
	if err == nil {
		return nil
	}
	if se, ok := err.(*statusError); ok && se.Code/100 == 5 {
		select {
		case <-time.After(2 * time.Second):
		case <-ctx.Done():
			return err
		}
		return m.c.do(ctx, http.MethodPost, url, body, nil)
	}
	return err
}

// drain snapshots every metric that changed since the last flush into
// wire form. Values are TOTALS, not deltas — the point of cumulative —
// so this reads without resetting anything.
func (m *MetricsStore) drain() []map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.dirty) == 0 {
		return nil
	}
	now := time.Now()
	var out []map[string]any
	for name, total := range m.counters {
		if !m.dirty["c/"+name] {
			continue
		}
		out = append(out, m.point(name, "INT64", now,
			map[string]any{"int64Value": strconv.FormatInt(total, 10)}))
	}
	for name, d := range m.dists {
		if !m.dirty["h/"+name] {
			continue
		}
		out = append(out, m.point(name, "DISTRIBUTION", now,
			map[string]any{"distributionValue": d.wire()}))
	}
	m.dirty = map[string]bool{}
	return out
}

// point renders one time series with a single cumulative point.
func (m *MetricsStore) point(name, valueType string, now time.Time, value map[string]any) map[string]any {
	return map[string]any{
		"metric": map[string]any{
			"type":   "custom.googleapis.com/tako/" + name,
			"labels": map[string]string{"revision": m.who.Revision},
		},
		"resource": map[string]any{
			"type": "generic_task",
			"labels": map[string]string{
				"project_id": m.c.Opts.Project,
				"location":   m.who.Location,
				"namespace":  m.who.Namespace,
				"job":        m.who.Job,
				"task_id":    m.who.TaskID,
			},
		},
		"metricKind": "CUMULATIVE",
		"valueType":  valueType,
		"points": []map[string]any{{
			"interval": map[string]any{
				"startTime": m.start.UTC().Format(time.RFC3339Nano),
				"endTime":   now.UTC().Format(time.RFC3339Nano),
			},
			"value": value,
		}},
	}
}

// distribution accumulates samples the way Cloud Monitoring wants them
// reported: a count, a mean, a sum of squared deviations (Welford, so it
// stays stable over a long-lived instance) and the bucket counts.
type distribution struct {
	count   int64
	mean    float64
	ssd     float64
	buckets []int64
}

func (d *distribution) record(v float64) {
	d.count++
	delta := v - d.mean
	d.mean += delta / float64(d.count)
	d.ssd += delta * (v - d.mean)
	d.buckets[bucketOf(v)]++
}

// bucketOf places a sample: 0 is the underflow bucket (below scale),
// 1..N are the doubling bands, N+1 is the overflow.
func bucketOf(v float64) int {
	if v < histScale || math.IsNaN(v) {
		return 0
	}
	i := int(math.Floor(math.Log2(v/histScale))) + 1
	if i > histBuckets {
		return histBuckets + 1
	}
	if i < 0 {
		return 0
	}
	return i
}

func (d *distribution) wire() map[string]any {
	counts := make([]string, len(d.buckets))
	for i, c := range d.buckets {
		counts[i] = strconv.FormatInt(c, 10)
	}
	return map[string]any{
		"count":                 strconv.FormatInt(d.count, 10),
		"mean":                  d.mean,
		"sumOfSquaredDeviation": d.ssd,
		"bucketOptions": map[string]any{
			"exponentialBuckets": map[string]any{
				"numFiniteBuckets": histBuckets,
				"growthFactor":     histGrowth,
				"scale":            histScale,
			},
		},
		"bucketCounts": counts,
	}
}
