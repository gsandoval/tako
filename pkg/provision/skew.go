package provision

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/gsandoval/tako/pkg/analyzer"
	"github.com/gsandoval/tako/pkg/backends"
)

// SkewSeverity classifies a version or configuration skew finding.
type SkewSeverity string

const (
	// SkewWarning indicates an additive or notable drift (e.g. removed index,
	// orphaned flag override, public API signature change).
	SkewWarning SkewSeverity = "warning"
	// SkewDanger indicates a breaking wire or config mismatch across rolling
	// revisions (e.g. queue payload type change, cross-service RPC signature
	// change, or a live flag value incompatible with the binary's Flag[T] type).
	SkewDanger SkewSeverity = "danger"
)

// SkewFinding is one version-skew or config-vs-binary skew diagnostic.
type SkewFinding struct {
	Severity SkewSeverity `json:"severity"`
	Category string       `json:"category"` // "entity", "messaging", "rpc", "flag"
	Resource string       `json:"resource"`
	Message  string       `json:"message"`
}

// SkewReport holds all skew findings between two AppGraphs and active flags.
type SkewReport struct {
	Findings []SkewFinding `json:"findings,omitempty"`
}

// HasDanger reports whether any finding has SkewDanger severity.
func (r SkewReport) HasDanger() bool {
	for _, f := range r.Findings {
		if f.Severity == SkewDanger {
			return true
		}
	}
	return false
}

// Render formats the skew report for CLI output.
func (r SkewReport) Render() string {
	if len(r.Findings) == 0 {
		return ""
	}
	var b strings.Builder
	for _, f := range r.Findings {
		icon := "!"
		if f.Severity == SkewDanger {
			icon = "✗"
		}
		fmt.Fprintf(&b, "  %s skew [%-9s] %-22s %s\n", icon, f.Category, f.Resource, f.Message)
	}
	return b.String()
}

// CheckSkew evaluates binary-vs-binary and configuration-vs-binary version
// skew between prev (the currently deployed AppGraph, if any), next (the
// target AppGraph), and activeFlags (the live flag rules in the target env).
func CheckSkew(prev, next *analyzer.AppGraph, activeFlags []backends.FlagRule) SkewReport {
	var out SkewReport
	if next == nil {
		return out
	}

	if prev != nil {
		checkEntitySkew(prev, next, &out)
		checkMessagingSkew(prev, next, &out)
		checkRPCSkew(prev, next, &out)
	}
	checkFlagSkew(prev, next, activeFlags, &out)
	return out
}

func checkEntitySkew(prev, next *analyzer.AppGraph, out *SkewReport) {
	prevEntities := map[string]analyzer.Resource{}
	for _, r := range prev.Resources {
		if r.Kind == "entity" {
			prevEntities[r.Name] = r
		}
	}
	for _, r := range next.Resources {
		if r.Kind != "entity" {
			continue
		}
		old, ok := prevEntities[r.Name]
		if !ok {
			continue
		}
		if old.Type != "" && r.Type != "" && old.Type != r.Type {
			out.Findings = append(out.Findings, SkewFinding{
				Severity: SkewWarning,
				Category: "entity",
				Resource: r.Name,
				Message:  fmt.Sprintf("entity struct type changed from %s to %s — verify wire-compatible JSON field tags for existing documents", old.Type, r.Type),
			})
		}
		for _, idx := range old.Indexes {
			if !slices.Contains(r.Indexes, idx) {
				out.Findings = append(out.Findings, SkewFinding{
					Severity: SkewWarning,
					Category: "entity",
					Resource: r.Name,
					Message:  fmt.Sprintf("indexed field %q is no longer queried in new code — keep the index until previous revisions drain before running `tako prune`", idx),
				})
			}
		}
		if old.TTLField != r.TTLField {
			out.Findings = append(out.Findings, SkewFinding{
				Severity: SkewWarning,
				Category: "entity",
				Resource: r.Name,
				Message:  fmt.Sprintf("entity TTL field changed from %q to %q", old.TTLField, r.TTLField),
			})
		}
	}
}

func checkMessagingSkew(prev, next *analyzer.AppGraph, out *SkewReport) {
	prevCarriers := map[string]analyzer.Resource{}
	for _, r := range prev.Resources {
		if r.Kind == "queue" || r.Kind == "topic" {
			prevCarriers[r.Kind+":"+r.Name] = r
		}
	}
	for _, r := range next.Resources {
		if r.Kind != "queue" && r.Kind != "topic" {
			continue
		}
		old, ok := prevCarriers[r.Kind+":"+r.Name]
		if !ok {
			continue
		}
		if old.Type != "" && r.Type != "" && old.Type != r.Type {
			out.Findings = append(out.Findings, SkewFinding{
				Severity: SkewDanger,
				Category: "messaging",
				Resource: r.Name,
				Message:  fmt.Sprintf("%s payload type changed from %s to %s — in-flight messages from previous revision may fail to decode", r.Kind, old.Type, r.Type),
			})
		}
	}
}

