package provision

import (
	"reflect"
	"strings"
	"testing"

	"github.com/gsandoval/tako/pkg/analyzer"
)

func TestDesiredAndGrantsWithDisks(t *testing.T) {
	g := &analyzer.AppGraph{
		Module: "github.com/acme/video",
		Services: []analyzer.Service{
			{Name: "api", Package: "github.com/acme/video", Handlers: []string{"video.Serve"}, Profile: "default"},
			{Name: "transcode", Package: "github.com/acme/video", Handlers: []string{"video.Transcode"}, Profile: "batch", Job: true},
		},
		Handlers: []analyzer.Handler{
			{Kind: "api", Var: "video.Serve", Package: "github.com/acme/video", Service: "api", Route: "GET /videos"},
			{Kind: "job", Var: "video.Transcode", Package: "github.com/acme/video", Service: "transcode", Binding: "transcode"},
		},
		Resources: []analyzer.Resource{
			{Kind: "disk", Name: "media", Var: "video.Media", Package: "github.com/acme/video"},
			{Kind: "disk", Name: "weights", Var: "video.Weights", Package: "github.com/acme/video", Config: map[string]string{"readonly": "true"}},
			{Kind: "disk", Name: "scratch", Var: "video.Scratch", Package: "github.com/acme/video", Config: map[string]string{"ephemeral": "true", "size_limit": "2Gi"}},
		},
		Edges: []analyzer.Edge{
			{From: "video.Serve", To: "media", ToKind: "disk", Kind: "resource", Verb: "read"},
			{From: "video.Serve", To: "weights", ToKind: "disk", Kind: "resource", Verb: "read"},
			{From: "video.Transcode", To: "media", ToKind: "disk", Kind: "resource", Verb: "write"},
			{From: "video.Transcode", To: "scratch", ToKind: "disk", Kind: "resource", Verb: "write"},
		},
	}

	cfg := Config{
		Project:       "myproj",
		ProjectNumber: "12345",
		Region:        "us-central1",
		App:           "video",
		Env:           "prod",
	}
	if err := CheckNames(g, cfg); err != nil {
		t.Fatalf("CheckNames: %v", err)
	}

	objs, err := Desired(g, cfg, map[string]string{
		"api":       "img-api",
		"transcode": "img-transcode",
	})
	if err != nil {
		t.Fatalf("Desired: %v", err)
	}

	buckets := map[string]Object{}
	var apiSvc, transcodeJob *Object
	grants := map[string][]string{}
	for i := range objs {
		o := &objs[i]
		switch o.Kind {
		case KindBucket:
			buckets[o.Name] = *o
		case KindRunService:
			if o.Name == "api" {
				apiSvc = o
			}
		case KindRunJob:
			if o.Name == "transcode" {
				transcodeJob = o
			}
		case KindIamGrants:
			var b []string
			for _, item := range o.Spec["bindings"].([]any) {
				m := item.(map[string]any)
				b = append(b, m["role"].(string)+"|"+m["member"].(string))
			}
			grants[o.Name] = b
		}
	}

	// Persistent disks get buckets with cors: false; ephemeral scratch does not.
	if _, ok := buckets["myproj-video-disk-media"]; !ok {
		t.Fatalf("missing bucket myproj-video-disk-media; got %v", buckets)
	}
	if buckets["myproj-video-disk-media"].Spec["cors"] != false {
		t.Errorf("disk bucket cors = %v, want false", buckets["myproj-video-disk-media"].Spec["cors"])
	}
	if _, ok := buckets["myproj-video-disk-weights"]; !ok {
		t.Fatalf("missing bucket myproj-video-disk-weights")
	}
	if _, ok := buckets["myproj-video-disk-scratch"]; ok {
		t.Fatalf("ephemeral disk scratch must not provision a GCS bucket")
	}

	// API service mounts media (readOnly: false) and weights (readOnly: true).
	wantAPIMounts := []any{
		map[string]any{
			"name":      "media",
			"mountPath": "/mnt/tako/media",
			"ephemeral": false,
			"bucket":    "myproj-video-disk-media",
			"readOnly":  false,
		},
		map[string]any{
			"name":      "weights",
			"mountPath": "/mnt/tako/weights",
			"ephemeral": false,
			"bucket":    "myproj-video-disk-weights",
			"readOnly":  true,
		},
	}
	if !reflect.DeepEqual(apiSvc.Spec["mounts"], wantAPIMounts) {
		t.Errorf("api mounts = %#v\nwant %#v", apiSvc.Spec["mounts"], wantAPIMounts)
	}

	// Transcode job mounts media (persistent) and scratch (ephemeral 2Gi).
	wantJobMounts := []any{
		map[string]any{
			"name":      "media",
			"mountPath": "/mnt/tako/media",
			"ephemeral": false,
			"bucket":    "myproj-video-disk-media",
			"readOnly":  false,
		},
		map[string]any{
			"name":      "scratch",
			"mountPath": "/mnt/tako/scratch",
			"ephemeral": true,
			"sizeLimit": "2Gi",
		},
	}
	if !reflect.DeepEqual(transcodeJob.Spec["mounts"], wantJobMounts) {
		t.Errorf("transcode job mounts = %#v\nwant %#v", transcodeJob.Spec["mounts"], wantJobMounts)
	}

	// Grants: api SA gets objectViewer on media & weights; transcode SA gets objectAdmin on media.
	mediaGrants := strings.Join(grants["bucket myproj-video-disk-media"], ",")
	if !strings.Contains(mediaGrants, "roles/storage.objectViewer|serviceAccount:"+cfg.SAEmail("api")) {
		t.Errorf("media grants missing objectViewer for api: %s", mediaGrants)
	}
	if !strings.Contains(mediaGrants, "roles/storage.objectAdmin|serviceAccount:"+cfg.SAEmail("transcode")) {
		t.Errorf("media grants missing objectAdmin for transcode: %s", mediaGrants)
	}
}
