package analyzer

import (
	"slices"
	"strings"
	"testing"
)

func TestInlineClosuresDeriveEdgesAndIndexes(t *testing.T) {
	dir := t.TempDir()
	writeModule(t, dir, "app", `
type Photo struct {
	ID    string `+"`entity:\"id\"`"+`
	Owner string `+"`entity:\"index\" json:\"owner_id\"`"+`
	Ready bool   `+"`entity:\"index\" json:\"ready\"`"+`
}

var photos = tako.Entity[Photo]("photos")
var jobs = tako.Queue[string]("jobs")

var Upload = tako.API("POST /photos", func(ctx tako.Ctx, p Photo) (Photo, error) {
	if err := photos.Put(ctx, p); err != nil {
		return Photo{}, err
	}
	return p, enqueue(ctx, p.ID)
})

func enqueue(ctx tako.Ctx, id string) error {
	return jobs.Publish(ctx, id)
}

var Feed = tako.API("GET /photos", func(ctx tako.Ctx, _ struct{}) ([]Photo, error) {
	return photos.Query(ctx).
		Where("Owner", "=", "u1").
		Where("Ready", "=", true).
		All()
})
`)
	graph, err := Analyze(Config{Dir: dir, Env: []string{"GOWORK=off"}})
	if err != nil {
		t.Fatalf("Analyze failed: %v", err)
	}

	wantEdges := []Edge{
		{From: "app.Upload", To: "jobs", ToKind: "queue", Kind: "resource", Verb: "publish"},
		{From: "app.Upload", To: "photos", ToKind: "entity", Kind: "resource", Verb: "write"},
		{From: "app.Feed", To: "photos", ToKind: "entity", Kind: "resource", Verb: "read"},
	}
	for _, want := range wantEdges {
		if !slices.Contains(graph.Edges, want) {
			t.Errorf("missing edge %+v in %+v", want, graph.Edges)
		}
	}

	var photoRes *Resource
	for i := range graph.Resources {
		if graph.Resources[i].Name == "photos" {
			photoRes = &graph.Resources[i]
		}
	}
	if photoRes == nil {
		t.Fatal("photos resource not found")
	}
	if !slices.Equal(photoRes.Indexes, []string{"Owner", "Ready"}) {
		t.Errorf("Indexes = %v, want [Owner Ready]", photoRes.Indexes)
	}
	if len(photoRes.Composites) != 1 || !slices.Equal(photoRes.Composites[0], []string{"owner_id", "ready"}) {
		t.Errorf("Composites = %v, want [[owner_id ready]]", photoRes.Composites)
	}
}

func TestPackageLevelAndExportedHandlerChecks(t *testing.T) {
	dir := t.TempDir()
	writeModule(t, dir, "app", `
var _ = tako.API("GET /blank", hello)
var hidden = tako.Cron("every 5m", tick)

func hello(ctx tako.Ctx, _ struct{}) (string, error) {
	_ = tako.Queue[string]("dynamic")
	return "ok", nil
}

func tick(ctx tako.Ctx) error { return nil }
`)
	_, err := Analyze(Config{Dir: dir, Env: []string{"GOWORK=off"}})
	if err == nil {
		t.Fatal("expected analysis errors, got nil")
	}
	got := err.Error()
	for _, want := range []string{
		"tako.API must initialize a named package-level var — a blank identifier (_) cannot be registered by the generated service (tako/package-level)",
		"handler hidden must be exported — the generated service registers it by name from outside the package; rename it Hidden (tako/package-level)",
		"tako.Queue must initialize a package-level var — declarations inside functions are invisible to analysis (tako/package-level)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing expected diagnostic:\n  want: %s\n  got:\n%s", want, got)
		}
	}
}

func TestQueryByJSONTagNamesGoStructField(t *testing.T) {
	dir := t.TempDir()
	writeModule(t, dir, "app", `
type Photo struct {
	ID    string `+"`entity:\"id\"`"+`
	Owner string `+"`entity:\"index\" json:\"owner_id\"`"+`
}

var photos = tako.Entity[Photo]("photos")

var List = tako.API("GET /photos", func(ctx tako.Ctx, _ struct{}) ([]Photo, error) {
	return photos.Query(ctx).Where("owner_id", "=", "u1").All()
})
`)
	_, err := Analyze(Config{Dir: dir, Env: []string{"GOWORK=off"}})
	if err == nil {
		t.Fatal("expected unindexed-query error, got nil")
	}
	want := `query filters on "owner_id" (the json tag of Photo.Owner) — Where and OrderBy take the Go struct field name "Owner", not the json tag (tako/unindexed-query)`
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("got:\n%s\nwant substring:\n%s", err.Error(), want)
	}
}
