package runtime

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tako "github.com/gsandoval/tako"
	"github.com/gsandoval/tako/internal/routes"
)

type testOrderReq struct {
	Store string   `path:"store"`
	Dry   bool     `query:"dry"`
	Items []string `json:"items"`
}

type testOrderRes struct {
	Store string `json:"store"`
	Count int    `json:"count"`
	Dry   bool   `json:"dry"`
	User  string `json:"user,omitempty"`
}

func TestMCPHandlerLifecycleAndGuards(t *testing.T) {
	app := tako.NewTestApp(t)
	_ = app

	calls := 0
	ep := tako.API("POST /stores/{store}/orders", func(ctx tako.Ctx, req testOrderReq) (testOrderRes, error) {
		calls++
		u := ""
		if usr, ok := ctx.MaybeUser(); ok {
			u = usr.ID
		}
		return testOrderRes{
			Store: req.Store,
			Count: len(req.Items),
			Dry:   req.Dry,
			User:  u,
		}, nil
	}, tako.Route{
		SignIn:     true,
		Idempotent: true,
		Tool:       "Create a store order",
	})

	decode := func(r *http.Request) (testOrderReq, error) {
		var req testOrderReq
		if err := DecodeJSON(r, &req); err != nil {
			return req, err
		}
		req.Store = PathString(r, "store")
		var err error
		if req.Dry, err = QueryBool(r, "dry"); err != nil {
			return req, err
		}
		return req, nil
	}

	appMux := http.NewServeMux()
	appMux.Handle("POST /stores/{store}/orders", JSONHandler(ep, decode, RequireAuth(), Idempotent()))

	takoMux := http.NewServeMux()
	takoMux.Handle(routes.PatternMCP, MCPHandler(appMux, MCPToolSpec{
		Name:        "create_order",
		Description: "Create a store order",
		Route:       "POST /stores/{store}/orders",
		InputSchema: `{"type":"object","properties":{"store":{"type":"string"},"dry":{"type":"boolean"},"items":{"type":"array","items":{"type":"string"}}},"required":["store","items"]}`,
		PathParams:  []string{"store"},
		QueryParams: []string{"dry"},
		HasBody:     true,
	}))
	srv := Reserved(takoMux, appMux)

	// 1. initialize
	initRec := postMCP(t, srv, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`, nil)
	if !strings.Contains(initRec.Body.String(), `"tako-app-mcp"`) {
		t.Fatalf("initialize response unexpected: %s", initRec.Body.String())
	}

	// 2. tools/list
	listRec := postMCP(t, srv, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`, nil)
	if !strings.Contains(listRec.Body.String(), `"create_order"`) || !strings.Contains(listRec.Body.String(), `"Create a store order"`) {
		t.Fatalf("tools/list response unexpected: %s", listRec.Body.String())
	}

	// 3. tools/call unauthenticated -> isError: true (401 enforced by RequireAuth)
	unauthRec := postMCP(t, srv, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"create_order","arguments":{"store":"nyc","dry":true,"items":["sku1","sku2"]}}}`, nil)
	if !strings.Contains(unauthRec.Body.String(), `"isError":true`) || !strings.Contains(unauthRec.Body.String(), authRequired) {
		t.Fatalf("expected unauthenticated tools/call to return isError with %q, got: %s", authRequired, unauthRec.Body.String())
	}
	if calls != 0 {
		t.Fatalf("handler should not have been called when unauthenticated, calls = %d", calls)
	}

	// 4. Mint a session and call with Cookie + Idempotency-Key
	sess, err := mintSession(app.Ctx(), tako.User{ID: "u_alice", Email: "alice@example.com", Name: "Alice"})
	if err != nil {
		t.Fatalf("mintSession: %v", err)
	}
	headers := map[string]string{
		"Cookie":          sessionCookie + "=" + sess.ID,
		"Idempotency-Key": "idem-mcp-1",
	}
	callRec := postMCP(t, srv, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"create_order","arguments":{"store":"nyc","dry":true,"items":["sku1","sku2"]}}}`, headers)
	if strings.Contains(callRec.Body.String(), `"isError":true`) {
		t.Fatalf("authenticated tools/call failed: %s", callRec.Body.String())
	}
	var rpcResp struct {
		Result struct {
			IsError           bool         `json:"isError"`
			StructuredContent testOrderRes `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.Unmarshal(callRec.Body.Bytes(), &rpcResp); err != nil {
		t.Fatalf("unmarshal tools/call response: %v", err)
	}
	if rpcResp.Result.StructuredContent.Store != "nyc" || rpcResp.Result.StructuredContent.Count != 2 || !rpcResp.Result.StructuredContent.Dry || rpcResp.Result.StructuredContent.User != "u_alice" {
		t.Fatalf("unexpected structuredContent: %+v", rpcResp.Result.StructuredContent)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}

	// 5. Replay with same Idempotency-Key -> cached response, handler not re-executed
	replayRec := postMCP(t, srv, `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"create_order","arguments":{"store":"nyc","dry":true,"items":["sku1","sku2"]}}}`, headers)
	if strings.Contains(replayRec.Body.String(), `"isError":true`) {
		t.Fatalf("idempotent replay failed: %s", replayRec.Body.String())
	}
	if calls != 1 {
		t.Fatalf("idempotent replay re-ran handler: calls = %d, want 1", calls)
	}
}

func postMCP(t *testing.T, h http.Handler, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, routes.MCP, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST %s: status %d, body: %s", routes.MCP, rec.Code, rec.Body.String())
	}
	return rec
}
