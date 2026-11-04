// Package trace provides distributed trace context propagation, child span
// timing, and cross-boundary attribute injection for HTTP requests,
// cross-service calls, async queues/topics, and background jobs.
package trace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Header and message attribute keys used across HTTP and async boundaries.
const (
	CloudTraceHeader = "X-Cloud-Trace-Context"
	RequestHeader    = "X-Tako-Request"
	ParentSpanHeader = "X-Tako-Parent-Span"
	FlagsHeader      = "X-Tako-Flags"

	AttrTrace      = "tako-trace"
	AttrRequest    = "tako-req"
	AttrParentSpan = "tako-parent-span"
	AttrFlags      = "tako-flags"
)

// Context holds the distributed trace correlation state for an execution.
type Context struct {
	TraceID      string // 32-char lowercase hex (128-bit)
	SpanID       string // 16-char lowercase hex (64-bit)
	ParentSpanID string // 16-char lowercase hex (64-bit), empty for root
	Sampled      bool   // true when ";o=1"
	ReqID        string // short Tako request correlation ID
	Service      string // logical service name
}

// Span represents one timed operation (handler, remote call, or resource op)
// within a distributed trace.
type Span struct {
	TraceID      string            `json:"trace_id"`
	SpanID       string            `json:"span_id"`
	ParentSpanID string            `json:"parent_span_id,omitempty"`
	ReqID        string            `json:"req_id,omitempty"`
	Service      string            `json:"service,omitempty"`
	Kind         string            `json:"kind"` // "http", "call", "entity", "bucket", "queue", "topic", "lock", "worker", "subscribe", "cron", "job"
	Name         string            `json:"name"`
	StartTime    time.Time         `json:"start_time"`
	EndTime      time.Time         `json:"end_time"`
	DurationMs   float64           `json:"duration_ms"`
	Status       int               `json:"status,omitempty"`
	Err          string            `json:"err,omitempty"`
	Attributes   map[string]string `json:"attributes,omitempty"`
}

// Sink receives completed spans.
type Sink interface {
	RecordSpan(s Span)
}

type ctxKey int

const (
	traceCtxKey ctxKey = iota
	sinkCtxKey
	flagsStateKey
)

var (
	globalMu   sync.RWMutex
	globalSink Sink
)

// SetGlobalSink installs the process-wide span sink (Cloud Trace exporter in
// GCP, in-memory collector locally).
func SetGlobalSink(s Sink) {
	globalMu.Lock()
	globalSink = s
	globalMu.Unlock()
}

// GlobalSink returns the process-wide span sink, if any.
func GlobalSink() Sink {
	globalMu.RLock()
	defer globalMu.RUnlock()
	return globalSink
}

// WithSink attaches a test-app-scoped span sink to ctx so parallel tests
// stay isolated.
func WithSink(ctx context.Context, s Sink) context.Context {
	return context.WithValue(ctx, sinkCtxKey, s)
}

func sinkFrom(ctx context.Context) Sink {
	if ctx != nil {
		if s, ok := ctx.Value(sinkCtxKey).(Sink); ok && s != nil {
			return s
		}
	}
	return GlobalSink()
}

// WithContext attaches tc to ctx.
func WithContext(ctx context.Context, tc Context) context.Context {
	return context.WithValue(ctx, traceCtxKey, tc)
}

// FromContext returns the trace Context carried by ctx, if any.
func FromContext(ctx context.Context) (Context, bool) {
	if ctx == nil {
		return Context{}, false
	}
	tc, ok := ctx.Value(traceCtxKey).(Context)
	return tc, ok && tc.TraceID != ""
}

// NewTraceID generates a 128-bit (32 hex char) trace ID.
func NewTraceID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	// Ensure non-zero.
	if b == [16]byte{} {
		b[15] = 1
	}
	return hex.EncodeToString(b[:])
}

// NewSpanID generates a 64-bit (16 hex char) span ID.
func NewSpanID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	if b == [8]byte{} {
		b[7] = 1
	}
	return hex.EncodeToString(b[:])
}

