package projdocs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gsandoval/tako/pkg/analyzer"
	"github.com/gsandoval/tako/pkg/schemagen"
)

// Options configures the generation of one documentation instance.
type Options struct {
	// Name is the declared tako.Docs instance name (e.g. "public", "ops").
	Name string
	// Public reports whether the documentation bucket is publicly readable.
	Public bool
	// Level is the effective security preset ("public", "internal", "full", "custom").
	Level string
	// Slices is the ordered list of enabled documentation slices.
	Slices []string
	// OutDir is the directory where the multi-page HTML bundle is written.
	OutDir string
}

// HasSlice reports whether slice s is enabled in opts.
func (o Options) HasSlice(s string) bool {
	for _, enabled := range o.Slices {
		if enabled == s {
			return true
		}
	}
	return false
}

// HidePrivateRoutes reports whether internal-only routes (Route{Private: true})
// should be omitted from the API documentation. Public-level docs that do not
// explicitly include roles or permissions omit private service-to-service routes.
func (o Options) HidePrivateRoutes() bool {
	return o.Level == "public" && !o.HasSlice("permissions") && !o.HasSlice("roles")
}

// GenerateAll generates documentation bundles for every declared tako.Docs
// resource in g under <dir>/.tako/gen/docs/<name>/. If no tako.Docs resource
// is declared, GenerateAll is a no-op and returns nil, nil.
func GenerateAll(g *analyzer.AppGraph, tg *analyzer.TypedGraph, dir string) ([]string, error) {
	hasDocs := false
	for _, r := range g.Resources {
		if r.Kind == "docs" {
			hasDocs = true
			break
		}
	}
	if !hasDocs {
		return nil, nil
	}
	if tg == nil && dir != "" {
		tg, _ = analyzer.AnalyzeTyped(analyzer.Config{Dir: dir})
	}
	var written []string
	for _, r := range g.Resources {
		if r.Kind != "docs" {
			continue
		}
		outDir := filepath.Join(dir, ".tako", "gen", "docs", r.Name)
		opts := OptionsFromResource(r, outDir)
		files, err := GenerateInstance(g, tg, opts)
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			if rel, rerr := filepath.Rel(dir, f); rerr == nil {
				written = append(written, rel)
			} else {
				written = append(written, f)
			}
		}
	}
	return written, nil
}

// OptionsFromResource builds Options from a Kind == "docs" Resource in the AppGraph.
func OptionsFromResource(r analyzer.Resource, outDir string) Options {
	level := r.Config["level"]
	if level == "" {
		level = "public"
	}
	slices := r.DocsSlices
	if len(slices) == 0 {
		slices, _ = analyzer.ResolveDocsSlices(level, true, nil)
	}
	return Options{
		Name:   r.Name,
		Public: r.Config["public"] == "true",
		Level:  level,
		Slices: slices,
		OutDir: outDir,
	}
}

// GenerateInstance writes the multi-page HTML documentation bundle for a single
// Options configuration and returns the absolute or OutDir-joined file paths.
func GenerateInstance(g *analyzer.AppGraph, tg *analyzer.TypedGraph, opts Options) ([]string, error) {
	if opts.Name == "" {
		opts.Name = "docs"
	}
	if opts.Level == "" {
		opts.Level = "public"
	}
	if len(opts.Slices) == 0 {
		opts.Slices, _ = analyzer.ResolveDocsSlices(opts.Level, true, nil)
	}
	// Clean OutDir first so stale slice files from a previous configuration
	// do not linger on disk or get synced to the bucket.
	if err := os.RemoveAll(opts.OutDir); err != nil {
		return nil, fmt.Errorf("cleaning docs output dir %s: %w", opts.OutDir, err)
	}
	if err := os.MkdirAll(opts.OutDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating docs output dir %s: %w", opts.OutDir, err)
	}

	var written []string
	writeFile := func(rel string, data []byte) error {
		target := filepath.Join(opts.OutDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			return err
		}
		written = append(written, target)
		return nil
	}

	// 1. Overview index.html and 404.html
	if err := writeFile("index.html", renderOverviewPage(g, opts)); err != nil {
		return nil, err
	}
	if err := writeFile("404.html", renderNotFoundPage(g, opts)); err != nil {
		return nil, err
	}

	// 2. Enabled slice pages (<slice>.html and <slice>/index.html for clean URLs)
	sliceRenderers := map[string]func(*analyzer.AppGraph, Options) []byte{
		"architecture": renderArchitecturePage,
		"apis":         func(g *analyzer.AppGraph, o Options) []byte { return renderAPIsPage(g, tg, o) },
		"schedules":    renderSchedulesPage,
		"resources":    renderResourcesPage,
		"roles":        renderRolesPage,
		"footprints":   renderFootprintsPage,
		"permissions":  renderPermissionsPage,
	}
	for _, s := range opts.Slices {
		renderFn, ok := sliceRenderers[s]
		if !ok {
			continue
		}
		htmlBytes := renderFn(g, opts)
		if err := writeFile(s+".html", htmlBytes); err != nil {
			return nil, err
		}
		if err := writeFile(filepath.ToSlash(filepath.Join(s, "index.html")), htmlBytes); err != nil {
			return nil, err
		}
	}

	// 3. openapi.json when the "apis" slice is enabled
	if opts.HasSlice("apis") {
		specBytes, err := buildFilteredOpenAPI(g, tg, opts)
		if err != nil {
			return nil, err
		}
		if err := writeFile("openapi.json", specBytes); err != nil {
			return nil, err
		}
	}

	// 4. llms.txt scoped strictly to the enabled slices
	if err := writeFile("llms.txt", renderLLMsTxt(g, tg, opts)); err != nil {
		return nil, err
	}

	sort.Strings(written)
	return written, nil
}

