package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/gsandoval/tako/pkg/analyzer"
	"github.com/gsandoval/tako/pkg/build"
	"github.com/gsandoval/tako/pkg/codegen"
	"github.com/gsandoval/tako/pkg/devserver"
	"github.com/gsandoval/tako/pkg/provision"
	"github.com/gsandoval/tako/pkg/provision/gcp"
)

// target is a parsed deploy destination.
type target struct {
	project, region, env string
	planOnly             bool
}

func parseTarget(args []string) (target, []string, error) {
	t := target{env: provision.DefaultEnv}
	var rest []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--plan":
			t.planOnly = true
		case "--project", "--region", "--env":
			flag := args[i]
			if i+1 >= len(args) {
				return t, nil, fmt.Errorf("%s needs a value", flag)
			}
			i++
			switch flag {
			case "--project":
				t.project = args[i]
			case "--region":
				t.region = args[i]
			case "--env":
				t.env = args[i]
			}
		default:
			rest = append(rest, args[i])
		}
	}
	if t.project == "" {
		t.project = os.Getenv("GOOGLE_CLOUD_PROJECT")
	}
	if t.project == "" {
		if out, err := exec.Command("gcloud", "config", "get-value", "project").Output(); err == nil {
			if p := strings.TrimSpace(string(out)); p != "" && p != "(unset)" {
				t.project = p
			}
		}
	}
	if t.project == "" {
		return t, nil, fmt.Errorf("no project set — pass --project, set GOOGLE_CLOUD_PROJECT, or run \"gcloud config set project <id>\"")
	}
	if t.region == "" {
		t.region = os.Getenv("TAKO_REGION")
	}
	if t.region == "" {
		t.region = "us-central1"
	}
	return t, rest, nil
}

// runDeploy is `tako deploy [--plan] [--project] [--region] [--env]`.
func runDeploy(args []string) error {
	t, rest, err := parseTarget(args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return fmt.Errorf("unknown argument %q (tako deploy accepts --plan, --project, --region, --env)", rest[0])
	}
	ctx := context.Background()
	start := time.Now()

	dir, err := filepath.Abs(".")
	if err != nil {
		return err
	}
	graph, err := analyzer.Analyze(analyzer.Config{Dir: dir})
	if err != nil {
		return err
	}
	app := graph.Module
	fmt.Printf("tako deploy · %s → %s (%s)\n\n", app, t.project, t.region)
	fmt.Printf("  analyzing    %s\n", devserver.CountLine(graph))
	if _, err := codegen.Generate(graph, dir); err != nil {
		return err
	}
	fmt.Printf("  generating   .tako/gen\n")

	client, err := gcp.NewClient(ctx, t.project, t.region)
	if err != nil {
		return err
	}
	probed, err := client.Preflight(ctx, needsOf(graph))
	if err != nil {
		return err
	}
	fmt.Printf("  preflight    ok (%s)\n", strings.Join(probed, ", "))
	projectNumber, err := client.ProjectNumber(ctx)
	if err != nil {
		return err
	}

	// Build first: digests are part of the spec, so plans see image
	// changes. Nothing is pushed until the plan says something changed.
	cfg := provision.Config{Project: t.project, ProjectNumber: projectNumber, Region: t.region, App: app, Env: t.env}
	repoHost := t.region + "-docker.pkg.dev/" + t.project + "/" + provision.RepoName
	images := map[string]string{}
	for _, svc := range graph.Services {
		res, err := build.Service(build.Options{Dir: dir, App: app}, svc.Name)
		if err != nil {
			return err
		}
		images[svc.Name] = repoHost + "/" + svc.Name + "@" + res.Digest
	}
	fmt.Printf("  building     %d service image(s), digest-pinned\n", len(images))

	desired, err := provision.Desired(graph, cfg, images)
	if err != nil {
		return err
	}
	repos := &gcp.Repos{C: client, Labels: cfg.Labels()}
	sas := &gcp.ServiceAccounts{C: client, App: app, Env: t.env}
	runs := &gcp.RunServices{C: client, Labels: cfg.Labels(), Annotations: versionStamps(dir, graph)}
	buckets := &gcp.Buckets{C: client, Labels: cfg.Labels()}
	indexes := &gcp.DatastoreIndexes{C: client, Desired: desired, Say: sayf}
	topics := &gcp.Topics{C: client, Labels: cfg.Labels()}
	subs := &gcp.PushSubscriptions{C: client, Labels: cfg.Labels()}
	grants := &gcp.Grants{C: client, Desired: desired}
	observed, err := observeAll(ctx, repos, sas, buckets, indexes, topics, runs, grants, subs)
	if err != nil {
		return err
	}
	plan := provision.Compute(app, t.env, desired, observed)
	fmt.Printf("  plan\n%s", indentPlan(plan.Render()))

	if t.planOnly {
		return nil
	}
	if !plan.Changed() {
		fmt.Printf("\n✓ no changes (%s)\n", time.Since(start).Round(time.Second))
		return nil
	}

	lease := &gcp.Lease{C: client, App: app, Env: t.env, Labels: cfg.Labels(), Say: sayf}
	release, err := lease.Acquire(ctx, holder())
	if err != nil {
		return err
	}
	defer release()
	fmt.Printf("  lease        acquired (%s on %s/%s/%s)\n", holder(), t.project, app, t.env)

	applier := provision.Route(repos, sas, buckets, indexes, topics, runs, grants, subs)
	if err := provision.Apply(ctx, plan.Filter(provision.KindRepo, provision.KindServiceAccount,
		provision.KindBucket, provision.KindDatastoreIndex, provision.KindTopic), desired, applier); err != nil {
		return err
	}
	for _, svc := range graph.Services {
		if _, err := build.Service(build.Options{Dir: dir, App: app, Repo: repoHost}, svc.Name); err != nil {
			return err
		}
		fmt.Printf("  ⛅ image      %s\n", images[svc.Name])
	}
	if err := provision.Apply(ctx, plan.Filter(provision.KindRunService), desired, applier); err != nil {
		return err
	}
	// Grants precede subscriptions: push delivery needs the invoker
	// binding on the worker before the first message arrives.
	if err := provision.Apply(ctx, plan.Filter(provision.KindIamGrants, provision.KindPushSubscription), desired, applier); err != nil {
		return err
	}

	fmt.Println()
	for _, svc := range graph.Services {
		url, err := runs.URL(ctx, cfg.Name(svc.Name))
		if err != nil {
			return err
		}
		fmt.Printf("✓ %s → %s\n", cfg.Name(svc.Name), url)
	}
	fmt.Printf("  (%s)\n\n", time.Since(start).Round(time.Second))
	printSecuritySummary(graph, cfg, desired)
	return nil
}

