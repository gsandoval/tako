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
	"strconv"
	"strings"
	"time"

	"github.com/gsandoval/tako/pkg/analyzer"
	gcpb "github.com/gsandoval/tako/pkg/backends/gcp"
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
	yes                  bool
	canary               int
}

func parseTarget(rawArgs []string) (target, []string, error) {
	t := target{env: provision.DefaultEnv}
	// Both --flag value and --flag=value forms are accepted (§4.6a
	// writes --env=pr-42).
	var args []string
	for _, a := range rawArgs {
		if name, value, ok := strings.Cut(a, "="); ok &&
			(name == "--project" || name == "--region" || name == "--env") {
			args = append(args, name, value)
			continue
		}
		args = append(args, a)
	}
	var rest []string
	for i := 0; i < len(args); i++ {
		if pct, ok := strings.CutPrefix(args[i], "--canary="); ok {
			n, err := strconv.Atoi(pct)
			if err != nil || n < 1 || n > 99 {
				return t, nil, fmt.Errorf("--canary wants a percent between 1 and 99, got %q", pct)
			}
			t.canary = n
			continue
		}
		switch args[i] {
		case "--plan":
			t.planOnly = true
		case "--yes":
			t.yes = true
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
		return fmt.Errorf("unknown argument %q (tako deploy accepts --plan, --canary=<pct>, --yes, --project, --region, --env)", rest[0])
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
	// Frontend builds run BEFORE codegen: the generated services embed
	// the built output.
	if err := build.Sites(dir, graph, sayf); err != nil {
		return err
	}
	if _, err := codegen.Generate(graph, dir); err != nil {
		return err
	}
	fmt.Printf("  generating   .tako/gen\n")

	client, err := gcp.NewClient(ctx, t.project, t.region)
	if err != nil {
		return err
	}
	probed, err := client.Preflight(ctx, needsOf(graph, t.env))
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
	// Auth apps get the browser client config injected, and their public
	// services' hosts registered as authorized sign-in domains (the
	// popup refuses unregistered origins). Deterministic URLs make the
	// hosts known before anything deploys.
	if len(needsOf(graph, t.env).AuthProviders) > 0 {
		var hosts []string
		for _, svc := range graph.Services {
			u := cfg.RunURL(svc.Name)
			hosts = append(hosts, strings.TrimPrefix(u, "https://"))
		}
		authClient, err := client.EnsureAuthClient(ctx, hosts, sayf)
		if err != nil {
			return err
		}
		cfg.AuthAPIKey, cfg.AuthDomain = authClient.APIKey, authClient.AuthDomain
	}
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
	subs := &gcp.PushSubscriptions{C: client, Labels: cfg.Labels(), Say: sayf}
	pulls := &gcp.PullSubscriptions{C: client, Labels: cfg.Labels()}
	notifications := &gcp.BucketNotifications{C: client, Desired: desired}
	managedSA, err := sas.ManagedEmails(ctx)
	if err != nil {
		return err
	}
	grants := &gcp.Grants{C: client, Desired: desired, ManagedSA: managedSA}
	observed, err := observeAll(ctx, repos, sas, buckets, indexes, topics, notifications, pulls, runs, grants, subs)
	if err != nil {
		return err
	}
	plan := provision.Compute(app, t.env, desired, observed)
	fmt.Printf("  plan\n%s", indentPlan(plan.Render()))
	warnInertCrons(graph)

	if t.planOnly {
		return nil
	}
	if !plan.Changed() {
		fmt.Printf("\n✓ no changes (%s)\n", time.Since(start).Round(time.Second))
		return nil
	}
	// §4.6a "plan before apply": anything beyond additive/in-place —
	// replaces, rename suspects — waits for a human unless --yes (CI).
	if plan.NeedsConfirm() && !t.yes {
		fmt.Printf("  the plan contains replaces or rename suspects — continue? [y/N] ")
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if l := strings.ToLower(strings.TrimSpace(line)); l != "y" && l != "yes" {
			return fmt.Errorf("aborted — nothing was changed (re-run with --yes to skip this prompt)")
		}
	}
	runs.Canary = t.canary

	lease := &gcp.Lease{C: client, App: app, Env: t.env, Labels: cfg.Labels(), Say: sayf}
	release, err := lease.Acquire(ctx, holder())
	if err != nil {
		return err
	}
	defer release()
	fmt.Printf("  lease        acquired (%s on %s/%s/%s)\n", holder(), t.project, app, t.env)

	applier := provision.Route(repos, sas, buckets, indexes, topics, notifications, pulls, runs, grants, subs)
	if err := provision.Apply(ctx, plan.Filter(provision.KindRepo, provision.KindServiceAccount,
		provision.KindBucket, provision.KindDatastoreIndex, provision.KindTopic,
		provision.KindPullSubscription), desired, applier); err != nil {
		return err
	}
	// SAs may have just been created: the grant passes below key their
	// managed-member strip on the exact email set, so refresh it.
	if grants.ManagedSA, err = sas.ManagedEmails(ctx); err != nil {
		return err
	}
	// Grant sequencing is dictated by boot/delivery dependencies:
	// services RESOLVE SECRETS AT BOOT, so secret (and every other
	// non-run, non-subscription) grant lands before the run services
	// start; bucket notifications need the GCS agent's events-topic
	// publisher grant (GCS validates it at config creation), so they
	// follow the pre-run grants; push delivery needs the invoker
	// binding on the target run service before the first message, so
	// run grants land before subscriptions; and the Pub/Sub agent's
	// dead-letter grants sit ON the subscriptions, which must exist to
	// hold a policy.
	grantsPlan := plan.Filter(provision.KindIamGrants)
	var preRun, onRuns, onSubs provision.Plan
	for _, e := range grantsPlan.Entries {
		switch {
		case strings.HasPrefix(e.Name, provision.TargetSubscription+" "):
			onSubs.Entries = append(onSubs.Entries, e)
		case strings.HasPrefix(e.Name, provision.TargetRunService+" "):
			onRuns.Entries = append(onRuns.Entries, e)
		default:
			preRun.Entries = append(preRun.Entries, e)
		}
	}
	if err := provision.Apply(ctx, preRun, desired, applier); err != nil {
		return err
	}
	if err := provision.Apply(ctx, plan.Filter(provision.KindBucketNotification), desired, applier); err != nil {
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
	if err := provision.Apply(ctx, onRuns, desired, applier); err != nil {
		return err
	}
	if err := provision.Apply(ctx, plan.Filter(provision.KindPushSubscription), desired, applier); err != nil {
		return err
	}
	if err := provision.Apply(ctx, onSubs, desired, applier); err != nil {
		return err
	}

	fmt.Println()
	var liveHosts []string
	for _, svc := range graph.Services {
		url, err := runs.URL(ctx, cfg.Name(svc.Name))
		if err != nil {
			return err
		}
		liveHosts = append(liveHosts, strings.TrimPrefix(url, "https://"))
		fmt.Printf("✓ %s → %s\n", cfg.Name(svc.Name), url)
	}
	// Cloud Run answers on TWO hosts per service — the deterministic
	// projectNumber form (authorized pre-deploy) and the legacy uri the
	// API reports (the one the ✓ lines print, so the one browsers
	// visit). Sign-in popups authorize by exact page host: register the
	// live ones too, now that the services exist.
	if cfg.AuthAPIKey != "" {
		if _, err := client.EnsureAuthClient(ctx, liveHosts, sayf); err != nil {
			return err
		}
	}
	if t.canary > 0 {
		fmt.Println()
		for _, svc := range graph.Services {
			name := cfg.Name(svc.Name)
			targets, err := runs.Traffic(ctx, name)
			if err != nil {
				return err
			}
			var parts []string
			for _, tt := range targets {
				rev := tt.Revision
				if rev == "" {
					rev = "latest"
				}
				parts = append(parts, fmt.Sprintf("%d%% %s", tt.Percent, rev))
			}
			fmt.Printf("  canary  %-24s %s\n", name, strings.Join(parts, " / "))
		}
		fmt.Printf("  (async services stage at 0%% — tako promote completes, tako rollback abandons)\n")
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
			ingress = "ingress: OIDC push from its own SA only · redelivery: 5 attempts with backoff, then dead-letter"
		}
		fmt.Printf("  %s runs as %s —\n    %s; %s\n", cfg.Name(svc.Name), email, grantText, ingress)
		if public[cfg.Name(svc.Name)] {
			var open []string
			for _, h := range graph.Handlers {
				if h.Service != svc.Name || (h.Kind != "api" && h.Kind != "page") || h.RequiresAuth {
					continue
				}
				method, _, _ := strings.Cut(h.Route, " ")
				if method != "GET" && method != "HEAD" {
					open = append(open, h.Route)
				}
			}
			if len(open) > 0 {
				fmt.Printf("    ⚠ %d mutating route(s) without WithAuth: %s — anyone on the internet can call these\n",
					len(open), strings.Join(open, ", "))
			}
		}
	}
}

// needsOf reports which optional backend surfaces the graph uses.
func needsOf(graph *analyzer.AppGraph, env string) gcp.Needs {
	var n gcp.Needs
	for _, r := range graph.Resources {
		switch r.Kind {
		case "entity":
			n.Datastore = true
		case "auth":
			// Sessions are entity records: auth alone needs Datastore,
			// plus Identity Platform initialized with the declared
			// providers (preflight prints the console steps if not).
			n.Datastore = true
			n.AuthProviders = r.Providers
		case "queue", "topic":
			n.PubSub = true
		case "secret":
			n.Secrets = append(n.Secrets, gcpb.SecretName(env, r.Name))
		}
	}
	// Async handlers mean dead-letter topics and subscriptions even
	// when the only source is a bucket's events topic.
	for _, h := range graph.Handlers {
		if h.Kind == "worker" || h.Kind == "subscribe" {
			n.PubSub = true
		}
	}
	// Sign edges mean the runtime mints signed URLs via signBlob.
	for _, e := range graph.Edges {
		if e.Kind == "resource" && e.Verb == "sign" {
			n.SignBlob = true
		}
	}
	return n
}

// warnInertCrons tells the truth about cron in the cloud: nothing
// schedules it yet (a scheduler feature follows C-phase), so nobody
// should believe a daily job is running.
func warnInertCrons(graph *analyzer.AppGraph) {
	for _, h := range graph.Handlers {
		if h.Kind == "cron" {
			fmt.Printf("  ⚠ cron %s is not scheduled in the cloud yet (a scheduler feature follows); it still runs under tako dev\n", h.Var)
		}
	}
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
	pulls := &gcp.PullSubscriptions{C: client, Labels: cfg.Labels()}
	notifications := &gcp.BucketNotifications{C: client, Desired: desired}
	managedSA, err := sas.ManagedEmails(ctx)
	if err != nil {
		return err
	}
	grants := &gcp.Grants{C: client, Desired: desired, ManagedSA: managedSA}
	observed, err := observeAll(ctx, repos, sas, buckets, indexes, topics, notifications, pulls, runs, grants, subs)
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
			if o.Kind == provision.KindRepo {
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
	fmt.Printf("type the app name to confirm: ")
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	if strings.TrimSpace(line) != app {
		return fmt.Errorf("aborted (typed %q, want %q)", strings.TrimSpace(line), app)
	}

	deleters := map[provision.Kind]interface {
		Delete(ctx context.Context, name string) error
	}{
		provision.KindPushSubscription:   subs,
		provision.KindIamGrants:          grants,
		provision.KindRunService:         runs,
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
	if len(dataKinds) > 0 {
		n, err := client.PurgeDatastore(ctx, t.env, dataKinds)
		if err != nil {
			return err
		}
		fmt.Printf("  ✗ datastore data (%d entities in namespace %s)\n", n, t.env)
	}
	if envScoped {
		fmt.Printf("✓ destroyed (env %s only — shared repo and framework bucket stay)\n", t.env)
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
