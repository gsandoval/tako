package analyzer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCfireAnalyzerBasics(t *testing.T) {
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
Clicks := tako.queue[Link]("clicks")
GetLink := tako.api("GET /links/{code}", handle_get)

fn handle_get(ctx: tako.Ctx, req: GetReq) -> Link! {
    return try Links.get(ctx, req.code)
}
`
	if err := os.WriteFile(filepath.Join(tmpDir, "app.cf"), []byte(cfCode), 0o644); err != nil {
		t.Fatal(err)
	}

	if !IsCfireProject(tmpDir) {
		t.Fatalf("expected IsCfireProject(%q) to be true", tmpDir)
	}

	graph, err := Analyze(Config{Dir: tmpDir})
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	if len(graph.Resources) != 2 {
		t.Fatalf("expected 2 resources, got %d", len(graph.Resources))
	}
	if len(graph.Handlers) != 1 {
		t.Fatalf("expected 1 handler, got %d", len(graph.Handlers))
	}
	h := graph.Handlers[0]
	if h.Route != "GET /links/{code}" {
		t.Fatalf("unexpected route: %s", h.Route)
	}
	if len(h.Bindings) != 1 || h.Bindings[0].Name != "code" {
		t.Fatalf("unexpected bindings: %+v", h.Bindings)
	}
}

func TestCfireAnalyzerDiagnostics(t *testing.T) {
	tmpDir := t.TempDir()
	badCode := `package badapp

import "core:tako"

Links := tako.entity[BadLink]("links")

type BadLink struct {
    foo: str
}
`
	if err := os.WriteFile(filepath.Join(tmpDir, "bad.cf"), []byte(badCode), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Analyze(Config{Dir: tmpDir})
	if err == nil {
		t.Fatalf("expected analysis error for missing entity id")
	}

	errList, ok := err.(ErrorList)
	if !ok {
		t.Fatalf("expected ErrorList, got %T: %v", err, err)
	}
	if len(errList) == 0 {
		t.Fatalf("expected at least one diagnostic")
	}
	if errList[0].Rule != "entity-missing-id" {
		t.Fatalf("expected rule entity-missing-id, got %s", errList[0].Rule)
	}
}

func TestCfireAnalyzerBadId(t *testing.T) {
	tmpDir := t.TempDir()
	badCode := `package badidapp

import "core:tako"

Links := tako.entity[BadIdLink]("links")

type BadIdLink struct {
    code: i64 ` + "`entity:\"id\"`" + `
}
`
	if err := os.WriteFile(filepath.Join(tmpDir, "bad.cf"), []byte(badCode), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Analyze(Config{Dir: tmpDir})
	if err == nil {
		t.Fatalf("expected analysis error for bad entity id type")
	}

	errList, ok := err.(ErrorList)
	if !ok || len(errList) == 0 {
		t.Fatalf("expected ErrorList, got: %v", err)
	}
	if errList[0].Rule != "entity-bad-id" {
		t.Fatalf("expected rule entity-bad-id, got %s", errList[0].Rule)
	}
}

func TestCfireAnalyzerRouteParamMismatch(t *testing.T) {
	tmpDir := t.TempDir()
	badCode := `package badrouteapp

import "core:tako"

type Req struct {
    code: str ` + "`path:\"code\"`" + `
}

Get := tako.api("GET /items/{wrong}", handle_get)

fn handle_get(ctx: tako.Ctx, req: Req) -> str! {
    return req.code
}
`
	if err := os.WriteFile(filepath.Join(tmpDir, "bad.cf"), []byte(badCode), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Analyze(Config{Dir: tmpDir})
	if err == nil {
		t.Fatalf("expected analysis error for route param mismatch")
	}

	errList, ok := err.(ErrorList)
	if !ok || len(errList) == 0 {
		t.Fatalf("expected ErrorList, got: %v", err)
	}
	var hasMismatch bool
	for _, e := range errList {
		if e.Rule == "route-param-mismatch" {
			hasMismatch = true
			break
		}
	}
	if !hasMismatch {
		t.Fatalf("expected route-param-mismatch, got: %v", errList)
	}
}

func TestCfireAnalyzeExamples(t *testing.T) {
	examples := []string{
		"../../examples/cfire-hello",
		"../../examples/cfire-shortlink",
		"../../examples/cfire-gallery",
	}

	for _, ex := range examples {
		t.Run(filepath.Base(ex), func(t *testing.T) {
			if !IsCfireProject(ex) {
				t.Fatalf("expected IsCfireProject(%s) to be true", ex)
			}
			g, err := Analyze(Config{Dir: ex})
			if err != nil {
				t.Fatalf("failed to analyze %s: %v", ex, err)
			}
			if len(g.Handlers) == 0 {
				t.Fatalf("expected handlers in %s", ex)
			}
		})
	}
}
