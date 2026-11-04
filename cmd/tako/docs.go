package main

// `tako docs`: the guide, the reference and the SDK surface, served
// from the copy inside this binary.
//
// The documentation existed only in the repository, which meant reading
// it required either a checkout or a browser tab on GitHub — and a
// GitHub tab shows whatever is on main, not the version the reader is
// compiling against. Embedding fixes both: the docs a binary serves are
// that binary's docs, and they work with no network at all.
//
// Serving IS what the command does, so there is no subcommand and no
// --serve flag. A flag that must always be passed is a flag that should
// not exist.

import (
	"context"
	"fmt"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/gsandoval/tako/docs"
	"github.com/gsandoval/tako/pkg/analyzer"
	"github.com/gsandoval/tako/pkg/docserver"
)

// runDocs is `tako docs [--port <n>] [--open] [--export <dir>]` or
// `tako docs <list | topic | search <query> | explain <rule>>`.
func runDocs(args []string) error {
	args = normalizeArgs(args)
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		site, err := docserver.Build(docs.FS)
		if err != nil {
			return err
		}
		switch args[0] {
		case "list", "ls":
			fmt.Print(site.List())
			return nil
		case "search":
			fmt.Print(site.Search(strings.Join(args[1:], " ")))
			return nil
		case "explain":
			if len(args) < 2 {
				return fmt.Errorf("usage: tako docs explain <rule> (e.g. tako docs explain entity-id)")
			}
			if exp, ok := analyzer.ExplainRule(args[1]); ok {
				fmt.Print(exp)
				return nil
			}
			return fmt.Errorf("unknown analysis rule %q — run `tako docs errors` to see all rules", args[1])
		default:
			if exp, ok := analyzer.ExplainRule(args[0]); ok && strings.HasPrefix(strings.ToLower(args[0]), "tako/") {
				fmt.Print(exp)
				return nil
			}
			if md, ok := site.Topic(args[0]); ok {
				fmt.Print(md)
				if !strings.HasSuffix(md, "\n") {
					fmt.Println()
				}
				return nil
			}
			if exp, ok := analyzer.ExplainRule(args[0]); ok {
				fmt.Print(exp)
				return nil
			}
			return fmt.Errorf("unknown docs topic %q — run `tako docs list` to see available topics or `tako docs search %s`", args[0], args[0])
		}
	}

	opts := docserver.Options{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		var raw string
		switch {
		case a == "--open":
			opts.Open = true
			continue
		case a == "--export" && i+1 < len(args):
			i++
			opts.ExportDir = args[i]
			continue
		case a == "--port" && i+1 < len(args):
			i++
			raw = args[i]
		default:
			return fmt.Errorf("unknown flag %q (tako docs accepts --port <n>, --open, and --export <dir>)", a)
		}
		port, err := strconv.Atoi(raw)
		if err != nil || port < 1 || port > 65535 {
			return fmt.Errorf("--port wants a number between 1 and 65535, got %q", raw)
		}
		opts.Port = port
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return docserver.Run(ctx, opts)
}
