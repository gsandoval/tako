package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gsandoval/tako/pkg/analyzer"
	"github.com/gsandoval/tako/pkg/build"
	"github.com/gsandoval/tako/pkg/codegen"
	"github.com/gsandoval/tako/pkg/devserver"
)

// runBuild is `tako build [--push <repo>]`: analyze → regenerate →
// compile each generated service statically → layer on distroless →
// write an OCI layout (or push, digest-pinned). Services without a
// generated main (async-only — per-service mains arrive with cloud
// async) are skipped with a note, never silently.
func runBuild(args []string) error {
	repo := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--push":
			if i+1 >= len(args) {
				return fmt.Errorf("--push needs a repository, e.g. --push us-central1-docker.pkg.dev/my-proj/tako")
			}
			i++
			repo = args[i]
		default:
			return fmt.Errorf("unknown flag %q (tako build accepts --push <repo>)", args[i])
		}
	}

	dir, err := filepath.Abs(".")
	if err != nil {
		return err
	}
	app := filepath.Base(dir)
	fmt.Printf("tako build · %s\n", app)

	graph, err := analyzer.Analyze(analyzer.Config{Dir: dir})
	if err != nil {
		return err
	}
	fmt.Printf("  analyzing   %s\n", devserver.CountLine(graph))
	written, err := codegen.Generate(graph, dir)
	if err != nil {
		return err
	}
	fmt.Printf("  generating  %s\n", joinComma(written))

	var results []build.Result
	baseShown := false
	for _, svc := range graph.Services {
		if _, err := os.Stat(filepath.Join(dir, ".tako", "gen", svc.Name, "main.go")); err != nil {
			fmt.Printf("  skipping    %s (async-only — its per-service main arrives with cloud async)\n", svc.Name)
			continue
		}
		start := time.Now()
		res, err := build.Service(build.Options{Dir: dir, App: graph.Module, Repo: repo}, svc.Name)
		if err != nil {
			return err
		}
		fmt.Printf("  building    %s  linux/amd64 static (%.1f MB in %s)\n",
			svc.Name, float64(res.BinarySize)/(1<<20), time.Since(start).Round(100*time.Millisecond))
		if !baseShown {
			fmt.Printf("  base        %s\n", res.Base)
			baseShown = true
		}
		if res.Pushed {
			fmt.Printf("  pushing     %s/%s\n", repo, svc.Name)
		} else {
			fmt.Printf("  image       %s (OCI layout)\n", relify(dir, res.Ref))
		}
		results = append(results, res)
	}
	fmt.Println()
	for _, res := range results {
		if res.Pushed {
			fmt.Printf("✓ %s  %s\n", res.Service, res.Ref)
		} else {
			fmt.Printf("✓ %s  %s   (not pushed — use --push <repo>)\n", res.Service, res.Digest)
		}
	}
	return nil
}

func joinComma(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ", "
		}
		out += p
	}
	return out
}

func relify(dir, path string) string {
	if rel, err := filepath.Rel(dir, path); err == nil {
		return rel
	}
	return path
}
