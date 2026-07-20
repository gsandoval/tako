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

func workerName(queue string) string {
	workerMu.Lock()
	defer workerMu.Unlock()
	if n := workerNames[queue]; n != "" {
		return n
	}
	return "worker"
}

// attachQueueConsole narrates delivery on the dev console: ⚙ per attempt
// and ack, ✗ per dead letter — the local async story stays visible.
func attachQueueConsole(d *local.QueueDispatcher, dataDir string) {
	d.OnAck(func(queue string, took time.Duration) {
		fmt.Printf("⚙ %s → %s ok (%s)\n", queue, workerName(queue), took.Round(time.Millisecond))
	})
	d.OnAttempt(func(queue string, attempt, max int, err error, retryIn time.Duration) {
		fmt.Printf("⚙ %s → %s attempt %d/%d failed: %v — retrying in %s\n",
			queue, workerName(queue), attempt, max, err, retryIn)
	})
	d.OnDeadLetter(func(dl backends.DeadLetter) {
		if dl.Permanent {
			fmt.Fprintf(os.Stdout, "✗ dead-letter %s: %s permanent failure: %s\n",
				dl.Queue, workerName(dl.Queue), dl.LastError)
		} else {
			fmt.Fprintf(os.Stdout, "✗ dead-letter %s: %s gave up after %d attempts: %s\n",
				dl.Queue, workerName(dl.Queue), dl.Attempts, dl.LastError)
		}
		fmt.Printf("  payload → %s/deadletter/%s/\n", dataDir, dl.Queue)
	})
}
