package main

// `tako logs`: cloud read-only. Split out of deploy.go, which held
// three separate commands.

import (
	"context"
	"fmt"
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
		fmt.Printf("%s %-12s %-7s %s\n", e.Time.Local().Format("15:04:05"), e.Service, e.Severity, e.Text)
	}
	return nil
}
