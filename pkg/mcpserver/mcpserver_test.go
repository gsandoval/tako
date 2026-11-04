package mcpserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gsandoval/tako/pkg/mcpserver"
)

func TestMCPServerResourcesToolsAndCallHandler(t *testing.T) {
	takoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(takoRoot, "examples", "hello")

	requests := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"resources/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"resources/read","params":{"uri":"tako://agents"}}`,
		`{"jsonrpc":"2.0","id":4,"method":"resources/read","params":{"uri":"tako://openapi"}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"tako_check","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"tako_docs","arguments":{"explain_rule":"tako/entity-id"}}}`,
		`{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"tako_flags","arguments":{"action":"set","name":"promo-banner","value":"true"}}}`,
		`{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"tako_flags","arguments":{"action":"list"}}}`,
		`{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"tako_call","arguments":{"target":"Greet","input":{"name":"Agent"}}}}`,
	}
	defer os.RemoveAll(filepath.Join(dir, ".tako"))

	in := strings.NewReader(strings.Join(requests, "\n") + "\n")
	var out bytes.Buffer
	if err := mcpserver.Serve(context.Background(), mcpserver.ServerOptions{Dir: dir}, in, &out); err != nil {
		t.Fatalf("Serve failed: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != len(requests) {
		t.Fatalf("got %d responses, want %d:\n%s", len(lines), len(requests), out.String())
	}

	// Verify resources/read tako://agents (id=3)
	if !strings.Contains(lines[2], "AGENTS.md") || !strings.Contains(lines[2], "hello.Greet") {
		t.Errorf("tako://agents response missing expected content: %s", lines[2])
	}
	// Verify resources/read tako://openapi (id=4)
	if !strings.Contains(lines[3], `\"openapi\": \"3.1.0\"`) || !strings.Contains(lines[3], `\"/greet/{name}\"`) {
		t.Errorf("tako://openapi response missing expected content: %s", lines[3])
	}
	// Verify tako_check (id=6)
	if !strings.Contains(lines[5], `"ok":true`) {
		t.Errorf("tako_check response unexpected: %s", lines[5])
	}
	// Verify tako_docs explain_rule (id=7)
	if !strings.Contains(lines[6], `entity:\"id\"`) {
		t.Errorf("tako_docs explain_rule response unexpected: %s", lines[6])
	}
	// Verify tako_flags list (id=9)
	if !strings.Contains(lines[8], `"promo-banner"`) {
		t.Errorf("tako_flags list response unexpected: %s", lines[8])
	}
	// Verify tako_call Greet (id=10)
	var callResp struct {
		Result struct {
			IsError           bool `json:"isError"`
			StructuredContent struct {
				OK       bool `json:"ok"`
				Status   int  `json:"status"`
				Response struct {
					Message string `json:"message"`
				} `json:"response"`
			} `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[9]), &callResp); err != nil {
		t.Fatalf("unmarshal tako_call response: %v (%s)", err, lines[9])
	}
	if callResp.Result.IsError || !callResp.Result.StructuredContent.OK || callResp.Result.StructuredContent.Response.Message != "Hello, Agent!" {
		t.Fatalf("unexpected tako_call result: %s", lines[9])
	}
}
