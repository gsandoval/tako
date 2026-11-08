package projdocs_test

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gsandoval/tako"
	"github.com/gsandoval/tako/internal/hooks"
	"github.com/gsandoval/tako/pkg/analyzer"
	"github.com/gsandoval/tako/pkg/codegen"
	"github.com/gsandoval/tako/pkg/projdocs"
	"github.com/gsandoval/tako/pkg/provision"
)

func sampleGraph() *analyzer.AppGraph {
	return &analyzer.AppGraph{
		Module: "example.com/acme",
		Roles:  []string{"admin", "viewer"},
		Services: []analyzer.Service{
			{
				Name:     "acme",
				Package:  "example.com/acme",
				Handlers: []string{"main.ListItems", "main.InternalSync", "main.NightlyCleanup"},
				Profile:  "latency",
				Footprint: &analyzer.Footprint{
					CPU:          2,
					Memory:       "1Gi",
					MinInstances: 1,
					MaxInstances: 5,
				},
			},
		},
		Resources: []analyzer.Resource{
			{
				Kind:    "entity",
				Name:    "items",
				Var:     "main.items",
				Package: "example.com/acme",
				Type:    "Item",
				Indexes: []string{"Owner"},
			},
		},
		Handlers: []analyzer.Handler{
			{
				Kind:    "api",
				Var:     "main.ListItems",
				Package: "example.com/acme",
				Service: "acme",
				Route:   "GET /api/items",
				ReqRef:  "ListReq",
				ResRef:  "ListRes",
			},
			{
				Kind:    "api",
				Var:     "main.InternalSync",
				Package: "example.com/acme",
				Service: "acme",
				Route:   "POST /internal/sync",
				Private: true,
			},
			{
				Kind:         "cron",
				Var:          "main.NightlyCleanup",
				Package:      "example.com/acme",
				Service:      "acme",
				Schedule:     "0 3 * * *",
				Singleflight: true,
			},
		},
		Edges: []analyzer.Edge{
			{
				From:   "main.ListItems",
				To:     "items",
				ToKind: "entity",
				Kind:   "resource",
				Verb:   "read",
			},
		},
	}
}

func TestDisabledByDefaultProducesZeroDocs(t *testing.T) {
	g := sampleGraph()
	dir := t.TempDir()
	written, err := projdocs.GenerateAll(g, nil, dir)
	if err != nil {
		t.Fatalf("GenerateAll: %v", err)
	}
	if len(written) != 0 {
		t.Fatalf("expected 0 written files when no tako.Docs declared, got %v", written)
	}
	if _, err := os.Stat(filepath.Join(dir, ".tako", "gen", "docs")); !os.IsNotExist(err) {
		t.Fatalf("expected .tako/gen/docs not to exist when disabled by default")
	}
}

