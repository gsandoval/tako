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
	TargetServiceAccount = "serviceAccount"
	TargetSubscription   = "subscription"
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
		case "entity":
			hasEntity = true
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
	if hasEntity {
		ensure(target{TargetProject, cfg.Project})
	}
	for _, svc := range g.Services {
		ensure(target{TargetRunService, cfg.Name(svc.Name)})
		ensure(target{TargetServiceAccount, cfg.SAEmail(svc.Name)})
	}

	svcOf := map[string]string{} // handler var → service name
	for _, h := range g.Handlers {
		svcOf[h.Var] = h.Service
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
				// signBlob grants arrive with C5's signed URLs.
			}
		case "queue", "topic":
			if e.Verb == "publish" {
				add(target{TargetTopic, cfg.Name(e.To)}, "roles/pubsub.publisher", member)
			}
		}
		// A write into a bucket with Uploaded subscribers means the
		// runtime will emit the event as this service — it publishes on
		// the events topic (bucket events lite).
		if e.ToKind == "bucket" && e.Verb == "write" && events[e.To] {
			add(target{TargetTopic, gcpb.EventTopicName(cfg.Env, e.To)}, "roles/pubsub.publisher", member)
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
