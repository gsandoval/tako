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
	want := filepath.Join(".tako", "gen", service, "main.go")
	if len(written) != 1 || written[0] != want {
		t.Fatalf("written = %v, want [%s]", written, want)
	}
	got, err := os.ReadFile(filepath.Join(outDir, written[0]))
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
