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
	"sort"
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
			(name == "--project" || name == "--region" || name == "--env" || name == "--canary") {
			args = append(args, name, value)
			continue
		}
		args = append(args, a)
	}
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
	channels := &gcp.AlertChannels{C: client, App: cfg.App, Env: cfg.Env, Labels: cfg.Labels()}
	policies := &gcp.AlertPolicies{C: client, App: cfg.App, Env: cfg.Env, Labels: cfg.Labels(), Channels: channels}
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

// projectGrantReason says WHY a grant had to land on the whole project
// instead of one resource. There is more than one reason, and reciting
// the Datastore one for a metrics grant would describe a narrower scope
// than the deploy actually asks for.
func projectGrantReason(role string) string {
	if strings.HasPrefix(role, "roles/monitoring") {
		return "counters and histograms derive no usage edges, so every service writes metrics — package-wide, like secrets"
	}
	return "GCP has no per-kind Datastore IAM"
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
				line = fmt.Sprintf("%s on the project (%s)", b["role"], projectGrantReason(fmt.Sprint(b["role"])))
			}
			if fmt.Sprint(target["type"]) == provision.TargetBucket && fmt.Sprint(target["id"]) == cfg.Project+"-tako" {
				line = fmt.Sprintf("%s on the framework bucket (leases — Lock/singleflight)", b["role"])
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
	// Private ROUTE services (Route.Private) close ingress like async
	// services, but the honest text names the granted callers instead
	// of the push identity.
	privateRoutes := map[string]bool{}
	svcOfVar := map[string]string{}
	for _, h := range graph.Handlers {
		svcOfVar[h.Var] = h.Service
		if (h.Kind == "api" || h.Kind == "page") && h.Private {
			privateRoutes[h.Service] = true
		}
	}
	callersOf := map[string][]string{}
	var callLines []string
	for _, e := range graph.Edges {
		if e.Kind != "call" || svcOfVar[e.From] == svcOfVar[e.To] {
			continue
		}
		callersOf[svcOfVar[e.To]] = append(callersOf[svcOfVar[e.To]], cfg.SAName(svcOfVar[e.From]))
		callLines = append(callLines, fmt.Sprintf("%s → %s", e.From, e.To))
	}
	for _, svc := range graph.Services {
		email := cfg.SAEmail(svc.Name)
		grants := byMember["serviceAccount:"+email]
		grantText := "no resource grants"
		if len(grants) > 0 {
			grantText = strings.Join(grants, ", ")
		}
		ingress := "public ingress"
		if svc.Domain != "" {
			// A domain mapping does not close the run.app door — it adds a
			// second one. Saying "public at blog.example.com" alone would
			// read as a restriction that is not there.
			ingress = "public ingress on " + svc.Domain + " AND its run.app URL (a domain mapping adds an address, it does not close one)"
		}
		if !public[cfg.Name(svc.Name)] {
			// The attempt count is declared now (tako.Delivery), so the
			// summary reads the carrier rather than reciting a default.
			ingress = fmt.Sprintf("ingress: OIDC push from its own SA only · redelivery: %d attempts with backoff, then dead-letter",
				deliveryAttemptsOf(graph, svc.Name))
			if svc.Job {
				// The retry count is declared now (RunnerConfig.Attempts), so the
				// summary must read it rather than recite the default.
				ingress = fmt.Sprintf("a Cloud Run Job — no ingress; executions run on demand (retries: %d, then the execution fails)",
					jobRetriesOf(graph, svc.Name))
			}
			if svc.Singleton {
				ingress = "ingress: none serving — the loop runs while this instance holds the lease"
			}
			if privateRoutes[svc.Name] {
				callers := "no granted callers yet"
				if cs := callersOf[svc.Name]; len(cs) > 0 {
					sort.Strings(cs)
					callers = "callers: " + strings.Join(cs, ", ")
				}
				ingress = "ingress: IAM-only (" + callers + ")"
			}
		}
		fmt.Printf("  %s runs as %s —\n    %s; %s\n", cfg.Name(svc.Name), email, grantText, ingress)
		// A footprint is explicit spend — show what it RESOLVES to, so
		// the plan carries the price, not just the override.
		if svc.Footprint != nil {
			profile := svc.Profile
			if svc.Singleton {
				profile = "singleton"
			}
			if svc.Job {
				profile = "batch"
			}
			r := svc.Footprint.Over(analyzer.ProfileKnobs[profile])
			if svc.Job {
				fmt.Printf("    task compute: %d cpu / %s (profile %s + footprint %s)\n",
					r.CPU, r.Memory, profile, svc.Footprint)
			} else {
				fmt.Printf("    compute: %d cpu / %s / concurrency %d / instances %d–%d (profile %s + footprint %s)\n",
					r.CPU, r.Memory, r.Concurrency, r.MinInstances, r.MaxInstances, profile, svc.Footprint)
			}
		}
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
				fmt.Printf("    ⚠ %d mutating route(s) without Route{SignIn: true}: %s — anyone on the internet can call these\n",
					len(open), strings.Join(open, ", "))
			}
		}
	}
	if len(callLines) > 0 {
		sort.Strings(callLines)
		fmt.Printf("  call graph: %s\n", strings.Join(callLines, " · "))
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

// warnCallEdges surfaces Cloud Run's sync-call costs at deploy time
// (§2.12 — profile-dependent judgments live here, not the analyzer):
// a call into a scale-to-zero callee pays its cold start on the
// caller's clock, and chains past one hop couple autoscaling.
func warnCallEdges(graph *analyzer.AppGraph) {
	svcOf := map[string]string{}
	profile := map[string]string{}
	for _, h := range graph.Handlers {
		svcOf[h.Var] = h.Service
	}
	for _, s := range graph.Services {
		profile[s.Name] = s.Profile
	}
	callPairs := map[[2]string]bool{}
	for _, e := range graph.Edges {
		if e.Kind != "call" || svcOf[e.From] == svcOf[e.To] {
			continue
		}
		callPairs[[2]string{svcOf[e.From], svcOf[e.To]}] = true
	}
	var pairs [][2]string
	for p := range callPairs {
		pairs = append(pairs, p)
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i][0]+pairs[i][1] < pairs[j][0]+pairs[j][1] })
	for _, p := range pairs {
		if profile[p[1]] == "default" {
			fmt.Printf("  ⚠ sync call %s → %s lands on a scale-to-zero service — cold starts bill the caller's latency; give %s tako.Service(tako.ServiceConfig{Profile: tako.ProfileLatency}) (min-instance cost) or make the interaction async\n",
				p[0], p[1], p[1])
		}
		for _, q := range pairs {
			if q[0] == p[1] {
				fmt.Printf("  ⚠ sync call chain %s → %s → %s — waiting callers hold their own request slots and couple the services' autoscaling; prefer events past one hop\n",
					p[0], p[1], q[1])
			}
		}
	}
}

