package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gsandoval/tako/pkg/provision/gcp"
)

func runAudit(raw []string) error {
	t, rest, err := parseTarget(raw)
	if err != nil {
		return err
	}
	var (
		stream    string
		actor     string
		cloudOnly bool
		jsonOut   bool
		since     = 24 * time.Hour
	)
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--json":
			jsonOut = true
		case "--cloud":
			cloudOnly = true
		case "--actor":
			if i+1 >= len(rest) {
				return fmt.Errorf("--actor needs a value")
			}
			i++
			actor = rest[i]
		case "--since":
			if i+1 >= len(rest) {
				return fmt.Errorf("--since needs a duration (e.g. 1h, 24h)")
			}
			i++
			d, err := time.ParseDuration(rest[i])
			if err != nil || d <= 0 {
				return fmt.Errorf("--since: invalid duration %q", rest[i])
			}
			since = d
		default:
			if strings.HasPrefix(rest[i], "-") {
				return fmt.Errorf("unknown flag %q (tako audit accepts --cloud, --actor, --since, --json, --project, --region, --env)", rest[i])
			}
			if stream != "" {
				return fmt.Errorf("unexpected argument %q", rest[i])
			}
			stream = rest[i]
		}
	}

	ctx := context.Background()
	client, err := gcp.NewClient(ctx, t.project, t.region)
	if err != nil {
		return err
	}
	entries, err := client.AuditLogs(ctx, stream, actor, cloudOnly, since)
	if err != nil {
		return err
	}
	if jsonOut {
		if entries == nil {
			entries = []gcp.AuditLogEntry{}
		}
		outBytes, err := json.MarshalIndent(map[string]any{
			"stream":  stream,
			"actor":   actor,
			"cloud":   cloudOnly,
			"entries": entries,
		}, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stdout, "%s\n", outBytes)
		return nil
	}
	if len(entries) == 0 {
		if cloudOnly {
			fmt.Fprintf(os.Stdout, "no Cloud Audit Log entries in the last %s\n", since)
		} else if stream != "" {
			fmt.Fprintf(os.Stdout, "no audit entries for stream %q in the last %s\n", stream, since)
		} else {
			fmt.Fprintf(os.Stdout, "no application audit entries in the last %s\n", since)
		}
		return nil
	}
	for _, e := range entries {
		who := e.Actor
		if who == "" {
			who = "anonymous"
		}
		if e.Resource != "" {
			fmt.Fprintf(os.Stdout, "%s  %-22s  %-22s  %s  (%s)\n",
				e.Time.Local().Format("2006-01-02 15:04:05"), e.Stream, who, e.Summary, e.Resource)
		} else {
			fmt.Fprintf(os.Stdout, "%s  %-18s  %-20s  %-15s  %s\n",
				e.Time.Local().Format("2006-01-02 15:04:05"), e.Stream, who, e.CallerIP, e.Summary)
		}
	}
	return nil
}
