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
	"github.com/gsandoval/tako/pkg/backends"
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
	args := normalizeArgs(rawArgs)
	var rest []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--canary" {
			if i+1 >= len(args) {
				return t, nil, fmt.Errorf("--canary needs a percent between 1 and 99")
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 1 || n > 99 {
				return t, nil, fmt.Errorf("--canary wants a percent between 1 and 99, got %q", args[i])
			}
			t.canary = n
			continue
		}
		switch args[i] {
		case "--plan":
			t.planOnly = true
		case "--yes":
			t.yes = true
		case "--project", "--region", "--env", "-C", "--dir":
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
			case "-C", "--dir":
				if err := os.Chdir(args[i]); err != nil {
					return t, nil, err
				}
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
	if err := checkEnv(t.env); err != nil {
		return t, nil, err
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
	fmt.Printf("tako deploy · %s → %s (%s)\n", app, t.project, t.region)
	// A module path Google will not take as a label value or a bucket
	// segment is derived into one (gcp.AppID). Said once, here, so a
	// person about to read a plan full of the derived name finds out
	// why — not repeated per object.
	if id := gcpb.AppID(app); id != app {
		fmt.Printf("              id %s — labels and bucket names use this\n", id)
	}
	fmt.Println()
	fmt.Printf("  analyzing    %s\n", devserver.CountLine(graph))
	// Names Google would refuse are refused here — before the frontend
	// build, before codegen, before the first network call. A length
	// failure discovered mid-apply names a string that appears nowhere
	// in the user's code.
	if err := provision.CheckNames(graph, provision.Config{Project: t.project, App: app, Env: t.env}); err != nil {
		return err
	}
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
	if providers := needsOf(graph, t.env).AuthProviders; len(providers) > 0 {
		var hosts []string
		for _, svc := range graph.Services {
			u := cfg.RunURL(svc.Name)
			hosts = append(hosts, strings.TrimPrefix(u, "https://"))
			// A custom domain is the host the browser actually shows, so
			// it is the origin the sign-in popup authorizes against.
			if svc.Domain != "" {
				hosts = append(hosts, svc.Domain)
			}
		}
		authClient, err := client.EnsureAuthClient(ctx, hosts, providers, !t.planOnly, sayf)
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
	jobs := &gcp.RunJobs{C: client, Labels: cfg.Labels()}
	scheds := &gcp.SchedulerJobs{C: client, App: cfg.App, Env: cfg.Env}
	tasks := &gcp.TasksQueues{C: client}
	domains := &gcp.DomainMappings{C: client, Labels: cfg.Labels(), Say: sayf}
	// Alert ownership is decided by the tako-app LABEL (gcp.ownedBy),
	// so these carry the id the labels carry, not the module path.
	channels := &gcp.AlertChannels{C: client, App: cfg.AppID(), Env: cfg.Env, Labels: cfg.Labels()}
	policies := &gcp.AlertPolicies{C: client, App: cfg.AppID(), Env: cfg.Env, Labels: cfg.Labels(), Channels: channels}
	buckets := &gcp.Buckets{C: client, Labels: cfg.Labels()}
	indexes := &gcp.DatastoreIndexes{C: client, Desired: desired, Kinds: entityKinds(graph), Say: sayf}
	topics := &gcp.Topics{C: client, Labels: cfg.Labels()}
	subs := &gcp.PushSubscriptions{C: client, Labels: cfg.Labels(), Say: sayf}
	pulls := &gcp.PullSubscriptions{C: client, Labels: cfg.Labels()}
	notifications := &gcp.BucketNotifications{C: client, Desired: desired}
	managedSA, err := sas.ManagedEmails(ctx)
	if err != nil {
		return err
	}
	grants := &gcp.Grants{C: client, Desired: desired, ManagedSA: managedSA}
	observed, err := observeAll(ctx, repos, sas, buckets, indexes, topics, notifications, pulls, runs, jobs, grants, subs, scheds, tasks, domains, channels, policies)
	if err != nil {
		return err
	}
	plan := provision.Compute(app, t.env, desired, observed)
	fmt.Printf("  plan\n%s", indentPlan(plan.Render()))
	warnCallEdges(graph)
	warnFootprints(graph)
	warnDomains(graph)
	warnSiteShadows(dir, graph)
	lease := &gcp.Lease{C: client, App: cfg.AppID(), Env: t.env, Labels: cfg.Labels(), Say: sayf}
	prevGraph, _ := lease.LoadDeployedGraph(ctx, false)
	var activeFlags []backends.FlagRule
	if rtClient, err := gcpb.New(ctx, gcpb.Options{Project: t.project, App: cfg.AppID(), Env: t.env}); err == nil {
		activeFlags, _ = gcpb.NewFlagStore(rtClient, 0).List(ctx)
	}
	skew := provision.CheckSkew(prevGraph, graph, activeFlags)
	if len(skew.Findings) > 0 {
		fmt.Printf("\n  version & config skew\n%s", skew.Render())
	}

	if t.planOnly {
		return nil
	}
	if !plan.Changed() {
		fmt.Printf("\n✓ no changes (%s)\n", time.Since(start).Round(time.Second))
		// A converged deploy is exactly the run the DNS instructions tell
		// you to make ("re-run tako deploy"), so it must still report
		// where the certificate got to — the mapping is unchanged, its
		// STATUS is not.
		return narrateDomains(ctx, graph, domains)
	}
	// §4.6a "plan before apply": anything beyond additive/in-place —
	// replaces, rename suspects, or dangerous version/config skew —
	// waits for a human unless --yes (CI).
	if (plan.NeedsConfirm() || skew.HasDanger()) && !t.yes {
		fmt.Printf("  the plan contains replaces, rename suspects, or dangerous version/config skew — continue? [y/N] ")
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if l := strings.ToLower(strings.TrimSpace(line)); l != "y" && l != "yes" {
			return fmt.Errorf("aborted — nothing was changed (re-run with --yes to skip this prompt)")
		}
	}
	runs.Canary = t.canary

	release, err := lease.Acquire(ctx, holder())
	if err != nil {
		return err
	}
	defer release()
	fmt.Printf("  lease        acquired (%s on %s/%s/%s)\n", holder(), t.project, app, t.env)

	applier := provision.Route(repos, sas, buckets, indexes, topics, notifications, pulls, runs, jobs, grants, subs, scheds, tasks, domains, channels, policies)
	if err := provision.Apply(ctx, plan.Filter(provision.KindRepo, provision.KindServiceAccount,
		provision.KindBucket, provision.KindDatastoreIndex, provision.KindTopic,
		provision.KindPullSubscription, provision.KindTasksQueue), desired, applier); err != nil {
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
	// A Pub/Sub alert channel is delivered by Cloud Monitoring's own
	// service agent, which does not exist in a fresh project until
	// something asks for it — and Pub/Sub refuses an IAM binding naming
	// an agent that does not exist, with a 400, after everything before
	// it has already been created. Provisioning the identity here is
	// idempotent and removes the ordering question entirely.
	if hasPubSubChannel(graph) {
		if _, err := client.EnsureServiceIdentity(ctx, gcp.MonitoringNotificationAgent); err != nil {
			return err
		}
	}
	if hasBucketSubscription(graph) {
		if _, err := client.EnsureStorageServiceAccount(ctx); err != nil {
			return err
		}
	}
	grantsPlan := plan.Filter(provision.KindIamGrants)
	var preRun, onRuns, onSubs provision.Plan
	for _, e := range grantsPlan.Entries {
		switch {
		case strings.HasPrefix(e.Name, provision.TargetSubscription+" "):
			onSubs.Entries = append(onSubs.Entries, e)
		case strings.HasPrefix(e.Name, provision.TargetRunService+" "),
			strings.HasPrefix(e.Name, provision.TargetRunJob+" "):
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
	// Index builds started back in the first Apply and have been running
	// ever since — through the grant passes, the bucket notifications and
	// the image push, which is most of a deploy. Collect them here, the
	// last moment before anything can serve a query against them.
	if err := indexes.Wait(ctx); err != nil {
		return err
	}
	if err := provision.Apply(ctx, plan.Filter(provision.KindRunService), desired, applier); err != nil {
		return err
	}
	if err := provision.Apply(ctx, plan.Filter(provision.KindRunJob), desired, applier); err != nil {
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
	if err := provision.Apply(ctx, plan.Filter(provision.KindSchedulerJob), desired, applier); err != nil {
		return err
	}
	// Domain mappings last: a mapping names a live run service as its
	// route, and a route that does not exist yet is a 404 with a
	// misleading shape (the T3 ordering lesson, one kind over).
	if err := provision.Apply(ctx, plan.Filter(provision.KindDomainMapping), desired, applier); err != nil {
		return err
	}
	// Alerting last, and channels before policies: a policy names its
	// channels by resource name, and an app that is still being built
	// should not page about its own deploy.
	//
	// The metric descriptors come first of all, because Cloud Monitoring
	// refuses a PromQL policy naming a metric that does not exist yet —
	// and a custom metric does not exist until something writes it, so a
	// FIRST deploy could otherwise never create its own alerts.
	if len(plan.Filter(provision.KindAlertPolicy).Entries) > 0 {
		descriptors := &gcp.MetricDescriptors{C: client}
		if err := descriptors.Ensure(ctx, graph); err != nil {
			return err
		}
		// A descriptor alone is not enough: PromQL resolves names from
		// DATA, so the series has to exist before a policy may name it.
		if err := descriptors.Seed(ctx, graph, cfg.AppID(), cfg.Env, cfg.Region); err != nil {
			return err
		}
		// Seeding covers tako's own metrics. It cannot cover Google's —
		// so ask which names resolve, and say plainly which alerts
		// cannot be created yet rather than failing partway through the
		// apply with Google's own sentence about it.
		known, err := client.KnownMetricNames(ctx)
		if err != nil {
			return err
		}
		if bad := gcp.CheckMetricNames(known, provision.AlertMetricNames(cfg, graph)); len(bad) > 0 {
			return gcp.UnresolvableError(bad)
		}
	}
	if err := provision.Apply(ctx, plan.Filter(provision.KindAlertChannel), desired, applier); err != nil {
		return err
	}
	if err := provision.Apply(ctx, plan.Filter(provision.KindAlertPolicy), desired, applier); err != nil {
		return err
	}
	for _, r := range graph.Resources {
		if r.Kind == "entity" && r.TTLField != "" {
			_ = client.EnsureEntityTTL(ctx, r.Name, r.TTLField)
		}
	}
	docsUploaded := map[string]int{}
	for _, r := range graph.Resources {
		if r.Kind == "docs" {
			bname := gcpb.DocsBucketName(cfg.Project, cfg.AppID(), cfg.Env, r.Name)
			docsDir := filepath.Join(".tako", "gen", "docs", r.Name)
			n, err := client.SyncDir(ctx, bname, docsDir)
			if err != nil {
				return err
			}
			docsUploaded[r.Name] = n
		}
	}
	_ = lease.SaveDeployedGraph(ctx, graph)

	fmt.Println()
	var liveHosts []string
	for _, svc := range graph.Services {
		if svc.Job {
			fmt.Printf("✓ %s → job %s (run with %s.Run, no URL, no request timeout)\n", svc.Name, cfg.Name(svc.Name), jobBinding(graph, svc.Name))
			continue
		}
		url, err := runs.URL(ctx, cfg.Name(svc.Name))
		if err != nil {
			return err
		}
		liveHosts = append(liveHosts, strings.TrimPrefix(url, "https://"))
		fmt.Printf("✓ %s → %s\n", cfg.Name(svc.Name), url)
	}
	for _, r := range graph.Resources {
		if r.Kind == "docs" {
			bname := gcpb.DocsBucketName(cfg.Project, cfg.AppID(), cfg.Env, r.Name)
			vis := "private bucket"
			if r.Config["public"] == "true" {
				vis = fmt.Sprintf("public → https://storage.googleapis.com/%s/index.html", bname)
			}
			fmt.Printf("✓ docs:%s → gs://%s (%d files, %s, slices: %s)\n",
				r.Name, bname, docsUploaded[r.Name], vis, strings.Join(r.DocsSlices, ", "))
		}
	}
	// Cloud Run answers on TWO hosts per service — the deterministic
	// projectNumber form (authorized pre-deploy) and the legacy uri the
	// API reports (the one the ✓ lines print, so the one browsers
	// visit). Sign-in popups authorize by exact page host: register the
	// live ones too, now that the services exist.
	if cfg.AuthAPIKey != "" {
		if _, err := client.EnsureAuthClient(ctx, liveHosts, nil, true, sayf); err != nil {
			return err
		}
	}
	if err := narrateDomains(ctx, graph, domains); err != nil {
		return err
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
			if r.Kind == "queue" && (r.RateLimit > 0 || r.Delayed) {
				n.Tasks = true
			}
		case "secret":
			n.Secrets = append(n.Secrets, gcpb.SecretName(env, r.Name))
		case "counter", "histogram":
			n.Metrics = true
		}
	}
	// Async handlers mean dead-letter topics and subscriptions even
	// when the only source is a bucket's events topic.
	for _, h := range graph.Handlers {
		if h.Kind == "worker" || h.Kind == "subscribe" {
			n.PubSub = true
		}
		if h.Kind == "cron" {
			n.Scheduler = true
		}
	}
	// Custom domains (U1): the region must support mappings and each
	// name must be verified — preflight names both before anything runs.
	for _, svc := range graph.Services {
		if svc.Domain != "" {
			n.Domains = append(n.Domains, svc.Domain)
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

// jobBinding names the tako.Job a job deployable executes.
func jobBinding(graph *analyzer.AppGraph, svc string) string {
	for _, h := range graph.Handlers {
		if h.Service == svc && h.Kind == "job" {
			return h.Binding
		}
	}
	return "?"
}

// entityKinds are the app's declared entity kinds — the scope within
// which orphaned composite indexes are tako's to report and prune.
func entityKinds(graph *analyzer.AppGraph) []string {
	var kinds []string
	for _, r := range graph.Resources {
		if r.Kind == "entity" {
			kinds = append(kinds, r.Name)
		}
	}
	return kinds
}

// hasPubSubChannel reports whether the app declares an alert channel
// delivered by Cloud Monitoring's notification agent — the one identity
// a deploy has to provision rather than derive.
func hasPubSubChannel(g *analyzer.AppGraph) bool {
	for _, r := range g.Resources {
		if r.Kind == "channel" && r.ChannelKind == "pubsub" {
			return true
		}
	}
	return false
}

func hasBucketSubscription(g *analyzer.AppGraph) bool {
	for _, h := range g.Handlers {
		if h.Kind == "subscribe" && strings.HasSuffix(h.Binding, ".Uploaded") {
			return true
		}
	}
	return false
}