// warnFootprints prices explicit compute at DECISION time — a footprint
// that raises min-instances above its profile's buys always-on capacity,
// and the plan is where that must be legible, not the post-apply summary.
func warnFootprints(graph *analyzer.AppGraph) {
	for _, svc := range graph.Services {
		if svc.Footprint == nil {
			continue
		}
		profile := svc.Profile
		if svc.Singleton {
			profile = "singleton"
		}
		if svc.Job {
			profile = "batch"
		}
		if svc.Footprint.MinInstances > analyzer.ProfileKnobs[profile].MinInstances {
			r := svc.Footprint.Over(analyzer.ProfileKnobs[profile])
			fmt.Printf("  ⚠ footprint on %s keeps %d instance(s) always on — they bill continuously whether traffic comes or not\n",
				svc.Name, r.MinInstances)
		}
	}
}

// deliveryAttemptsOf is the declared MaxAttempts of the carrier feeding
// this async service, or the framework default.
func deliveryAttemptsOf(graph *analyzer.AppGraph, service string) int {
	for _, h := range graph.Handlers {
		if h.Service != service || (h.Kind != "worker" && h.Kind != "subscribe") {
			continue
		}
		binding := strings.TrimSuffix(h.Binding, ".Uploaded")
		for _, r := range graph.Resources {
			if (r.Kind == "queue" || r.Kind == "topic") && r.Name == binding && r.MaxAttempts > 0 {
				return r.MaxAttempts
			}
		}
	}
	return analyzer.DeliveryDefaults.MaxAttempts
}

