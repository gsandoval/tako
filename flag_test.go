package tako

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/gsandoval/tako/pkg/backends"
)

type flagTaskMsg struct {
	OrderID string `json:"order_id"`
}

var (
	checkoutFlowFlag = Flag("checkout-flow", "v1")
	maxBatchFlag     = Flag("max-batch-size", 50)
	flagTaskQueue    = Queue[flagTaskMsg]("flag-orders")
	flagWorkerSaw    string
	flagTaskWorker   = Worker(flagTaskQueue, func(ctx Ctx, msg flagTaskMsg) error {
		flagWorkerSaw = checkoutFlowFlag.Get(ctx)
		return nil
	})
)

func TestFlagDefaultAndSetFlag(t *testing.T) {
	app := NewTestApp(t)
	ctx := app.Ctx()
	if got := checkoutFlowFlag.Get(ctx); got != "v1" {
		t.Fatalf("default checkoutFlowFlag = %q, want v1", got)
	}
	if got := maxBatchFlag.Get(ctx); got != 50 {
		t.Fatalf("default maxBatchFlag = %d, want 50", got)
	}

	app.SetFlag(checkoutFlowFlag, "v2")
	app.SetFlag(maxBatchFlag, 200)

	// New task context sees the updated flag values.
	ctx2 := app.Ctx()
	if got := checkoutFlowFlag.Get(ctx2); got != "v2" {
		t.Fatalf("overridden checkoutFlowFlag = %q, want v2", got)
	}
	if got := maxBatchFlag.Get(ctx2); got != 200 {
		t.Fatalf("overridden maxBatchFlag = %d, want 200", got)
	}
}

func TestFlagGetForAndRolloutSplit(t *testing.T) {
	app := NewTestApp(t)
	if err := app.app.Flags.Put(context.Background(), backends.FlagRule{
		Name: "checkout-flow",
		Variants: []backends.FlagVariant{
			{Value: json.RawMessage(`"v1"`), Weight: 50},
			{Value: json.RawMessage(`"v2"`), Weight: 50},
		},
	}); err != nil {
		t.Fatalf("Put split rule: %v", err)
	}

	seenVariants := map[string]int{}
	for i := 0; i < 40; i++ {
		subj := fmt.Sprintf("user-%d", i)
		ctx := app.Ctx(User{ID: subj})
		implicit := checkoutFlowFlag.Get(ctx)

		bgCtx := app.Ctx()
		explicit := checkoutFlowFlag.GetFor(bgCtx, subj)
		if implicit != explicit {
			t.Fatalf("subject %s: implicit %q != explicit %q", subj, implicit, explicit)
		}
		seenVariants[explicit]++

		// Re-evaluating the same subject on another context must be 100% deterministic.
		if again := checkoutFlowFlag.GetFor(app.Ctx(), subj); again != explicit {
			t.Fatalf("non-deterministic bucket for %s: %q vs %q", subj, explicit, again)
		}
	}
	if seenVariants["v1"] == 0 || seenVariants["v2"] == 0 {
		t.Fatalf("expected 50/50 split to hit both v1 and v2 across 40 subjects, got %v", seenVariants)
	}
}

func TestFlagSnapshotPinnedDuringTaskExecution(t *testing.T) {
	app := NewTestApp(t)
	app.SetFlag(checkoutFlowFlag, "v1")

	// Start a task and pin its context.
	inFlightCtx := app.Ctx()
	before := checkoutFlowFlag.Get(inFlightCtx)

	// Mutate the underlying flag store while the task is already in flight.
	app.SetFlag(checkoutFlowFlag, "v2-mid-flight")
	after := checkoutFlowFlag.Get(inFlightCtx)

	if before != "v1" || after != "v1" {
		t.Fatalf("in-flight task saw flag flip mid-execution: before=%q after=%q (want both v1)", before, after)
	}

	// A new task started after the update sees the new snapshot.
	nextTaskCtx := app.Ctx()
	if now := checkoutFlowFlag.Get(nextTaskCtx); now != "v2-mid-flight" {
		t.Fatalf("subsequent task saw %q, want v2-mid-flight", now)
	}
}

func TestFlagDecisionPropagatesAcrossAsyncQueue(t *testing.T) {
	_ = flagTaskWorker
	app := NewTestApp(t)
	_ = app.app.Flags.Put(context.Background(), backends.FlagRule{
		Name:       "checkout-flow",
		Value:      json.RawMessage(`"v2-rollout"`),
		RolloutPct: 100,
	})

	reqCtx := app.Ctx(User{ID: "user-42"})
	httpSaw := checkoutFlowFlag.Get(reqCtx)
	if httpSaw != "v2-rollout" {
		t.Fatalf("httpSaw = %q, want v2-rollout", httpSaw)
	}

	// Flip the flag store BEFORE publishing and draining the async queue:
	// the decision made in reqCtx must stick across the queue boundary.
	_ = app.app.Flags.Put(context.Background(), backends.FlagRule{
		Name:  "checkout-flow",
		Value: json.RawMessage(`"v3-changed-later"`),
	})

	flagWorkerSaw = ""
	if err := flagTaskQueue.Publish(reqCtx, flagTaskMsg{OrderID: "ord-1"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	app.DrainQueues()

	if flagWorkerSaw != httpSaw {
		t.Fatalf("worker saw %q, want upstream sticky decision %q", flagWorkerSaw, httpSaw)
	}
}
