package schemagen_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gsandoval/tako/pkg/analyzer"
	"github.com/gsandoval/tako/pkg/schemagen"
)

func TestOpenAPIAndToolsFromTypedGraph(t *testing.T) {
	takoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	goMod := "module example.com/shop\n\ngo 1.26\n\nrequire github.com/gsandoval/tako v0.0.0\n\nrequire (\n\tgo.etcd.io/bbolt v1.5.0 // indirect\n\tgolang.org/x/sys v0.47.0 // indirect\n)\n\nreplace github.com/gsandoval/tako => " + takoRoot + "\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}
	if sumBytes, err := os.ReadFile(filepath.Join(takoRoot, "go.sum")); err == nil {
		_ = os.WriteFile(filepath.Join(dir, "go.sum"), sumBytes, 0o644)
	}
	appSrc := `package shop

import (
	"time"
	"github.com/gsandoval/tako"
)

var auth = tako.Auth(tako.AuthConfig{Password: true})
var Admin = tako.Role("admin")
var Admins = tako.RequireRole(Admin)

type CreateOrderReq struct {
	StoreID string   ` + "`path:\"store\"`" + `
	DryRun  bool     ` + "`query:\"dry_run\"`" + `
	Items   []string ` + "`json:\"items\"`" + `
	Notes   *string  ` + "`json:\"notes,omitempty\"`" + `
}

type OrderRes struct {
	OrderID   string    ` + "`json:\"order_id\"`" + `
	Total     int       ` + "`json:\"total\"`" + `
	CreatedAt time.Time ` + "`json:\"created_at\"`" + `
}

var CreateOrder = tako.API("POST /stores/{store}/orders", createOrder, tako.Route{
	Allow:      Admins,
	Idempotent: true,
	RateLimit:  tako.RateLimit{Requests: 30, Window: time.Minute},
	Tool:       "Create a store order with item SKUs.",
})

var Health = tako.API("GET /health", health)

func createOrder(ctx tako.Ctx, req CreateOrderReq) (OrderRes, error) {
	return OrderRes{OrderID: "ord_1", Total: len(req.Items)}, nil
}

func health(ctx tako.Ctx, _ struct{}) (map[string]string, error) {
	return map[string]string{"status": "ok"}, nil
}
`
	if err := os.WriteFile(filepath.Join(dir, "app.go"), []byte(appSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	tg, err := analyzer.AnalyzeTyped(analyzer.Config{Dir: dir, Env: []string{"GOWORK=off"}})
	if err != nil {
		t.Fatalf("AnalyzeTyped failed: %v", err)
	}

	tools, err := schemagen.Tools(tg)
	if err != nil {
		t.Fatalf("Tools failed: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("got %d tools, want 1", len(tools))
	}
	tool := tools[0]
	if tool.Name != "create_order" {
		t.Errorf("tool.Name = %q, want create_order", tool.Name)
	}
	if tool.Description != "Create a store order with item SKUs." {
		t.Errorf("tool.Description = %q", tool.Description)
	}
	if !tool.RequiresAuth || tool.Policy != "Admins" || !tool.Idempotent {
		t.Errorf("unexpected security metadata: %+v", tool)
	}
	if tool.InputSchema == nil || tool.InputSchema.Properties["store"] == nil || tool.InputSchema.Properties["items"] == nil {
		t.Fatalf("missing input schema properties: %+v", tool.InputSchema)
	}

	specBytes, sum, err := schemagen.OpenAPI(tg)
	if err != nil {
		t.Fatalf("OpenAPI failed: %v", err)
	}
	if sum.Operations != 2 || sum.Tools != 1 {
		t.Errorf("unexpected summary: %+v", sum)
	}
	var doc map[string]any
	if err := json.Unmarshal(specBytes, &doc); err != nil {
		t.Fatalf("invalid OpenAPI JSON: %v", err)
	}
	if doc["openapi"] != "3.1.0" {
		t.Errorf("openapi = %v, want 3.1.0", doc["openapi"])
	}
	rawStr := string(specBytes)
	for _, want := range []string{
		`"/stores/{store}/orders"`,
		`"Idempotency-Key"`,
		`"x-tako-tool": "create_order"`,
		`"x-tako-policy": "Admins"`,
	} {
		if !strings.Contains(rawStr, want) {
			t.Errorf("OpenAPI spec missing %s:\n%s", want, rawStr)
		}
	}
}