func checkRPCSkew(prev, next *analyzer.AppGraph, out *SkewReport) {
	prevHandlers := map[string]analyzer.Handler{}
	for _, h := range prev.Handlers {
		if h.Kind == "api" || h.Kind == "page" {
			prevHandlers[h.Var] = h
		}
	}
	calledEndpoints := map[string]bool{}
	for _, e := range prev.Edges {
		if e.Kind == "call" {
			calledEndpoints[e.To] = true
		}
	}
	for _, e := range next.Edges {
		if e.Kind == "call" {
			calledEndpoints[e.To] = true
		}
	}

	for _, h := range next.Handlers {
		if h.Kind != "api" && h.Kind != "page" {
			continue
		}
		old, ok := prevHandlers[h.Var]
		if !ok {
			continue
		}
		if old.Route != h.Route || old.ReqRef != h.ReqRef || old.ResRef != h.ResRef {
			sev := SkewWarning
			why := "public endpoint signature or route changed"
			if calledEndpoints[h.Var] {
				sev = SkewDanger
				why = "cross-service RPC endpoint changed while other services call it"
			}
			out.Findings = append(out.Findings, SkewFinding{
				Severity: sev,
				Category: "rpc",
				Resource: h.Var,
				Message: fmt.Sprintf("%s (%s %s -> %s vs %s %s -> %s)",
					why, old.Route, old.ReqRef, old.ResRef, h.Route, h.ReqRef, h.ResRef),
			})
		}
	}
}

func checkFlagSkew(prev, next *analyzer.AppGraph, activeFlags []backends.FlagRule, out *SkewReport) {
	prevFlags := map[string]analyzer.Resource{}
	if prev != nil {
		for _, r := range prev.Resources {
			if r.Kind == "flag" {
				prevFlags[r.Name] = r
			}
		}
	}
	nextFlags := map[string]analyzer.Resource{}
	for _, r := range next.Resources {
		if r.Kind == "flag" {
			nextFlags[r.Name] = r
			if old, ok := prevFlags[r.Name]; ok && old.Type != "" && r.Type != "" && old.Type != r.Type {
				out.Findings = append(out.Findings, SkewFinding{
					Severity: SkewDanger,
					Category: "flag",
					Resource: r.Name,
					Message:  fmt.Sprintf("flag type changed from %s to %s — running revisions will disagree on the manifest value's type", old.Type, r.Type),
				})
			}
		}
	}

	for _, rule := range activeFlags {
		decl, ok := nextFlags[rule.Name]
		if !ok {
			out.Findings = append(out.Findings, SkewFinding{
				Severity: SkewWarning,
				Category: "flag",
				Resource: rule.Name,
				Message:  "live flag override exists in environment, but no tako.Flag declares this name in the binary (orphaned rule)",
			})
			continue
		}
		if len(rule.Value) > 0 && !flagJSONMatchesType(rule.Value, decl.Type) {
			out.Findings = append(out.Findings, SkewFinding{
				Severity: SkewDanger,
				Category: "flag",
				Resource: rule.Name,
				Message:  fmt.Sprintf("live flag value %s does not match binary's declared type %s", string(rule.Value), decl.Type),
			})
		}
		for _, v := range rule.Variants {
			if len(v.Value) > 0 && !flagJSONMatchesType(v.Value, decl.Type) {
				out.Findings = append(out.Findings, SkewFinding{
					Severity: SkewDanger,
					Category: "flag",
					Resource: rule.Name,
					Message:  fmt.Sprintf("live experiment variant value %s does not match binary's declared type %s", string(v.Value), decl.Type),
				})
			}
		}
	}
}

func flagJSONMatchesType(raw json.RawMessage, goType string) bool {
	switch goType {
	case "bool":
		var b bool
		return json.Unmarshal(raw, &b) == nil
	case "int", "int64", "int32":
		var n int64
		return json.Unmarshal(raw, &n) == nil
	case "float64", "float32":
		var f float64
		return json.Unmarshal(raw, &f) == nil
	case "string":
		var s string
		return json.Unmarshal(raw, &s) == nil
	default:
		return json.Valid(raw)
	}
}
