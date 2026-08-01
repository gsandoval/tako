package gcp

import (
	"encoding/json"
	"testing"

	"github.com/gsandoval/tako/pkg/analyzer"
	"github.com/gsandoval/tako/pkg/provision"
)

// V0: tako.Latency's doc comment promised "CPU always on" while cpuIdle
// was never set anywhere, so a Latency service was throttled exactly
// like Default. These pin the promise to the wire.
func TestLatencyAllocatesCPUBetweenRequests(t *testing.T) {
	if !analyzer.CPUAlwaysOn("latency") {
		t.Error("latency must keep CPU allocated — it is half of what a warm instance buys")
	}
	for _, p := range []string{"default", "batch", "singleton"} {
		if analyzer.CPUAlwaysOn(p) {
			t.Errorf("%s should take Cloud Run's throttled default", p)
		}
	}
}

func TestRunServiceEmitsCPUIdle(t *testing.T) {
	r := &RunServices{C: &Client{Project: "p", Region: "us-central1"}}
	for _, tc := range []struct {
		name        string
		alwaysOn    bool
		wantCPUIdle bool
	}{
		{"latency", true, false},
		{"default", false, true},
	} {
		obj := provision.Object{
			Kind: provision.KindRunService,
			Name: "svc",
			Spec: map[string]any{
				"image":          "img@sha256:aaa",
				"serviceAccount": "sa@p.iam.gserviceaccount.com",
				"env":            map[string]any{},
				"public":         true,
				"scaling": map[string]any{
					"min": 1, "max": 10, "concurrency": 40,
					"cpu": "2", "memory": "1Gi", "cpuAlwaysOn": tc.alwaysOn,
				},
			},
		}
		raw, err := json.Marshal(r.body(obj))
		if err != nil {
			t.Fatal(err)
		}
		var live runService
		if err := json.Unmarshal(raw, &live); err != nil {
			t.Fatal(err)
		}
		got := live.Template.Containers[0].Resources.CPUIdle
		if got != tc.wantCPUIdle {
			t.Errorf("%s: cpuIdle = %v, want %v", tc.name, got, tc.wantCPUIdle)
		}
		// The drift trap: liveHash re-derives the spec from the response,
		// so a field emitted but not read back reports drift forever.
		if h := liveHash(live, true); h != provision.SpecHash(obj.Spec) {
			t.Errorf("%s: liveHash %s != SpecHash %s — every deploy would report drift",
				tc.name, h, provision.SpecHash(obj.Spec))
		}
	}
}
