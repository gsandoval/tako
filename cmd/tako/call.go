package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/gsandoval/tako/pkg/mcpserver"
	"github.com/gsandoval/tako/pkg/runtime"
)

const callUsage = `tako call — execute any declared handler in-process against local .tako/data

  tako call <HandlerVar|route> [json-input]
            [--as <user>] [--roles r1,r2] [--flag k=v]
            [--idempotency-key <key>] [--device-id <id>] [--json]

Examples:
  tako call Greet '{"name":"tako"}'
  tako call "POST /orders" '{"item":"sku-1","qty":2}' --as alice --roles admin
  tako call ChargeOrder '{"order_id":"ord-1"}' --flag new-checkout=true --json
`

// runCall is `tako call <HandlerVar|route> [json-input] [flags]`.
func runCall(args []string) error {
	return runCallWithWriter(os.Stdout, args, nil)
}

func runCallWithWriter(out io.Writer, args []string, extraEnv []string) error {
	args = normalizeArgs(args)
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, callUsage)
		os.Exit(2)
	}

	opts := mcpserver.CallOptions{
		Flags: map[string]string{},
		Env:   extraEnv,
	}
	var jsonOut bool
	var positional []string

	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--json":
			jsonOut = true
		case a == "--as":
			i++
			if i >= len(args) {
				return fmt.Errorf("--as needs a user name")
			}
			opts.AsUser = args[i]
		case a == "--roles":
			i++
			if i >= len(args) {
				return fmt.Errorf("--roles needs a comma-separated list of roles")
			}
			for _, r := range strings.Split(args[i], ",") {
				if r = strings.TrimSpace(r); r != "" {
					opts.Roles = append(opts.Roles, r)
				}
			}
		case a == "--flag":
			i++
			if i >= len(args) {
				return fmt.Errorf("--flag needs key=value")
			}
			k, v, ok := strings.Cut(args[i], "=")
			if !ok || strings.TrimSpace(k) == "" {
				return fmt.Errorf("--flag wants key=value, got %q", args[i])
			}
			opts.Flags[strings.TrimSpace(k)] = strings.TrimSpace(v)
		case a == "--idempotency-key":
			i++
			if i >= len(args) {
				return fmt.Errorf("--idempotency-key needs a value")
			}
			opts.IdempotencyKey = args[i]
		case a == "--device-id":
			i++
			if i >= len(args) {
				return fmt.Errorf("--device-id needs a value")
			}
			opts.DeviceID = args[i]
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("unknown flag %q (tako call accepts --as, --roles, --flag, --idempotency-key, --device-id, --json)", a)
		default:
			positional = append(positional, a)
		}
	}

	if len(positional) == 0 || len(positional) > 2 {
		return fmt.Errorf("usage: tako call <HandlerVar|route> [json-input] [--as <user>] [--roles r1,r2] [--flag k=v] [--json]")
	}
	opts.Target = positional[0]
	if len(positional) == 2 {
		raw := strings.TrimSpace(positional[1])
		if raw != "" {
			var obj map[string]json.RawMessage
			if err := json.Unmarshal([]byte(raw), &obj); err == nil && obj != nil {
				opts.Input = obj
			} else if json.Valid([]byte(raw)) {
				opts.RawPayload = json.RawMessage(raw)
			} else {
				return fmt.Errorf("invalid JSON input %q", raw)
			}
		}
	}

	res, err := mcpserver.CallHandler(context.Background(), ".", opts)
	if err != nil {
		return err
	}
	if jsonOut {
		b, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "%s\n", b)
		if !res.OK {
			return fmt.Errorf("handler %s returned status %d", res.Handler, res.Status)
		}
		return nil
	}
	printCallResult(out, res)
	if !res.OK {
		return fmt.Errorf("handler %s failed with status %d", res.Handler, res.Status)
	}
	return nil
}

func printCallResult(out io.Writer, res runtime.OneShotResult) {
	route := res.Route
	if route == "" {
		route = res.Kind
	}
	fmt.Fprintf(out, "tako call · %s (%s) → %d (%.1fms)\n", res.Handler, route, res.Status, res.DurationMs)
	if res.Response != nil {
		b, err := json.MarshalIndent(res.Response, "  ", "  ")
		if err == nil {
			fmt.Fprintf(out, "  %s\n", b)
		}
	} else if res.RawBody != "" {
		fmt.Fprintf(out, "  %s\n", res.RawBody)
	} else if res.Error != "" {
		fmt.Fprintf(out, "  error: %s\n", res.Error)
	}
	if len(res.Spans) > 0 {
		fmt.Fprintf(out, "  spans (%d):\n", len(res.Spans))
		for _, s := range res.Spans {
			fmt.Fprintf(out, "    [%s] %s (%.1fms)\n", s.Kind, s.Name, s.DurationMs)
		}
	}
}