// buildFilteredOpenAPI produces an OpenAPI 3.1.0 JSON document containing only
// the routes visible under opts.
func buildFilteredOpenAPI(g *analyzer.AppGraph, tg *analyzer.TypedGraph, opts Options) ([]byte, error) {
	if tg != nil && tg.Graph == g && !opts.HidePrivateRoutes() {
		if raw, _, err := schemagen.OpenAPI(tg); err == nil {
			return raw, nil
		}
	}
	paths := map[string]map[string]any{}
	for _, h := range visibleRoutes(g, opts) {
		method, rawPath := splitRoutePattern(h.Route)
		openPath := strings.ReplaceAll(rawPath, "...}", "}")
		if paths[openPath] == nil {
			paths[openPath] = map[string]any{}
		}
		op := map[string]any{
			"operationId": analyzer.ShortVar(h),
			"summary":     fmt.Sprintf("%s (%s)", analyzer.ShortVar(h), h.Service),
			"responses": map[string]any{
				"200": map[string]any{
					"description": "OK",
				},
			},
		}
		var params []map[string]any
		for _, b := range h.Bindings {
			if b.Kind != "path" && b.Kind != "query" && b.Kind != "header" {
				continue
			}
			params = append(params, map[string]any{
				"name":     b.Name,
				"in":       b.Kind,
				"required": b.Kind == "path",
				"schema":   map[string]any{"type": openapiPrimitiveType(b.Type)},
			})
		}
		if len(params) > 0 {
			op["parameters"] = params
		}
		if h.HasBody && h.ReqType != "" {
			op["requestBody"] = map[string]any{
				"required": true,
				"content": map[string]any{
					"application/json": map[string]any{
						"schema": map[string]any{"type": "object", "title": h.ReqType},
					},
				},
			}
		}
		paths[openPath][strings.ToLower(method)] = op
	}
	doc := map[string]any{
		"openapi": "3.1.0",
		"info": map[string]any{
			"title":   g.Module + " (" + opts.Name + ")",
			"version": "0.1.0",
		},
		"paths": paths,
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

func openapiPrimitiveType(t string) string {
	switch t {
	case "int", "int64":
		return "integer"
	case "float64":
		return "number"
	case "bool":
		return "boolean"
	case "[]string":
		return "array"
	default:
		return "string"
	}
}

func visibleRoutes(g *analyzer.AppGraph, opts Options) []analyzer.Handler {
	var out []analyzer.Handler
	for _, h := range g.Handlers {
		if h.Kind != "api" && h.Kind != "page" {
			continue
		}
		if opts.HidePrivateRoutes() && h.Private {
			continue
		}
		out = append(out, h)
	}
	return out
}

func splitRoutePattern(route string) (method, path string) {
	method = "GET"
	path = route
	if i := strings.IndexByte(route, ' '); i >= 0 {
		method = route[:i]
		path = strings.TrimSpace(route[i+1:])
	}
	return method, path
}
