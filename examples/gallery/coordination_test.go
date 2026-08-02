package gallery

// The W8 contract, user-style: reindex runs under the "reindex" lock
// (a caller finding it held collapses, the ledger shows disjoint
// windows) and Cleanup's CronConfig.Singleflight collapses overlapping cron
// runs through the same lease mechanism. Contention is deterministic:
// the tests occupy the very lease the code campaigns for — lock names
// ARE the identity, pinned here as contract.

import (
	"strings"
	"testing"

	tako "github.com/gsandoval/tako"
)

func textBlob(s string) tako.Blob {
	return tako.Blob{Reader: strings.NewReader(s), ContentType: "image/jpeg", Size: int64(len(s))}
}

func TestReindexRepairsAndKeepsALedger(t *testing.T) {
	app := tako.NewTestApp(t)
	app.SetSecret("notifier-key", "nk_test_123")
	ctx := app.Ctx()

	// A ready photo WITH its thumb (survives) and one whose thumb an
	// operator deleted (must be demoted).
	if err := photoStore.Put(ctx, Photo{ID: "whole", Owner: "alice", Ready: true}); err != nil {
		t.Fatal(err)
	}
	if err := photos.Put(ctx, "whole_thumb", textBlob("x")); err != nil {
		t.Fatal(err)
	}
	if err := photoStore.Put(ctx, Photo{ID: "torn", Owner: "alice", Ready: true}); err != nil {
		t.Fatal(err)
	}

	res, err := Reindex.Call(ctx, struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Started || res.Run.Photos != 2 || res.Run.Demoted != 1 {
		t.Fatalf("reindex = %+v, want started with 2 photos, 1 demoted", res)
	}
	torn, err := photoStore.Get(ctx, "torn")
	if err != nil || torn.Ready {
		t.Fatalf("torn photo = %+v, %v — want demoted (Ready=false)", torn, err)
	}
	runs, err := ReindexRuns.Call(ctx, struct{}{})
	if err != nil || len(runs) != 1 {
		t.Fatalf("ledger = %+v, %v — want exactly one run", runs, err)
	}
	if !runs[0].Finished.After(runs[0].Started) {
		t.Fatalf("run window is empty: %+v", runs[0])
	}
}

func TestReindexCollapsesWhileHeld(t *testing.T) {
	app := tako.NewTestApp(t)
	app.SetSecret("notifier-key", "nk_test_123")
	ctx := app.Ctx()

	holding := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- tako.Lock("reindex").Do(ctx, func(tako.Ctx) error {
			close(holding)
			<-release
			return nil
		})
	}()
	<-holding

	res, err := Reindex.Call(ctx, struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Started {
		t.Fatal("reindex claims to have started while the lock was held elsewhere")
	}

	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	res2, err := Reindex.Call(ctx, struct{}{})
	if err != nil || !res2.Started {
		t.Fatalf("reindex after release = %+v, %v — want started", res2, err)
	}
	// Two calls, one ledger entry: the collapsed call never ran.
	runs, err := ReindexRuns.Call(ctx, struct{}{})
	if err != nil || len(runs) != 1 {
		t.Fatalf("ledger = %d runs, %v — the collapsed call must not have written one", len(runs), err)
	}
}

func TestCleanupSingleflightCollapses(t *testing.T) {
	app := tako.NewTestApp(t)
	app.SetSecret("notifier-key", "nk_test_123")
	ctx := app.Ctx()

	// An orphan the cleanup would delete.
	if err := photoStore.Put(ctx, Photo{ID: "orphan", Owner: "alice", Ready: false}); err != nil {
		t.Fatal(err)
	}

	// Hold the CRON's lease — named "cron-<handler>" — and fire the
	// cron: the run must collapse into the (simulated) overlapping one.
	holding := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- tako.Lock("cron-cleanupOrphans").Do(ctx, func(tako.Ctx) error {
			close(holding)
			<-release
			return nil
		})
	}()
	<-holding
	app.AdvanceCron(Cleanup)
	if _, err := photoStore.Get(ctx, "orphan"); err != nil {
		t.Fatalf("collapsed cron run deleted the orphan anyway: %v", err)
	}

	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	app.AdvanceCron(Cleanup)
	if _, err := photoStore.Get(ctx, "orphan"); !tako.IsNotFound(err) {
		t.Fatalf("orphan after a free cron run = %v, want deleted", err)
	}
}
