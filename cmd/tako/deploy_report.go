package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gsandoval/tako/pkg/analyzer"
	"github.com/gsandoval/tako/pkg/provision"
	"github.com/gsandoval/tako/pkg/provision/gcp"
)

// projectGrantReason says WHY a grant had to land on the whole project
// instead of one resource. There is more than one reason, and reciting
// the Datastore one for a metrics grant would describe a narrower scope
// than the deploy actually asks for.
func projectGrantReason(role string) string {
	if strings.HasPrefix(role, "roles/monitoring") {
		return "every service exports requests and request_ms from the request middleware, and counters derive no usage edges — package-wide, like secrets"
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
	for _, r := range graph.Resources {
		if r.Kind == "docs" {
			vis := "private (IAM-gated to services with a read edge)"
			if r.Config["public"] == "true" {
				vis = "public (allUsers roles/storage.objectViewer)"
			}
			fmt.Printf("  docs %s — %s; slices: %s\n",
				r.Name, vis, strings.Join(r.DocsSlices, ", "))
		}
	}
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

// warnSiteShadows names the site files a declared route takes over.
// The warning belongs at DECISION time like the ones above: a shadowed
// stylesheet is not a failure anywhere — the route answers, honestly,
// 404 — so the only signal it ever produces is an unstyled site someone
// notices later. It runs after build.Sites above, because a site that
// has not been generated yet has no files to shadow.
func warnSiteShadows(dir string, graph *analyzer.AppGraph) {
	for _, s := range analyzer.ShadowedSiteFiles(dir, graph) {
		fmt.Printf("  %s\n", s.Warning())
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
