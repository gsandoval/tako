package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gsandoval/tako/internal/trace"
	"github.com/gsandoval/tako/pkg/analyzer"
	"github.com/gsandoval/tako/pkg/provision"
	"github.com/gsandoval/tako/pkg/provision/gcp"
)

// runTrace is `tako trace <id> [--since 1h] [--json] [--project …]`.
func runTrace(args []string) error {
	t, rest, err := parseTarget(args)
	if err != nil {
		return err
	}
	since := time.Hour
	var id string
	var jsonOut bool
	for i := 0; i < len(rest); i++ {
		if rest[i] == "--json" {
			jsonOut = true
			continue
		}
		if rest[i] == "--since" && i+1 < len(rest) {
			i++
			d, err := time.ParseDuration(rest[i])
			if err != nil {
				return fmt.Errorf("--since wants a duration like 15m or 1h, got %q", rest[i])
			}
			since = d
			continue
		}
		if id == "" {
			id = rest[i]
		} else {
			return fmt.Errorf("unexpected argument %q (usage: tako trace <trace-id-or-req-id> [--since 1h] [--json])", rest[i])
		}
	}
	if id == "" {
		return fmt.Errorf("tako trace requires a trace ID or request ID (e.g. tako trace 4a9f1c)")
	}
	graph, err := analyzer.Analyze(analyzer.Config{Dir: "."})
	if err != nil {
		return err
	}
	cfg := provision.Config{Project: t.project, Region: t.region, App: graph.Module, Env: t.env}
	var services []string
	for _, svc := range graph.Services {
		services = append(services, cfg.Name(svc.Name))
	}
	client, err := gcp.NewClient(context.Background(), t.project, t.region)
	if err != nil {
		return err
	}
	spans, logs, err := client.Trace(context.Background(), services, id, since)
	if err != nil {
		return err
	}
	if jsonOut {
		if spans == nil {
			spans = []trace.Span{}
		}
		if logs == nil {
			logs = []gcp.LogEntry{}
		}
		raw, err := json.MarshalIndent(map[string]any{
			"query_id": id,
			"spans":    spans,
			"logs":     logs,
		}, "", "  ")
		if err != nil {
			return err
		}
		fmt.Printf("%s\n", raw)
		return nil
	}
	if len(spans) == 0 && len(logs) == 0 {
		fmt.Printf("no trace spans or logs found for %q in the last %s\n", id, since)
		return nil
	}
	fmt.Print(formatTraceWaterfall(id, spans, logs))
	return nil
}

// formatTraceWaterfall renders a hierarchical span waterfall followed by
// correlated log entries.
func formatTraceWaterfall(queryID string, spans []trace.Span, logs []gcp.LogEntry) string {
	var b strings.Builder
	traceID := queryID
	reqID := ""
	for _, s := range spans {
		if s.TraceID != "" {
			traceID = s.TraceID
		}
		if s.ReqID != "" && reqID == "" {
			reqID = s.ReqID
		}
	}
	header := fmt.Sprintf("trace %s", traceID)
	if reqID != "" && reqID != traceID {
		header += fmt.Sprintf(" [req=%s]", reqID)
	}
	b.WriteString(header + "\n")

	if len(spans) > 0 {
		b.WriteString("\n  spans:\n")
		byID := map[string]trace.Span{}
		for _, s := range spans {
			if s.SpanID != "" {
				byID[s.SpanID] = s
			}
		}
		children := map[string][]trace.Span{}
		var roots []trace.Span
		for _, s := range spans {
			if s.ParentSpanID != "" {
				if _, ok := byID[s.ParentSpanID]; ok && s.ParentSpanID != s.SpanID {
					children[s.ParentSpanID] = append(children[s.ParentSpanID], s)
					continue
				}
			}
			roots = append(roots, s)
		}
		var renderNode func(s trace.Span, prefix, childPrefix string)
		renderNode = func(s trace.Span, prefix, childPrefix string) {
			svc := s.Service
			if svc == "" {
				svc = "-"
			}
			statusPart := ""
			if s.Status > 0 {
				statusPart = fmt.Sprintf(" → %d", s.Status)
			}
			if s.Err != "" {
				statusPart += fmt.Sprintf(" (err: %s)", s.Err)
			}
			var attrParts []string
			for k, v := range s.Attributes {
				if strings.HasPrefix(k, "flag.") {
					attrParts = append(attrParts, k+"="+v)
				}
			}
			sort.Strings(attrParts)
			attrSuffix := ""
			if len(attrParts) > 0 {
				attrSuffix = " [" + strings.Join(attrParts, ", ") + "]"
			}
			fmt.Fprintf(&b, "  %s[%s] %s: %s (%.1fms)%s%s\n",
				prefix, svc, s.Kind, s.Name, s.DurationMs, statusPart, attrSuffix)
			kids := children[s.SpanID]
			for i, child := range kids {
				last := i == len(kids)-1
				conn := "├─ "
				nextPref := childPrefix + "│  "
				if last {
					conn = "└─ "
					nextPref = childPrefix + "   "
				}
				renderNode(child, childPrefix+conn, nextPref)
			}
		}
		for _, r := range roots {
			renderNode(r, "", "   ")
		}
	}

	if len(logs) > 0 {
		b.WriteString("\n  logs:\n")
		for _, e := range logs {
			lines := strings.Split(e.Text, "\n")
			fmt.Fprintf(&b, "  %s %-12s %-7s %s\n", e.Time.Local().Format("15:04:05"), e.Service, e.Severity, lines[0])
			for _, l := range lines[1:] {
				fmt.Fprintf(&b, "  %s%s\n", strings.Repeat(" ", logGutter), l)
			}
		}
	}
	return b.String()
}