// printSecuritySummary derives every grant from the desired objects —
// the user sees each grant and why, every deploy.
func printSecuritySummary(graph *analyzer.AppGraph, cfg provision.Config, desired []provision.Object) {
	fmt.Println("Security summary:")
	byMember := map[string][]string{}
	for _, o := range desired {
		if o.Kind != provision.KindIamGrants {
			continue
		}
		target, _ := o.Spec["target"].(map[string]any)
		for _, raw := range o.Spec["bindings"].([]any) {
			b := raw.(map[string]any)
			member := fmt.Sprint(b["member"])
			line := fmt.Sprintf("%s on %s", b["role"], o.Name)
			if fmt.Sprint(target["type"]) == provision.TargetProject {
				line = fmt.Sprintf("%s on the project (GCP has no per-kind Datastore IAM)", b["role"])
			}
			byMember[member] = append(byMember[member], line)
		}
	}
	public := map[string]bool{}
	for _, o := range desired {
		if o.Kind == provision.KindRunService {
			public[o.Name], _ = o.Spec["public"].(bool)
		}
	}
	for _, svc := range graph.Services {
		email := cfg.SAEmail(svc.Name)
		grants := byMember["serviceAccount:"+email]
		grantText := "no resource grants"
		if len(grants) > 0 {
			grantText = strings.Join(grants, ", ")
		}
		ingress := "public ingress"
		if !public[cfg.Name(svc.Name)] {
			ingress = "ingress: OIDC push from its own SA only"
		}
		fmt.Printf("  %s runs as %s —\n    %s; %s\n", cfg.Name(svc.Name), email, grantText, ingress)
	}
}

// needsOf reports which optional backend surfaces the graph uses.
func needsOf(graph *analyzer.AppGraph) gcp.Needs {
	var n gcp.Needs
	for _, r := range graph.Resources {
		switch r.Kind {
		case "entity":
			n.Datastore = true
		case "queue", "topic":
			n.PubSub = true
		}
	}
	return n
}

// runLogs is `tako logs [service] [--since 15m] [--project …]`.
func runLogs(args []string) error {
	t, rest, err := parseTarget(args)
	if err != nil {
		return err
	}
	since := 15 * time.Minute
	var service string
	for i := 0; i < len(rest); i++ {
		if rest[i] == "--since" && i+1 < len(rest) {
			i++
			d, err := time.ParseDuration(rest[i])
			if err != nil {
				return fmt.Errorf("--since wants a duration like 15m, got %q", rest[i])
			}
			since = d
			continue
		}
		service = rest[i]
	}
	graph, err := analyzer.Analyze(analyzer.Config{Dir: "."})
	if err != nil {
		return err
	}
	cfg := provision.Config{Project: t.project, Region: t.region, App: graph.Module, Env: t.env}
	var services []string
	for _, svc := range graph.Services {
		if service == "" || svc.Name == service || cfg.Name(svc.Name) == service {
			services = append(services, cfg.Name(svc.Name))
		}
	}
	if len(services) == 0 {
		return fmt.Errorf("no service named %q — services: %s", service, serviceNames(graph))
	}
	client, err := gcp.NewClient(context.Background(), t.project, t.region)
	if err != nil {
		return err
	}
	entries, err := client.Logs(context.Background(), services, since, 200)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		fmt.Printf("no log entries in the last %s\n", since)
		return nil
	}
	for _, e := range entries {
		fmt.Printf("%s %-12s %-7s %s\n", e.Time.Local().Format("15:04:05"), e.Service, e.Severity, e.Text)
	}
	return nil
}

