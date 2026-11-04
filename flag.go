package tako

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"sync"

	"github.com/gsandoval/tako/internal/hooks"
	"github.com/gsandoval/tako/internal/trace"
	"github.com/gsandoval/tako/pkg/backends"
)

var (
	declaredFlagsMu sync.Mutex
	declaredFlags   = map[string]hooks.DeclaredFlag{}
)

func init() {
	hooks.DeclaredFlags = func() []hooks.DeclaredFlag {
		declaredFlagsMu.Lock()
		defer declaredFlagsMu.Unlock()
		out := make([]hooks.DeclaredFlag, 0, len(declaredFlags))
		for _, f := range declaredFlags {
			out = append(out, f)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
		return out
	}
}

// Flag declares a typed feature flag, dynamic configuration value, or
// experiment with a compiled fallback defaultVal. The name is the flag's
// stable identity across services and the CLI, and must be a string
// constant (analyzer-enforced).
//
//	var NewCheckout = tako.Flag("new-checkout", false)
//	var CheckoutFlow = tako.Flag("checkout-flow", "control")
func Flag[T any](name string, defaultVal T) *FlagHandle[T] {
	raw, _ := json.Marshal(defaultVal)
	t := reflect.TypeFor[T]()
	declaredFlagsMu.Lock()
	declaredFlags[name] = hooks.DeclaredFlag{
		Name:        name,
		TypeName:    t.String(),
		DefaultJSON: raw,
	}
	declaredFlagsMu.Unlock()
	return &FlagHandle[T]{
		name:       name,
		defaultVal: defaultVal,
	}
}

// FlagHandle is a declared feature flag, dynamic config, or experiment of
// type T.
type FlagHandle[T any] struct {
	name       string
	defaultVal T
}

// Name returns the flag's declared name.
func (f *FlagHandle[T]) Name() string { return f.name }

// Get evaluates the flag or experiment for the current execution context.
//
// For percentage rollouts and multi-variant experiments, Get automatically
// selects the subject identifier from ctx: the authenticated User.ID when
// signed in, else Client.DeviceID (X-Tako-Device-ID), else Client.IP. In
// background workers, crons, or jobs without an interactive user on ctx,
// call GetFor with the target user or entity ID.
//
// Within a single execution (and across downstream cross-service calls and
// queue/topic deliveries in the same trace), the flag snapshot is pinned at
// execution start so mid-task flag updates never cause split-brain.
func (f *FlagHandle[T]) Get(ctx Ctx) T {
	return f.eval(ctx, subjectFromCtx(ctx), true)
}

// GetFor evaluates the flag or experiment for an explicit subjectID (such
// as a user ID, organization ID, or device ID in a background worker or
// batch job).
func (f *FlagHandle[T]) GetFor(ctx Ctx, subjectID string) T {
	return f.eval(ctx, subjectID, false)
}

func (f *FlagHandle[T]) eval(ctx Ctx, subjectID string, defaultSubject bool) T {
	if ctx == nil {
		return f.defaultVal
	}
	// 1. Check sticky decision propagated from upstream in the same trace
	// (or already evaluated on this context for the same subject).
	decisionKey := f.name
	if !defaultSubject && subjectID != "" {
		decisionKey = f.name + "@" + subjectID
	}
	if raw, ok := trace.LookupFlagDecision(ctx, decisionKey); ok {
		var val T
		if json.Unmarshal([]byte(raw), &val) == nil {
			return val
		}
	}
	if defaultSubject {
		if raw, ok := trace.LookupFlagDecision(ctx, f.name); ok {
			var val T
			if json.Unmarshal([]byte(raw), &val) == nil {
				return val
			}
		}
	}

	// 2. Resolve the execution-pinned FlagSnapshot so mid-task updates
	// after this execution started never cause split-brain.
	snap := pinnedFlagSnapshot(ctx)
	if snap == nil {
		return f.defaultVal
	}
	rule, ok := snap.Lookup(f.name)
	if !ok {
		if raw, err := json.Marshal(f.defaultVal); err == nil {
			trace.RecordFlagDecision(ctx, decisionKey, string(raw))
			if defaultSubject {
				trace.RecordFlagDecision(ctx, f.name, string(raw))
			}
		}
		return f.defaultVal
	}
	raw, matched := rule.Evaluate(subjectID)
	if !matched {
		if defRaw, err := json.Marshal(f.defaultVal); err == nil {
			trace.RecordFlagDecision(ctx, decisionKey, string(defRaw))
			if defaultSubject {
				trace.RecordFlagDecision(ctx, f.name, string(defRaw))
			}
		}
		return f.defaultVal
	}
	var val T
	if err := json.Unmarshal(raw, &val); err != nil {
		return f.defaultVal
	}
	trace.RecordFlagDecision(ctx, decisionKey, string(raw))
	if defaultSubject {
		trace.RecordFlagDecision(ctx, f.name, string(raw))
	}
	return val
}

func subjectFromCtx(ctx Ctx) string {
	if ctx == nil {
		return ""
	}
	if u, ok := ctx.MaybeUser(); ok && u.ID != "" {
		return u.ID
	}
	c := ctx.Client()
	if c.DeviceID != "" {
		return c.DeviceID
	}
	return c.IP
}

type flagPinHolder struct {
	mu   sync.Mutex
	snap *backends.FlagSnapshot
}

type flagPinKey struct{}

func withFlagPinHolder(ctx context.Context) context.Context {
	if ctx != nil {
		if _, ok := ctx.Value(flagPinKey{}).(*flagPinHolder); ok {
			return ctx
		}
	}
	return context.WithValue(ctx, flagPinKey{}, &flagPinHolder{})
}

func pinnedFlagSnapshot(ctx Ctx) *backends.FlagSnapshot {
	if snap, ok := backends.FlagSnapshotFrom(ctx); ok {
		return snap
	}
	if h, ok := ctx.Value(flagPinKey{}).(*flagPinHolder); ok && h != nil {
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.snap == nil {
			if fb := flagsBackend(ctx); fb != nil {
				h.snap = fb.Snapshot()
			}
		}
		return h.snap
	}
	if fb := flagsBackend(ctx); fb != nil {
		return fb.Snapshot()
	}
	return nil
}

func flagsBackend(ctx context.Context) backends.Flags {
	if a := appFrom(ctx); a != nil && a.Flags != nil {
		return a.Flags
	}
	return hooks.Flags
}

func flagNameOf(flag any) (string, error) {
	if flag == nil {
		return "", fmt.Errorf("flag handle is nil")
	}
	if n, ok := flag.(interface{ Name() string }); ok {
		return n.Name(), nil
	}
	if s, ok := flag.(string); ok && s != "" {
		return s, nil
	}
	return "", fmt.Errorf("expected a *tako.FlagHandle[T] (or flag name string), got %T", flag)
}
