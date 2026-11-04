package tako

import (
	"bytes"
	"testing"

	"github.com/gsandoval/tako/internal/trace"
)

type traceOrder struct {
	ID   string `entity:"id" json:"id"`
	Item string `json:"item"`
}

var (
	traceOrders = Entity[traceOrder]("trace-orders")
	traceAssets = Bucket("trace-assets")
	traceQueue  = Queue[traceOrder]("trace-queue")
	traceWorker = Worker(traceQueue, func(ctx Ctx, o traceOrder) error {
		return traceOrders.Put(ctx, o)
	})
)

func TestDistributedTraceSpansAcrossEntityBucketQueueAndWorker(t *testing.T) {
	_ = traceWorker
	app := NewTestApp(t)
	ctx := app.Ctx()
	tc, ok := trace.FromContext(ctx)
	if !ok || tc.TraceID == "" {
		t.Fatal("expected TestApp.Ctx() to carry a root trace context")
	}

	if err := traceAssets.Put(ctx, "receipt.txt", Blob{Reader: bytes.NewReader([]byte("ok")), ContentType: "text/plain"}); err != nil {
		t.Fatalf("bucket Put: %v", err)
	}
	if err := traceQueue.Publish(ctx, traceOrder{ID: "ord-1", Item: "book"}); err != nil {
		t.Fatalf("queue Publish: %v", err)
	}
	app.DrainQueues()

	spans := app.app.Traces.ForTrace(tc.TraceID)
	if len(spans) < 4 {
		t.Fatalf("got %d spans for trace %s, want >= 4: %+v", len(spans), tc.TraceID, spans)
	}
	seen := map[string]bool{}
	for _, s := range spans {
		seen[s.Kind+":"+s.Name] = true
		if s.TraceID != tc.TraceID {
			t.Errorf("span %s TraceID = %q, want %q", s.Name, s.TraceID, tc.TraceID)
		}
	}
	for _, want := range []string{
		"bucket:trace-assets.Put",
		"queue:trace-queue.Publish",
		"entity:trace-orders.Put",
	} {
		if !seen[want] {
			t.Errorf("missing span %q in %v", want, seen)
		}
	}
}
