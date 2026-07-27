package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gsandoval/tako/pkg/analyzer"
	"github.com/gsandoval/tako/pkg/clientgen"
)

// runGenClient is `tako gen client ts`: the typed TypeScript client for
// the app's api endpoints, written under .tako/gen (or --out).
func runGenClient(args []string) error {
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
		case strings.HasPrefix(args[i], "--out="):
			out = strings.TrimPrefix(args[i], "--out=")
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

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
