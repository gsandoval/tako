package build

import (
	"archive/tar"
	"bytes"
	"debug/elf"
	"io"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"

	"github.com/gsandoval/tako/pkg/analyzer"
	"github.com/gsandoval/tako/pkg/codegen"
)

// testEnv boots an in-memory registry holding a one-config base image
// (User nonroot, like distroless), and generates hello's service main.
type testEnv struct {
	registryHost string
	base         string // base ref on the local registry
	dir          string // examples/hello absolute
	service      string
}

func setup(t *testing.T) testEnv {
	t.Helper()
	srv := httptest.NewServer(registry.New())
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}

	base, err := mutate.ConfigFile(empty.Image, &v1.ConfigFile{
		Architecture: "amd64",
		OS:           "linux",
		Config:       v1.Config{User: "nonroot"},
	})
	if err != nil {
		t.Fatal(err)
	}
	baseRef := u.Host + "/distroless-stand-in:nonroot"
	ref, err := name.ParseReference(baseRef)
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.Write(ref, base); err != nil {
		t.Fatal(err)
	}

	dir := repoPath(t, "examples/hello")
	graph, err := analyzer.Analyze(analyzer.Config{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := codegen.Generate(graph, dir); err != nil {
		t.Fatal(err)
	}
	if len(graph.Services) != 1 {
		t.Fatalf("hello services = %d", len(graph.Services))
	}
	return testEnv{registryHost: u.Host, base: baseRef, dir: dir, service: graph.Services[0].Name}
}

func repoPath(t *testing.T, rel string) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", rel)
}

func TestBuildPushAndPullBack(t *testing.T) {
	env := setup(t)
	res, err := Service(Options{Dir: env.dir, App: "hello", Base: env.base, Repo: env.registryHost + "/tako"}, env.service)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Pushed || res.Ref != env.registryHost+"/tako/"+env.service+"@"+res.Digest {
		t.Fatalf("result = %+v", res)
	}
	// The base was pinned by digest, not tag.
	if want := env.registryHost + "/distroless-stand-in@sha256:"; len(res.Base) == 0 || res.Base[:len(want)] != want {
		t.Fatalf("base = %q", res.Base)
	}

	ref, err := name.ParseReference(res.Ref)
	if err != nil {
		t.Fatal(err)
	}
	img, err := remote.Image(ref)
	if err != nil {
		t.Fatal(err)
	}
	pulled, err := img.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if pulled.String() != res.Digest {
		t.Fatalf("pulled digest %s, pushed %s", pulled, res.Digest)
	}

	// Exactly base + one appended layer.
	layers, err := img.Layers()
	if err != nil {
		t.Fatal(err)
	}
	if len(layers) != 1 { // test base is empty.Image: zero layers + ours
		t.Fatalf("layers = %d, want 1", len(layers))
	}

	cfg, err := img.ConfigFile()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Config.Entrypoint; len(got) != 1 || got[0] != "/app/"+env.service {
		t.Fatalf("entrypoint = %v", got)
	}
	if cfg.Config.User != "nonroot" {
		t.Fatalf("user = %q (must inherit the base's nonroot)", cfg.Config.User)
	}
	if cfg.Config.Labels["tako.app"] != "hello" || cfg.Config.Labels["tako.service"] != env.service {
		t.Fatalf("labels = %v", cfg.Config.Labels)
	}
	if !cfg.Created.IsZero() && cfg.Created.Unix() != 0 {
		t.Fatalf("created = %v (must be the fixed epoch)", cfg.Created)
	}
}

func TestImageDigestIsDeterministic(t *testing.T) {
	env := setup(t)
	opts := Options{Dir: env.dir, App: "hello", Base: env.base}
	first, err := Service(opts, env.service)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Service(opts, env.service)
	if err != nil {
		t.Fatal(err)
	}
	if first.Digest != second.Digest {
		t.Fatalf("digests differ across identical builds: %s vs %s", first.Digest, second.Digest)
	}
}

func TestLayerCarriesStaticBinary(t *testing.T) {
	env := setup(t)
	res, err := Service(Options{Dir: env.dir, App: "hello", Base: env.base, Repo: env.registryHost + "/tako"}, env.service)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := name.ParseReference(res.Ref)
	if err != nil {
		t.Fatal(err)
	}
	img, err := remote.Image(ref)
	if err != nil {
		t.Fatal(err)
	}
	layers, err := img.Layers()
	if err != nil {
		t.Fatal(err)
	}
	rc, err := layers[len(layers)-1].Uncompressed()
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()

	var binary []byte
	tr := tar.NewReader(rc)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if hdr.Name == "app/"+env.service {
			if hdr.Mode&0o111 == 0 {
				t.Fatalf("binary mode = %o, not executable", hdr.Mode)
			}
			if hdr.ModTime.Unix() != 0 {
				t.Fatalf("binary mtime = %v (must be the fixed epoch)", hdr.ModTime)
			}
			binary, err = io.ReadAll(tr)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(binary) == 0 {
		t.Fatalf("layer does not contain app/%s", env.service)
	}

	// Statically linked: a pure-Go CGO_ENABLED=0 binary has no ELF
	// interpreter — that is what lets it run on distroless/static.
	f, err := elf.NewFile(bytes.NewReader(binary))
	if err != nil {
		t.Fatalf("binary is not ELF: %v", err)
	}
	defer f.Close()
	if f.Machine != elf.EM_X86_64 {
		t.Fatalf("machine = %v, want x86-64", f.Machine)
	}
	for _, p := range f.Progs {
		if p.Type == elf.PT_INTERP {
			t.Fatal("binary requests an ELF interpreter — not static")
		}
	}
}

// TestPushErrorTaxonomy pins the three registry failures to their
// remediation texts — 401 (unauthenticated), 403 (unauthorized), and
// 404 (repo missing) are different problems with different fixes.
func TestPushErrorTaxonomy(t *testing.T) {
	const repo = "us-central1-docker.pkg.dev/my-proj/tako"
	cases := []struct {
		status int
		wants  []string
	}{
		{401, []string{"cannot authenticate", "gcloud auth login"}},
		{403, []string{"authenticated but lack write access", "add-iam-policy-binding tako --project my-proj --location us-central1", "roles/artifactregistry.writer"}},
		{404, []string{"does not exist", "repositories create tako --project my-proj --location us-central1 --repository-format docker"}},
	}
	for _, c := range cases {
		err := classifyPushError(&transport.Error{StatusCode: c.status}, repo, "hello")
		if err == nil {
			t.Fatalf("status %d: nil error", c.status)
		}
		for _, want := range c.wants {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("status %d error %q\n  missing %q", c.status, err, want)
			}
		}
	}
}
