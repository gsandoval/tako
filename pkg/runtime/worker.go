package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	tako "github.com/gsandoval/tako"
	"github.com/gsandoval/tako/internal/hooks"
	"github.com/gsandoval/tako/pkg/backends"
	"github.com/gsandoval/tako/pkg/backends/local"
)

var (
	workerMu    sync.Mutex
	workerNames = map[string]string{} // queue → worker var name, for console lines
)

// RegisterWorker binds a tako.Worker handler to its queue on the wired
// backend. Generated mains call it once per worker at startup.
func RegisterWorker(name string, h tako.Handler) {
	kind, queue, invoke := hooks.HandlerInfo(h)
	if kind != "worker" {
		panic(fmt.Sprintf("tako: RegisterWorker(%s): handler is %q, not a worker", name, kind))
	}
	fn, ok := invoke.(func(tako.Ctx, []byte) error)
	if !ok || hooks.Queue == nil {
		panic("tako: RegisterWorker before Boot")
	}
	workerMu.Lock()
	workerNames[queue] = name
	workerMu.Unlock()
	hooks.Queue.Consume(queue, func(ctx context.Context, payload []byte) error {
		wctx := &requestCtx{Context: ctx, log: slog.Default().With("worker", name)}
		return fn(wctx, payload)
	})
}

// RegisterSubscriber binds a tako.Subscribe handler to its event source —
// a topic or a bucket's Uploaded event — on the wired backend. Generated
// mains call it once per subscriber at startup; subscribers of one source
// deliver independently.
func RegisterSubscriber(name string, h tako.Handler) {
	kind, source, invoke := hooks.HandlerInfo(h)
	if kind != "subscribe" {
		panic(fmt.Sprintf("tako: RegisterSubscriber(%s): handler is %q, not a subscriber", name, kind))
	}
	fn, ok := invoke.(func(tako.Ctx, []byte) error)
	if !ok || hooks.Topic == nil {
		panic("tako: RegisterSubscriber before Boot")
	}
	hooks.Topic.Subscribe(source, name, func(ctx context.Context, payload []byte) error {
		sctx := &requestCtx{Context: ctx, log: slog.Default().With("subscriber", name)}
		return fn(sctx, payload)
	})
}

// consumerName resolves the console name for one delivery: the subscriber
// name topics carry, or the queue's registered worker.
func consumerName(source, consumer string) string {
	if consumer != "" {
		return consumer
	}
	workerMu.Lock()
	defer workerMu.Unlock()
	if n := workerNames[source]; n != "" {
		return n
	}
	return "worker"
}

// attachQueueConsole narrates delivery on the dev console: ⚙ per attempt
// and ack, ✗ per dead letter — the local async story stays visible.
func attachQueueConsole(d *local.QueueDispatcher, dataDir string) {
	d.OnAck(func(source, consumer string, took time.Duration) {
		fmt.Printf("⚙ %s → %s ok (%s)\n", source, consumerName(source, consumer), took.Round(time.Millisecond))
	})
	d.OnAttempt(func(source, consumer string, attempt, max int, err error, retryIn time.Duration) {
		fmt.Printf("⚙ %s → %s attempt %d/%d failed: %v — retrying in %s\n",
			source, consumerName(source, consumer), attempt, max, err, retryIn)
	})
	d.OnDeadLetter(func(dl backends.DeadLetter) {
		who := consumerName(dl.Queue, dl.Subscriber)
		if dl.Permanent {
			fmt.Fprintf(os.Stdout, "✗ dead-letter %s: %s permanent failure: %s\n",
				dl.Queue, who, dl.LastError)
		} else {
			fmt.Fprintf(os.Stdout, "✗ dead-letter %s: %s gave up after %d attempts: %s\n",
				dl.Queue, who, dl.Attempts, dl.LastError)
		}
		if dl.Subscriber != "" {
			fmt.Printf("  payload → %s/deadletter/%s/%s/\n", dataDir, dl.Queue, dl.Subscriber)
		} else {
			fmt.Printf("  payload → %s/deadletter/%s/\n", dataDir, dl.Queue)
		}
	})
}
