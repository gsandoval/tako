package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/gsandoval/tako/pkg/mcpserver"
)

// runMCP is `tako mcp`: runs Tako's built-in Model Context Protocol
// (MCP) JSON-RPC 2.0 server over stdin/stdout so coding agents and IDE
// hosts (Claude Code, Cursor, Antigravity, Gemini CLI) can inspect the
// AppGraph, read embedded docs, check types, invoke handlers, and query
// local .tako/data.
func runMCP(args []string) error {
	args = normalizeArgs(args)
	opts := mcpserver.ServerOptions{Dir: "."}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--dir":
			i++
			if i >= len(args) {
				return fmt.Errorf("--dir needs a path")
			}
			opts.Dir = args[i]
		default:
			return fmt.Errorf("unknown flag %q (tako mcp accepts [--dir <path>])", args[i])
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return mcpserver.Serve(ctx, opts, os.Stdin, os.Stdout)
}
