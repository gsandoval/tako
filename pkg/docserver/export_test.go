package docserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportGeneratesCompleteStaticSite(t *testing.T) {
	site := built(t)
	tmpDir := t.TempDir()

	if err := Export(site, tmpDir); err != nil {
		t.Fatalf("Export failed: %v", err)
	}

	for _, rel := range []string{
		"index.html",
		"404.html",
		".nojekyll",
		"api/index.html",
		"api.html",
		"guide/index.html",
		"guide/01-hello/index.html",
		"guide/01-hello.html",
		"guide/07-cfire/index.html",
		"guide/07-cfire.html",
		"reference/index.html",
		"reference/entity/index.html",
		"reference/entity.html",
		"reference/cfire/index.html",
		"reference/cfire.html",
	} {
		p := filepath.Join(tmpDir, rel)
		data, err := os.ReadFile(p)
		if err != nil {
			t.Errorf("expected %s to exist: %v", rel, err)
			continue
		}
		if rel != ".nojekyll" && len(data) == 0 {
			t.Errorf("%s was empty", rel)
		}
	}

	homeData, err := os.ReadFile(filepath.Join(tmpDir, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(homeData), "invisible infrastructure") {
		t.Errorf("index.html missing tagline 'invisible infrastructure'")
	}
}
