package siren

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gsandoval/tako"
	"github.com/gsandoval/tako/pkg/runtime"
)

// siren's switches have to actually break, or the alerting walk proves
// nothing. These run against `local`, where alerts are inert by design —
// so what is under test is the FAILURE MODES the walk relies on, not the
// alerts themselves.

func TestBoomFailsAtTheGivenRate(t *testing.T) {
	app := tako.NewTestApp(t)
	ctx := app.Ctx()
	var failures int
	for i := 0; i < 40; i++ {
		if _, err := Boom.Call(ctx, BoomReq{Rate: 1}); err != nil {
			failures++
		}
	}
	if failures != 40 {
		t.Errorf("rate=1 failed %d/40 — a switch that does not break cannot demonstrate an alert", failures)
	}
	if _, err := Boom.Call(ctx, BoomReq{Rate: 0}); err != nil {
		t.Errorf("rate=0 must always succeed, got %v", err)
	}
}

func TestSlowSleepsForAsLongAsAsked(t *testing.T) {
	app := tako.NewTestApp(t)
	start := time.Now()
	if _, err := Slow.Call(app.Ctx(), SlowReq{Ms: 120}); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took < 100*time.Millisecond {
		t.Errorf("slept %v — RequestsSlow needs a route that is actually slow", took)
	}
}

// registerHandlers wires the async handlers the local dispatcher needs;
// BootLocal brings up the backends, not the bindings.
func registerHandlers(t *testing.T) {
	t.Helper()
	runtime.RegisterWorker("Grind", Grind)
	runtime.RegisterSubscriber("OnAlert", OnAlert)
	runtime.RegisterCron("Sweep", Sweep)
	runtime.RegisterCron("Flaky", Flaky)
}

// deadLetters reads the local dead-letter queue off disk, which is where
// the local backend keeps it.
func deadLetters(t *testing.T, dir, queue string) []deadLetter {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, "deadletter", queue, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	var out []deadLetter
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var dl deadLetter
		if err := json.Unmarshal(raw, &dl); err != nil {
			t.Fatal(err)
		}
		out = append(out, dl)
	}
	return out
}

type deadLetter struct {
	Attempts  int    `json:"attempts"`
	LastError string `json:"lastError"`
	Permanent bool   `json:"permanent"`
}

func poisonInto(t *testing.T, kind string) []deadLetter {
	t.Helper()
	dir := t.TempDir()
	if err := runtime.BootLocal(dir); err != nil {
		t.Fatal(err)
	}
	registerHandlers(t)
	ctx := runtime.NewCtx(t.Context())
	if err := work.Publish(ctx, Task{Fail: kind}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.WaitIdle(30 * time.Second); err != nil {
		t.Fatal(err)
	}
	return deadLetters(t, dir, "work")
}

// The two poisons take DIFFERENT PATHS to the dead-letter queue, and
// that difference is the whole reason QueueBacklogged watches the
// queue's depth rather than Pub/Sub's dead-letter counter — which sees
// only the retry-exhaustion path.
//
// A walk that poisons with the wrong one watches a dead letter arrive
// and an alert stay quiet, and goes looking for a bug that is not there.
func TestHardPoisonGivesUpImmediately(t *testing.T) {
	dead := poisonInto(t, "hard")
	if len(dead) != 1 {
		t.Fatalf("dead letters = %d, want 1", len(dead))
	}
	if dead[0].Attempts != 1 {
		t.Errorf("hard poison took %d attempts, want 1 — tako.Permanent is what makes it one", dead[0].Attempts)
	}
	if !dead[0].Permanent {
		t.Error("hard poison is not marked permanent — it is the path Pub/Sub's own counter cannot see")
	}
}

func TestSoftPoisonExhaustsItsRetries(t *testing.T) {
	dead := poisonInto(t, "soft")
	if len(dead) != 1 {
		t.Fatalf("dead letters = %d, want 1", len(dead))
	}
	if dead[0].Attempts != 5 {
		t.Errorf("soft poison took %d attempts, want 5", dead[0].Attempts)
	}
	if dead[0].Permanent {
		t.Error("soft poison must not be permanent — it is the retry-exhaustion path")
	}
}

// A flood of ordinary work drains clean: the backlog it builds is a
// matter of timing, not of errors.
func TestFloodDrainsWithoutDying(t *testing.T) {
	dir := t.TempDir()
	if err := runtime.BootLocal(dir); err != nil {
		t.Fatal(err)
	}
	registerHandlers(t)
	if _, err := Flood.Call(runtime.NewCtx(t.Context()), FloodReq{N: 7}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.WaitIdle(30 * time.Second); err != nil {
		t.Fatal(err)
	}
	if dead := deadLetters(t, dir, "work"); len(dead) != 0 {
		t.Errorf("a plain flood dead-lettered %d messages", len(dead))
	}
}

// The two crons must behave differently: CronMissed watches one stop,
// CronFailed counts the other failing. AdvanceCron is not used here
// because it fails the test on a cron error, and one of these is
// SUPPOSED to error.
func TestFlakyCronAlwaysFailsAndSweepDoesNot(t *testing.T) {
	app := tako.NewTestApp(t)
	app.AdvanceCron(Sweep)
	if err := flaky(app.Ctx()); err == nil {
		t.Error("Flaky must fail — CronFailed has nothing to count otherwise")
	}
	if err := sweep(app.Ctx()); err != nil {
		t.Errorf("Sweep must succeed, got %v", err)
	}
}
