package docserver

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
)

// Export writes the entire documentation website as static HTML files to outDir,
// formatted for static web hosting platforms like GitHub Pages.
func Export(site *Site, outDir string) error {
	handler := Handler(site)

	fetchAndSave := func(route, relPath string) error {
		req := httptest.NewRequest(http.MethodGet, route, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK && rec.Code != http.StatusNotFound {
			return fmt.Errorf("rendering %s failed with status %d", route, rec.Code)
		}

		fullPath := filepath.Join(outDir, relPath)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			return err
		}
		return os.WriteFile(fullPath, rec.Body.Bytes(), 0644)
	}

	// 1. Root index and 404 page
	if err := fetchAndSave("/", "index.html"); err != nil {
		return err
	}
	if err := fetchAndSave("/404-not-found", "404.html"); err != nil {
		return err
	}

	// 2. API page (both /api/index.html and /api.html for server compatibility)
	if err := fetchAndSave("/api", "api/index.html"); err != nil {
		return err
	}
	if err := fetchAndSave("/api", "api.html"); err != nil {
		return err
	}

	// 3. Guide and Reference section indexes
	for _, section := range []string{SectionGuide, SectionReference} {
		if err := fetchAndSave("/"+section+"/", section+"/index.html"); err != nil {
			return err
		}
	}

	// 4. Every guide page
	for _, p := range site.Guide {
		routePath := strings.Trim(strings.TrimPrefix(p.Route, "/"), "/")
		if routePath == SectionGuide {
			continue
		}
		if err := fetchAndSave(p.Route, routePath+"/index.html"); err != nil {
			return err
		}
		if err := fetchAndSave(p.Route, routePath+".html"); err != nil {
			return err
		}
	}

	// 5. Every reference page
	for _, p := range site.Reference {
		routePath := strings.Trim(strings.TrimPrefix(p.Route, "/"), "/")
		if routePath == SectionReference {
			continue
		}
		if err := fetchAndSave(p.Route, routePath+"/index.html"); err != nil {
			return err
		}
		if err := fetchAndSave(p.Route, routePath+".html"); err != nil {
			return err
		}
	}

	// 6. .nojekyll to prevent GitHub Pages from ignoring underscore folders or files
	if err := os.WriteFile(filepath.Join(outDir, ".nojekyll"), []byte{}, 0644); err != nil {
		return err
	}

	return nil
}
