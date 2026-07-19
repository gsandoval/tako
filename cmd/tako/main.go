// Command tako is the Tako CLI: dev, deploy, gen, test, logs, destroy,
// prune.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"

	"github.com/gsandoval/tako/pkg/analyzer"
	"github.com/gsandoval/tako/pkg/devserver"
)

const usage = `tako — a Go framework over Google Cloud Run

Usage:
  tako dev          run the app in this directory locally, with reload
  tako gen graph    print the app's AppGraph (JSON + summary)

Commands arriving in later slices: deploy, test, logs, destroy, prune.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "dev":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		if err := devserver.Run(ctx, devserver.Options{Dir: "."}); err != nil {
			fail(err)
		}
	case "gen":
		if len(os.Args) < 3 || os.Args[2] != "graph" {
			fmt.Fprint(os.Stderr, usage)
			os.Exit(2)
		}
		graph, err := analyzer.Analyze(analyzer.Config{Dir: "."})
		if err != nil {
			fail(err)
		}
		out, err := json.MarshalIndent(graph, "", "  ")
		if err != nil {
			fail(err)
		}
		fmt.Printf("%s\n\n%s", out, analyzer.Summary(graph))
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
