package backends

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"maps"
	"time"
)

// FlagVariant is one weighted variant in a multi-variant experiment split.
type FlagVariant struct {
	Value  json.RawMessage `json:"value"`
	Weight int             `json:"weight"` // 0..100
}

// FlagRule is the stored dynamic configuration or experiment rule for one
// declared tako.Flag[T].
type FlagRule struct {
	Name       string          `json:"name"`
	Value      json.RawMessage `json:"value,omitempty"`
	RolloutPct int             `json:"rollout_pct,omitempty"`
	Variants   []FlagVariant   `json:"variants,omitempty"`
	Salt       string          `json:"salt,omitempty"`
	UpdatedAt  time.Time       `json:"updated_at"`
}

// Evaluate evaluates r for subjectID, returning the raw JSON value when a
// rule or experiment bucket matches, or (nil, false) when the caller should
// fall back to the flag's compiled default.
func (r FlagRule) Evaluate(subjectID string) (json.RawMessage, bool) {
	if len(r.Variants) > 0 {
		// If a single variant has weight >= 100, it applies even without a subject.
		if len(r.Variants) == 1 && r.Variants[0].Weight >= 100 && len(r.Variants[0].Value) > 0 {
			return r.Variants[0].Value, true
		}
		if subjectID == "" {
			return nil, false
		}
		b := FlagBucket(r.Name, r.Salt, subjectID)
		cum := 0
		for _, v := range r.Variants {
			if v.Weight <= 0 {
				continue
			}
			cum += v.Weight
			if b < cum {
				return v.Value, len(v.Value) > 0
			}
		}
		return nil, false
	}
	if r.RolloutPct > 0 {
		if r.RolloutPct >= 100 {
			return r.Value, len(r.Value) > 0
		}
		if subjectID == "" {
			return nil, false
		}
		if FlagBucket(r.Name, r.Salt, subjectID) < r.RolloutPct {
			return r.Value, len(r.Value) > 0
		}
		return nil, false
	}
	if len(r.Value) > 0 {
		return r.Value, true
	}
	return nil, false
}

// FlagBucket deterministically hashes (flagName, salt, subjectID) into 0..99.
func FlagBucket(flagName, salt, subjectID string) int {
	sum := sha256.Sum256([]byte(flagName + ":" + salt + ":" + subjectID))
	n := binary.BigEndian.Uint64(sum[:8])
	return int(n % 100)
}

// FlagSnapshot is an immutable point-in-time snapshot of all active flag rules.
// Pinning a FlagSnapshot onto a request/task Ctx ensures mid-execution flag
// updates never cause split-brain within a single execution.
type FlagSnapshot struct {
	Generation int64               `json:"generation"`
	Rules      map[string]FlagRule `json:"rules"`
}

// NewFlagSnapshot constructs an immutable FlagSnapshot from a rule map.
func NewFlagSnapshot(gen int64, rules map[string]FlagRule) *FlagSnapshot {
	cloned := make(map[string]FlagRule, len(rules))
	maps.Copy(cloned, rules)
	return &FlagSnapshot{Generation: gen, Rules: cloned}
}

// Lookup returns the FlagRule for name in this snapshot, if any.
func (s *FlagSnapshot) Lookup(name string) (FlagRule, bool) {
	if s == nil || len(s.Rules) == 0 {
		return FlagRule{}, false
	}
	r, ok := s.Rules[name]
	return r, ok
}

type flagSnapshotCtxKey struct{}

// WithFlagSnapshot pins snap onto ctx for the lifetime of an execution.
func WithFlagSnapshot(ctx context.Context, snap *FlagSnapshot) context.Context {
	if snap == nil {
		return ctx
	}
	return context.WithValue(ctx, flagSnapshotCtxKey{}, snap)
}

// FlagSnapshotFrom returns the execution-pinned FlagSnapshot from ctx, if any.
func FlagSnapshotFrom(ctx context.Context) (*FlagSnapshot, bool) {
	if ctx == nil {
		return nil, false
	}
	s, ok := ctx.Value(flagSnapshotCtxKey{}).(*FlagSnapshot)
	return s, ok && s != nil
}

// Flags is the runtime and operator store for feature flags, dynamic config,
// and experiments.
type Flags interface {
	// Snapshot returns the latest immutable in-memory FlagSnapshot.
	Snapshot() *FlagSnapshot
	// List returns all active FlagRules sorted by name.
	List(ctx context.Context) ([]FlagRule, error)
	// Put creates or updates a FlagRule and advances the snapshot generation.
	Put(ctx context.Context, rule FlagRule) error
	// Delete removes a FlagRule (reverting the flag to its compiled default).
	Delete(ctx context.Context, name string) error
}
