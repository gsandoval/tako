package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gsandoval/tako/pkg/analyzer"
	"github.com/gsandoval/tako/pkg/clientgen"
	"github.com/gsandoval/tako/pkg/projdocs"
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

// runGenDocs is `tako gen docs [<name>] [--level public|internal|full] [--include s1,s2] [--out <dir>]`.
func runGenDocs(args []string) error {
	args = normalizeArgs(args)
	var nameArg, levelFlag, includeFlag, outFlag string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--level":
			i++
			if i >= len(args) {
				return fmt.Errorf("--level needs a value (public, internal, or full)")
			}
			levelFlag = strings.ToLower(strings.TrimSpace(args[i]))
			if levelFlag != "public" && levelFlag != "internal" && levelFlag != "full" {
				return fmt.Errorf("unknown --level %q (expected public, internal, or full)", args[i])
			}
		case "--include":
			i++
			if i >= len(args) {
				return fmt.Errorf("--include needs a comma-separated list of slices (%s)", strings.Join(analyzer.AllDocsSlices, ", "))
			}
			includeFlag = args[i]
		case "--out":
			i++
			if i >= len(args) {
				return fmt.Errorf("--out needs a directory path")
			}
			outFlag = args[i]
		default:
			if strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("unknown flag %q (tako gen docs accepts [<name>] [--level public|internal|full] [--include s1,s2] [--out <dir>])", args[i])
			}
			if nameArg != "" {
				return fmt.Errorf("unexpected extra argument %q", args[i])
			}
			nameArg = args[i]
		}
	}

	cherry := map[string]bool{}
	if includeFlag != "" {
		validSlice := map[string]bool{}
		for _, s := range analyzer.AllDocsSlices {
			validSlice[s] = true
		}
		for _, part := range strings.Split(includeFlag, ",") {
			s := strings.ToLower(strings.TrimSpace(part))
			if s == "" {
				continue
			}
			if !validSlice[s] {
				return fmt.Errorf("unknown documentation slice %q (valid slices: %s)", part, strings.Join(analyzer.AllDocsSlices, ", "))
			}
			cherry[s] = true
		}
	}

	tg, err := analyzer.AnalyzeTyped(analyzer.Config{Dir: "."})
	if err != nil {
		return err
	}
	g := tg.Graph

	var declared []analyzer.Resource
	for _, r := range g.Resources {
		if r.Kind == "docs" {
			declared = append(declared, r)
		}
	}

	// Case 1: No flags or name, and project declares one or more tako.Docs instances.
	if nameArg == "" && levelFlag == "" && includeFlag == "" && outFlag == "" && len(declared) > 0 {
		for _, r := range declared {
			outDir := filepath.Join(".tako", "gen", "docs", r.Name)
			opts := projdocs.OptionsFromResource(r, outDir)
			files, err := projdocs.GenerateInstance(g, tg, opts)
			if err != nil {
				return err
			}
			fmt.Printf("  docs     %s — %d file%s (level: %s; slices: %s)\n",
				outDir, len(files), plural(len(files)), opts.Level, strings.Join(opts.Slices, ", "))
		}
		return nil
	}

	// Case 2: Named declared instance (with optional flag overrides) or ad-hoc generation.
	instName := nameArg
	if instName == "" {
		if len(declared) == 1 {
			instName = declared[0].Name
		} else {
			instName = "docs"
		}
	}
	outDir := outFlag
	if outDir == "" {
		outDir = filepath.Join(".tako", "gen", "docs", instName)
	}

	var opts projdocs.Options
	matched := false
	for _, r := range declared {
		if r.Name == instName {
			opts = projdocs.OptionsFromResource(r, outDir)
			matched = true
			break
		}
	}
	if !matched && nameArg != "" && levelFlag == "" && includeFlag == "" && len(declared) > 0 {
		var names []string
		for _, r := range declared {
			names = append(names, r.Name)
		}
		return fmt.Errorf("no declared tako.Docs(%q) found (declared: %s)", nameArg, strings.Join(names, ", "))
	}

	if !matched || levelFlag != "" || includeFlag != "" {
		slices, effLevel := analyzer.ResolveDocsSlices(levelFlag, levelFlag != "", cherry)
		opts = projdocs.Options{
			Name:   instName,
			Public: opts.Public,
			Level:  effLevel,
			Slices: slices,
			OutDir: outDir,
		}
	}

	files, err := projdocs.GenerateInstance(g, tg, opts)
	if err != nil {
		return err
	}
	fmt.Printf("  docs     %s — %d file%s (level: %s; slices: %s)\n",
		outDir, len(files), plural(len(files)), opts.Level, strings.Join(opts.Slices, ", "))
	return nil
}
