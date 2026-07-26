package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/gsandoval/tako/pkg/analyzer"
	"github.com/gsandoval/tako/pkg/provision"
	"github.com/gsandoval/tako/pkg/provision/gcp"
)

// lifecycleSetup is the shared preamble of promote/rollback/prune:
// analyze the app, authenticate, derive config.
func lifecycleSetup(args []string) (target, *analyzer.AppGraph, *gcp.Client, provision.Config, error) {
	t, rest, err := parseTarget(args)
	if err != nil {
		return t, nil, nil, provision.Config{}, err
	}
	if len(rest) > 0 && rest[0] != "--data" {
		return t, nil, nil, provision.Config{}, fmt.Errorf("unknown argument %q", rest[0])
	}
	graph, err := analyzer.Analyze(analyzer.Config{Dir: "."})
	if err != nil {
		return t, nil, nil, provision.Config{}, err
	}
	client, err := gcp.NewClient(context.Background(), t.project, t.region)
	if err != nil {
		return t, nil, nil, provision.Config{}, err
	}
	cfg := provision.Config{Project: t.project, Region: t.region, App: graph.Module, Env: t.env}
	return t, graph, client, cfg, nil
}

// runPromote is `tako promote`: 100% of every service's traffic to the
// latest revision — a canary's completion. Pure traffic; no rebuild.
func runPromote(args []string) error {
	_, graph, client, cfg, err := lifecycleSetup(args)
	if err != nil {
		return err
	}
	ctx := context.Background()
	runs := &gcp.RunServices{C: client, Labels: cfg.Labels()}
	for _, svc := range graph.Services {
		name := cfg.Name(svc.Name)
		if err := runs.Promote(ctx, name); err != nil {
			return err
		}
		revs, err := runs.Revisions(ctx, name)
		if err != nil {
			return err
		}
		sha := "sha unknown"
		if len(revs) > 0 && revs[0].GitSHA != "" {
			sha = "sha " + revs[0].GitSHA
		}
		fmt.Printf("✓ %-24s → 100%% latest (%s)\n", name, sha)
	}
	return nil
}

// runRollback is `tako rollback`: the whole revision set returns to the
// revision before the one serving now — instant, the images and
// revisions already exist. Infra-shape changes roll back via git revert
// + tako deploy instead (the reconciler makes the cloud match old code).
func runRollback(args []string) error {
	_, graph, client, cfg, err := lifecycleSetup(args)
	if err != nil {
		return err
	}
	ctx := context.Background()
	runs := &gcp.RunServices{C: client, Labels: cfg.Labels()}
	rolled := 0
	for _, svc := range graph.Services {
		name := cfg.Name(svc.Name)
		from, to, err := runs.Rollback(ctx, name)
		if err != nil {
			if strings.Contains(err.Error(), "single revision") {
				fmt.Printf("  %-24s stays — only one revision exists\n", name)
				continue
			}
			return err
		}
		sha := to.GitSHA
		if sha == "" {
			sha = "unknown"
		}
		fmt.Printf("✓ %-24s %s ← %s (sha %s)\n", name, from.Name, to.Name, sha)
		rolled++
	}
	if rolled == 0 {
		return fmt.Errorf("nothing to roll back to — every service has a single revision")
	}
	return nil
}

// pruneStateless are the kinds tako prune deletes without --data:
// compute and messaging, in delete order. Data (buckets, entity kinds)
// stays behind the §4.6a confirmation of --data.
var pruneStateless = map[provision.Kind]bool{
	provision.KindPushSubscription: true,
	provision.KindRunService:       true,
	provision.KindPullSubscription: true,
	provision.KindTopic:            true,
	provision.KindDatastoreIndex:   true,
	provision.KindServiceAccount:   true,
}

