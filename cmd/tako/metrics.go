package main

// `tako metrics <search>`: cloud read-only, and the answer to a question
// the reference documentation deliberately refuses to answer.
//
// A project publishes upwards of eight thousand metric descriptors, so
// any list in the docs would be both incomplete and stale. Without this
// command, ServiceMetric and PlatformMetric are usable only by someone
// who already knows the name they want.

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/gsandoval/tako/pkg/provision/gcp"
)

// runMetrics is `tako metrics <search> [--limit N] [--project …]`.
func runMetrics(args []string) error {
	t, rest, err := parseTarget(args)
	if err != nil {
		return err
	}
	limit := 20
	var term string
	for i := 0; i < len(rest); i++ {
		if rest[i] == "--limit" && i+1 < len(rest) {
			i++
			n, err := strconv.Atoi(rest[i])
			if err != nil || n < 1 {
				return fmt.Errorf("--limit wants a positive number, got %q", rest[i])
			}
			limit = n
			continue
		}
		term = rest[i]
	}
	if term == "" {
		return fmt.Errorf("tako metrics needs something to search for — try `tako metrics cpu` or `tako metrics dead_letter`")
	}
	client, err := gcp.NewClient(context.Background(), t.project, t.region)
	if err != nil {
		return err
	}
	found, err := client.SearchDescriptors(context.Background(), term, limit)
	if err != nil {
		return err
	}
	if len(found) == 0 {
		fmt.Printf("no metric type contains %q in %s\n", term, t.project)
		fmt.Printf("  the search is a substring of the TYPE, so try a fragment: `cpu`, `dead_letter`, `oldest`\n")
		return nil
	}
	var unresolvable int
	for _, d := range found {
		mark := "✓"
		if !d.Resolvable {
			mark = "·"
			unresolvable++
		}
		fmt.Printf("%s %s\n", mark, d.Type)
		fmt.Printf("    %s %s", d.Kind, d.ValueType)
		if len(d.Resources) > 0 {
			fmt.Printf(" · on %s", strings.Join(d.Resources, ", "))
		}
		if len(d.Labels) > 0 {
			fmt.Printf(" · labels: %s", strings.Join(d.Labels, ", "))
		}
		fmt.Println()
		if s := gcp.FirstSentence(d.Description); s != "" {
			fmt.Printf("    %s\n", s)
		}
		fmt.Printf("    %s\n", d.Constructor())
		fmt.Println()
	}
	// The legend earns its place: the mark stands for a failure that
	// otherwise costs a deploy to discover.
	fmt.Printf("✓ nameable — a policy can be created against it today\n")
	if unresolvable > 0 {
		fmt.Printf("· not nameable here yet — Cloud Monitoring cannot resolve it in this project,\n")
		fmt.Printf("  so an alert naming it is refused. A DISTRIBUTION never resolves under its\n")
		fmt.Printf("  bare name; anything else usually resolves once you have used that service.\n")
	}
	return nil
}
