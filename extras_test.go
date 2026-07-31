package tako

// User-style tests for F8's primitives: Cache TTL + GetOrFill, Secret
// via SetSecret, and WithDelay riding the TestApp fake clock. The
// package-level vars mirror how a user declares these in an app.

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type testDigest struct{ N int }

var (
	testDigests      = Queue[testDigest]("test-digests")
	testFeed         = Cache[[]string]("test-feed", WithTTL(time.Minute))
	testKey          = Secret("test-key")
	digestDeliveries atomic.Int32

	TestDigestWorker = Worker(testDigests, handleTestDigest)
)

func handleTestDigest(ctx Ctx, d testDigest) error {
	digestDeliveries.Add(1)
	return nil
}

func TestWithDelayRidesTheFakeClock(t *testing.T) {
	app := NewTestApp(t)
	ctx := app.Ctx()
	digestDeliveries.Store(0)

	if err := testDigests.Publish(ctx, testDigest{N: 1}, WithDelay(4*time.Hour)); err != nil {
		t.Fatal(err)
	}
	// Not due: the parked message is idle, so DrainQueues returns at once.
	app.DrainQueues()
	if n := digestDeliveries.Load(); n != 0 {
		t.Fatalf("delivered %d times before the delay elapsed", n)
	}
	app.Advance(3 * time.Hour)
	app.DrainQueues()
	if n := digestDeliveries.Load(); n != 0 {
		t.Fatalf("delivered %d times at 3h of a 4h delay", n)
	}
	app.Advance(time.Hour)
	app.DrainQueues()
	if n := digestDeliveries.Load(); n != 1 {
		t.Fatalf("delivered %d times, want exactly 1", n)
	}
}

func TestPublishRejectsNonPublishOptions(t *testing.T) {
	app := NewTestApp(t)
	err := testDigests.Publish(app.Ctx(), testDigest{}, WithTTL(time.Minute))
	if err == nil || err.Error() != "tako.WithTTL is not a Publish option (Publish accepts WithDelay)" {
		t.Fatalf("err = %v", err)
	}
}

func TestCacheGetOrFillFillsOnceThenHits(t *testing.T) {
	app := NewTestApp(t)
	ctx := app.Ctx()
	fills := 0
	fill := func(Ctx) ([]string, error) { fills++; return []string{"a", "b"}, nil }

	got, err := testFeed.GetOrFill(ctx, "k", fill)
	if err != nil || len(got) != 2 {
		t.Fatalf("got = %v, %v", got, err)
	}
	if _, err := testFeed.GetOrFill(ctx, "k", fill); err != nil {
		t.Fatal(err)
	}
	if fills != 1 {
		t.Fatalf("fills = %d, want 1 (second call must hit)", fills)
	}
	if err := testFeed.Delete(ctx, "k"); err != nil {
		t.Fatal(err)
	}
	if _, err := testFeed.GetOrFill(ctx, "k", fill); err != nil {
		t.Fatal(err)
	}
	if fills != 2 {
		t.Fatalf("fills = %d, want 2 (delete must refill)", fills)
	}
}

func TestCacheMissIsNotFoundAndAppsAreIsolated(t *testing.T) {
	a := NewTestApp(t)
	b := NewTestApp(t)
	if err := testFeed.Put(a.Ctx(), "k", []string{"a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := testFeed.Get(b.Ctx(), "k"); !IsNotFound(err) {
		t.Fatalf("app b sees app a's cache (err = %v)", err)
	}
	if _, err := testFeed.Get(a.Ctx(), "k"); err != nil {
		t.Fatalf("app a lost its own entry: %v", err)
	}
}

func TestCacheRejectsNonCacheOptions(t *testing.T) {
	app := NewTestApp(t)
	bad := Cache[int]("test-bad-cache", WithDelay(time.Second))
	_, err := bad.Get(app.Ctx(), "k")
	if err == nil || err.Error() != "tako.WithDelay is not a Cache option (Cache accepts WithTTL)" {
		t.Fatalf("err = %v", err)
	}
}

func TestSecretResolvesViaSetSecret(t *testing.T) {
	app := NewTestApp(t)
	app.SetSecret("test-key", "s3cr3t")
	if v := testKey.Value(); v != "s3cr3t" {
		t.Fatalf("Value = %q", v)
	}
}

func TestUnsetSecretPanicsWithGuidance(t *testing.T) {
	defer func() {
		r := recover()
		msg, ok := r.(string)
		if !ok || !strings.Contains(msg, `secret "test-key" is not set`) || !strings.Contains(msg, "app.SetSecret") {
			t.Fatalf("panic = %v", r)
		}
	}()
	testKey.Value() // no SetSecret in this test — the cleanup removed it
	t.Fatal("Value on an unset secret did not panic")
}

func TestMetricsRecordWithoutError(t *testing.T) {
	app := NewTestApp(t)
	ctx := app.Ctx()
	Counter("test-ups").Add(ctx, 1)
	Histogram("test-ms").Observe(ctx, 12.5)
}

// TestHTMXCtx pins the T1 contract: app.HTMX drives a handler's htmx
// branch; the plain Ctx stays on the no-JS path.
func TestHTMXCtx(t *testing.T) {
	app := NewTestApp(t)
	if app.Ctx().IsHTMX() {
		t.Fatal("plain Ctx must not report htmx")
	}
	if !app.HTMX().IsHTMX() {
		t.Fatal("app.HTMX ctx must report htmx")
	}
	if !app.HTMX(app.SignIn("alice")).IsHTMX() {
		t.Fatal("HTMX with a user must keep the flag")
	}
}
