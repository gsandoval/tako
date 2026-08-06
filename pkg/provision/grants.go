package provision

import (
	"fmt"
	"sort"
	"strings"

	"github.com/gsandoval/tako/pkg/analyzer"
	gcpb "github.com/gsandoval/tako/pkg/backends/gcp"
)

// Grant targets. A grants object owns the tako-managed slice of ONE
// resource's IAM policy: its spec is the full desired binding set for
// that target, so removing a code edge converges to a policy rewrite
// without the stale binding — grants are not data, revocation is safe
// (docs/tako-cloud-permissions.md).
const (
	TargetProject        = "project"
	TargetBucket         = "bucket"
	TargetTopic          = "topic"
	TargetRunService     = "run"
	TargetRunJob         = "runjob"
	TargetServiceAccount = "serviceAccount"
	TargetSubscription   = "subscription"
	TargetTasksQueue     = "tasksQueue"
	TargetSecret         = "secret"
)

// deriveGrants projects AppGraph usage edges onto IAM bindings per spec
// §4.6's table. One grants object per known target — including targets
// with zero desired bindings, so stale grants on still-managed
// resources are revoked by convergence.
func deriveGrants(g *analyzer.AppGraph, cfg Config) []Object {
	type target struct{ typ, id string }
	bindings := map[target]map[string]bool{} // target → "role|member" set
	ensure := func(t target) map[string]bool {
		if bindings[t] == nil {
			bindings[t] = map[string]bool{}
		}
		return bindings[t]
	}
	add := func(t target, role, member string) {
		ensure(t)[role+"|"+member] = true
	}

	// Every managed resource that can hold tako bindings is a target
	// even when nothing is desired on it.
	hasEntity := false
	hasAuth := false
	hasMetrics := false
	events := map[string]bool{} // buckets with Uploaded subscribers
	for _, h := range g.Handlers {
		if h.Kind == "subscribe" && h.BindingKind == "bucket" {
			events[strings.TrimSuffix(h.Binding, ".Uploaded")] = true
		}
	}
	for _, r := range g.Resources {
		switch r.Kind {
		case "bucket":
			ensure(target{TargetBucket, gcpb.BucketName(cfg.Project, cfg.App, cfg.Env, r.Name)})
			if events[r.Name] {
				ensure(target{TargetTopic, gcpb.EventTopicName(cfg.Env, r.Name)})
			}
		case "queue", "topic":
			ensure(target{TargetTopic, cfg.Name(r.Name)})
			if r.Kind == "queue" && (r.RateLimit > 0 || r.Delayed) {
				ensure(target{TargetTasksQueue, cfg.Name(r.Name) + "-tako"})
			}
		case "entity":
			hasEntity = true
		case "auth":
			hasAuth = true
		case "counter", "histogram":
			hasMetrics = true
		case "secret":
			// Package-wide resolution (approved C4 call (a)): boot
			// resolves every declared secret in every service, so every
			// service SA reads every declared secret — "Value never
			// fails" holds verbatim.
			for _, svc := range g.Services {
				add(target{TargetSecret, cfg.Name(r.Name)},
					"roles/secretmanager.secretAccessor", "serviceAccount:"+cfg.SAEmail(svc.Name))
			}
		}
	}
	if hasEntity || hasAuth || hasMetrics {
		ensure(target{TargetProject, cfg.Project})
	}
	// Metrics are carriers with no usage edges — the analyzer records a
	// counter or histogram as a declaration and derives nothing about who
	// touches it — so there is no per-service set to narrow this to.
	// Package-wide, like secrets, and the security summary says so rather
	// than implying tako scoped it.
	//
	// EVERY service, declared counters or not (RC3): the request
	// middleware exports requests and request_ms from all of them, so
	// gating this on a declared Counter left four of the six examples
	// deploying services that write metrics they were refused —
	// observed on shortlink as "Permission monitoring.timeSeries.create
	// denied", which tako's own client correctly calls a deploy bug.
	for _, svc := range g.Services {
		add(target{TargetProject, cfg.Project}, "roles/monitoring.metricWriter",
			"serviceAccount:"+cfg.SAEmail(svc.Name))
	}
	// Sessions are entity records (W5): every service that serves
	// routes reads/writes them, so Auth alone grants datastore.user to
	// those services' identities.
	if hasAuth {
		routeServices := map[string]bool{}
		for _, h := range g.Handlers {
			if h.Kind == "api" || h.Kind == "page" {
				routeServices[h.Service] = true
			}
		}
		for svc := range routeServices {
			add(target{TargetProject, cfg.Project}, "roles/datastore.user", "serviceAccount:"+cfg.SAEmail(svc))
		}
	}
	for _, svc := range g.Services {
		ensure(target{TargetRunService, cfg.Name(svc.Name)})
		ensure(target{TargetServiceAccount, cfg.SAEmail(svc.Name)})
	}
	// The framework bucket is always managed (the deploy lease lives
	// there), so it is always a grants target: an app that STOPS using
	// locks converges to zero lease bindings instead of keeping stale
	// objectAdmin.
	ensure(target{TargetBucket, cfg.Project + "-tako"})

	// Tasks-routed queues (T3): the worker service behind each.
	tasksWorker := map[string]string{}
	for _, r := range g.Resources {
		if r.Kind == "queue" && (r.RateLimit > 0 || r.Delayed) {
			tasksWorker[r.Name] = ""
		}
	}
	for _, h := range g.Handlers {
		if h.Kind == "worker" {
			if _, ok := tasksWorker[h.Binding]; ok {
				tasksWorker[h.Binding] = h.Service
			}
		}
	}

	svcOf := map[string]string{} // handler var → service name
	for _, h := range g.Handlers {
		svcOf[h.Var] = h.Service
	}

	// Run edges (§2.7): triggering a job needs run.invoker ON THE JOB
	// (Cloud Run jobs accept the same role for jobs.run).
	jobSvcOf := map[string]string{} // job resource name → its deployable
	for _, h := range g.Handlers {
		if h.Kind == "job" {
			jobSvcOf[h.Binding] = h.Service
		}
	}
	for _, e := range g.Edges {
		if e.Kind != "resource" || e.ToKind != "job" || e.Verb != "run" {
			continue
		}
		svc, ok := svcOf2(g, e.From)
		if !ok {
			continue
		}
		if jobSvc := jobSvcOf[e.To]; jobSvc != "" {
			// runWithOverrides (params ride execution overrides) sits in
			// run.developer, not run.invoker — scoped to THIS job only.
			add(target{TargetRunJob, cfg.Name(jobSvc)}, "roles/run.developer", "serviceAccount:"+cfg.SAEmail(svc))
		}
	}

	// Call edges (§2.12): the caller's SA may invoke the callee — the
	// whole cross-service trust model in one derived binding.
	for _, e := range g.Edges {
		if e.Kind != "call" {
			continue
		}
		from, to := svcOf[e.From], svcOf[e.To]
		if from == "" || to == "" || from == to {
			continue
		}
		add(target{TargetRunService, cfg.Name(to)}, "roles/run.invoker", "serviceAccount:"+cfg.SAEmail(from))
	}

	for _, e := range g.Edges {
		if e.Kind != "resource" {
			continue
		}
		svc, ok := svcOf[e.From]
		if !ok {
			continue
		}
		member := "serviceAccount:" + cfg.SAEmail(svc)
		switch e.ToKind {
		case "entity":
			// GCP has no per-kind Datastore IAM — the grant is
			// project-wide, and the security summary says so.
			add(target{TargetProject, cfg.Project}, "roles/datastore.user", member)
		case "bucket":
			b := gcpb.BucketName(cfg.Project, cfg.App, cfg.Env, e.To)
			switch e.Verb {
			case "read":
				add(target{TargetBucket, b}, "roles/storage.objectViewer", member)
			case "write":
				add(target{TargetBucket, b}, "roles/storage.objectAdmin", member)
			case "sign":
				// Signed URLs authorize AS the signer at use time: the
				// SA signs via its own signBlob (self tokenCreator) and
				// needs object access for the URLs to work — admin, since
				// SignedPut writes and SignedGet reads.
				add(target{TargetServiceAccount, cfg.SAEmail(svc)}, "roles/iam.serviceAccountTokenCreator", member)
				add(target{TargetBucket, b}, "roles/storage.objectAdmin", member)
			}
		case "queue", "topic":
			if e.Verb == "publish" {
				add(target{TargetTopic, cfg.Name(e.To)}, "roles/pubsub.publisher", member)
				// Tasks-routed queues (T3): enqueue on the queue, and
				// actAs the worker's SA (the task's OIDC identity).
				if worker, ok := tasksWorker[e.To]; ok && worker != "" && e.ToKind == "queue" {
					add(target{TargetTasksQueue, cfg.Name(e.To) + "-tako"}, "roles/cloudtasks.enqueuer", member)
					add(target{TargetServiceAccount, cfg.SAEmail(worker)}, "roles/iam.serviceAccountUser", member)
				}
			}
		case "lock":
			// Leases are objects in the framework bucket (spec §4.4) —
			// object-level CAS writes need objectAdmin there. Bucket-wide
			// (GCS has no prefix IAM); the summary names the lease use.
			add(target{TargetBucket, cfg.Project + "-tako"}, "roles/storage.objectAdmin", member)
		}
	}

	// Singleflight crons and singleton loops hold the same kind of
	// lease without a Lock var.
	for _, h := range g.Handlers {
		if (h.Kind == "cron" && h.Singleflight) || h.Kind == "singleton" {
			add(target{TargetBucket, cfg.Project + "-tako"}, "roles/storage.objectAdmin",
				"serviceAccount:"+cfg.SAEmail(h.Service))
		}
	}

	// Bucket events are GCS's own notifications (C5): the project's GCS
	// service agent publishes into each events topic — no service
	// publishes there itself anymore.
	gcsAgent := "serviceAccount:service-" + cfg.ProjectNumber + "@gs-project-accounts.iam.gserviceaccount.com"
	for _, r := range g.Resources {
		if r.Kind == "bucket" && events[r.Name] {
			add(target{TargetTopic, gcpb.EventTopicName(cfg.Env, r.Name)}, "roles/pubsub.publisher", gcsAgent)
		}
	}

	// Push delivery, per async service: the subscription authenticates
	// as the service's own SA, so its Run service admits it and the
	// Pub/Sub service agent may mint its OIDC tokens. Dead-letter
	// forwarding needs the agent publishing into the dead topic and
	// acking the source subscription; the service itself publishes into
	// its own dead topic too (Permanent short-circuits there directly).
	pubsubAgent := "serviceAccount:service-" + cfg.ProjectNumber + "@gcp-sa-pubsub.iam.gserviceaccount.com"
	for _, h := range g.Handlers {
		if h.Kind != "worker" && h.Kind != "subscribe" {
			continue
		}
		member := "serviceAccount:" + cfg.SAEmail(h.Service)
		dead := gcpb.DeadTopicName(cfg.Env, h.Service)
		add(target{TargetRunService, cfg.Name(h.Service)}, "roles/run.invoker", member)
		add(target{TargetServiceAccount, cfg.SAEmail(h.Service)}, "roles/iam.serviceAccountTokenCreator", pubsubAgent)
		add(target{TargetTopic, dead}, "roles/pubsub.publisher", member)
		add(target{TargetTopic, dead}, "roles/pubsub.publisher", pubsubAgent)
		add(target{TargetSubscription, cfg.Name(h.Service)}, "roles/pubsub.subscriber", pubsubAgent)
	}

	// A Pub/Sub alert channel is delivered by Cloud Monitoring's OWN
	// service agent, not by anything tako runs, so that agent needs
	// publisher on the topic the channel names.
	//
	// Google does not create this agent until something asks for it, and
	// Pub/Sub REFUSES a binding that names an agent which does not
	// exist — 400, after everything before it has been created. An
	// earlier comment here claimed the opposite; nothing had tested it,
	// because no example declared a Pub/Sub channel until siren. The
	// deploy now provisions the identity before applying any grant
	// (cmd/tako/deploy.go, EnsureServiceIdentity).
	monAgent := "serviceAccount:service-" + cfg.ProjectNumber + "@gcp-sa-monitoring-notification.iam.gserviceaccount.com"
	for _, r := range g.Resources {
		if r.Kind == "channel" && r.ChannelKind == "pubsub" && r.Target != "" {
			add(target{TargetTopic, cfg.Name(r.Target)}, "roles/pubsub.publisher", monAgent)
		}
	}

	targets := make([]target, 0, len(bindings))
	for t := range bindings {
		targets = append(targets, t)
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].typ != targets[j].typ {
			return targets[i].typ < targets[j].typ
		}
		return targets[i].id < targets[j].id
	})

	objs := make([]Object, 0, len(targets))
	for _, t := range targets {
		set := bindings[t]
		pairs := make([]string, 0, len(set))
		for p := range set {
			pairs = append(pairs, p)
		}
		sort.Strings(pairs)
		bs := make([]any, 0, len(pairs))
		for _, p := range pairs {
			role, member, _ := strings.Cut(p, "|")
			bs = append(bs, map[string]any{"role": role, "member": member})
		}
		objs = append(objs, Object{
			Kind: KindIamGrants,
			Name: grantName(t.typ, t.id),
			Spec: map[string]any{
				"target":   map[string]any{"type": t.typ, "id": t.id},
				"bindings": bs,
			},
		})
	}
	return objs
}

func grantName(typ, id string) string {
	if typ == TargetProject {
		return "project"
	}
	return fmt.Sprintf("%s %s", typ, id)
}

// svcOf2 finds a handler var's service.
func svcOf2(g *analyzer.AppGraph, v string) (string, bool) {
	for _, h := range g.Handlers {
		if h.Var == v {
			return h.Service, true
		}
	}
	return "", false
}
