package main

import (
	"strings"
	"testing"
	"time"

	"github.com/gsandoval/tako/internal/trace"
	"github.com/gsandoval/tako/pkg/provision/gcp"
)

func TestFormatTraceWaterfall(t *testing.T) {
	t0 := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	spans := []trace.Span{
		{
			TraceID:    "105445aa7843bc8bf206b12000100000",
			SpanID:     "0000000000000001",
			ReqID:      "4a9f1c",
			Service:    "shop",
			Kind:       "http",
			Name:       "POST /orders",
			StartTime:  t0,
			EndTime:    t0.Add(15 * time.Millisecond),
			DurationMs: 15.0,
			Status:     200,
			Attributes: map[string]string{"flag.checkout-v2": `"treatment"`},
		},
		{
			TraceID:      "105445aa7843bc8bf206b12000100000",
			SpanID:       "0000000000000002",
			ParentSpanID: "0000000000000001",
			ReqID:        "4a9f1c",
			Service:      "shop",
			Kind:         "entity",
			Name:         "orders.Put",
			StartTime:    t0.Add(2 * time.Millisecond),
			EndTime:      t0.Add(5 * time.Millisecond),
			DurationMs:   3.0,
		},
		{
			TraceID:      "105445aa7843bc8bf206b12000100000",
			SpanID:       "0000000000000003",
			ParentSpanID: "0000000000000001",
			ReqID:        "4a9f1c",
			Service:      "shop",
			Kind:         "queue",
			Name:         "fulfill.Publish",
			StartTime:    t0.Add(6 * time.Millisecond),
			EndTime:      t0.Add(8 * time.Millisecond),
			DurationMs:   2.0,
		},
		{
			TraceID:      "105445aa7843bc8bf206b12000100000",
			SpanID:       "0000000000000004",
			ParentSpanID: "0000000000000003",
			ReqID:        "4a9f1c",
			Service:      "worker-fulfill",
			Kind:         "worker",
			Name:         "FulfillOrder",
			StartTime:    t0.Add(20 * time.Millisecond),
			EndTime:      t0.Add(45 * time.Millisecond),
			DurationMs:   25.0,
			Status:       204,
		},
	}
	logs := []gcp.LogEntry{
		{
			Time:     t0.Add(3 * time.Millisecond),
			Service:  "shop",
			Severity: "INFO",
			Text:     "order created [req=4a9f1c]",
		},
	}
	out := formatTraceWaterfall("4a9f1c", spans, logs)
	for _, want := range []string{
		"trace 105445aa7843bc8bf206b12000100000 [req=4a9f1c]",
		"[shop] http: POST /orders (15.0ms) → 200 [flag.checkout-v2=\"treatment\"]",
		"├─ [shop] entity: orders.Put (3.0ms)",
		"└─ [shop] queue: fulfill.Publish (2.0ms)",
		"└─ [worker-fulfill] worker: FulfillOrder (25.0ms) → 204",
		"order created [req=4a9f1c]",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("waterfall missing %q in:\n%s", want, out)
		}
	}
}
