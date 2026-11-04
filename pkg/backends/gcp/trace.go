package gcp

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/gsandoval/tako/internal/trace"
)

const (
	maxSpansPerBatch = 200
	traceFlushEvery  = 5 * time.Second
)

// TraceStore buffers recorded trace spans in memory and ships them in
// batches to Google Cloud Trace v2 API
// (POST /v2/projects/{project}/traces:batchWrite).
//
// In addition to shipping to Cloud Trace, every completed span is also
// emitted as a structured Cloud Logging record under the trace and spanId
// keys when logSpans is enabled, so `tako trace` and Cloud Logging
// queries can reconstruct the entire multi-service waterfall even on
// projects without separate Cloud Trace viewer permissions.
type TraceStore struct {
	c        *Client
	logSpans bool

	mu       sync.Mutex
	pending  []trace.Span
	flushMu  sync.Mutex
	stop     chan struct{}
	stopOnce sync.Once
}

// NewTraceStore constructs a Cloud Trace v2 exporter. Call Start to begin
// periodic background flushing.
func NewTraceStore(c *Client, logSpans bool) *TraceStore {
	return &TraceStore{
		c:        c,
		logSpans: logSpans,
		stop:     make(chan struct{}),
	}
}

// RecordSpan implements trace.Sink. Recording never blocks the caller on
// network I/O.
func (s *TraceStore) RecordSpan(sp trace.Span) {
	if sp.Service == "" && s.c != nil {
		sp.Service = s.c.Opts.Service
	}
	if s.logSpans && sp.TraceID != "" && s.c != nil && s.c.Opts.Project != "" {
		attrs := []any{
			"logging.googleapis.com/trace", "projects/" + s.c.Opts.Project + "/traces/" + sp.TraceID,
			"logging.googleapis.com/spanId", sp.SpanID,
			"tako_span", map[string]any{
				"kind":           sp.Kind,
				"name":           sp.Name,
				"span_id":        sp.SpanID,
				"parent_span_id": sp.ParentSpanID,
				"service":        sp.Service,
				"duration_ms":    sp.DurationMs,
				"status":         sp.Status,
				"err":            sp.Err,
				"attributes":     sp.Attributes,
			},
		}
		if sp.ReqID != "" {
			attrs = append(attrs, "req", sp.ReqID)
		}
		slog.Default().Debug("span "+sp.Kind+" "+sp.Name, attrs...)
	}
	s.mu.Lock()
	// Bound in-memory backlog if flushes fall behind.
	if len(s.pending) >= 5000 {
		s.pending = s.pending[1:]
	}
	s.pending = append(s.pending, sp)
	s.mu.Unlock()
}

// Start begins periodic background flushing of buffered spans.
func (s *TraceStore) Start(every time.Duration) {
	if every <= 0 {
		every = traceFlushEvery
	}
	go func() {
		tick := time.NewTicker(every)
		defer tick.Stop()
		for {
			select {
			case <-s.stop:
				return
			case <-tick.C:
				_ = s.Flush(context.Background())
			}
		}
	}()
}

// Close stops the background ticker and performs a final drain flush.
func (s *TraceStore) Close(ctx context.Context) {
	s.stopOnce.Do(func() { close(s.stop) })
	_ = s.Flush(ctx)
}

// Flush sends all currently buffered spans to Cloud Trace v2.
func (s *TraceStore) Flush(ctx context.Context) error {
	s.flushMu.Lock()
	defer s.flushMu.Unlock()

	s.mu.Lock()
	if len(s.pending) == 0 {
		s.mu.Unlock()
		return nil
	}
	batch := s.pending
	s.pending = nil
	s.mu.Unlock()

	for len(batch) > 0 {
		n := len(batch)
		if n > maxSpansPerBatch {
			n = maxSpansPerBatch
		}
		chunk := batch[:n]
		batch = batch[n:]
		if err := s.writeBatch(ctx, chunk); err != nil {
			return err
		}
	}
	return nil
}

func (s *TraceStore) writeBatch(ctx context.Context, spans []trace.Span) error {
	if s.c == nil || s.c.Opts.Project == "" {
		return nil
	}
	project := s.c.Opts.Project
	wireSpans := make([]map[string]any, 0, len(spans))
	for _, sp := range spans {
		if sp.TraceID == "" || sp.SpanID == "" {
			continue
		}
		attrMap := map[string]any{
			"tako.kind": strAttr(sp.Kind),
		}
		if sp.Service != "" {
			attrMap["tako.service"] = strAttr(sp.Service)
		}
		if sp.ReqID != "" {
			attrMap["tako.req"] = strAttr(sp.ReqID)
		}
		for k, v := range sp.Attributes {
			attrMap[k] = strAttr(v)
		}
		ws := map[string]any{
			"name":        fmt.Sprintf("projects/%s/traces/%s/spans/%s", project, sp.TraceID, sp.SpanID),
			"spanId":      sp.SpanID,
			"displayName": map[string]any{"value": truncateStr(sp.Name, 128)},
			"startTime":   sp.StartTime.UTC().Format(time.RFC3339Nano),
			"endTime":     sp.EndTime.UTC().Format(time.RFC3339Nano),
			"attributes":  map[string]any{"attributeMap": attrMap},
		}
		if sp.ParentSpanID != "" {
			ws["parentSpanId"] = sp.ParentSpanID
		}
		if sp.Err != "" {
			// code 2 = UNKNOWN / ERROR in google.rpc.Code
			ws["status"] = map[string]any{
				"code":    2,
				"message": truncateStr(sp.Err, 256),
			}
		} else if sp.Status >= 400 {
			ws["status"] = map[string]any{
				"code":    2,
				"message": fmt.Sprintf("HTTP %d", sp.Status),
			}
		}
		wireSpans = append(wireSpans, ws)
	}
	if len(wireSpans) == 0 {
		return nil
	}
	u := s.c.url(traceHost, "/v2/projects/%s/traces:batchWrite", project)
	return s.c.do(ctx, "POST", u, map[string]any{"spans": wireSpans}, nil)
}

func strAttr(val string) map[string]any {
	return map[string]any{
		"stringValue": map[string]any{"value": truncateStr(val, 256)},
	}
}

func truncateStr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
