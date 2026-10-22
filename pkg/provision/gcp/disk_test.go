package gcp

import (
	"encoding/json"
	"testing"

	"github.com/gsandoval/tako/pkg/provision"
)

func TestRunServiceAndJobMountsLiveHashRoundTrip(t *testing.T) {
	mounts := []any{
		map[string]any{
			"name":      "media",
			"mountPath": "/mnt/tako/media",
			"ephemeral": false,
			"bucket":    "proj-app-disk-media",
			"readOnly":  true,
		},
		map[string]any{
			"name":      "scratch",
			"mountPath": "/mnt/tako/scratch",
			"ephemeral": true,
			"sizeLimit": "1Gi",
		},
	}

	svcObj := provision.Object{
		Kind: provision.KindRunService,
		Name: "api",
		Spec: map[string]any{
			"image":          "us-central1-docker.pkg.dev/p/tako/api@sha256:123",
			"serviceAccount": "api-sa@p.iam.gserviceaccount.com",
			"env":            map[string]any{"TAKO_BACKEND": "gcp"},
			"scaling": map[string]any{
				"min":          0,
				"max":          10,
				"concurrency":  80,
				"cpu":          "1",
				"memory":       "512Mi",
				"cpuAlwaysOn":  false,
				"startupBoost": false,
				"affinity":     false,
				"http2":        false,
				"timeout":      300,
			},
			"public": true,
			"mounts": mounts,
		},
	}

	rs := &RunServices{Labels: map[string]string{"tako-app": "app"}}
	rawSvc, err := json.Marshal(rs.body(svcObj))
	if err != nil {
		t.Fatal(err)
	}
	var liveSvc runService
	if err := json.Unmarshal(rawSvc, &liveSvc); err != nil {
		t.Fatal(err)
	}
	if got, want := liveHash(liveSvc, true), provision.SpecHash(svcObj.Spec); got != want {
		t.Errorf("service liveHash = %s, want SpecHash = %s", got, want)
	}

	jobObj := provision.Object{
		Kind: provision.KindRunJob,
		Name: "transcode",
		Spec: map[string]any{
			"image":          "us-central1-docker.pkg.dev/p/tako/transcode@sha256:456",
			"serviceAccount": "transcode-sa@p.iam.gserviceaccount.com",
			"env":            map[string]any{"TAKO_BACKEND": "gcp"},
			"taskCount":      2,
			"cpu":            "2",
			"memory":         "2Gi",
			"mounts":         mounts,
		},
	}

	rj := &RunJobs{Labels: map[string]string{"tako-app": "app"}}
	rawJob, err := json.Marshal(rj.body(jobObj))
	if err != nil {
		t.Fatal(err)
	}
	var liveJob runJob
	if err := json.Unmarshal(rawJob, &liveJob); err != nil {
		t.Fatal(err)
	}
	if got, want := jobLiveHash(liveJob), provision.SpecHash(jobObj.Spec); got != want {
		t.Errorf("job jobLiveHash = %s, want SpecHash = %s", got, want)
	}
}
