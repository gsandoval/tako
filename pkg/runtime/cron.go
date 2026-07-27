package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	tako "github.com/gsandoval/tako"
	"github.com/gsandoval/tako/internal/hooks"
	"github.com/gsandoval/tako/internal/schedule"
)

// cronInert, set by BootGCP, keeps crons from ticking in-process in
// the cloud (per-instance tickers under autoscaling are not a
// schedule); a scheduler feature will drive them instead.
var cronInert bool

// RegisterCron starts the named cron on its schedule. Generated mains
// call it once per cron at startup; runs are serialized per cron — a
// tick landing mid-run is skipped with a narration. WithSingleflight
// additionally wraps the handler itself in the §4.4 lease (done at
// declaration, so every trigger inherits it): overlapping runs
// collapse across instances, narrated with the ↻ line.
func RegisterCron(name string, h tako.Handler) {
	kind, sched, invoke := hooks.HandlerInfo(h)
	if kind != "cron" {
		panic(fmt.Sprintf("tako: RegisterCron(%s): handler is %q, not a cron", name, kind))
	}
	fn, ok := invoke.(func(tako.Ctx, []byte) error)
	if !ok {
		panic("tako: RegisterCron before Boot")
	}
	interval, err := schedule.Parse(sched)
	if err != nil {
		// The analyzer rejects unparseable schedules; reaching this means
		// the binary predates the declaration — fail loudly.
		panic("tako: " + err.Error())
	}
	if cronInert {
		// In the cloud a per-instance ticker would fire once per live
		// instance and never when scaled to zero — worse than nothing.
		// Cron stays inert until the scheduler feature; the deploy
		// banner says so.
		slog.Default().Info("cron not scheduled in the cloud yet (a scheduler feature follows)", "cron", name, "schedule", sched)
		return
	}
	fmt.Printf("⏰ cron %s: %s\n", name, sched)
	go func() {
		ticker := time.NewTicker(interval)
		var running atomic.Bool
		for range ticker.C {
			if !running.CompareAndSwap(false, true) {
				fmt.Printf("⏰ cron %s: run still in progress — tick skipped\n", name)
				continue
			}
			go func() {
				defer running.Store(false)
				start := time.Now()
				cctx := &requestCtx{Context: context.Background(), log: slog.Default().With("cron", name)}
				if err := fn(cctx, nil); err != nil {
					fmt.Printf("⏰ cron %s failed: %v — next run on schedule\n", name, err)
					return
				}
				fmt.Printf("⏰ cron %s ok (%s)\n", name, time.Since(start).Round(time.Millisecond))
			}()
		}
	}()
}
