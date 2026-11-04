package trace

import (
	"context"
	"errors"
	"testing"
)

func TestParseAndFormatCloudTraceContext(t *testing.T) {
	header := "105445aa7843bc8bf206b12000100000/1234567890;o=1"
	traceID, spanHex, sampled := ParseCloudTraceContext(header)
	if traceID != "105445aa7843bc8bf206b12000100000" {
		t.Fatalf("traceID = %q", traceID)
	}
	if spanHex != "00000000499602d2" {
		t.Fatalf("spanHex = %q, want 00000000499602d2", spanHex)
	}
	if !sampled {
		t.Fatal("expected sampled=true")
	}

	formatted := FormatCloudTraceContext(traceID, spanHex, sampled)
	if formatted != header {
		t.Fatalf("FormatCloudTraceContext = %q, want %q", formatted, header)
	}
}

func TestStartRootAndChildSpans(t *testing.T) {
	col := NewMemoryCollector(100)
	ctx := WithSink(context.Background(), col)

	ctx, rootTC, finishRoot := StartRoot(ctx, "http", "POST /orders", "shop", "r-123456", "105445aa7843bc8bf206b12000100000/1;o=1", "")
	RecordFlagDecision(ctx, "checkout-v2", "true")

	childCtx, finishChild := StartSpan(ctx, "entity", "orders.Put", "key", "ord_1")
	childTC, ok := FromContext(childCtx)
	if !ok {
		t.Fatal("missing child trace context")
	}
	if childTC.ParentSpanID != rootTC.SpanID {
		t.Fatalf("child ParentSpanID = %q, want %q", childTC.ParentSpanID, rootTC.SpanID)
	}
	finishChild(errors.New("datastore timeout"))
	finishRoot(200, nil)

	spans := col.ForTrace("r-123456")
	if len(spans) != 2 {
		t.Fatalf("got %d spans, want 2", len(spans))
	}
	if spans[0].Kind != "entity" || spans[0].Err != "datastore timeout" || spans[0].Attributes["key"] != "ord_1" {
		t.Fatalf("unexpected child span: %+v", spans[0])
	}
	if spans[1].Kind != "http" || spans[1].Attributes["flag.checkout-v2"] != "true" {
		t.Fatalf("unexpected root span: %+v", spans[1])
	}
}

func TestDetachAndInjectAttrs(t *testing.T) {
	col := NewMemoryCollector(100)
	ctx := WithSink(context.Background(), col)
	ctx, tc, finish := StartRoot(ctx, "http", "POST /pub", "app", "r-abc", "", "")
	RecordFlagDecision(ctx, "exp", `"variant-a"`)
	defer finish(200, nil)

	attrs := InjectAttrs(ctx, nil)
	if attrs[AttrRequest] != "r-abc" {
		t.Fatalf("AttrRequest = %q", attrs[AttrRequest])
	}
	if attrs[AttrParentSpan] != tc.SpanID {
		t.Fatalf("AttrParentSpan = %q, want %q", attrs[AttrParentSpan], tc.SpanID)
	}
	decodedFlags := DecodeFlagsHeader(attrs[AttrFlags])
	if decodedFlags["exp"] != `"variant-a"` {
		t.Fatalf("decodedFlags = %v", decodedFlags)
	}

	detached := Detach(ctx)
	if val, ok := LookupFlagDecision(detached, "exp"); !ok || val != `"variant-a"` {
		t.Fatalf("LookupFlagDecision on detached = %q, %v", val, ok)
	}
}
