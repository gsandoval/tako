package clientgen

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/gsandoval/tako/pkg/analyzer"
)

var update = flag.Bool("update", false, "rewrite golden files")

// TestShortlinkClient goldens the client of examples/shortlink: body,
// path, and query bindings, no exclusions. The output is product UX —
// users read this file — so it is under test byte for byte.
func TestShortlinkClient(t *testing.T) {
	content, sum, err := generate(t, repoPath(t, "examples/shortlink"))
	if err != nil {
		t.Fatalf("Generate(shortlink): %v", err)
	}
	if sum.Endpoints != 3 || len(sum.Excluded) != 0 {
		t.Fatalf("summary: %+v", sum)
	}
	compareGolden(t, "testdata/shortlink_api.golden.ts", content)
}

// TestGalleryClient goldens the exclusion story: the multipart upload
// is excluded WITH a visible reason; the blob download returns the raw
// Response.
func TestGalleryClient(t *testing.T) {
	content, sum, err := generate(t, repoPath(t, "examples/gallery"))
	if err != nil {
		t.Fatalf("Generate(gallery): %v", err)
	}
	if len(sum.Excluded) == 0 {
		t.Fatalf("gallery's multipart upload should be excluded; summary: %+v", sum)
	}
	compareGolden(t, "testdata/gallery_api.golden.ts", content)
}

// TestDeterministic pins the byte-identical-regeneration promise.
func TestDeterministic(t *testing.T) {
	a, _, err := generate(t, repoPath(t, "examples/shortlink"))
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := generate(t, repoPath(t, "examples/shortlink"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("two generations of an unchanged app differ")
	}
}

// TestErrors goldens the generation diagnostics — their text is product
// UX, formatted path:line:col: message (tako/rule) like every analyzer
// error.
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
			// Fixture modules sit outside the go.work workspace and
			// reach the SDK via a replace directive.
			dir := filepath.Join("testdata/errors", entry.Name())
			_, _, err := generate(t, dir, "GOWORK=off")
			if err == nil {
				t.Fatalf("Generate(%s) unexpectedly succeeded", dir)
			}
			if _, ok := err.(analyzer.ErrorList); !ok {
				t.Fatalf("Generate(%s) failed to load rather than generate:\n%v", dir, err)
			}
			compareGolden(t, filepath.Join(dir, "expect.golden.txt"), []byte(err.Error()+"\n"))
		})
	}
}

func generate(t *testing.T, dir string, env ...string) ([]byte, Summary, error) {
	t.Helper()
	tg, err := analyzer.AnalyzeTyped(analyzer.Config{Dir: dir, Env: env})
	if err != nil {
		t.Fatalf("AnalyzeTyped(%s): %v", dir, err)
	}
	return Generate(tg)
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