// ParseCloudTraceContext parses Google's X-Cloud-Trace-Context header:
// "TRACE_ID/SPAN_ID;o=TRACE_TRUE".
//
// In X-Cloud-Trace-Context, TRACE_ID is 32-char hex and SPAN_ID is an
// unsigned 64-bit decimal integer (though hex is also accepted as a
// fallback). Internally, SpanID is normalized to 16-char lowercase hex
// so it matches Cloud Trace v2 API and Cloud Logging's spanId format.
func ParseCloudTraceContext(h string) (traceID, spanIDHex string, sampled bool) {
	h = strings.TrimSpace(h)
	if h == "" {
		return "", "", false
	}
	sampled = true // default to sampled unless explicitly ;o=0
	semi := strings.IndexByte(h, ';')
	if semi >= 0 {
		opts := h[semi+1:]
		h = h[:semi]
		if strings.Contains(opts, "o=0") {
			sampled = false
		}
	}
	tracePart, spanPart, hasSpan := strings.Cut(h, "/")
	tracePart = strings.ToLower(strings.TrimSpace(tracePart))
	if tracePart == "" {
		return "", "", false
	}
	if len(tracePart) < 32 {
		tracePart = strings.Repeat("0", 32-len(tracePart)) + tracePart
	} else if len(tracePart) > 32 {
		tracePart = tracePart[:32]
	}
	if !hasSpan || spanPart == "" || spanPart == "0" {
		return tracePart, "", sampled
	}
	spanPart = strings.TrimSpace(spanPart)
	if n, err := strconv.ParseUint(spanPart, 10, 64); err == nil && n > 0 {
		return tracePart, fmt.Sprintf("%016x", n), sampled
	}
	if n, err := strconv.ParseUint(spanPart, 16, 64); err == nil && n > 0 {
		return tracePart, fmt.Sprintf("%016x", n), sampled
	}
	return tracePart, "", sampled
}

// FormatCloudTraceContext formats a trace context as Google's
// X-Cloud-Trace-Context header ("TRACE_ID/SPAN_ID_DECIMAL;o=1").
func FormatCloudTraceContext(traceID, spanIDHex string, sampled bool) string {
	if traceID == "" {
		return ""
	}
	var spanDec string
	if spanIDHex != "" {
		if n, err := strconv.ParseUint(spanIDHex, 16, 64); err == nil && n > 0 {
			spanDec = strconv.FormatUint(n, 10)
		}
	}
	if spanDec == "" {
		spanDec = "1"
	}
	opt := "1"
	if !sampled {
		opt = "0"
	}
	return fmt.Sprintf("%s/%s;o=%s", traceID, spanDec, opt)
}

// StartRoot initializes a root or incoming execution span on ctx.
func StartRoot(ctx context.Context, kind, name, service, reqID, cloudHeader, parentSpanOverride string) (context.Context, Context, func(status int, err error)) {
	traceID, incomingSpanID, sampled := ParseCloudTraceContext(cloudHeader)
	parentSpan := incomingSpanID
	if parentSpanOverride != "" {
		parentSpan = parentSpanOverride
	}
	if traceID == "" {
		if existing, ok := FromContext(ctx); ok {
			traceID = existing.TraceID
			sampled = existing.Sampled
			if parentSpan == "" {
				parentSpan = existing.SpanID
			}
			if reqID == "" {
				reqID = existing.ReqID
			}
			if service == "" {
				service = existing.Service
			}
		} else {
			traceID = NewTraceID()
			sampled = true
		}
	}
	spanID := NewSpanID()
	if incomingSpanID != "" && parentSpanOverride == "" {
		// Keep the platform-assigned span ID as the parent so child spans
		// nest directly under Cloud Run's HTTP request entry.
		parentSpan = incomingSpanID
	}
	tc := Context{
		TraceID:      traceID,
		SpanID:       spanID,
		ParentSpanID: parentSpan,
		Sampled:      sampled,
		ReqID:        reqID,
		Service:      service,
	}
	ctx = WithContext(ctx, tc)
	ctx = ensureFlagsState(ctx)
	start := time.Now().UTC()
	return ctx, tc, func(status int, err error) {
		s := sinkFrom(ctx)
		if s == nil {
			return
		}
		end := time.Now().UTC()
		sp := Span{
			TraceID:      tc.TraceID,
			SpanID:       tc.SpanID,
			ParentSpanID: tc.ParentSpanID,
			ReqID:        tc.ReqID,
			Service:      tc.Service,
			Kind:         kind,
			Name:         name,
			StartTime:    start,
			EndTime:      end,
			DurationMs:   float64(end.Sub(start).Microseconds()) / 1000.0,
			Status:       status,
		}
		if err != nil {
			sp.Err = err.Error()
		}
		if decisions := PropagatedFlags(ctx); len(decisions) > 0 {
			sp.Attributes = map[string]string{}
			for k, v := range decisions {
				sp.Attributes["flag."+k] = v
			}
		}
		s.RecordSpan(sp)
	}
}

