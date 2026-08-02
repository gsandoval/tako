package main

// `tako destroy`: the one irreversible command, and the only one that
// takes a typed confirmation. Split out of deploy.go.

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

// runDestroy is `tako destroy [--project …] [--env …]`: delete every
// object holding the app's labels, in reverse reconcile order, after an
// explicit typed confirmation.
func runDestroy(args []string) error {
	t, rest, err := parseTarget(args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return fmt.Errorf("unknown argument %q (tako destroy accepts --plan, --project, --region, --env)", rest[0])
	}
	ctx := context.Background()
	graph, err := analyzer.Analyze(analyzer.Config{Dir: "."})
	if err != nil {
		return err
	}
	app := graph.Module
	client, err := gcp.NewClient(ctx, t.project, t.region)
	if err != nil {
		return err
	}
	projectNumber, err := client.ProjectNumber(ctx)
	if err != nil {
		return err
	}
	cfg := provision.Config{Project: t.project, ProjectNumber: projectNumber, Region: t.region, App: app, Env: t.env}
	// Content-addressed kinds (indexes, grants) observe against the
	// desired set; destroy derives it with placeholder digests since
	// only identities matter here.
	images := map[string]string{}
	for _, svc := range graph.Services {
		images[svc.Name] = "destroyed@sha256:none"
	}
	desired, err := provision.Desired(graph, cfg, images)
	if err != nil {
		return err
	}
	repos := &gcp.Repos{C: client, Labels: cfg.Labels()}
	sas := &gcp.ServiceAccounts{C: client, App: app, Env: t.env}
	runs := &gcp.RunServices{C: client, Labels: cfg.Labels()}
	jobs := &gcp.RunJobs{C: client, Labels: cfg.Labels()}
	scheds := &gcp.SchedulerJobs{C: client, App: cfg.App, Env: cfg.Env}
	tasks := &gcp.TasksQueues{C: client}
	domains := &gcp.DomainMappings{C: client, Labels: cfg.Labels(), Say: sayf}
	buckets := &gcp.Buckets{C: client, Labels: cfg.Labels()}
	indexes := &gcp.DatastoreIndexes{C: client, Desired: desired, Kinds: entityKinds(graph), Say: sayf}
	topics := &gcp.Topics{C: client, Labels: cfg.Labels()}
	subs := &gcp.PushSubscriptions{C: client, Labels: cfg.Labels()}
	pulls := &gcp.PullSubscriptions{C: client, Labels: cfg.Labels()}
	notifications := &gcp.BucketNotifications{C: client, Desired: desired}
	managedSA, err := sas.ManagedEmails(ctx)
	if err != nil {
		return err
	}
	grants := &gcp.Grants{C: client, Desired: desired, ManagedSA: managedSA}
	observed, err := observeAll(ctx, repos, sas, buckets, indexes, topics, notifications, pulls, runs, jobs, grants, subs, scheds, tasks, domains)
	if err != nil {
		return err
	}
	// A preview env's destroy is env-scoped: the artifact repo and the
	// framework (lease) bucket are PROJECT-shared machinery — deleting
	// them would break every other env (the C6 walk caught pr-42's
	// destroy taking prod's repo with it).
	envScoped := t.env != provision.DefaultEnv
	if envScoped {
		kept := observed[:0]
		for _, o := range observed {
			// Shared machinery survives env-scoped destroys: the repo,
			// and datastore indexes — they serve EVERY namespace (a
			// pr-env teardown once deleted prod's composite index).
			if o.Kind == provision.KindRepo || o.Kind == provision.KindDatastoreIndex {
				continue
			}
			kept = append(kept, o)
		}
		observed = kept
	}
	if len(observed) == 0 {
		fmt.Printf("nothing to destroy: no objects labeled tako-app=%s tako-env=%s in %s\n", app, t.env, t.project)
		return nil
	}
	var dataKinds []string
	purgeKinds := []string{}
	for _, r := range graph.Resources {
		if r.Kind == "entity" {
			dataKinds = append(dataKinds, r.Name)
			purgeKinds = append(purgeKinds, r.Name)
		}
		if r.Kind == "auth" {
			// Session records purge with the namespace but are not DATA
			// to warn about (they are revocable credentials, not content).
			purgeKinds = append(purgeKinds, "_tako_session")
		}
	}
	fmt.Printf("tako destroy · %s (%s) in %s — this deletes:\n", app, t.env, t.project)
	for _, o := range observed {
		fmt.Printf("  %s %s\n", o.Kind, o.Name)
	}
	if !envScoped {
		fmt.Printf("  framework bucket %s-tako\n", t.project)
	}
	if hasData := len(dataKinds) > 0 || countKind(observed, provision.KindBucket) > 0; hasData {
		fmt.Println("  DATA — deleted with the app, gone forever:")
		for _, o := range observed {
			if o.Kind == provision.KindBucket {
				fmt.Printf("    bucket %s (every object)\n", o.Name)
			}
		}
		if len(dataKinds) > 0 {
			fmt.Printf("    datastore kinds %s (namespace %s)\n", strings.Join(dataKinds, ", "), t.env)
		}
		for _, o := range observed {
			if o.Kind == provision.KindPullSubscription {
				fmt.Printf("    dead-letter messages retained on %s (if any)\n", o.Name)
			}
		}
	}
	// --plan stops here, having observed everything and deleted nothing.
	// Destroy always BUILT this preview; until now the only way to see it
	// was to start the one irreversible command and type the wrong name
	// on purpose. Inspecting a teardown should not require arming it.
	if t.planOnly {
		fmt.Println("\n(--plan: nothing was deleted; drop --plan to run it)")
		return nil
	}
	fmt.Printf("type the app name to confirm: ")
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	if strings.TrimSpace(line) != app {
		return fmt.Errorf("aborted (typed %q, want %q)", strings.TrimSpace(line), app)
	}

	// Sign-in domains tako registered come off on the way out — the
	// deterministic hosts plus each live service URI, collected BEFORE
	// the services are deleted.
	var authHosts []string
	if len(needsOf(graph, t.env).AuthProviders) > 0 {
		for _, svc := range graph.Services {
			authHosts = append(authHosts, strings.TrimPrefix(cfg.RunURL(svc.Name), "https://"))
			if svc.Domain != "" {
				authHosts = append(authHosts, svc.Domain)
			}
			if u, err := runs.URL(ctx, cfg.Name(svc.Name)); err == nil {
				authHosts = append(authHosts, strings.TrimPrefix(u, "https://"))
			}
		}
	}

	deleters := map[provision.Kind]interface {
		Delete(ctx context.Context, name string) error
	}{
		provision.KindDomainMapping:      domains,
		provision.KindSchedulerJob:       scheds,
		provision.KindTasksQueue:         tasks,
		provision.KindPushSubscription:   subs,
		provision.KindIamGrants:          grants,
		provision.KindRunService:         runs,
		provision.KindRunJob:             jobs,
		provision.KindPullSubscription:   pulls,
		provision.KindBucketNotification: notifications,
		provision.KindTopic:              topics,
		provision.KindDatastoreIndex:     indexes,
		provision.KindBucket:             buckets,
		provision.KindServiceAccount:     sas,
		provision.KindRepo:               repos,
	}
	for _, kind := range provision.DeleteOrder() {
		for _, o := range observed {
			if o.Kind != kind {
				continue
			}
			fmt.Printf("  ✗ %s %s\n", o.Kind, o.Name)
			if err := deleters[kind].Delete(ctx, o.Name); err != nil {
				return err
			}
		}
	}
	if len(purgeKinds) > 0 {
		n, err := client.PurgeDatastore(ctx, t.env, purgeKinds)
		if err != nil {
			return err
		}
		fmt.Printf("  ✗ datastore data (%d entities in namespace %s)\n", n, t.env)
	}
	// Sign-in domains tako registered come off on the way out — before
	// the env-scoped return, so previews unregister too.
	if len(authHosts) > 0 {
		if err := client.RemoveAuthorizedDomains(ctx, authHosts, sayf); err != nil {
			return err
		}
	}
	if envScoped {
		fmt.Printf("✓ destroyed (env %s only — shared repo, indexes, and framework bucket stay)\n", t.env)
		return nil
	}
	lease := &gcp.Lease{C: client, App: app, Env: t.env, Labels: cfg.Labels(), Say: sayf}
	if err := lease.DeleteBucket(ctx); err != nil {
		return err
	}
	fmt.Printf("  ✗ framework bucket %s-tako\n✓ destroyed\n", t.project)
	return nil
}

func countKind(observed []provision.Observed, kind provision.Kind) int {
	n := 0
	for _, o := range observed {
		if o.Kind == kind {
			n++
		}
	}
	return n
}
