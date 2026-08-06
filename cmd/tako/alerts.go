package main

// `tako alerts`: what every declared alert actually compiles to.
//
// Alerting had no way to show its own work. `tako deploy --plan` prints
// "+ create alert policy Slowing" and never the query behind it, and the
// rendered PromQL existed only as a golden file inside tako's own
// repository — so a user could not see what their alert would evaluate
// without deploying it and opening the console.
//
// That gap is not just an inspection problem. A preset is meant to be a
// starting point: read what it compiles to, then write your own version
// with the threshold, window or label you actually wanted. You cannot
// fork what you cannot read, which is why every entry names the
// constructor that produced it.
//
// Nothing here touches the cloud. An alert is a property of the code;
// only the env-prefixed names inside it depend on where it would deploy,
// which is what --env selects.

import (
	"fmt"
	"os"
	"strings"

	"github.com/gsandoval/tako/pkg/analyzer"
	"github.com/gsandoval/tako/pkg/provision"
)

// runAlerts is `tako alerts [name] [--env <env>]`.
func runAlerts(args []string) error {
	env := provision.DefaultEnv
	var only string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--env" && i+1 < len(args):
			i++
			env = args[i]
		case strings.HasPrefix(a, "--env="):
			env = strings.TrimPrefix(a, "--env=")
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("unknown flag %q (tako alerts accepts [name] and --env)", a)
		default:
			only = a
		}
	}
	// The env reaches the rendered PromQL through the namespace, so it
	// is checked here too — this command's whole promise is that what
	// it prints is what deploys.
	if err := checkEnv(env); err != nil {
		return err
	}

	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	graph, err := analyzer.Analyze(analyzer.Config{Dir: dir})
	if err != nil {
		return err
	}
	// Project and region are deliberately not resolved: no name in a
	// query carries them, so demanding a gcloud config for a local read
	// would be friction with nothing behind it.
	cfg := provision.Config{App: graph.Module, Env: env}
	described, err := provision.DescribeAlerts(cfg, graph)
	if err != nil {
		return err
	}
	if len(described) == 0 {
		fmt.Println("no alerts declared — a condition becomes one with tako.Alert(cond, channel)")
		fmt.Println("  see docs/reference/alerts.md for the presets and the raw vocabulary")
		return nil
	}

	shown := 0
	for _, d := range described {
		if only != "" && !strings.EqualFold(d.Name, only) {
			continue
		}
		fmt.Println(d.Render())
		shown++
	}
	if only != "" && shown == 0 {
		var names []string
		for _, d := range described {
			names = append(names, d.Name)
		}
		return fmt.Errorf("no alert named %q — this app declares %s", only, strings.Join(names, ", "))
	}
	// The env matters to every physical name printed above, and a reader
	// who forgot which one they asked for would misread every matcher.
	fmt.Printf("%d alert(s), as they would deploy to env %q\n", shown, env)
	return nil
}