// jobRetriesOf is the job's declared retry count, or Cloud Run's 3.
func jobRetriesOf(graph *analyzer.AppGraph, service string) int {
	for _, h := range graph.Handlers {
		if h.Service == service && h.Kind == "job" && h.Retries > 0 {
			return h.Retries
		}
	}
	return 3
}

// warnDomains states the cost of a custom domain at DECISION time. The
// cost is not money — a Cloud Run domain mapping has no standing charge,
// which is why tako uses one (docs/domain-options.md prices the
// alternatives) — it is MATURITY: Google ships mappings as preview and
// does not recommend them for production. Saying nothing would be the
// dishonest choice; a money ⚠ would be a false one.
func warnDomains(graph *analyzer.AppGraph) {
	for _, svc := range graph.Services {
		if svc.Domain == "" {
			continue
		}
		fmt.Printf("  ⚠ %s answers on a Cloud Run domain mapping — Google ships these as preview and does not recommend them for production (latency); no standing charge. Moving %s to a load balancer later is a tako change, not a change to your code\n",
			svc.Domain, svc.Name)
	}
}

// narrateDomains reports where each custom domain actually is, every
// deploy: the records still to create, or the live URL once the
// certificate is issued. Deploys are stateless and resumable, so this
// re-reads live status rather than remembering anything — a re-run after
// DNS propagates picks up exactly where the certificate got to.
func narrateDomains(ctx context.Context, graph *analyzer.AppGraph, domains *gcp.DomainMappings) error {
	for _, svc := range graph.Services {
		if svc.Domain == "" {
			continue
		}
		// Cloud Run fills status.resourceRecords a beat AFTER the create
		// returns, so a straight read right after applying comes back
		// empty — on the very first deploy, the one run that most needs
		// the records printed. Wait briefly for them.
		var st *gcp.Status
		deadline := time.Now().Add(30 * time.Second)
		for {
			var err error
			if st, err = domains.Status(ctx, svc.Domain); err != nil {
				return err
			}
			if st.Ready || len(st.Records) > 0 || time.Now().After(deadline) {
				break
			}
			time.Sleep(2 * time.Second)
		}
		switch {
		case st.Ready:
			fmt.Printf("\n  domain  %-24s certificate ACTIVE → https://%s\n", svc.Domain, svc.Domain)
		case len(st.Records) > 0:
			fmt.Printf("\n  domain  %-24s awaiting DNS\n", svc.Domain)
			// Record names come back RELATIVE to the zone ("blog", not
			// "blog.ker.gs"), which is what a registrar's host field wants
			// — printing the FQDN would get "blog.ker.gs.ker.gs" created.
			// So name the zone instead of rewriting the records.
			fmt.Printf("    create these records in the DNS zone for %s:\n", gcp.ZoneOf(svc.Domain))
			for _, r := range st.Records {
				fmt.Printf("      %-28s %-6s %s\n", r.Name, r.Type, r.Data)
			}
			fmt.Printf("    then re-run tako deploy — the certificate provisions once the\n")
			fmt.Printf("    records resolve (usually ~15 minutes, up to 24 hours).\n")
		default:
			reason := st.Reason
			if reason == "" {
				reason = "provisioning"
			}
			fmt.Printf("\n  domain  %-24s certificate %s — re-run tako deploy to check again\n", svc.Domain, reason)
		}
	}
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
