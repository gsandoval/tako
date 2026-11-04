package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/gsandoval/tako/pkg/analyzer"
	"github.com/gsandoval/tako/pkg/clientgen"
	"github.com/gsandoval/tako/pkg/schemagen"
)

// runGenClient is `tako gen client ts`: the typed TypeScript client for
// the app's api endpoints, written under .tako/gen (or --out).
func runGenClient(args []string) error {
	args = normalizeArgs(args)
	if len(args) < 1 || args[0] != "ts" {
		return fmt.Errorf("tako gen client generates TypeScript today — run: tako gen client ts")
	}
	out := filepath.Join(".tako", "gen", "client", "ts", "api.ts")
	for i := 1; i < len(args); i++ {
		switch {
		case args[i] == "--out":
			i++
			if i >= len(args) {
				return fmt.Errorf("--out needs a path")
			}
			out = args[i]
		default:
			return fmt.Errorf("unknown flag %q (tako gen client ts accepts --out <path>)", args[i])
		}
	}
	tg, err := analyzer.AnalyzeTyped(analyzer.Config{Dir: "."})
	if err != nil {
		return err
	}
	content, sum, err := clientgen.Generate(tg)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(out, content, 0o644); err != nil {
		return err
	}
	fmt.Printf("  client   %s — %d endpoint%s, %d type%s\n",
		out, sum.Endpoints, plural(sum.Endpoints), sum.Types, plural(sum.Types))
	for _, e := range sum.Excluded {
		fmt.Printf("           excluded: %s %s — %s\n", e.Var, e.Route, e.Reason)
	}
	if sum.Endpoints == 0 {
		fmt.Println("           note: the app declares no browser-callable api endpoints")
	}
	return nil
}

// runGenOpenAPI is `tako gen openapi [--out <path>] [--stdout]`.
func runGenOpenAPI(args []string) error {
	args = normalizeArgs(args)
	out := filepath.Join(".tako", "gen", "openapi.json")
	stdout := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--out":
			i++
			if i >= len(args) {
				return fmt.Errorf("--out needs a path")
			}
			out = args[i]
		case "--stdout":
			stdout = true
		default:
			return fmt.Errorf("unknown flag %q (tako gen openapi accepts --out <path> and --stdout)", args[i])
		}
	}
	tg, err := analyzer.AnalyzeTyped(analyzer.Config{Dir: "."})
	if err != nil {
		return err
	}
	spec, sum, err := schemagen.OpenAPI(tg)
	if err != nil {
		return err
	}
	if stdout {
		fmt.Printf("%s\n", spec)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(out, append(spec, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Printf("  openapi  %s — %d endpoint%s, %d schema%s, %d mcp tool%s\n",
		out,
		sum.Operations, plural(sum.Operations),
		sum.Schemas, plural(sum.Schemas),
		sum.Tools, plural(sum.Tools))
	return nil
}

// runGenAgents is `tako gen agents [--out <path>] [--stdout]`.
func runGenAgents(args []string) error {
	args = normalizeArgs(args)
	out := "AGENTS.md"
	stdout := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--out":
			i++
			if i >= len(args) {
				return fmt.Errorf("--out needs a path")
			}
			out = args[i]
		case "--stdout":
			stdout = true
		default:
			return fmt.Errorf("unknown flag %q (tako gen agents accepts --out <path> and --stdout)", args[i])
		}
	}
	tg, err := analyzer.AnalyzeTyped(analyzer.Config{Dir: "."})
	if err != nil {
		return err
	}
	existing, _ := os.ReadFile(out)
	md := schemagen.GenerateAgentsMD(tg, string(existing))
	if stdout {
		fmt.Print(md)
		return nil
	}
	if dir := filepath.Dir(out); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if err := os.WriteFile(out, []byte(md), 0o644); err != nil {
		return err
	}
	fmt.Printf("  agents   %s — %d service%s, %d handler%s, %d resource%s\n",
		out,
		len(tg.Graph.Services), plural(len(tg.Graph.Services)),
		len(tg.Graph.Handlers), plural(len(tg.Graph.Handlers)),
		len(tg.Graph.Resources), plural(len(tg.Graph.Resources)))
	return nil
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
