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
	"github.com/gsandoval/tako/pkg/provision"
)

type checkReport struct {
	OK        bool                    `json:"ok"`
	Services  int                     `json:"services"`
	Handlers  int                     `json:"handlers"`
	Resources int                     `json:"resources"`
	Written   []string                `json:"written,omitempty"`
	Errors    []*analyzer.Error       `json:"errors,omitempty"`
	Warnings  []analyzer.Shadow       `json:"warnings,omitempty"`
	Skew      []provision.SkewFinding `json:"skew,omitempty"`
	LoadError string                  `json:"load_error,omitempty"`
}

// runCheck is `tako check [--json] [--skew <graph.json>]`: fast static
// analysis, site-shadow warnings, version-skew comparison, and code
// generation (including the test harness overlay) without starting the
// dev server or running tests.
func runCheck(args []string) error {
	args = normalizeArgs(args)
	jsonOut := false
	skewPath := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			jsonOut = true
		case "--skew":
			if i+1 >= len(args) {
				return fmt.Errorf("--skew needs a path to a baseline AppGraph JSON file")
			}
			i++
			skewPath = args[i]
		default:
			return fmt.Errorf("unknown flag %q (tako check accepts --json, --skew <graph.json>)", args[i])
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

	var skew provision.SkewReport
	if skewPath != "" {
		raw, err := os.ReadFile(skewPath)
		if err != nil {
			return fmt.Errorf("reading --skew baseline %s: %v", skewPath, err)
		}
		var prev analyzer.AppGraph
		if err := json.Unmarshal(raw, &prev); err != nil {
			return fmt.Errorf("decoding --skew baseline %s: %v", skewPath, err)
		}
		skew = provision.CheckSkew(&prev, graph, nil)
	}

	if jsonOut {
		rep := checkReport{
			OK:        !skew.HasDanger(),
			Services:  len(graph.Services),
			Handlers:  len(graph.Handlers),
			Resources: len(graph.Resources),
			Written:   codegen.Mains(written),
			Warnings:  warnings,
			Skew:      skew.Findings,
		}
		out, mErr := json.MarshalIndent(rep, "", "  ")
		if mErr != nil {
			return mErr
		}
		fmt.Printf("%s\n", out)
		if skew.HasDanger() {
			os.Exit(1)
		}
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
	if len(skew.Findings) > 0 {
		fmt.Print(skew.Render())
		if skew.HasDanger() {
			return fmt.Errorf("dangerous version skew detected against %s", skewPath)
		}
	}
	return nil
}