// runPrune is `tako prune [--data]`: delete labeled orphans — objects
// tako once managed whose declarations left the code. Stateless kinds
// go immediately; --data walks buckets and undeclared datastore kinds
// through counts and the typed confirmation.
func runPrune(args []string) error {
	withData := false
	var filtered []string
	for _, a := range args {
		if a == "--data" {
			withData = true
			continue
		}
		filtered = append(filtered, a)
	}
	t, graph, client, cfg, err := lifecycleSetup(filtered)
	if err != nil {
		return err
	}
	ctx := context.Background()
	app := graph.Module
	// Identities matter here, not digests (same trick as destroy).
	images := map[string]string{}
	for _, svc := range graph.Services {
		images[svc.Name] = "pruned@sha256:none"
	}
	desired, err := provision.Desired(graph, cfg, images)
	if err != nil {
		return err
	}
	repos := &gcp.Repos{C: client, Labels: cfg.Labels()}
	sas := &gcp.ServiceAccounts{C: client, App: app, Env: t.env}
	runs := &gcp.RunServices{C: client, Labels: cfg.Labels()}
	buckets := &gcp.Buckets{C: client, Labels: cfg.Labels()}
	indexes := &gcp.DatastoreIndexes{C: client, Desired: desired, Say: sayf}
	topics := &gcp.Topics{C: client, Labels: cfg.Labels()}
	subs := &gcp.PushSubscriptions{C: client, Labels: cfg.Labels(), Say: sayf}
	pulls := &gcp.PullSubscriptions{C: client, Labels: cfg.Labels()}
	observed, err := observeAll(ctx, repos, sas, buckets, indexes, topics, pulls, runs, subs)
	if err != nil {
		return err
	}
	inDesired := map[string]bool{}
	for _, d := range desired {
		inDesired[string(d.Kind)+"/"+d.Name] = true
	}
	var orphans []provision.Observed
	for _, o := range observed {
		if !inDesired[string(o.Kind)+"/"+o.Name] {
			orphans = append(orphans, o)
		}
	}

	deleters := map[provision.Kind]interface {
		Delete(ctx context.Context, name string) error
	}{
		provision.KindPushSubscription: subs,
		provision.KindRunService:       runs,
		provision.KindPullSubscription: pulls,
		provision.KindTopic:            topics,
		provision.KindDatastoreIndex:   indexes,
		provision.KindServiceAccount:   sas,
	}
	pruned := 0
	for _, kind := range provision.DeleteOrder() {
		if !pruneStateless[kind] {
			continue
		}
		for _, o := range orphans {
			if o.Kind != kind {
				continue
			}
			fmt.Printf("  ✗ %s %s\n", o.Kind, o.Name)
			if err := deleters[kind].Delete(ctx, o.Name); err != nil {
				return err
			}
			pruned++
		}
	}

	// Data orphans: labeled buckets whose declaration left, and kinds
	// present in the env's namespace but no longer declared.
	var dataBuckets []string
	for _, o := range orphans {
		if o.Kind == provision.KindBucket {
			dataBuckets = append(dataBuckets, o.Name)
		}
	}
	declared := map[string]bool{}
	hasEntity := false
	for _, r := range graph.Resources {
		if r.Kind == "entity" {
			declared[r.Name] = true
			hasEntity = true
		}
	}
	var dataKinds []string
	if hasEntity {
		kinds, err := client.KindsInNamespace(ctx, t.env)
		if err != nil {
			return err
		}
		for _, k := range kinds {
			// _-prefixed kinds are framework-internal (sessions) — never
			// user data to prune.
			if !declared[k] && !strings.HasPrefix(k, "_") {
				dataKinds = append(dataKinds, k)
			}
		}
	}

	if len(dataBuckets) == 0 && len(dataKinds) == 0 {
		if pruned == 0 {
			fmt.Println("no orphans — nothing to prune")
		} else {
			fmt.Printf("✓ pruned %d stateless orphan(s)\n", pruned)
		}
		return nil
	}
	if !withData {
		if pruned > 0 {
			fmt.Printf("✓ pruned %d stateless orphan(s)\n", pruned)
		}
		for _, b := range dataBuckets {
			fmt.Printf("  kept (data): bucket %s\n", b)
		}
		for _, k := range dataKinds {
			fmt.Printf("  kept (data): datastore kind %s (namespace %s)\n", k, t.env)
		}
		fmt.Println("  run `tako prune --data` to delete data orphans (interactive confirmation)")
		return nil
	}

	fmt.Println("  DATA — deleted forever:")
	for _, b := range dataBuckets {
		n, capped, err := countObjects(ctx, client, b)
		if err != nil {
			return err
		}
		suffix := ""
		if capped {
			suffix = "+"
		}
		fmt.Printf("    bucket %s (%d%s objects)\n", b, n, suffix)
	}
	for _, k := range dataKinds {
		n, err := client.CountKind(ctx, t.env, k)
		if err != nil {
			return err
		}
		fmt.Printf("    datastore kind %s (%d entities, namespace %s)\n", k, n, t.env)
	}
	fmt.Printf("type the app name to confirm: ")
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	if strings.TrimSpace(line) != app {
		return fmt.Errorf("aborted (typed %q, want %q)", strings.TrimSpace(line), app)
	}
	for _, b := range dataBuckets {
		fmt.Printf("  ✗ bucket %s\n", b)
		if err := buckets.Delete(ctx, b); err != nil {
			return err
		}
	}
	if len(dataKinds) > 0 {
		n, err := client.PurgeDatastore(ctx, t.env, dataKinds)
		if err != nil {
			return err
		}
		fmt.Printf("  ✗ datastore data (%d entities in namespace %s)\n", n, t.env)
	}
	fmt.Println("✓ pruned")
	return nil
}

// countObjects counts a bucket's objects, capped at 1000 (the
// confirmation listing needs scale, not precision).
func countObjects(ctx context.Context, client *gcp.Client, bucket string) (int, bool, error) {
	names, err := client.ListObjects(ctx, bucket, 1000)
	if err != nil {
		return 0, false, err
	}
	return len(names), len(names) == 1000, nil
}
