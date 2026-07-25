// Command tako is the Tako CLI: dev, deploy, gen, test, logs, destroy,
// prune.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/gsandoval/tako/pkg/analyzer"
	"github.com/gsandoval/tako/pkg/codegen"
	"github.com/gsandoval/tako/pkg/devserver"
)

const usage = `tako — a Go framework over Google Cloud Run

Usage:
  tako dev [--chaos]   run the app in this directory locally, with reload
                       (--chaos: seeded duplicate/reordered deliveries)
  tako test [args]     analyze, refresh generated code, then go test ./...
  tako build [--push <repo>]
                       build every service as a digest-pinned distroless
                       image (no Docker daemon); --push uploads each to
                       <repo>/<service> on Artifact Registry
  tako deploy [--plan] [--project <id>] [--region <r>] [--env <e>]
                       reconcile the app onto GCP: preflight, classified
                       plan (--plan stops there), lease, apply, live URL
  tako logs [service] [--since 15m]
                       recent logs for the deployed services
  tako dlq [service] [--ack]
                       dead-lettered messages per async service (peek);
                       --ack drains the printed messages for good
  tako destroy [--project <id>] [--env <e>]
                       delete everything labeled for this app (typed
                       confirmation; never touches unlabeled resources)
  tako gen graph [--json | --mermaid]
                       print the app's AppGraph: human summary (default),
                       pipeable JSON, or a mermaid diagram

Commands arriving in later slices: prune, rename, canary/promote/rollback.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "dev":
		opts := devserver.Options{Dir: "."}
		for _, arg := range os.Args[2:] {
			if arg == "--chaos" {
				opts.ChaosSeed = time.Now().UnixNano()
			}
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		if err := devserver.Run(ctx, opts); err != nil {
			fail(err)
		}
	case "test":
		if err := runTest(os.Args[2:]); err != nil {
			fail(err)
		}
	case "build":
		if err := runBuild(os.Args[2:]); err != nil {
			fail(err)
		}
	case "deploy":
		if err := runDeploy(os.Args[2:]); err != nil {
			fail(err)
		}
	case "dlq":
		if err := runDLQ(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "tako: %v\n", err)
			os.Exit(1)
		}
	case "logs":
		if err := runLogs(os.Args[2:]); err != nil {
			fail(err)
		}
	case "destroy":
		if err := runDestroy(os.Args[2:]); err != nil {
			fail(err)
		}
	case "gen":
		if len(os.Args) < 3 || os.Args[2] != "graph" {
			fmt.Fprint(os.Stderr, usage)
			os.Exit(2)
		}
		mode := "summary"
		for _, arg := range os.Args[3:] {
			switch arg {
			case "--json", "--mermaid":
				mode = arg[2:]
			default:
				fail(fmt.Errorf("unknown flag %q (tako gen graph accepts --json or --mermaid)", arg))
			}
		}
		graph, err := analyzer.Analyze(analyzer.Config{Dir: "."})
		if err != nil {
			fail(err)
		}
		switch mode {
		case "json":
			out, err := json.MarshalIndent(graph, "", "  ")
			if err != nil {
				fail(err)
			}
			fmt.Printf("%s\n", out)
		case "mermaid":
			fmt.Print(analyzer.Mermaid(graph))
		default:
			fmt.Print(analyzer.Summary(graph))
		}
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
}

// runTest is `tako test`: analysis errors block (they are the app's
// build errors), generated code is refreshed so it can't be stale, then
// go test runs with output passed through.
func runTest(args []string) error {
	dir, err := filepath.Abs(".")
	if err != nil {
		return err
	}
	fmt.Printf("tako test · %s\n", filepath.Base(dir))

	graph, err := analyzer.Analyze(analyzer.Config{Dir: dir})
	if err != nil {
		return err
	}
	summary := devserver.CountLine(graph)
	fmt.Printf("  analyzing   %s\n", summary)

	prior := map[string][]byte{}
	for _, svc := range graph.Services {
		raw, err := os.ReadFile(filepath.Join(dir, ".tako", "gen", svc.Name, "main.go"))
		if err == nil {
			prior[svc.Name] = raw
		}
	}
	written, err := codegen.Generate(graph, dir)
	if err != nil {
		return err
	}
	// The freshness check: tests always run against regenerated code, and
	// a drifted checkout is called out rather than silently used.
	var refreshed []string
	for _, rel := range written {
		svc := filepath.Base(filepath.Dir(rel))
		now, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			return err
		}
		if !bytes.Equal(prior[svc], now) {
			refreshed = append(refreshed, svc)
		}
	}
	note := ""
	if len(refreshed) > 0 {
		note = fmt.Sprintf(" (regenerated: %s was stale)", strings.Join(refreshed, ", "))
	}
	fmt.Printf("  generating  %s%s\n", strings.Join(written, ", "), note)

	goArgs := append([]string{"test"}, args...)
	if len(args) == 0 {
		goArgs = append(goArgs, "./...")
	}
	fmt.Printf("  go %s\n", strings.Join(goArgs, " "))
	cmd := exec.Command("go", goArgs...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
