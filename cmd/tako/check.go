package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gsandoval/tako/pkg/analyzer"
	"github.com/gsandoval/tako/pkg/codegen"
	"github.com/gsandoval/tako/pkg/devserver"
)

type checkReport struct {
	OK        bool              `json:"ok"`
	Services  int               `json:"services"`
	Handlers  int               `json:"handlers"`
	Resources int               `json:"resources"`
	Written   []string          `json:"written,omitempty"`
	Errors    []*analyzer.Error `json:"errors,omitempty"`
	Warnings  []analyzer.Shadow `json:"warnings,omitempty"`
	LoadError string            `json:"load_error,omitempty"`
}

// runCheck is `tako check [--json]`: fast static analysis, site-shadow
// warnings, and code generation (including the test harness overlay)
// without starting the dev server or running tests.
func runCheck(args []string) error {
	jsonOut := false
	for _, arg := range args {
		switch arg {
		case "--json":
			jsonOut = true
		default:
			return fmt.Errorf("unknown flag %q (tako check accepts --json)", arg)
		}
	}

	dir, err := filepath.Abs(".")
	if err != nil {
		return err
	}

	graph, err := analyzer.Analyze(analyzer.Config{Dir: dir})
	if err != nil {
		if jsonOut {
			rep := checkReport{OK: false}
			if list, ok := err.(analyzer.ErrorList); ok {
				rep.Errors = list
			} else {
				rep.LoadError = err.Error()
			}
			out, _ := json.MarshalIndent(rep, "", "  ")
			fmt.Printf("%s\n", out)
			os.Exit(1)
		}
		return err
	}

	warnings := analyzer.ShadowedSiteFiles(dir, graph)
	written, err := codegen.Generate(graph, dir)
	if err != nil {
		return err
	}
	if !analyzer.IsCfireProject(dir) {
		_, _ = writeOverlay(dir, graph)
	}

	if jsonOut {
		rep := checkReport{
			OK:        true,
			Services:  len(graph.Services),
			Handlers:  len(graph.Handlers),
			Resources: len(graph.Resources),
			Written:   codegen.Mains(written),
			Warnings:  warnings,
		}
		out, mErr := json.MarshalIndent(rep, "", "  ")
		if mErr != nil {
			return mErr
		}
		fmt.Printf("%s\n", out)
		return nil
	}

	fmt.Printf("tako check · %s\n", filepath.Base(dir))
	fmt.Printf("  ✓ analyzed   %s\n", devserver.CountLine(graph))
	if len(written) > 0 {
		fmt.Printf("  ✓ generated  %s\n", strings.Join(codegen.Mains(written), ", "))
	}
	for _, w := range warnings {
		fmt.Printf("  %s\n", w.Warning())
	}
	return nil
}
