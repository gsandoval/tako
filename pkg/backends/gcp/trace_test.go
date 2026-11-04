package gcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/gsandoval/tako/internal/trace"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestTraceStoreBatchWrite(t *testing.T) {
	var received struct {
		Spans []struct {
			Name         string `json:"name"`
			SpanID       string `json:"spanId"`
			ParentSpanID string `json:"parentSpanId"`
			DisplayName  struct {
				Value string `json:"value"`
			} `json:"displayName"`
			Status struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"status"`
		} `json:"spans"`
	}
	hc := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Host != traceHost || r.URL.Path != "/v2/projects/my-proj/traces:batchWrite" {
				t.Errorf("unexpected URL: %s", r.URL.String())
			}
			if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
				t.Errorf("decode: %v", err)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader([]byte(`{}`))),
				Header:     make(http.Header),
			}, nil
		}),
	}

	c := &Client{
		HC:   hc,
		Opts: Options{Project: "my-proj", App: "shop", Env: "prod", Service: "shop"},
	}
	store := NewTraceStore(c, false)
	now := time.Now().UTC()
	store.RecordSpan(trace.Span{
		TraceID:      "105445aa7843bc8bf206b12000100000",
		SpanID:       "0000000000000002",
		ParentSpanID: "0000000000000001",
		ReqID:        "r-123",
		Kind:         "entity",
		Name:         "orders.Get",
		StartTime:    now,
		EndTime:      now.Add(5 * time.Millisecond),
		DurationMs:   5.0,
	})
	if err := store.Flush(context.Background()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if len(received.Spans) != 1 {
		t.Fatalf("got %d spans, want 1", len(received.Spans))
	}
	got := received.Spans[0]
	wantName := "projects/my-proj/traces/105445aa7843bc8bf206b12000100000/spans/0000000000000002"
	if got.Name != wantName || got.ParentSpanID != "0000000000000001" || got.DisplayName.Value != "orders.Get" {
		t.Fatalf("unexpected wire span: %+v", got)
	}
}
