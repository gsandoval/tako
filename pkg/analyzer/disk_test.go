package analyzer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTempModule(t *testing.T, appSrc string) string {
	t.Helper()
	dir := t.TempDir()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	gomod := "module example.com/diskapp\n\ngo 1.26\n\nrequire github.com/gsandoval/tako v0.0.0\n\nrequire (\n\tgo.etcd.io/bbolt v1.5.0 // indirect\n\tgolang.org/x/sys v0.47.0 // indirect\n)\n\nreplace github.com/gsandoval/tako => " + root + "\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatal(err)
	}
	gosum, err := os.ReadFile("testdata/compile/payloadmismatch/go.sum")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.sum"), gosum, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.go"), []byte(appSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestDiskExtractionAndEdges(t *testing.T) {
	dir := writeTempModule(t, `package diskapp

import "github.com/gsandoval/tako"

var Scratch = tako.Disk("scratch", tako.DiskConfig{
	Ephemeral: true,
	SizeLimit: "1Gi",
})

var Weights = tako.Disk("weights", tako.DiskConfig{
	ReadOnly: true,
})

var Shared = tako.Disk("shared")

type Req struct{}
type Res struct {
	Path string `+"`json:\"path\"`"+`
}

var Transcode = tako.API("POST /transcode", func(ctx tako.Ctx, req Req) (Res, error) {
	_, _ = Weights.ReadFile(ctx, "model.bin")
	_ = Scratch.WriteFile(ctx, "tmp.bin", []byte("ok"), 0o644)
	_ = Shared.MkdirAll(ctx, "out", 0o755)
	return Res{Path: Scratch.Path(ctx, "tmp.bin")}, nil
})
`)
	g, err := Analyze(Config{Dir: dir, Env: []string{"GOWORK=off"}})
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	disks := map[string]Resource{}
	for _, r := range g.Resources {
		if r.Kind == "disk" {
			disks[r.Name] = r
		}
	}
	if len(disks) != 3 {
		t.Fatalf("got %d disk resources, want 3", len(disks))
	}
	if disks["scratch"].Config["ephemeral"] != "true" || disks["scratch"].Config["size_limit"] != "1Gi" {
		t.Errorf("scratch config = %v", disks["scratch"].Config)
	}
	if disks["weights"].Config["readonly"] != "true" {
		t.Errorf("weights config = %v", disks["weights"].Config)
	}

	verbs := map[string]string{}
	for _, e := range g.Edges {
		if e.ToKind == "disk" {
			verbs[e.To+":"+e.Verb] = e.From
		}
	}
	for _, want := range []string{"weights:read", "scratch:write", "scratch:read", "shared:write"} {
		if verbs[want] == "" {
			t.Errorf("missing expected disk edge %q in %v", want, g.Edges)
		}
	}

	m := Mermaid(g)
	if !strings.Contains(m, `r_disk_scratch[("disk scratch")]`) {
		t.Errorf("Mermaid output missing disk cylinder node:\n%s", m)
	}
}

func TestDiskReadOnlyWriteRejected(t *testing.T) {
	dir := writeTempModule(t, `package diskapp

import "github.com/gsandoval/tako"

var Weights = tako.Disk("weights", tako.DiskConfig{
	ReadOnly: true,
})

type Req struct{}
type Res struct{}

var Mutate = tako.API("POST /mutate", func(ctx tako.Ctx, req Req) (Res, error) {
	_ = Weights.WriteFile(ctx, "illegal.bin", []byte("x"), 0o644)
	return Res{}, nil
})
`)
	_, err := Analyze(Config{Dir: dir, Env: []string{"GOWORK=off"}})
	if err == nil || !strings.Contains(err.Error(), `writes to disk "weights", which is declared with ReadOnly: true`) {
		t.Fatalf("expected ReadOnly write error, got: %v", err)
	}
}

func TestDiskSizeLimitWithoutEphemeralRejected(t *testing.T) {
	dir := writeTempModule(t, `package diskapp

import "github.com/gsandoval/tako"

var Bad = tako.Disk("bad", tako.DiskConfig{
	SizeLimit: "1Gi",
})
`)
	_, err := Analyze(Config{Dir: dir, Env: []string{"GOWORK=off"}})
	if err == nil || !strings.Contains(err.Error(), "sets SizeLimit without Ephemeral: true") {
		t.Fatalf("expected SizeLimit-without-Ephemeral error, got: %v", err)
	}
}