func TestSecurityLevelsAndCherryPickedSlices(t *testing.T) {
	g := sampleGraph()

	// 1. Public level: only APIs, omits private routes and sensitive slices.
	pubDir := filepath.Join(t.TempDir(), "public")
	pubSlices, pubLevel := analyzer.ResolveDocsSlices("public", true, nil)
	_, err := projdocs.GenerateInstance(g, nil, projdocs.Options{
		Name:   "public",
		Public: true,
		Level:  pubLevel,
		Slices: pubSlices,
		OutDir: pubDir,
	})
	if err != nil {
		t.Fatalf("GenerateInstance public: %v", err)
	}
	if _, err := os.Stat(filepath.Join(pubDir, "apis.html")); err != nil {
		t.Errorf("expected apis.html in public bundle: %v", err)
	}
	for _, forbidden := range []string{"architecture.html", "schedules.html", "resources.html", "roles.html", "footprints.html", "permissions.html"} {
		if _, err := os.Stat(filepath.Join(pubDir, forbidden)); !os.IsNotExist(err) {
			t.Errorf("expected %s to be omitted from public bundle", forbidden)
		}
	}
	apisHTML, err := os.ReadFile(filepath.Join(pubDir, "apis.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(apisHTML), "GET /api/items") {
		t.Errorf("expected public route GET /api/items in apis.html")
	}
	if strings.Contains(string(apisHTML), "POST /internal/sync") {
		t.Errorf("expected private route POST /internal/sync to be omitted from public apis.html")
	}

	// 2. Cherry-picked slices: only APIs + Schedules.
	cherryDir := filepath.Join(t.TempDir(), "cherry")
	cherrySlices, cherryLevel := analyzer.ResolveDocsSlices("", false, map[string]bool{
		"apis":      true,
		"schedules": true,
	})
	_, err = projdocs.GenerateInstance(g, nil, projdocs.Options{
		Name:   "cherry",
		Level:  cherryLevel,
		Slices: cherrySlices,
		OutDir: cherryDir,
	})
	if err != nil {
		t.Fatalf("GenerateInstance cherry: %v", err)
	}
	for _, want := range []string{"index.html", "404.html", "apis.html", "schedules.html", "openapi.json", "llms.txt"} {
		if _, err := os.Stat(filepath.Join(cherryDir, want)); err != nil {
			t.Errorf("expected %s in cherry bundle: %v", want, err)
		}
	}
	for _, forbidden := range []string{"architecture.html", "resources.html", "roles.html", "footprints.html", "permissions.html"} {
		if _, err := os.Stat(filepath.Join(cherryDir, forbidden)); !os.IsNotExist(err) {
			t.Errorf("expected %s to be omitted from cherry bundle", forbidden)
		}
	}

	// 3. Full level: all 7 slices generated, including footprints and permissions.
	fullDir := filepath.Join(t.TempDir(), "full")
	fullSlices, fullLevel := analyzer.ResolveDocsSlices("full", true, nil)
	_, err = projdocs.GenerateInstance(g, nil, projdocs.Options{
		Name:   "ops",
		Level:  fullLevel,
		Slices: fullSlices,
		OutDir: fullDir,
	})
	if err != nil {
		t.Fatalf("GenerateInstance full: %v", err)
	}
	for _, s := range analyzer.AllDocsSlices {
		if _, err := os.Stat(filepath.Join(fullDir, s+".html")); err != nil {
			t.Errorf("expected %s.html in full bundle: %v", s, err)
		}
	}
	permsHTML, err := os.ReadFile(filepath.Join(fullDir, "permissions.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(permsHTML), "roles/datastore.user") {
		t.Errorf("expected derived roles/datastore.user grant in permissions.html")
	}
}

func TestAnalyzerCodegenAndProvisioningForDocs(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(repoRoot, "examples", "docstest_tmp")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	appSrc := `package main

import "github.com/gsandoval/tako"

var Admins = tako.Role("admin")
var AdminOnly = tako.RequireRole(Admins)

var publicDocs = tako.Docs("public", tako.DocsConfig{
	Public: true,
	Level:  tako.DocsPublic,
})

var opsDocs = tako.Docs("ops", tako.DocsConfig{
	Public:      false,
	APIs:        true,
	Schedules:   true,
	Permissions: true,
})

var PublicDocsPage = tako.Page("GET /docs/{path...}", publicDocs.Serve)
var OpsDocsPage    = tako.Page("GET /ops/docs/{path...}", opsDocs.Serve, tako.Route{Allow: AdminOnly})
`
	if err := os.WriteFile(filepath.Join(dir, "app.go"), []byte(appSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	g, err := analyzer.Analyze(analyzer.Config{Dir: dir})
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	// Verify 2 docs resources and 2 read edges from the Page handlers.
	var docsRes []analyzer.Resource
	for _, r := range g.Resources {
		if r.Kind == "docs" {
			docsRes = append(docsRes, r)
		}
	}
	if len(docsRes) != 2 {
		t.Fatalf("expected 2 docs resources, got %d", len(docsRes))
	}

	readEdges := map[string]bool{}
	for _, e := range g.Edges {
		if e.ToKind == "docs" && e.Verb == "read" {
			readEdges[e.From+"->"+e.To] = true
		}
	}
	if !readEdges["main.PublicDocsPage->public"] || !readEdges["main.OpsDocsPage->ops"] {
		t.Fatalf("expected read edges for both docs handlers, got edges: %+v", g.Edges)
	}

	// Verify codegen.Generate writes service main AND .tako/gen/docs/<name>/ bundles.
	if _, err := codegen.Generate(g, dir); err != nil {
		t.Fatalf("codegen.Generate failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".tako", "gen", "docs", "public", "apis.html")); err != nil {
		t.Errorf("expected generated public/apis.html: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".tako", "gen", "docs", "ops", "permissions.html")); err != nil {
		t.Errorf("expected generated ops/permissions.html: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".tako", "gen", "docs", "ops", "footprints.html")); !os.IsNotExist(err) {
		t.Errorf("expected footprints.html to be omitted from ops bundle")
	}

	// Verify provision.Desired creates dedicated GCS buckets and objectViewer IAM grants.
	cfg := provision.Config{
		Project:       "myproj",
		ProjectNumber: "123456",
		Region:        "us-central1",
		App:           g.Module,
		Env:           "prod",
	}
	images := map[string]string{"main": "img:main"}
	desired, err := provision.Desired(g, cfg, images)
	if err != nil {
		t.Fatalf("provision.Desired: %v", err)
	}
	buckets := map[string]provision.Object{}
	for _, o := range desired {
		if o.Kind == provision.KindBucket {
			buckets[o.Name] = o
		}
	}
	pubBucketName := "myproj-" + cfg.AppID() + "-docs-public"
	opsBucketName := "myproj-" + cfg.AppID() + "-docs-ops"
	if b, ok := buckets[pubBucketName]; !ok {
		t.Errorf("missing public docs bucket %s", pubBucketName)
	} else if b.Spec["public"] != true || b.Spec["website"] != true {
		t.Errorf("expected public docs bucket spec public=true, website=true; got %+v", b.Spec)
	}
	if b, ok := buckets[opsBucketName]; !ok {
		t.Errorf("missing ops docs bucket %s", opsBucketName)
	} else if b.Spec["public"] != false || b.Spec["website"] != true {
		t.Errorf("expected ops docs bucket spec public=false, website=true; got %+v", b.Spec)
	}
}

func TestDocsHandleServeAndRender(t *testing.T) {
	app := tako.NewTestApp(t)
	ctx := app.Ctx()

	g := sampleGraph()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	docsOut := filepath.Join(wd, ".tako", "gen", "docs", "public")
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Join(wd, ".tako")) })

	slices, level := analyzer.ResolveDocsSlices("public", true, nil)
	if _, err := projdocs.GenerateInstance(g, nil, projdocs.Options{
		Name:   "public",
		Level:  level,
		Slices: slices,
		OutDir: docsOut,
	}); err != nil {
		t.Fatalf("GenerateInstance: %v", err)
	}

	handle := tako.Docs("public", tako.DocsConfig{Level: tako.DocsPublic})

	// 1. Root index.html
	res, err := handle.Serve(ctx, tako.DocsReq{Path: ""})
	if err != nil {
		t.Fatalf("Serve root: %v", err)
	}
	markup, _, _, _ := hooks.HTMLParts(res)
	if !strings.Contains(string(markup), "example.com/acme") {
		t.Errorf("expected overview markup to mention module name")
	}

	// 2. Clean subpath "apis" -> apis.html
	res, err = handle.Serve(ctx, tako.DocsReq{Path: "apis"})
	if err != nil {
		t.Fatalf("Serve apis: %v", err)
	}
	markup, _, _, _ = hooks.HTMLParts(res)
	if !strings.Contains(string(markup), "GET /api/items") {
		t.Errorf("expected apis page to contain GET /api/items")
	}

	// 3. openapi.json with application/json Content-Type
	res, err = handle.Render(ctx, "openapi.json")
	if err != nil {
		t.Fatalf("Render openapi.json: %v", err)
	}
	if ct := hooks.HTMLContentType(res); ct != "application/json; charset=utf-8" {
		t.Errorf("expected application/json; charset=utf-8 content type, got %q", ct)
	}

	// 4. Disabled slice (permissions) and path traversal return NotFound
	if _, err := handle.Serve(ctx, tako.DocsReq{Path: "permissions"}); !tako.IsNotFound(err) {
		t.Errorf("expected NotFound for disabled slice permissions, got %v", err)
	}
	if _, err := handle.Serve(ctx, tako.DocsReq{Path: "../secret"}); !tako.IsNotFound(err) {
		t.Errorf("expected NotFound for path traversal, got %v", err)
	}

	// 5. LLMsTxt serves llms.txt as plain text Blob
	blob, err := handle.LLMsTxt(ctx, struct{}{})
	if err != nil {
		t.Fatalf("LLMsTxt: %v", err)
	}
	if blob.ContentType != "text/plain; charset=utf-8" {
		t.Errorf("LLMsTxt ContentType = %q, want text/plain; charset=utf-8", blob.ContentType)
	}
	rawLLMs, _ := io.ReadAll(blob.Reader)
	if !strings.Contains(string(rawLLMs), "GET /api/items") {
		t.Errorf("expected LLMsTxt output to include GET /api/items, got %s", string(rawLLMs))
	}
}

func TestBucketTakeAndPutPolicyHeadersAndInviteOnly(t *testing.T) {
	app := tako.NewTestApp(t)
	ctx := app.Ctx()

	// 1. PutPolicy.Headers includes exact ContentType and x-goog-content-length-range
	pol := tako.PutPolicy{MaxSize: 50 << 20, ContentType: "application/octet-stream"}
	hdrs := pol.Headers()
	if hdrs["x-goog-content-length-range"] != "0,52428800" {
		t.Errorf("unexpected length range header: %v", hdrs)
	}
	if hdrs["Content-Type"] != "application/octet-stream" {
		t.Errorf("expected Content-Type header, got %v", hdrs)
	}
	wildPol := tako.PutPolicy{MaxSize: 1024, ContentType: "image/*"}
	if _, has := wildPol.Headers()["Content-Type"]; has {
		t.Errorf("wildcard ContentType should not emit exact Content-Type header")
	}

	// 2. Bucket.Take streams and deletes on Close
	b := tako.Bucket("ephemeral-files", tako.BucketConfig{TTL: 30 * 24 * time.Hour})
	if err := b.Put(ctx, "secret.bin", tako.Blob{
		Reader:      strings.NewReader("burn-after-reading"),
		ContentType: "application/octet-stream",
		Size:        int64(len("burn-after-reading")),
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	taken, err := b.Take(ctx, "secret.bin")
	if err != nil {
		t.Fatalf("Take: %v", err)
	}
	data, err := io.ReadAll(taken.Reader)
	if err != nil || string(data) != "burn-after-reading" {
		t.Fatalf("ReadAll = %q, err = %v", string(data), err)
	}
	if c, ok := taken.Reader.(io.Closer); ok {
		_ = c.Close()
	}
	if _, err := b.Get(ctx, "secret.bin"); !tako.IsNotFound(err) {
		t.Errorf("expected object to be deleted after Take Close(), got err=%v", err)
	}

	// 3. Auth.IsInviteOnly / SetInviteOnly
	adminRole := tako.Role("admin")
	posterRole := tako.Role("poster")
	auth := tako.Auth(tako.AuthConfig{
		Google:          true,
		Password:        true,
		FirstAdmin:      adminRole,
		FirstAdminEmail: "yo@memo.engineer",
		DefaultRole:     posterRole,
		InviteOnly:      true,
	})
	if !auth.IsInviteOnly(ctx) {
		t.Errorf("expected IsInviteOnly=true from AuthConfig default")
	}
	if err := auth.SetInviteOnly(ctx, false); err != nil {
		t.Fatalf("SetInviteOnly(false): %v", err)
	}
	if auth.IsInviteOnly(ctx) {
		t.Errorf("expected IsInviteOnly=false after SetInviteOnly(false)")
	}
}
