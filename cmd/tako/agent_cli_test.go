package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gsandoval/tako/pkg/backends/local"
)

func TestFlagsListJSON(t *testing.T) {
	store := local.NewFlagStore(t.TempDir())
	var out bytes.Buffer
	cmd := &flagsCmd{
		out:    &out,
		target: accountsTarget{},
		declared: []declaredFlag{
			{Name: "checkout-v2", Type: "bool", Pos: "app.go:10"},
		},
		store:   store,
		jsonOut: true,
	}
	if err := cmd.set("checkout-v2", "true"); err != nil {
		t.Fatalf("set: %v", err)
	}
	out.Reset()
	if err := cmd.list(); err != nil {
		t.Fatalf("list --json: %v", err)
	}
	var parsed struct {
		Target string `json:"target"`
		Flags  []struct {
			Name       string `json:"name"`
			Type       string `json:"type"`
			ActiveRule string `json:"active_rule"`
		} `json:"flags"`
	}
	if err := json.Unmarshal(out.Bytes(), &parsed); err != nil {
		t.Fatalf("invalid JSON from flags list --json: %v\n%s", err, out.String())
	}
	if len(parsed.Flags) != 1 || parsed.Flags[0].Name != "checkout-v2" || parsed.Flags[0].ActiveRule != "true" {
		t.Fatalf("unexpected flags JSON: %+v", parsed)
	}
}

func TestGenOpenAPIAndAgentsAndCallCLI(t *testing.T) {
	takoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(takoRoot, "examples", "hello")
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.RemoveAll(filepath.Join(dir, ".tako"))
		_ = os.Chdir(origDir)
	}()

	tmp := t.TempDir()
	// 1. tako gen openapi
	openAPIOut := filepath.Join(tmp, "openapi.json")
	if err := runGenOpenAPI([]string{"--out", openAPIOut}); err != nil {
		t.Fatalf("runGenOpenAPI: %v", err)
	}
	specBytes, err := os.ReadFile(openAPIOut)
	if err != nil {
		t.Fatalf("read openapi.json: %v", err)
	}
	if !strings.Contains(string(specBytes), `"/greet/{name}"`) {
		t.Errorf("openapi.json missing /greet/{name}:\n%s", string(specBytes))
	}

	// 2. tako gen agents
	agentsOut := filepath.Join(tmp, "AGENTS.md")
	if err := runGenAgents([]string{"--out", agentsOut}); err != nil {
		t.Fatalf("runGenAgents: %v", err)
	}
	agentsBytes, err := os.ReadFile(agentsOut)
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	if !strings.Contains(string(agentsBytes), "GET /greet/{name}") {
		t.Errorf("AGENTS.md missing GET /greet/{name}:\n%s", string(agentsBytes))
	}

	// 3. tako call --json
	var callBuf bytes.Buffer
	if err := runCallWithWriter(&callBuf, []string{"Greet", `{"name":"agent"}`, "--json"}, nil); err != nil {
		t.Fatalf("runCallWithWriter: %v", err)
	}
	var callRes struct {
		OK       bool `json:"ok"`
		Status   int  `json:"status"`
		Response struct {
			Message string `json:"message"`
		} `json:"response"`
	}
	if err := json.Unmarshal(callBuf.Bytes(), &callRes); err != nil {
		t.Fatalf("unmarshal call result: %v\n%s", err, callBuf.String())
	}
	if !callRes.OK || callRes.Status != 200 || !strings.Contains(callRes.Response.Message, "agent") {
		t.Fatalf("unexpected call result: %+v", callRes)
	}
}
