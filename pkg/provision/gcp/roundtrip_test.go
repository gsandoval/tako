package gcp

import (
	"encoding/json"
	"testing"

	"github.com/gsandoval/tako/pkg/analyzer"
	"github.com/gsandoval/tako/pkg/provision"
)

func TestDesiredToLiveHashRoundTrip(t *testing.T) {
	g := &analyzer.AppGraph{
		Module: "github.com/acme/roundtrip",
		Services: []analyzer.Service{
			{
				Name:     "web",
				Package:  "github.com/acme/roundtrip",
				Handlers: []string{"roundtrip.Home", "roundtrip.Tick"},
				Profile:  "default",
				Domain:   "app.example.com",
			},
			{
				Name:     "worker",
				Package:  "github.com/acme/roundtrip",
				Handlers: []string{"roundtrip.Work"},
				Profile:  "batch",
			},
			{
				Name:     "transcode",
				Package:  "github.com/acme/roundtrip",
				Handlers: []string{"roundtrip.Transcode"},
				Profile:  "batch",
				Job:      true,
			},
		},
		Handlers: []analyzer.Handler{
			{Kind: "api", Var: "roundtrip.Home", Package: "github.com/acme/roundtrip", Service: "web", Route: "GET /"},
			{Kind: "cron", Var: "roundtrip.Tick", Package: "github.com/acme/roundtrip", Service: "web", Schedule: "every 1h", Timezone: "America/New_York", DeadlineSeconds: 120},
			{Kind: "worker", Var: "roundtrip.Work", Package: "github.com/acme/roundtrip", Service: "worker", Binding: "jobs"},
			{Kind: "job", Var: "roundtrip.Transcode", Package: "github.com/acme/roundtrip", Service: "transcode", Binding: "transcode", Parallel: 4, Retries: 5},
		},
		Resources: []analyzer.Resource{
			{Kind: "queue", Name: "jobs", RateLimit: 10, MaxAttempts: 7},
			{Kind: "disk", Name: "media", Config: map[string]string{"readonly": "true"}},
			{Kind: "disk", Name: "scratch", Config: map[string]string{"ephemeral": "true", "size_limit": "1Gi"}},
		},
		Edges: []analyzer.Edge{
			{From: "roundtrip.Home", To: "media", ToKind: "disk", Kind: "resource", Verb: "read"},
			{From: "roundtrip.Transcode", To: "media", ToKind: "disk", Kind: "resource", Verb: "read"},
			{From: "roundtrip.Transcode", To: "scratch", ToKind: "disk", Kind: "resource", Verb: "write"},
		},
	}

	cfg := provision.Config{
		Project:       "proj-1",
		ProjectNumber: "999",
		Region:        "us-central1",
		App:           "roundtrip",
		Env:           "prod",
	}
	objs, err := provision.Desired(g, cfg, map[string]string{
		"web":       "us-central1-docker.pkg.dev/proj-1/tako/web@sha256:111",
		"worker":    "us-central1-docker.pkg.dev/proj-1/tako/worker@sha256:222",
		"transcode": "us-central1-docker.pkg.dev/proj-1/tako/transcode@sha256:333",
	})
	if err != nil {
		t.Fatalf("Desired: %v", err)
	}

	client := &Client{Project: cfg.Project, Region: cfg.Region}
	rs := &RunServices{C: client, Labels: cfg.Labels()}
	rj := &RunJobs{C: client, Labels: cfg.Labels()}
	sj := &SchedulerJobs{C: client, App: cfg.AppID(), Env: cfg.Env}
	tq := &TasksQueues{C: client}
	dm := &DomainMappings{C: client, Labels: cfg.Labels()}

	seenKinds := map[provision.Kind]int{}
	for _, o := range objs {
		want := provision.SpecHash(o.Spec)
		switch o.Kind {
		case provision.KindRunService:
			seenKinds[o.Kind]++
			raw, err := json.Marshal(rs.body(o))
			if err != nil {
				t.Fatal(err)
			}
			var live runService
			if err := json.Unmarshal(raw, &live); err != nil {
				t.Fatal(err)
			}
			pub, _ := o.Spec["public"].(bool)
			if got := liveHash(live, pub); got != want {
				t.Errorf("RunService %s: liveHash = %s, want SpecHash = %s", o.Name, got, want)
			}

		case provision.KindRunJob:
			seenKinds[o.Kind]++
			raw, err := json.Marshal(rj.body(o))
			if err != nil {
				t.Fatal(err)
			}
			var live runJob
			if err := json.Unmarshal(raw, &live); err != nil {
				t.Fatal(err)
			}
			if got := jobLiveHash(live); got != want {
				t.Errorf("RunJob %s: jobLiveHash = %s, want SpecHash = %s (spec=%v)", o.Name, got, want, o.Spec)
			}

		case provision.KindSchedulerJob:
			seenKinds[o.Kind]++
			raw, err := json.Marshal(sj.body(o))
			if err != nil {
				t.Fatal(err)
			}
			var live schedulerJob
			if err := json.Unmarshal(raw, &live); err != nil {
				t.Fatal(err)
			}
			got := provision.SpecHash(map[string]any{
				"schedule":       live.Schedule,
				"uri":            live.HTTPTarget.URI,
				"serviceAccount": live.HTTPTarget.OIDCToken.ServiceAccountEmail,
				"audience":       live.HTTPTarget.OIDCToken.Audience,
				"timeZone":       live.TimeZone,
				"deadline":       secondsOf(live.AttemptDeadline),
			})
			if got != want {
				t.Errorf("SchedulerJob %s: liveHash = %s, want SpecHash = %s", o.Name, got, want)
			}

		case provision.KindTasksQueue:
			seenKinds[o.Kind]++
			raw, err := json.Marshal(tq.body(o))
			if err != nil {
				t.Fatal(err)
			}
			var live tasksQueue
			if err := json.Unmarshal(raw, &live); err != nil {
				t.Fatal(err)
			}
			got := provision.SpecHash(map[string]any{
				"rate":     int(live.RateLimits.MaxDispatchesPerSecond),
				"attempts": live.RetryConfig.MaxAttempts,
			})
			if got != want {
				t.Errorf("TasksQueue %s: liveHash = %s, want SpecHash = %s", o.Name, got, want)
			}

		case provision.KindDomainMapping:
			seenKinds[o.Kind]++
			raw, err := json.Marshal(dm.body(o))
			if err != nil {
				t.Fatal(err)
			}
			var live domainMapping
			if err := json.Unmarshal(raw, &live); err != nil {
				t.Fatal(err)
			}
			got := provision.SpecHash(map[string]any{
				"routeName":       live.Spec.RouteName,
				"certificateMode": live.Spec.CertificateMode,
			})
			if got != want {
				t.Errorf("DomainMapping %s: liveHash = %s, want SpecHash = %s", o.Name, got, want)
			}
		}
	}

	for _, kind := range []provision.Kind{
		provision.KindRunService,
		provision.KindRunJob,
		provision.KindSchedulerJob,
		provision.KindTasksQueue,
		provision.KindDomainMapping,
	} {
		if seenKinds[kind] == 0 {
			t.Errorf("expected at least one object of kind %q in round-trip test", kind)
		}
	}
}
