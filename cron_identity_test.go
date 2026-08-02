package tako

// X1: one cron, one name. A cron used to answer to two — the function
// name in a test, the declaring variable everywhere tako deployed it.
// These pin that the variable is now the only identity, and that the
// SDK no longer derives one by reflection.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gsandoval/tako/internal/hooks"
)

func TestAdvanceCronTakesTheValueNotAName(t *testing.T) {
	ran := 0
	cron := Cron("every 1h", func(Ctx) error { ran++; return nil })

	app := NewTestApp(t)
	app.AdvanceCron(cron)
	if ran != 1 {
		t.Fatalf("the cron ran %d times, want 1", ran)
	}
}

// The whole point of passing a value: the compiler checks it. There is
// no string to mistype and no second name to get wrong — which is what
// this test documents, since the old failure mode cannot be reproduced
// in code that compiles.
func TestWrongKindIsRefusedWithItsOwnRoute(t *testing.T) {
	q := Queue[struct{}]("x-refuse")
	worker := Worker(q, func(Ctx, struct{}) error { return nil })

	fake := &recordingTB{TB: t}
	app := &TestApp{tb: fake, app: NewTestApp(t).app}
	app.AdvanceCron(worker)

	if !strings.Contains(fake.msg, "that is a worker, not a cron") {
		t.Errorf("message = %q", fake.msg)
	}
	if !strings.Contains(fake.msg, "publish to the queue and DrainQueues") {
		t.Errorf("the refusal must say how a worker DOES run; got %q", fake.msg)
	}
}

func TestZeroHandlerIsRefusedByName(t *testing.T) {
	var never Handler // declared, never assigned a constructor

	fake := &recordingTB{TB: t}
	app := &TestApp{tb: fake, app: NewTestApp(t).app}
	app.AdvanceCron(never)

	if !strings.Contains(fake.msg, "the handler is empty") {
		t.Errorf("message = %q", fake.msg)
	}
}

// The closure case: two crons sharing one function. Under the old
// reflection scheme these were `func1`/`func2` — positional names that
// RENUMBERED when a cron was added above them, silently repointing a
// Cloud Scheduler job at a different body. Identity is now the value,
// so declaration order cannot rename anything.
func TestCronsSharingOneFunctionStayDistinct(t *testing.T) {
	sweep := func(depth int) func(Ctx) error {
		return func(Ctx) error { return nil }
	}
	hourly := Cron("every 1h", sweep(1))
	daily := Cron("every 24h", sweep(24))

	if hourly.id == daily.id {
		t.Fatal("two crons over one function share an id — their leases would collide")
	}
	if leaseKey(hourly.id) == leaseKey(daily.id) {
		t.Errorf("both lease on %q — a singleflight run of one would collapse the other",
			leaseKey(hourly.id))
	}
}

// The lease key is the declared name once a generated main registers
// it: that is the identity Cloud Scheduler and the reconciler use, and
// the only one stable across instances and deploys.
func TestLeaseKeyFollowsTheDeclaredName(t *testing.T) {
	cron := Cron("every 1h", func(Ctx) error { return nil })

	// Before registration a test can still name it, by function.
	if got := leaseKey(cron.id); !strings.HasPrefix(got, "cron-") {
		t.Fatalf("lease key %q", got)
	}
	hooks.SetHandlerIdentity(cron.id, "Cleanup")
	if got := leaseKey(cron.id); got != "cron-Cleanup" {
		t.Errorf("lease key = %q, want cron-Cleanup — the deployed identity", got)
	}
}

// Singleton lost its name argument: the variable already said it, and
// the name it took was never what the service was called.
func TestSingletonIsIdentifiedByItsVariable(t *testing.T) {
	loop := Singleton(func(Ctx) error { return nil })
	if loop.kind != "singleton" || loop.id == 0 {
		t.Fatalf("singleton = %+v", loop)
	}
}

// recordingTB captures a Fatalf instead of ending the test, so refusal
// texts can be asserted.
type recordingTB struct {
	testing.TB
	msg string
}

func (r *recordingTB) Fatal(args ...any)            { r.msg = fmt.Sprint(args...) }
func (r *recordingTB) Fatalf(f string, args ...any) { r.msg = fmt.Sprintf(f, args...) }
func (r *recordingTB) Helper()                      {}
