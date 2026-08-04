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

	"github.com/gsandoval/tako/pkg/docserver"
)

// runDocs is `tako docs [--port <n>] [--open]`.
func runDocs(args []string) error {
	opts := docserver.Options{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		var raw string
		switch {
		case a == "--open":
			opts.Open = true
			continue
		case a == "--port" && i+1 < len(args):
			i++
			raw = args[i]
		case strings.HasPrefix(a, "--port="):
			raw = strings.TrimPrefix(a, "--port=")
		default:
			return fmt.Errorf("unknown flag %q (tako docs accepts --port <n> and --open)", a)
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