// StartSpan starts a child span under ctx's current trace context. If ctx
// carries no trace context or no Sink is active, it returns ctx unchanged
// with a no-op finish closure.
func StartSpan(ctx context.Context, kind, name string, attrs ...string) (context.Context, func(err error)) {
	if ctx == nil {
		return ctx, func(error) {}
	}
	tc, ok := FromContext(ctx)
	s := sinkFrom(ctx)
	if !ok && s == nil {
		return ctx, func(error) {}
	}
	if !ok {
		tc = Context{
			TraceID: NewTraceID(),
			SpanID:  NewSpanID(),
			Sampled: true,
		}
	} else {
		tc = Context{
			TraceID:      tc.TraceID,
			SpanID:       NewSpanID(),
			ParentSpanID: tc.SpanID,
			Sampled:      tc.Sampled,
			ReqID:        tc.ReqID,
			Service:      tc.Service,
		}
	}
	childCtx := WithContext(ctx, tc)
	start := time.Now().UTC()
	return childCtx, func(err error) {
		if s == nil {
			return
		}
		end := time.Now().UTC()
		sp := Span{
			TraceID:      tc.TraceID,
			SpanID:       tc.SpanID,
			ParentSpanID: tc.ParentSpanID,
			ReqID:        tc.ReqID,
			Service:      tc.Service,
			Kind:         kind,
			Name:         name,
			StartTime:    start,
			EndTime:      end,
			DurationMs:   float64(end.Sub(start).Microseconds()) / 1000.0,
		}
		if err != nil {
			sp.Err = err.Error()
		}
		if len(attrs) >= 2 {
			sp.Attributes = make(map[string]string, len(attrs)/2)
			for i := 0; i+1 < len(attrs); i += 2 {
				sp.Attributes[attrs[i]] = attrs[i+1]
			}
		}
		s.RecordSpan(sp)
	}
}

// --- Cross-service & async flag/experiment decision propagation ---

type flagsState struct {
	mu        sync.RWMutex
	decisions map[string]string
}

func ensureFlagsState(ctx context.Context) context.Context {
	if ctx != nil {
		if _, ok := ctx.Value(flagsStateKey).(*flagsState); ok {
			return ctx
		}
	}
	return context.WithValue(ctx, flagsStateKey, &flagsState{decisions: map[string]string{}})
}

// WithPropagatedFlags initializes ctx with upstream-evaluated flag decisions.
func WithPropagatedFlags(ctx context.Context, decisions map[string]string) context.Context {
	ctx = ensureFlagsState(ctx)
	if len(decisions) == 0 {
		return ctx
	}
	st, _ := ctx.Value(flagsStateKey).(*flagsState)
	if st == nil {
		return ctx
	}
	st.mu.Lock()
	for k, v := range decisions {
		st.decisions[k] = v
	}
	st.mu.Unlock()
	return ctx
}

// RecordFlagDecision records an evaluated flag/experiment decision on ctx so
// downstream services in the same trace reuse it without mid-trace skew.
func RecordFlagDecision(ctx context.Context, name, rawJSON string) {
	if ctx == nil {
		return
	}
	st, _ := ctx.Value(flagsStateKey).(*flagsState)
	if st == nil {
		return
	}
	st.mu.Lock()
	if _, exists := st.decisions[name]; !exists {
		st.decisions[name] = rawJSON
	}
	st.mu.Unlock()
}

