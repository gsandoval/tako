package runtime

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	tako "github.com/gsandoval/tako"
	"github.com/gsandoval/tako/internal/hooks"
	"github.com/gsandoval/tako/pkg/backends/local"
)

func TestSafetyIdempotentReplayAndRetryOn5xx(t *testing.T) {
	store, err := local.OpenEntity(t.TempDir())
	if err != nil {
		t.Fatalf("OpenEntity: %v", err)
	}
	defer store.Close()
	prevEntity := hooks.Entity
	hooks.Entity = store
	defer func() { hooks.Entity = prevEntity }()

	var calls atomic.Int32
	failNext := false
	ep := tako.API("POST /charge", func(ctx tako.Ctx, _ struct{}) (map[string]any, error) {
		n := calls.Add(1)
		if failNext {
			return nil, errors.New("transient upstream error")
		}
		return map[string]any{"charge_id": n}, nil
	})
	h := JSONHandler(ep, nil, Idempotent())

	// 1. First request with Idempotency-Key runs the handler.
	req1 := httptest.NewRequest("POST", "/charge", nil)
	req1.Header.Set("Idempotency-Key", "idem-123")
	req1.Header.Set("X-Tako-Device-ID", "dev-1")
	rec1 := httptest.NewRecorder()
	h.ServeHTTP(rec1, req1)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first call status = %d: %s", rec1.Code, rec1.Body.String())
	}

	// 2. Duplicate request with same Idempotency-Key replays stored response without calling handler.
	req2 := httptest.NewRequest("POST", "/charge", nil)
	req2.Header.Set("Idempotency-Key", "idem-123")
	req2.Header.Set("X-Tako-Device-ID", "dev-1")
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("replay call status = %d", rec2.Code)
	}
	if rec2.Header().Get("X-Tako-Idempotent-Replay") != "true" {
		t.Fatalf("expected X-Tako-Idempotent-Replay: true header")
	}
	if calls.Load() != 1 {
		t.Fatalf("handler called %d times, want 1", calls.Load())
	}
	var body1, body2 map[string]any
	_ = json.Unmarshal(rec1.Body.Bytes(), &body1)
	_ = json.Unmarshal(rec2.Body.Bytes(), &body2)
	if body1["charge_id"] != body2["charge_id"] {
		t.Fatalf("replayed body %v != original %v", body2, body1)
	}

	// 3. A 500 error releases the key so a retry with the same Idempotency-Key succeeds.
	failNext = true
	reqFail := httptest.NewRequest("POST", "/charge", nil)
	reqFail.Header.Set("Idempotency-Key", "idem-retry")
	reqFail.Header.Set("X-Tako-Device-ID", "dev-1")
	recFail := httptest.NewRecorder()
	h.ServeHTTP(recFail, reqFail)
	if recFail.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", recFail.Code)
	}

	failNext = false
	reqRetry := httptest.NewRequest("POST", "/charge", nil)
	reqRetry.Header.Set("Idempotency-Key", "idem-retry")
	reqRetry.Header.Set("X-Tako-Device-ID", "dev-1")
	recRetry := httptest.NewRecorder()
	h.ServeHTTP(recRetry, reqRetry)
	if recRetry.Code != http.StatusOK {
		t.Fatalf("retry after 500 status = %d, want 200", recRetry.Code)
	}
}

func TestSafetyGlobalRateLimitAcrossTasks(t *testing.T) {
	store, err := local.OpenEntity(t.TempDir())
	if err != nil {
		t.Fatalf("OpenEntity: %v", err)
	}
	defer store.Close()
	prevEntity := hooks.Entity
	hooks.Entity = store
	defer func() { hooks.Entity = prevEntity }()

	ep := tako.API("POST /login", func(ctx tako.Ctx, _ struct{}) (map[string]bool, error) {
		return map[string]bool{"ok": true}, nil
	})
	h := JSONHandler(ep, nil, WithRateLimit(2, 60))

	send := func(device string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/login", nil)
		req.Header.Set("X-Tako-Device-ID", device)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	// Task 1: request 1 and 2 from dev-A succeed.
	if r := send("dev-A"); r.Code != http.StatusOK {
		t.Fatalf("req 1 status = %d", r.Code)
	}
	// Simulate request 2 arriving at a different Cloud Run task (clear local exhaustion cache).
	exhaustedRateBuckets = sync.Map{}
	if r := send("dev-A"); r.Code != http.StatusOK {
		t.Fatalf("req 2 status = %d", r.Code)
	}

	// Simulate request 3 arriving at a third Cloud Run task (empty local cache, but shared Datastore counter is at 2).
	exhaustedRateBuckets = sync.Map{}
	r3 := send("dev-A")
	if r3.Code != http.StatusTooManyRequests {
		t.Fatalf("req 3 across task boundary: status = %d, want 429", r3.Code)
	}
	if r3.Header().Get("Retry-After") == "" {
		t.Fatalf("expected Retry-After header on 429")
	}

	// Request 4 on the same task hits the local exhaustion fast-path and still returns 429.
	r4 := send("dev-A")
	if r4.Code != http.StatusTooManyRequests {
		t.Fatalf("req 4 status = %d, want 429", r4.Code)
	}

	// Another device has its own independent quota.
	if rB := send("dev-B"); rB.Code != http.StatusOK {
		t.Fatalf("dev-B status = %d, want 200", rB.Code)
	}
}
