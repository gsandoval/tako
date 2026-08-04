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

Local — nothing outside this directory is touched:
  tako dev [--chaos]   run the app locally with reload (--chaos: seeded
                       duplicate/reordered deliveries)
  tako test [args]     analyze, refresh generated code, then go test ./...
  tako gen graph [--json | --mermaid]
                       print the app's AppGraph: human summary (default),
                       pipeable JSON, or a mermaid diagram
  tako gen client ts [--out <path>]
                       generate a typed TypeScript client for the app's
                       api endpoints (default .tako/gen/client/ts/api.ts)
  tako alerts [name] [--env prod]
                       what every declared alert compiles to: the PromQL,
                       the notification sentence, and the constructor it
                       came from — so a preset can be read and forked
  tako docs [--port 8090] [--open]
                       serve the guide, the reference and the SDK's
                       exported signatures from this binary — no
                       network, no checkout needed
  tako build [--push <repo>]
                       build digest-pinned distroless images (no Docker
                       daemon); --push CROSSES THE LINE below: uploads
                       to <repo>/<service> on Artifact Registry

Cloud, read-only:
  tako logs [service] [--since 15m]
  tako metrics <search> [--limit 20]
                       recent logs for the deployed services
  tako dlq [service]   peek dead-lettered messages per async service

Cloud, mutating (see the confirmation ladder below):
  tako deploy [--plan] [--canary <pct>] [--yes]
                       reconcile the app onto GCP: preflight, classified
                       plan (--plan stops there), lease, apply, live URL;
                       --canary holds new revisions at <pct>% traffic
  tako promote         send 100% of every service's traffic to latest
  tako rollback        return traffic to the previous revision set
                       (instant; infra shape rolls back via git revert +
                       tako deploy)
  tako prune [--data]  delete orphans tako once managed: stateless
                       (compute, messaging, indexes) immediately; data
                       (buckets, entity kinds) only behind --data's
                       counts + typed confirmation
  tako dlq [service] --ack
                       drain the printed dead-letter messages for good
  tako dlq <service> --replay <id>|--all
                       re-deliver a dead letter, then take it off the DLQ
  tako dlq --local [carrier]
                       the same, against .tako/data
  tako destroy         delete everything labeled for this app (typed app
                       name, always; never touches unlabeled resources)
  tako destroy --plan  show exactly what destroy would delete, and stop

The confirmation ladder — friction scales with irreversibility:
  reversible/derivable  no ceremony (promote/rollback undo each other;
                        a deploy re-derives what prune removed)
  risky but recoverable deploy prompts y/N on replaces and rename
                        suspects; --yes pre-answers THAT PROMPT ONLY (CI)
  data, gone forever    typed confirmation (destroy, prune --data) or an
                        explicit flag (dlq --ack) — --yes never applies

Cloud commands resolve --project (else GOOGLE_CLOUD_PROJECT, else gcloud
config), --region (else TAKO_REGION, else us-central1), and --env
(default prod) identically; --flag value and --flag=value both work.
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
			fail(err)
		}
	case "promote":
		if err := runPromote(os.Args[2:]); err != nil {
			fail(err)
		}
	case "rollback":
		if err := runRollback(os.Args[2:]); err != nil {
			fail(err)
		}
	case "prune":
		if err := runPrune(os.Args[2:]); err != nil {
			fail(err)
		}
	case "logs":
		if err := runLogs(os.Args[2:]); err != nil {
			fail(err)
		}
	case "alerts":
		if err := runAlerts(os.Args[2:]); err != nil {
			fail(err)
		}
	case "docs":
		if err := runDocs(os.Args[2:]); err != nil {
			fail(err)
		}
	case "metrics":
		if err := runMetrics(os.Args[2:]); err != nil {
			fail(err)
		}
	case "destroy":
		if err := runDestroy(os.Args[2:]); err != nil {
			fail(err)
		}
	case "gen":
		if len(os.Args) >= 3 && os.Args[2] == "client" {
			if err := runGenClient(os.Args[3:]); err != nil {
				fail(err)
			}
			return
		}
		if len(os.Args) < 3 || os.Args[2] != "graph" {
			// The gen NAMESPACE gets its own help — a bare or unknown
			// gen should list the generators, not dump everything.
			fmt.Fprint(os.Stderr, `tako gen — code and artifact generators (local, no cloud touched)

  tako gen graph [--json | --mermaid]   the app's AppGraph
  tako gen client ts [--out <path>]     typed TypeScript client
`)
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

// fail is how every command exits on error — one prefix, so a message
// from tako is distinguishable from one produced by something tako
// shelled out to (tako test runs go test; its failures are go's).
func fail(err error) {
	fmt.Fprintf(os.Stderr, "tako: %v\n", err)
	os.Exit(1)
}
