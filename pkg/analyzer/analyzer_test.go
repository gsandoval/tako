package analyzer

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files")

// TestHelloGraph goldens the AppGraph of examples/hello — the walking
// skeleton's acceptance graph.
func TestHelloGraph(t *testing.T) {
	g, err := Analyze(Config{Dir: repoPath(t, "examples/hello")})
	if err != nil {
		t.Fatalf("Analyze(examples/hello): %v", err)
	}
	got, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	compareGolden(t, "testdata/hello_appgraph.golden.json", got)
}

// TestHelloSummary goldens the human summary — it is CLI output, which is
// product UX.
func TestHelloSummary(t *testing.T) {
	g, err := Analyze(Config{Dir: repoPath(t, "examples/hello")})
	if err != nil {
		t.Fatalf("Analyze(examples/hello): %v", err)
	}
	compareGolden(t, "testdata/hello_summary.golden.txt", []byte(Summary(g)))
}

// TestShortlinkGraph goldens the AppGraph of examples/shortlink — the
// first graph with resources and derived indexes.
func TestShortlinkGraph(t *testing.T) {
	g, err := Analyze(Config{Dir: repoPath(t, "examples/shortlink")})
	if err != nil {
		t.Fatalf("Analyze(examples/shortlink): %v", err)
	}
	got, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	compareGolden(t, "testdata/shortlink_appgraph.golden.json", got)
}

// TestShortlinkSummary goldens the summary with a resource line.
func TestShortlinkSummary(t *testing.T) {
	g, err := Analyze(Config{Dir: repoPath(t, "examples/shortlink")})
	if err != nil {
		t.Fatalf("Analyze(examples/shortlink): %v", err)
	}
	compareGolden(t, "testdata/shortlink_summary.golden.txt", []byte(Summary(g)))
}

// TestErrors runs each testdata/errors/* fixture module and asserts the
// exact analysis-error text — error messages are the product's primary UX
// and are under test, byte for byte.
func TestErrors(t *testing.T) {
	entries, err := os.ReadDir("testdata/errors")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		t.Run(entry.Name(), func(t *testing.T) {
			dir := filepath.Join("testdata/errors", entry.Name())
			// Fixture modules sit outside the go.work workspace and reach
			// the SDK via a replace directive.
			_, err := Analyze(Config{Dir: dir, Env: []string{"GOWORK=off"}})
			if err == nil {
				t.Fatalf("Analyze(%s) unexpectedly succeeded", dir)
			}
			if _, ok := err.(ErrorList); !ok {
				t.Fatalf("Analyze(%s) failed to load rather than analyze:\n%v", dir, err)
			}
			compareGolden(t, filepath.Join(dir, "expect.golden.txt"), []byte(err.Error()+"\n"))
		})
	}
}

func compareGolden(t *testing.T, path string, got []byte) {
	t.Helper()
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s (run with -update to create it): %v", path, err)
	}
	if string(got) != string(want) {
		t.Fatalf("%s differs.\n--- want\n%s--- got\n%s", path, want, got)
	}
}

func repoPath(t *testing.T, rel string) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("../..", rel))
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

// TestPayloadShapeIsCompilersJob proves a mis-shaped handler is a compile
// error, not an analysis error — the fixture must fail to load.
func TestPayloadShapeIsCompilersJob(t *testing.T) {
	_, err := Analyze(Config{Dir: "testdata/compile/payloadmismatch", Env: []string{"GOWORK=off"}})
	if err == nil {
		t.Fatal("expected the fixture to fail compilation")
	}
	if _, ok := err.(ErrorList); ok {
		t.Fatalf("expected a compile error, got analysis errors:\n%v", err)
	}
	if !strings.Contains(err.Error(), "greet") {
		t.Fatalf("compile error does not mention the handler:\n%v", err)
	}
}
