package analyzer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRegistryCompleteness(t *testing.T) {
	wantHandlers := []string{"API", "Page", "Worker", "Subscribe", "Cron", "Runner", "Singleton"}
	for _, ctor := range wantHandlers {
		spec, ok := Registry[ctor]
		if !ok {
			t.Errorf("Registry missing handler %q", ctor)
			continue
		}
		if !spec.IsHandler || !spec.RejectLocalDecl {
			t.Errorf("Registry[%q] = %+v, want IsHandler=true and RejectLocalDecl=true", ctor, spec)
		}
	}

	wantDecls := []string{
		"Entity", "Bucket", "Queue", "Topic", "Cache", "Secret",
		"Counter", "Histogram", "Site", "Service", "Auth", "Policy",
		"RequireRole", "Role", "StatusPage", "Job", "Lock", "Alert",
		"AlertEmail", "AlertTopic", "AlertWebhook", "Disk",
	}
	for _, ctor := range wantDecls {
		spec, ok := Registry[ctor]
		if !ok {
			t.Errorf("Registry missing declaration %q", ctor)
			continue
		}
		if spec.IsHandler || !spec.RejectLocalDecl {
			t.Errorf("Registry[%q] = %+v, want IsHandler=false and RejectLocalDecl=true", ctor, spec)
		}
	}

	for _, kind := range []string{"bucket", "queue", "topic", "disk"} {
		if !ByKind[kind].PhysicalName {
			t.Errorf("ByKind[%q].PhysicalName = false, want true", kind)
		}
	}
	for _, kind := range []string{"entity", "cache", "secret", "counter", "histogram", "lock"} {
		if ByKind[kind].PhysicalName {
			t.Errorf("ByKind[%q].PhysicalName = true, want false", kind)
		}
	}

	if got := verbOf("disk", "ReadFile"); got != "read" {
		t.Errorf("verbOf(disk, ReadFile) = %q, want read", got)
	}
	if got := verbOf("disk", "WriteFile"); got != "write" {
		t.Errorf("verbOf(disk, WriteFile) = %q, want write", got)
	}
	if got := verbOf("job", "Run"); got != "run" {
		t.Errorf("verbOf(job, Run) = %q, want run", got)
	}
	if got := verbOf("secret", "Value"); got != "read" {
		t.Errorf("verbOf(secret, Value) = %q, want read", got)
	}
}

func TestCfireSingletonProfileAndDiskOpts(t *testing.T) {
	tmpDir := t.TempDir()
	cfCode := `package diskcf
import "core:tako"

Scratch := tako.disk("scratch", tako.DiskOpts{ ephemeral: true, size_limit: "1Gi" })
Weights := tako.disk("weights", tako.DiskOpts{ read_only: true })
Shared  := tako.disk("shared")
Loop    := tako.singleton(run_loop)

fn run_loop(ctx: tako.Ctx) -> void! {
}
`
	if err := os.WriteFile(filepath.Join(tmpDir, "app.cf"), []byte(cfCode), 0o644); err != nil {
		t.Fatal(err)
	}

	g, err := Analyze(Config{Dir: tmpDir})
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	var foundSingleton bool
	for _, svc := range g.Services {
		if svc.Singleton {
			foundSingleton = true
			if svc.Profile != "singleton" {
				t.Errorf("singleton service profile = %q, want \"singleton\"", svc.Profile)
			}
		}
	}
	if !foundSingleton {
		t.Errorf("missing singleton service in %+v", g.Services)
	}

	disks := map[string]Resource{}
	for _, r := range g.Resources {
		if r.Kind == "disk" {
			disks[r.Name] = r
		}
	}
	if len(disks) != 3 {
		t.Fatalf("got %d disks, want 3", len(disks))
	}
	if disks["scratch"].Config["ephemeral"] != "true" || disks["scratch"].Config["size_limit"] != "1Gi" {
		t.Errorf("scratch config = %v", disks["scratch"].Config)
	}
	if disks["weights"].Config["readonly"] != "true" {
		t.Errorf("weights config = %v", disks["weights"].Config)
	}
	if disks["shared"].Config != nil {
		t.Errorf("shared config = %v, want nil", disks["shared"].Config)
	}
}

func TestResourceLineDiskFormatting(t *testing.T) {
	cases := []struct {
		r    Resource
		want string
	}{
		{
			r:    Resource{Kind: "disk", Name: "shared"},
			want: "disk shared",
		},
		{
			r:    Resource{Kind: "disk", Name: "weights", Config: map[string]string{"readonly": "true"}},
			want: "disk weights (readonly)",
		},
		{
			r:    Resource{Kind: "disk", Name: "scratch", Config: map[string]string{"ephemeral": "true", "size_limit": "1Gi"}},
			want: "disk scratch (ephemeral, size_limit 1Gi)",
		},
	}
	for _, tc := range cases {
		if got := ResourceLine(tc.r); got != tc.want {
			t.Errorf("ResourceLine(%+v) = %q, want %q", tc.r, got, tc.want)
		}
	}
}