// runDestroy is `tako destroy [--project …] [--env …]`: delete every
// object holding the app's labels, in reverse reconcile order, after an
// explicit typed confirmation.
func runDestroy(args []string) error {
	t, rest, err := parseTarget(args)
	if err != nil {
		return err
	}
	if len(rest) > 0 {
		return fmt.Errorf("unknown argument %q", rest[0])
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
	buckets := &gcp.Buckets{C: client, Labels: cfg.Labels()}
	indexes := &gcp.DatastoreIndexes{C: client, Desired: desired, Say: sayf}
	topics := &gcp.Topics{C: client, Labels: cfg.Labels()}
	subs := &gcp.PushSubscriptions{C: client, Labels: cfg.Labels()}
	grants := &gcp.Grants{C: client, Desired: desired}
	observed, err := observeAll(ctx, repos, sas, buckets, indexes, topics, runs, grants, subs)
	if err != nil {
		return err
	}
	if len(observed) == 0 {
		fmt.Printf("nothing to destroy: no objects labeled tako-app=%s tako-env=%s in %s\n", app, t.env, t.project)
		return nil
	}
	var dataKinds []string
	for _, r := range graph.Resources {
		if r.Kind == "entity" {
			dataKinds = append(dataKinds, r.Name)
		}
	}
	fmt.Printf("tako destroy · %s (%s) in %s — this deletes:\n", app, t.env, t.project)
	for _, o := range observed {
		fmt.Printf("  %s %s\n", o.Kind, o.Name)
	}
	fmt.Printf("  framework bucket %s-tako\n", t.project)
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
	}
	fmt.Printf("type the app name to confirm: ")
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	if strings.TrimSpace(line) != app {
		return fmt.Errorf("aborted (typed %q, want %q)", strings.TrimSpace(line), app)
	}

	deleters := map[provision.Kind]interface {
		Delete(ctx context.Context, name string) error
	}{
		provision.KindPushSubscription: subs,
		provision.KindIamGrants:        grants,
		provision.KindRunService:       runs,
		provision.KindTopic:            topics,
		provision.KindDatastoreIndex:   indexes,
		provision.KindBucket:           buckets,
		provision.KindServiceAccount:   sas,
		provision.KindRepo:             repos,
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
	if len(dataKinds) > 0 {
		n, err := client.PurgeDatastore(ctx, t.env, dataKinds)
		if err != nil {
			return err
		}
		fmt.Printf("  ✗ datastore data (%d entities in namespace %s)\n", n, t.env)
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

type observer interface {
	List(ctx context.Context) ([]provision.Observed, error)
}

func observeAll(ctx context.Context, observers ...observer) ([]provision.Observed, error) {
	var all []provision.Observed
	for _, o := range observers {
		got, err := o.List(ctx)
		if err != nil {
			return nil, err
		}
		all = append(all, got...)
	}
	return all, nil
}

func versionStamps(dir string, graph *analyzer.AppGraph) map[string]string {
	stamps := map[string]string{}
	cmd := exec.Command("git", "rev-parse", "--short", "HEAD")
	cmd.Dir = dir
	if out, err := cmd.Output(); err == nil {
		stamps["tako-git-sha"] = strings.TrimSpace(string(out))
	}
	if raw, err := json.Marshal(graph); err == nil {
		sum := sha256.Sum256(raw)
		stamps["tako-graph-hash"] = hex.EncodeToString(sum[:])[:16]
	}
	return stamps
}

func holder() string {
	name := "unknown"
	if u, err := user.Current(); err == nil {
		name = u.Username
	}
	host, _ := os.Hostname()
	return name + "@" + host
}

func sayf(format string, args ...any) { fmt.Printf("  "+format+"\n", args...) }

func indentPlan(rendered string) string {
	var out strings.Builder
	for _, line := range strings.Split(strings.TrimRight(rendered, "\n"), "\n") {
		out.WriteString("  " + line + "\n")
	}
	return out.String()
}

func serviceNames(g *analyzer.AppGraph) string {
	var names []string
	for _, svc := range g.Services {
		names = append(names, svc.Name)
	}
	return strings.Join(names, ", ")
}
