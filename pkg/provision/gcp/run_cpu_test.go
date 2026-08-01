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
	if analyzer.ProfileKnobs["latency"].CPUAlwaysOn != analyzer.ToggleOn {
		t.Error("latency must keep CPU allocated — it is half of what a warm instance buys")
	}
	// A singleton's loop runs BETWEEN requests, so it needs CPU too.
	if analyzer.ProfileKnobs["singleton"].CPUAlwaysOn != analyzer.ToggleOn {
		t.Error("a singleton loop runs between requests and needs CPU allocated")
	}
	for _, p := range []string{"default", "batch"} {
		if analyzer.ProfileKnobs[p].CPUAlwaysOn != analyzer.ToggleOff {
			t.Errorf("%s should take Cloud Run's throttled default", p)
		}
	}
}

// The other half of the same promise: a warm floor cannot prevent every
// cold start, so Latency boosts the ones it gets. Leaving this unset was
// the same shape of bug as cpuIdle — a profile named for tail latency
// declining the knob that moves it.
func TestLatencyBoostsColdStart(t *testing.T) {
	if analyzer.ProfileKnobs["latency"].StartupBoost != analyzer.ToggleOn {
		t.Error("latency must boost startup CPU — a warm floor does not cover a scale-out")
	}
	for _, p := range []string{"default", "batch", "singleton"} {
		if analyzer.ProfileKnobs[p].StartupBoost != analyzer.ToggleOff {
			t.Errorf("%s should not pay for a startup boost it did not ask for", p)
		}
	}
}

// Every profile divides Cloud Run's fixed shutdown window, and none may
// claim more of it than exists.
func TestProfilesDrainInsideTheWindow(t *testing.T) {
	for name, k := range analyzer.ProfileKnobs {
		if k.DrainSeconds <= 0 {
			t.Errorf("%s has no drain budget — in-flight requests would be cut off at SIGTERM", name)
		}
		if k.DrainSeconds > analyzer.MaxDrainSeconds {
			t.Errorf("%s drains %ds of a %ds window — the kill lands first",
				name, k.DrainSeconds, analyzer.ShutdownWindowSeconds)
		}
	}
}

func TestRunServiceEmitsCPUIdle(t *testing.T) {
	r := &RunServices{C: &Client{Project: "p", Region: "us-central1"}}
	for _, tc := range []struct {
		name        string
		alwaysOn    bool
		boost       bool
		affinity    bool
		http2       bool
		wantCPUIdle bool
	}{
		{"latency", true, true, false, false, false},
		{"default", false, false, false, false, true},
		// Affinity and h2c are nobody's profile default, so the only way
		// either is ever true is an explicit footprint — exactly the case
		// a forgotten liveHash field would break.
		{"sticky", false, false, true, false, true},
		{"h2c", false, false, false, true, true},
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
					"startupBoost": tc.boost, "affinity": tc.affinity,
					"http2": tc.http2, "timeout": 300,
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
		if b := live.Template.Containers[0].Resources.StartupCPUBoost; b != tc.boost {
			t.Errorf("%s: startupCpuBoost = %v, want %v", tc.name, b, tc.boost)
		}
		if a := live.Template.SessionAffinity; a != tc.affinity {
			t.Errorf("%s: sessionAffinity = %v, want %v", tc.name, a, tc.affinity)
		}
		// The port NAME is the whole protocol setting: "h2c" or "http1".
		wantPort := "http1"
		if tc.http2 {
			wantPort = "h2c"
		}
		ports := live.Template.Containers[0].Ports
		if len(ports) != 1 || ports[0].Name != wantPort {
			t.Errorf("%s: ports = %+v, want one named %q", tc.name, ports, wantPort)
		}
		// The drift trap: liveHash re-derives the spec from the response,
		// so a field emitted but not read back reports drift forever.
		if h := liveHash(live, true); h != provision.SpecHash(obj.Spec) {
			t.Errorf("%s: liveHash %s != SpecHash %s — every deploy would report drift",
				tc.name, h, provision.SpecHash(obj.Spec))
		}
	}
}
