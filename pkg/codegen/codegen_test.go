package codegen

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/gsandoval/tako/pkg/analyzer"
)

var update = flag.Bool("update", false, "rewrite golden files")

// TestHelloMain goldens the generated main for examples/hello — generated
// code is user-readable surface, so its exact text is under test.
func TestHelloMain(t *testing.T) {
	appDir, err := filepath.Abs("../../examples/hello")
	if err != nil {
		t.Fatal(err)
	}
	g, err := analyzer.Analyze(analyzer.Config{Dir: appDir})
	if err != nil {
		t.Fatal(err)
	}

	outDir := t.TempDir()
	written, err := Generate(g, outDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 1 || written[0] != filepath.Join(".tako", "gen", "hello", "main.go") {
		t.Fatalf("written = %v, want one .tako/gen/hello/main.go", written)
	}
	got, err := os.ReadFile(filepath.Join(outDir, written[0]))
	if err != nil {
		t.Fatal(err)
	}

	golden := "testdata/hello_main.golden"
	if *update {
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("reading %s (run with -update to create it): %v", golden, err)
	}
	if string(got) != string(want) {
		t.Fatalf("%s differs.\n--- want\n%s--- got\n%s", golden, want, got)
	}
}
