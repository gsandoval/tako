package main

// `tako logs`: cloud read-only. Split out of deploy.go, which held
// three separate commands.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gsandoval/tako/pkg/analyzer"
	"github.com/gsandoval/tako/pkg/provision"
	"github.com/gsandoval/tako/pkg/provision/gcp"
)

// runLogs is `tako logs [service] [--since 15m] [--project …]`.
func runLogs(args []string) error {
	t, rest, err := parseTarget(args)
	if err != nil {
		return err
	}
	since := 15 * time.Minute
	var service string
	for i := 0; i < len(rest); i++ {
		if rest[i] == "--since" && i+1 < len(rest) {
			i++
			d, err := time.ParseDuration(rest[i])
			if err != nil {
				return fmt.Errorf("--since wants a duration like 15m, got %q", rest[i])
			}
			since = d
			continue
		}
		service = rest[i]
	}
	graph, err := analyzer.Analyze(analyzer.Config{Dir: "."})
	if err != nil {
		return err
	}
	cfg := provision.Config{Project: t.project, Region: t.region, App: graph.Module, Env: t.env}
	var services []string
	for _, svc := range graph.Services {
		if service == "" || svc.Name == service || cfg.Name(svc.Name) == service {
			services = append(services, cfg.Name(svc.Name))
		}
	}
	if len(services) == 0 {
		return fmt.Errorf("no service named %q — services: %s", service, serviceNames(graph))
	}
	client, err := gcp.NewClient(context.Background(), t.project, t.region)
	if err != nil {
		return err
	}
	entries, err := client.Logs(context.Background(), services, since, 200)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		fmt.Printf("no log entries in the last %s\n", since)
		return nil
	}
	for _, e := range entries {
		lines := strings.Split(e.Text, "\n")
		fmt.Printf("%s %-12s %-7s %s\n", e.Time.Local().Format("15:04:05"), e.Service, e.Severity, lines[0])
		// A structured entry's fields hang under its message, aligned
		// past the columns — an err carrying a Datastore index
		// recommendation is several lines and is worth every one.
		for _, l := range lines[1:] {
			fmt.Printf("%s%s\n", strings.Repeat(" ", logGutter), l)
		}
	}
	return nil
}

// logGutter is the width of the timestamp, service and severity columns
// above, so continuation lines start where the message does.
const logGutter = 9 + 13 + 8
