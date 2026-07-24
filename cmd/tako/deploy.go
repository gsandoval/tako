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
	if err := client.Preflight(ctx); err != nil {
		return err
	}
	fmt.Printf("  preflight    ok (run, iam, artifactregistry, storage)\n")

	// Build first: digests are part of the spec, so plans see image
	// changes. Nothing is pushed until the plan says something changed.
	cfg := provision.Config{Project: t.project, Region: t.region, App: app, Env: t.env}
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
	observed, err := observeAll(ctx, repos, sas, runs)
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

	applier := provision.Route(repos, sas, runs)
	if err := provision.Apply(ctx, plan.Filter(provision.KindRepo, provision.KindServiceAccount), desired, applier); err != nil {
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

	fmt.Println()
	for _, svc := range graph.Services {
		url, err := runs.URL(ctx, cfg.Name(svc.Name))
		if err != nil {
			return err
		}
		fmt.Printf("✓ %s → %s\n", cfg.Name(svc.Name), url)
	}
	fmt.Printf("  (%s)\n\nSecurity summary:\n", time.Since(start).Round(time.Second))
	for _, svc := range graph.Services {
		fmt.Printf("  %s runs as %s — no resource grants (declares no resources); public ingress\n",
			cfg.Name(svc.Name), sas.Email(cfg.Name(svc.Name)))
	}
	return nil
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
	cfg := provision.Config{Project: t.project, Region: t.region, App: app, Env: t.env}
	client, err := gcp.NewClient(ctx, t.project, t.region)
	if err != nil {
		return err
	}
	repos := &gcp.Repos{C: client, Labels: cfg.Labels()}
	sas := &gcp.ServiceAccounts{C: client, App: app, Env: t.env}
	runs := &gcp.RunServices{C: client, Labels: cfg.Labels()}
	observed, err := observeAll(ctx, repos, sas, runs)
	if err != nil {
		return err
	}
	if len(observed) == 0 {
		fmt.Printf("nothing to destroy: no objects labeled tako-app=%s tako-env=%s in %s\n", app, t.env, t.project)
		return nil
	}
	fmt.Printf("tako destroy · %s (%s) in %s — this deletes:\n", app, t.env, t.project)
	for _, o := range observed {
		fmt.Printf("  %s %s\n", o.Kind, o.Name)
	}
	fmt.Printf("  framework bucket %s-tako\n", t.project)
	fmt.Printf("type the app name to confirm: ")
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	if strings.TrimSpace(line) != app {
		return fmt.Errorf("aborted (typed %q, want %q)", strings.TrimSpace(line), app)
	}
	// Reverse reconcile order: services → SAs → repos → framework bucket.
	for _, o := range observed {
		if o.Kind == provision.KindRunService {
			fmt.Printf("  ✗ run service %s\n", o.Name)
			if err := runs.Delete(ctx, o.Name); err != nil {
				return err
			}
		}
	}
	for _, o := range observed {
		if o.Kind == provision.KindServiceAccount {
			fmt.Printf("  ✗ service account %s\n", o.Name)
			if err := sas.Delete(ctx, o.Name); err != nil {
				return err
			}
		}
	}
	for _, o := range observed {
		if o.Kind == provision.KindRepo {
			fmt.Printf("  ✗ artifact repo %s (and its images)\n", o.Name)
			if err := repos.Delete(ctx, o.Name); err != nil {
				return err
			}
		}
	}
	lease := &gcp.Lease{C: client, App: app, Env: t.env, Labels: cfg.Labels(), Say: sayf}
	if err := lease.DeleteBucket(ctx); err != nil {
		return err
	}
	fmt.Printf("  ✗ framework bucket %s-tako\n✓ destroyed\n", t.project)
	return nil
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
