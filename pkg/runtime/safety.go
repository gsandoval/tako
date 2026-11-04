package runtime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	tako "github.com/gsandoval/tako"
	"github.com/gsandoval/tako/pkg/backends"
)

const (
	idempotencyKind   = "_tako_idempotency"
	idempotencyHeader = "Idempotency-Key"
	idempotencyTTL    = 24 * time.Hour
	idempotencyLock   = 5 * time.Minute

	rateLimitKind = "_tako_ratelimit"
)

// Idempotent enables Route.Idempotent deduplication on a generated route
// handler using the Idempotency-Key request header.
func Idempotent() HandlerOption {
	return func(c *handlerConf) { c.idempotent = true }
}

// WithRateLimit configures Route.RateLimit on a generated route handler.
func WithRateLimit(requests int, windowSeconds int) HandlerOption {
	return func(c *handlerConf) {
		c.rateLimitRequests = requests
		c.rateLimitWindowSeconds = windowSeconds
	}
}

type idempotencyRecord struct {
	State       string    `json:"state"` // "in_progress" or "completed"
	Status      int       `json:"status,omitempty"`
	ContentType string    `json:"content_type,omitempty"`
	Location    string    `json:"location,omitempty"`
	Body        []byte    `json:"body,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}

type rateLimitRecord struct {
	Count     int       `json:"count"`
	ResetAt   time.Time `json:"reset_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

var exhaustedRateBuckets sync.Map // bucketKey -> time.Time (windowReset)

func callerSubject(r *http.Request, session *sessionRecord) string {
	if session != nil && session.UID != "" {
		return "uid:" + session.UID
	}
	if dev := strings.TrimSpace(r.Header.Get("X-Tako-Device-ID")); dev != "" {
		return "dev:" + dev
	}
	if ip := clientOf(r).IP; ip != "" {
		return "ip:" + ip
	}
	return "anon"
}

func hashSafetyKey(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(sum[:16])
}

// checkRateLimit enforces a globally visible quota across all running
// instances via an atomic entity-store transaction on _tako_ratelimit,
// paired with an in-memory exhaustion cache so shed traffic after quota
// exhaustion costs zero Datastore operations.
func checkRateLimit(ctx context.Context, r *http.Request, session *sessionRecord, conf handlerConf) (allowed bool, retryAfterSecs int) {
	if conf.rateLimitRequests <= 0 || conf.rateLimitWindowSeconds <= 0 {
		return true, 0
	}
	winSecs := int64(conf.rateLimitWindowSeconds)
	now := time.Now().UTC()
	slot := now.Unix() / winSecs
	resetAt := time.Unix((slot+1)*winSecs, 0).UTC()
	retryAfter := int(resetAt.Sub(now).Seconds())
	if retryAfter < 1 {
		retryAfter = 1
	}

	subj := callerSubject(r, session)
	bucketKey := hashSafetyKey(r.Method, r.URL.Path, subj, strconv.FormatInt(slot, 10))

	if v, ok := exhaustedRateBuckets.Load(bucketKey); ok {
		if exp, ok := v.(time.Time); ok && now.Before(exp) {
			return false, retryAfter
		}
		exhaustedRateBuckets.Delete(bucketKey)
	}

	store := entityStore(ctx)
	if store == nil {
		return true, 0
	}

	allowed = true
	_ = store.Tx(ctx, rateLimitKind, nil, func(tx backends.EntityTxOps) error {
		var rec rateLimitRecord
		if raw, err := tx.Get(bucketKey); err == nil {
			_ = json.Unmarshal(raw, &rec)
		}
		rec.Count++
		rec.ResetAt = resetAt
		rec.ExpiresAt = resetAt.Add(time.Minute)
		if rec.Count > conf.rateLimitRequests {
			allowed = false
		}
		raw, err := json.Marshal(rec)
		if err != nil {
			return err
		}
		return tx.Put(bucketKey, raw)
	})
	if !allowed {
		exhaustedRateBuckets.Store(bucketKey, resetAt)
		return false, retryAfter
	}
	return true, 0
}

type safetyRecorder struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (sr *safetyRecorder) WriteHeader(code int) {
	sr.status = code
	sr.ResponseWriter.WriteHeader(code)
}

func (sr *safetyRecorder) Write(p []byte) (int, error) {
	if sr.status == 0 {
		sr.status = http.StatusOK
	}
	sr.body.Write(p)
	return sr.ResponseWriter.Write(p)
}

// runWithSafety wraps an HTTP handler execution with RateLimit and
// Idempotency-Key enforcement when configured on conf.
func runWithSafety(w http.ResponseWriter, r *http.Request, ctx context.Context, session *sessionRecord, reqID string, isPage bool, conf handlerConf, next func(http.ResponseWriter)) {
	if ok, retryAfter := checkRateLimit(ctx, r, session, conf); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
		if isPage {
			writeStatus(w, r, tako.Status{
				Code:      http.StatusTooManyRequests,
				RequestID: reqID,
				Message:   "rate limit exceeded — try again shortly",
			})
		} else {
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate limit exceeded"})
		}
		return
	}

	idemKey := strings.TrimSpace(r.Header.Get(idempotencyHeader))
	if !conf.idempotent || idemKey == "" {
		next(w)
		return
	}
	store := entityStore(ctx)
	if store == nil {
		next(w)
		return
	}

	subj := callerSubject(r, session)
	docKey := hashSafetyKey(r.Method, r.URL.Path, subj, idemKey)
	now := time.Now().UTC()

	var replay *idempotencyRecord
	conflict := false
	err := store.Tx(ctx, idempotencyKind, nil, func(tx backends.EntityTxOps) error {
		if raw, getErr := tx.Get(docKey); getErr == nil {
			var existing idempotencyRecord
			if json.Unmarshal(raw, &existing) == nil && now.Before(existing.ExpiresAt) {
				if existing.State == "completed" {
					replay = &existing
					return nil
				}
				if existing.State == "in_progress" && now.Sub(existing.CreatedAt) < idempotencyLock {
					conflict = true
					return nil
				}
			}
		}
		rec := idempotencyRecord{
			State:     "in_progress",
			CreatedAt: now,
			ExpiresAt: now.Add(idempotencyTTL),
		}
		raw, err := json.Marshal(rec)
		if err != nil {
			return err
		}
		return tx.Put(docKey, raw)
	})
	if err != nil {
		next(w)
		return
	}
	if conflict {
		if isPage {
			writeStatus(w, r, tako.Status{
				Code:      http.StatusConflict,
				RequestID: reqID,
				Message:   "request with this Idempotency-Key is already in progress",
			})
		} else {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "request with this Idempotency-Key is already in progress"})
		}
		return
	}
	if replay != nil {
		if replay.ContentType != "" {
			w.Header().Set("Content-Type", replay.ContentType)
		}
		if replay.Location != "" {
			w.Header().Set("Location", replay.Location)
		}
		w.Header().Set("X-Tako-Idempotent-Replay", "true")
		status := replay.Status
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		_, _ = w.Write(replay.Body)
		return
	}

	rec := &safetyRecorder{ResponseWriter: w}
	next(rec)
	status := rec.status
	if status == 0 {
		status = http.StatusOK
	}
	if status >= 500 {
		_ = store.Delete(ctx, idempotencyKind, docKey)
		return
	}
	done := idempotencyRecord{
		State:       "completed",
		Status:      status,
		ContentType: rec.Header().Get("Content-Type"),
		Location:    rec.Header().Get("Location"),
		Body:        rec.body.Bytes(),
		CreatedAt:   now,
		ExpiresAt:   now.Add(idempotencyTTL),
	}
	if raw, err := json.Marshal(done); err == nil {
		_ = store.Put(ctx, idempotencyKind, docKey, raw, nil)
	}
}
