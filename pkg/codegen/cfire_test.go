package codegen

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gsandoval/tako/pkg/analyzer"
)

func TestCfireCodegen(t *testing.T) {
	tmpDir := t.TempDir()
	cfCode := `package testshortlink

import "core:tako"

type Link struct {
    code:     str ` + "`entity:\"id\" json:\"code\"`" + `
    url:      str ` + "`json:\"url\"`" + `
    clicks:   i64 ` + "`entity:\"index\" json:\"clicks\"`" + `
    owner_id: str ` + "`entity:\"index\" json:\"owner_id\"`" + `
}

type GetReq struct {
    code: str ` + "`path:\"code\"`" + `
}

Links := tako.entity[Link]("links")
GetLink := tako.api("GET /links/{code}", handle_get)

fn handle_get(ctx: tako.Ctx, req: GetReq) -> Link! {
    return try Links.get(ctx, req.code)
}
`
	if err := os.WriteFile(filepath.Join(tmpDir, "app.cf"), []byte(cfCode), 0o644); err != nil {
		t.Fatal(err)
	}

	graph, err := analyzer.Analyze(analyzer.Config{Dir: tmpDir})
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	written, err := Generate(graph, tmpDir)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	mains := Mains(written)
	if len(mains) != 1 {
		t.Fatalf("expected 1 main, got %d: %v", len(mains), mains)
	}

	mainPath := filepath.Join(tmpDir, mains[0])
	content, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatalf("reading main: %v", err)
	}
	s := string(content)
	if !stringsContains(s, "package main") || !stringsContains(s, "GET /links/{code}") {
		t.Fatalf("unexpected generated content:\n%s", s)
	}
}

func TestCfireCodegenExamples(t *testing.T) {
	examples := []string{
		"../../examples/cfire-hello",
		"../../examples/cfire-shortlink",
		"../../examples/cfire-gallery",
		"../../examples/cfire-journal",
		"../../examples/cfire-shop",
		"../../examples/cfire-blog",
		"../../examples/cfire-siren",
	}

	for _, ex := range examples {
		t.Run(filepath.Base(ex), func(t *testing.T) {
			graph, err := analyzer.Analyze(analyzer.Config{Dir: ex})
			if err != nil {
				t.Fatalf("Analyze failed for %s: %v", ex, err)
			}
			tmpDir := t.TempDir()
			written, err := GenerateCfire(graph, tmpDir)
			if err != nil {
				t.Fatalf("GenerateCfire failed for %s: %v", ex, err)
			}
			if len(written) == 0 {
				t.Fatalf("expected written files for %s", ex)
			}
			mains := Mains(written)
			if len(mains) == 0 {
				t.Fatalf("expected main files for %s", ex)
			}
			for _, f := range mains {
				full := filepath.Join(tmpDir, f)
				content, err := os.ReadFile(full)
				if err != nil {
					t.Fatalf("failed reading %s: %v", f, err)
				}
				if !stringsContains(string(content), "package main") {
					t.Fatalf("expected package main in %s", f)
				}
			}
		})
	}
}

func stringsContains(s, sub string) bool {
	return filepath.FromSlash(s) != "" && (len(s) >= len(sub) && (s == sub || len(sub) == 0 || (len(s) > 0 && len(sub) > 0 && stringContainsIndex(s, sub))))
}

func stringContainsIndex(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