// LookupFlagDecision returns an upstream-propagated or previously evaluated
// decision for name within ctx's execution.
func LookupFlagDecision(ctx context.Context, name string) (string, bool) {
	if ctx == nil {
		return "", false
	}
	st, _ := ctx.Value(flagsStateKey).(*flagsState)
	if st == nil {
		return "", false
	}
	st.mu.RLock()
	defer st.mu.RUnlock()
	v, ok := st.decisions[name]
	return v, ok
}

// PropagatedFlags returns a snapshot of all flag decisions recorded on ctx.
func PropagatedFlags(ctx context.Context) map[string]string {
	if ctx == nil {
		return nil
	}
	st, _ := ctx.Value(flagsStateKey).(*flagsState)
	if st == nil {
		return nil
	}
	st.mu.RLock()
	defer st.mu.RUnlock()
	if len(st.decisions) == 0 {
		return nil
	}
	return maps.Clone(st.decisions)
}

// EncodeFlagsHeader serializes decisions for X-Tako-Flags / tako-flags.
func EncodeFlagsHeader(decisions map[string]string) string {
	if len(decisions) == 0 {
		return ""
	}
	raw, err := json.Marshal(decisions)
	if err != nil {
		return ""
	}
	return string(raw)
}

// DecodeFlagsHeader parses decisions from X-Tako-Flags / tako-flags.
func DecodeFlagsHeader(raw string) map[string]string {
	if raw == "" {
		return nil
	}
	var out map[string]string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

// InjectAttrs writes the trace context and propagated flags from ctx into a
// message attribute map (used by Pub/Sub and Cloud Tasks publishers).
func InjectAttrs(ctx context.Context, attrs map[string]string) map[string]string {
	if attrs == nil {
		attrs = map[string]string{}
	}
	if tc, ok := FromContext(ctx); ok {
		attrs[AttrTrace] = FormatCloudTraceContext(tc.TraceID, tc.SpanID, tc.Sampled)
		if tc.ReqID != "" {
			attrs[AttrRequest] = tc.ReqID
		}
		if tc.SpanID != "" {
			attrs[AttrParentSpan] = tc.SpanID
		}
	}
	if flags := EncodeFlagsHeader(PropagatedFlags(ctx)); flags != "" {
		attrs[AttrFlags] = flags
	}
	return attrs
}

// Detach copies the trace context, sink, and flag decisions from parent onto a
// background context so async in-process deliveries outlive the request's
// cancellation while preserving distributed trace correlation.
func Detach(parent context.Context) context.Context {
	out := context.Background()
	if parent == nil {
		return out
	}
	if s := sinkFrom(parent); s != nil {
		out = WithSink(out, s)
	}
	if tc, ok := FromContext(parent); ok {
		out = WithContext(out, tc)
	}
	if decisions := PropagatedFlags(parent); len(decisions) > 0 {
		out = WithPropagatedFlags(out, decisions)
	} else {
		out = ensureFlagsState(out)
	}
	return out
}

// MemoryCollector is a thread-safe bounded in-memory span recorder used by
// local dev and TestApp.
type MemoryCollector struct {
	mu    sync.RWMutex
	max   int
	spans []Span
}

// NewMemoryCollector creates a collector retaining up to max spans.
func NewMemoryCollector(max int) *MemoryCollector {
	if max <= 0 {
		max = 5000
	}
	return &MemoryCollector{max: max}
}

// RecordSpan implements Sink.
func (m *MemoryCollector) RecordSpan(s Span) {
	m.mu.Lock()
	if len(m.spans) >= m.max {
		copy(m.spans, m.spans[len(m.spans)-m.max+1:])
		m.spans = m.spans[:m.max-1]
	}
	m.spans = append(m.spans, s)
	m.mu.Unlock()
}

// Spans returns a copy of all recorded spans, oldest first.
func (m *MemoryCollector) Spans() []Span {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Span, len(m.spans))
	copy(out, m.spans)
	return out
}

// ForTrace returns all spans matching traceID or reqID, sorted by start time.
func (m *MemoryCollector) ForTrace(id string) []Span {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []Span
	for _, s := range m.spans {
		if s.TraceID == id || s.ReqID == id {
			out = append(out, s)
		}
	}
	return out
}
