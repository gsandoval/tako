package codegen

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/gsandoval/tako/pkg/analyzer"
)

var update = flag.Bool("update", false, "rewrite golden files")

// Generated code is user-readable surface, so its exact text is under
// test for each example.

func TestHelloMain(t *testing.T) {
	goldenService(t, "../../examples/hello", "hello", "testdata/hello_main.golden")
}

// Shortlink is the first service with backend boot and body+path+query
// decoding.
func TestShortlinkMain(t *testing.T) {
	goldenService(t, "../../examples/shortlink", "shortlink", "testdata/shortlink_main.golden")
}

// Gallery is the first service with multipart decode and blob streaming.
func TestGalleryMain(t *testing.T) {
	goldenService(t, "../../examples/gallery", "gallery", "testdata/gallery_main.golden")
}

// The worker service's generated main: register + push endpoint.
func TestShortlinkWorkerMain(t *testing.T) {
	goldenService(t, "../../examples/shortlink", "shortlink-countclick", "testdata/shortlink_worker_main.golden")
}

func goldenService(t *testing.T, appDir, service, golden string) {
	t.Helper()
	abs, err := filepath.Abs(appDir)
	if err != nil {
		t.Fatal(err)
	}
	g, err := analyzer.Analyze(analyzer.Config{Dir: abs})
	if err != nil {
		t.Fatal(err)
	}

	outDir := t.TempDir()
	written, err := Generate(g, outDir)
	if err != nil {
		t.Fatal(err)
	}
	// Every service gets a main now (async ones serve the push endpoint);
	// this golden checks the named service's output.
	if len(written) != len(g.Services) {
		t.Fatalf("written = %v, want one main per service (%d)", written, len(g.Services))
	}
	want := filepath.Join(".tako", "gen", service, "main.go")
	found := false
	for _, rel := range written {
		if rel == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("written = %v, missing %s", written, want)
	}
	got, err := os.ReadFile(filepath.Join(outDir, want))
	if err != nil {
		t.Fatal(err)
	}

	if *update {
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	wantSrc, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("reading %s (run with -update to create it): %v", golden, err)
	}
	if string(got) != string(wantSrc) {
		t.Fatalf("%s differs.\n--- want\n%s--- got\n%s", golden, wantSrc, got)
	}
}
