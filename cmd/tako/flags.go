package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gsandoval/tako/pkg/analyzer"
	"github.com/gsandoval/tako/pkg/backends"
	gcpb "github.com/gsandoval/tako/pkg/backends/gcp"
	"github.com/gsandoval/tako/pkg/backends/local"
	"github.com/gsandoval/tako/pkg/provision"
)

const flagsUsage = `tako flags — the dynamic flags and experiments your app declares with tako.Flag

  tako flags [list]                              declared flags and active rules
  tako flags set     <name> <json-or-scalar>     override a flag to a fixed value
  tako flags rollout <name> <value> --pct <1..100>
                                                 sticky percentage rollout by user/device
  tako flags split   <name> <variant=weight>...  weighted multi-variant experiment
  tako flags reset   <name>                      remove override, revert to default

Local by default: no --project means .tako/data/flags.json, which
` + "`tako dev`" + ` reads live. Add --project <id> [--env <env>] to update a
deployed app's flag manifest on Cloud Storage.
`

type declaredFlag struct {
	Name string
	Type string
	Pos  string
}

func declaredFlags(graph *analyzer.AppGraph) []declaredFlag {
	var out []declaredFlag
	for _, r := range graph.Resources {
		if r.Kind == "flag" {
			out = append(out, declaredFlag{Name: r.Name, Type: r.Type, Pos: r.Pos})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func parseFlagsTarget(raw []string) (accountsTarget, []string, error) {
	return parseDataTarget("tako flags", "flags", ".tako/data", raw)
}

func runFlags(args []string) error {
	var jsonOut bool
	var filtered []string
	for _, a := range normalizeArgs(args) {
		if a == "--json" {
			jsonOut = true
			continue
		}
		filtered = append(filtered, a)
	}
	t, rest, err := parseFlagsTarget(filtered)
	if err != nil {
		return err
	}
	sub := "list"
	if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		sub = rest[0]
		rest = rest[1:]
	}
	switch sub {
	case "list", "set", "rollout", "split", "reset":
	default:
		fmt.Fprint(os.Stderr, flagsUsage)
		os.Exit(2)
	}

	graph, err := analyzer.Analyze(analyzer.Config{Dir: "."})
	if err != nil {
		return err
	}
	store, err := openFlagStore(t, graph)
	if err != nil {
		return err
	}
	cmd := &flagsCmd{
		out:      os.Stdout,
		target:   t,
		declared: declaredFlags(graph),
		store:    store,
		jsonOut:  jsonOut,
	}
	switch sub {
	case "list":
		if len(rest) > 0 {
			return fmt.Errorf("unknown argument %q", rest[0])
		}
		return cmd.list()
	case "set":
		if len(rest) != 2 {
			return fmt.Errorf("usage: tako flags set <name> <value>")
		}
		return cmd.set(rest[0], rest[1])
	case "rollout":
		return cmd.rollout(rest)
	case "split":
		if len(rest) < 2 {
			return fmt.Errorf("usage: tako flags split <name> <variant=weight>...")
		}
		return cmd.split(rest[0], rest[1:])
	case "reset":
		if len(rest) != 1 {
			return fmt.Errorf("usage: tako flags reset <name>")
		}
		return cmd.reset(rest[0])
	}
	return nil
}

func openFlagStore(t accountsTarget, graph *analyzer.AppGraph) (backends.Flags, error) {
	if !t.isCloud() {
		return local.NewFlagStore(filepath.Join(".tako", "data")), nil
	}
	cfg := provision.Config{Project: t.project, App: graph.Module, Env: t.env}
	c, err := gcpb.New(context.Background(), gcpb.Options{
		Project: t.project,
		App:     cfg.AppID(),
		Env:     t.env,
	})
	if err != nil {
		return nil, fmt.Errorf("not authenticated to Google Cloud — run:\n"+
			"        gcloud auth application-default login\n\n      (%v)", err)
	}
	return gcpb.NewFlagStore(c, 0), nil
}

type flagsCmd struct {
	out      io.Writer
	target   accountsTarget
	declared []declaredFlag
	store    backends.Flags
	jsonOut  bool
}

func (c *flagsCmd) header() {
	if c.jsonOut {
		return
	}
	fmt.Fprintf(c.out, "tako flags · %s\n", c.target.describe())
}

func (c *flagsCmd) list() error {
	c.header()
	rules, err := c.store.List(context.Background())
	if err != nil {
		return err
	}
	byName := make(map[string]backends.FlagRule, len(rules))
	for _, r := range rules {
		byName[r.Name] = r
	}
	if c.jsonOut {
		type jsonFlag struct {
			Name       string             `json:"name"`
			Type       string             `json:"type"`
			Pos        string             `json:"pos,omitempty"`
			ActiveRule string             `json:"active_rule"`
			Rule       *backends.FlagRule `json:"rule,omitempty"`
		}
		var list []jsonFlag
		seen := map[string]bool{}
		for _, d := range c.declared {
			seen[d.Name] = true
			jf := jsonFlag{
				Name:       d.Name,
				Type:       d.Type,
				Pos:        d.Pos,
				ActiveRule: "(default)",
			}
			if r, ok := byName[d.Name]; ok {
				rc := r
				jf.ActiveRule = formatFlagRule(r)
				jf.Rule = &rc
			}
			list = append(list, jf)
		}
		for _, r := range rules {
			if !seen[r.Name] {
				rc := r
				list = append(list, jsonFlag{
					Name:       r.Name,
					Type:       "(undeclared)",
					ActiveRule: formatFlagRule(r),
					Rule:       &rc,
				})
			}
		}
		if list == nil {
			list = []jsonFlag{}
		}
		raw, err := json.MarshalIndent(map[string]any{
			"target": c.target.describe(),
			"flags":  list,
		}, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintf(c.out, "%s\n", raw)
		return nil
	}
	if len(c.declared) == 0 && len(rules) == 0 {
		fmt.Fprintln(c.out, "  this app declares no flags — tako.Flag(\"name\", default) is what puts one here")
		return nil
	}
	rows := [][]string{{"NAME", "TYPE", "ACTIVE RULE", "DECLARED AT"}}
	seen := map[string]bool{}
	for _, d := range c.declared {
		seen[d.Name] = true
		ruleDesc := "(default)"
		if r, ok := byName[d.Name]; ok {
			ruleDesc = formatFlagRule(r)
		}
		rows = append(rows, []string{d.Name, d.Type, ruleDesc, d.Pos})
	}
	for _, r := range rules {
		if !seen[r.Name] {
			rows = append(rows, []string{r.Name, "(undeclared)", formatFlagRule(r), "-"})
		}
	}
	writeTable(c.out, rows)
	return nil
}

func formatFlagRule(r backends.FlagRule) string {
	if len(r.Variants) > 0 {
		parts := make([]string, 0, len(r.Variants))
		for _, v := range r.Variants {
			parts = append(parts, fmt.Sprintf("%s=%d%%", string(v.Value), v.Weight))
		}
		return "split: " + strings.Join(parts, ", ")
	}
	if r.RolloutPct > 0 {
		return fmt.Sprintf("rollout %d%% -> %s", r.RolloutPct, string(r.Value))
	}
	if len(r.Value) > 0 {
		return string(r.Value)
	}
	return "(default)"
}

func (c *flagsCmd) mustBeDeclared(name string) error {
	var names []string
	for _, d := range c.declared {
		if d.Name == name {
			return nil
		}
		names = append(names, d.Name)
	}
	return fmt.Errorf("%q is not a flag this app declares\n      declared: %s (tako.Flag)",
		name, strings.Join(names, ", "))
}

func parseFlagJSONValue(raw string) json.RawMessage {
	trimmed := strings.TrimSpace(raw)
	if json.Valid([]byte(trimmed)) {
		return json.RawMessage(trimmed)
	}
	b, _ := json.Marshal(trimmed)
	return json.RawMessage(b)
}

func (c *flagsCmd) set(name, rawVal string) error {
	if err := c.mustBeDeclared(name); err != nil {
		return err
	}
	c.header()
	val := parseFlagJSONValue(rawVal)
	rule := backends.FlagRule{
		Name:      name,
		Value:     val,
		UpdatedAt: time.Now().UTC(),
	}
	if err := c.store.Put(context.Background(), rule); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "  ✓ set %s = %s\n", name, string(val))
	return nil
}

func (c *flagsCmd) rollout(args []string) error {
	var pos []string
	var pct int
	var pctGiven bool
	for i := 0; i < len(args); i++ {
		if args[i] == "--pct" {
			if i+1 >= len(args) {
				return fmt.Errorf("--pct needs a percentage (1..100)")
			}
			i++
			n, err := strconv.Atoi(strings.TrimSuffix(args[i], "%"))
			if err != nil || n < 1 || n > 100 {
				return fmt.Errorf("--pct must be an integer between 1 and 100")
			}
			pct = n
			pctGiven = true
		} else if strings.HasPrefix(args[i], "-") {
			return fmt.Errorf("unknown flag %q (tako flags rollout accepts --pct <1..100>)", args[i])
		} else {
			pos = append(pos, args[i])
		}
	}
	if len(pos) != 2 || !pctGiven {
		return fmt.Errorf("usage: tako flags rollout <name> <value> --pct <1..100>")
	}
	name, rawVal := pos[0], pos[1]
	if err := c.mustBeDeclared(name); err != nil {
		return err
	}
	c.header()
	val := parseFlagJSONValue(rawVal)
	rule := backends.FlagRule{
		Name:       name,
		Value:      val,
		RolloutPct: pct,
		UpdatedAt:  time.Now().UTC(),
	}
	if err := c.store.Put(context.Background(), rule); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "  ✓ rollout %s = %s to %d%% of subjects\n", name, string(val), pct)
	return nil
}

func (c *flagsCmd) split(name string, pairs []string) error {
	if err := c.mustBeDeclared(name); err != nil {
		return err
	}
	var variants []backends.FlagVariant
	total := 0
	for _, p := range pairs {
		idx := strings.LastIndex(p, "=")
		if idx <= 0 || idx == len(p)-1 {
			return fmt.Errorf("invalid variant %q: expected <value>=<weight>", p)
		}
		vStr := p[:idx]
		wStr := strings.TrimSuffix(p[idx+1:], "%")
		w, err := strconv.Atoi(wStr)
		if err != nil || w < 0 || w > 100 {
			return fmt.Errorf("invalid weight in %q: must be 0..100", p)
		}
		total += w
		variants = append(variants, backends.FlagVariant{
			Value:  parseFlagJSONValue(vStr),
			Weight: w,
		})
	}
	if total > 100 {
		return fmt.Errorf("variant weights sum to %d%% (must be <= 100%%)", total)
	}
	c.header()
	rule := backends.FlagRule{
		Name:      name,
		Variants:  variants,
		UpdatedAt: time.Now().UTC(),
	}
	if err := c.store.Put(context.Background(), rule); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "  ✓ split %s across %d variants\n", name, len(variants))
	return nil
}

func (c *flagsCmd) reset(name string) error {
	c.header()
	if err := c.store.Delete(context.Background(), name); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "  ✓ reset %s to compiled default\n", name)
	return nil
}
