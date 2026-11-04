// Package mcpserver implements Tako's built-in Model Context Protocol
// (MCP) server (`tako mcp`) and programmatic handler invocation engine
// (`tako call`) for coding and SRE agents.
package mcpserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gsandoval/tako/docs"
	"github.com/gsandoval/tako/pkg/analyzer"
	"github.com/gsandoval/tako/pkg/backends"
	"github.com/gsandoval/tako/pkg/backends/local"
	"github.com/gsandoval/tako/pkg/build"
	"github.com/gsandoval/tako/pkg/codegen"
	"github.com/gsandoval/tako/pkg/docserver"
	"github.com/gsandoval/tako/pkg/provision"
	"github.com/gsandoval/tako/pkg/runtime"
	"github.com/gsandoval/tako/pkg/schemagen"
)

// CallOptions configures a single local handler invocation (`tako call`
// or the `tako_call` MCP tool).
type CallOptions struct {
	// Target is the handler var ("Greet", "hello.Greet", "greet") or
	// route ("GET /greet/{name}", "/greet/{name}").
	Target string `json:"target"`
	// Input is the decoded JSON object containing path, query, and body
	// fields.
	Input map[string]json.RawMessage `json:"input,omitempty"`
	// RawPayload is the raw JSON payload when Input is not a map (e.g.
	// primitive or array payload for a worker/subscriber/job).
	RawPayload json.RawMessage `json:"raw_payload,omitempty"`
	// AsUser simulates an authenticated session for this user name.
	AsUser string `json:"as_user,omitempty"`
	// Roles assigns roles to the simulated user session.
	Roles []string `json:"roles,omitempty"`
	// Flags pins flag overrides for this execution.
	Flags map[string]string `json:"flags,omitempty"`
	// IdempotencyKey sets the Idempotency-Key header.
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	// DeviceID sets the X-Tako-Device-ID header.
	DeviceID string `json:"device_id,omitempty"`
	// Env passes extra environment variables to analysis/build (e.g. GOWORK=off in tests).
	Env []string `json:"-"`
}

// ResolveHandler finds a handler in g matching target by qualified Var,
// short Var, snake_case tool name, exact route, or route path.
func ResolveHandler(g *analyzer.AppGraph, target string) (analyzer.Handler, error) {
	q := strings.TrimSpace(target)
	if q == "" {
		return analyzer.Handler{}, fmt.Errorf("handler target is required")
	}
	multi := len(g.Services) > 1
	for _, h := range g.Handlers {
		short := analyzer.ShortVar(h)
		if strings.EqualFold(h.Var, q) ||
			strings.EqualFold(short, q) ||
			strings.EqualFold(schemagen.ToolName(h, multi), q) ||
			strings.EqualFold(schemagen.ToolName(h, false), q) ||
			strings.EqualFold(h.Route, q) {
			return h, nil
		}
		if h.Route != "" {
			if i := strings.IndexByte(h.Route, ' '); i >= 0 {
				routePath := strings.TrimSpace(h.Route[i+1:])
				if strings.EqualFold(routePath, q) {
					return h, nil
				}
			}
		}
	}
	var names []string
	for _, h := range g.Handlers {
		if h.Route != "" {
			names = append(names, fmt.Sprintf("%s (%s)", analyzer.ShortVar(h), h.Route))
		} else {
			names = append(names, fmt.Sprintf("%s (%s)", analyzer.ShortVar(h), h.Kind))
		}
	}
	return analyzer.Handler{}, fmt.Errorf("unknown handler %q — available handlers: %s", target, strings.Join(names, ", "))
}

// CallHandler analyzes dir, generates/builds the target handler's
// service, executes the handler in-process via TAKO_ONESHOT against
// .tako/data, and returns the structured OneShotResult.
func CallHandler(ctx context.Context, dir string, opts CallOptions) (runtime.OneShotResult, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return runtime.OneShotResult{}, err
	}
	graph, err := analyzer.Analyze(analyzer.Config{Dir: absDir, Env: opts.Env})
	if err != nil {
		return runtime.OneShotResult{}, err
	}
	h, err := ResolveHandler(graph, opts.Target)
	if err != nil {
		return runtime.OneShotResult{}, err
	}
	if _, err := codegen.Generate(graph, absDir); err != nil {
		return runtime.OneShotResult{}, err
	}

	var pathParams, queryParams []string
	for _, b := range h.Bindings {
		switch b.Kind {
		case "path":
			pathParams = append(pathParams, b.Name)
		case "query":
			queryParams = append(queryParams, b.Name)
		}
	}
	spec := runtime.OneShotSpec{
		Kind:           h.Kind,
		HandlerVar:     h.Var,
		ShortVar:       analyzer.ShortVar(h),
		Route:          h.Route,
		PathParams:     pathParams,
		QueryParams:    queryParams,
		HasBody:        h.HasBody,
		Input:          opts.Input,
		RawPayload:     opts.RawPayload,
		AsUser:         opts.AsUser,
		Roles:          opts.Roles,
		Flags:          opts.Flags,
		IdempotencyKey: opts.IdempotencyKey,
		DeviceID:       opts.DeviceID,
	}
	specBytes, err := json.Marshal(spec)
	if err != nil {
		return runtime.OneShotResult{}, err
	}

	binRel := filepath.Join(".tako", "bin", "call-"+h.Service)
	binAbs := filepath.Join(absDir, binRel)
	if err := os.MkdirAll(filepath.Dir(binAbs), 0o755); err != nil {
		return runtime.OneShotResult{}, err
	}
	if analyzer.IsCfireProject(absDir) {
		libPath, _, err := build.EnsureLibTako(absDir, "", "")
		if err != nil {
			return runtime.OneShotResult{}, fmt.Errorf("ensuring libtako: %w", err)
		}
		mainCf := filepath.Join(".tako", "gen", h.Service, "main.cf")
		buildArgs := []string{"build", mainCf, "-o", binRel, "--link-lib", libPath}
		if incDir := build.GetCfireIncludeDir(); incDir != "" {
			buildArgs = append(buildArgs, "--include", incDir)
		}
		cmd := exec.CommandContext(ctx, analyzer.FindCfireBin(), buildArgs...)
		cmd.Dir = absDir
		if out, err := cmd.CombinedOutput(); err != nil {
			return runtime.OneShotResult{}, fmt.Errorf("cfire build failed:\n%s", strings.TrimSpace(string(out)))
		}
	} else {
		cmd := exec.CommandContext(ctx, "go", "build", "-o", binRel, "./"+filepath.Join(".tako", "gen", h.Service))
		cmd.Dir = absDir
		if len(opts.Env) > 0 {
			cmd.Env = append(os.Environ(), opts.Env...)
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			return runtime.OneShotResult{}, fmt.Errorf("go build failed:\n%s", strings.TrimSpace(string(out)))
		}
	}

	runCmd := exec.CommandContext(ctx, binAbs)
	runCmd.Dir = absDir
	runCmd.Env = append(os.Environ(),
		"TAKO_ONESHOT="+string(specBytes),
		"TAKO_SERVICE="+h.Service,
	)
	out, runErr := runCmd.CombinedOutput()
	outStr := string(out)
	if idx := strings.LastIndex(outStr, runtime.OneShotMarker); idx >= 0 {
		jsonLine := strings.TrimSpace(outStr[idx+len(runtime.OneShotMarker):])
		if nl := strings.IndexByte(jsonLine, '\n'); nl >= 0 {
			jsonLine = strings.TrimSpace(jsonLine[:nl])
		}
		var res runtime.OneShotResult
		if err := json.Unmarshal([]byte(jsonLine), &res); err == nil {
			return res, nil
		}
	}
	if runErr != nil {
		return runtime.OneShotResult{}, fmt.Errorf("executing %s failed: %v\n%s", h.Var, runErr, strings.TrimSpace(outStr))
	}
	return runtime.OneShotResult{}, fmt.Errorf("unexpected output from %s:\n%s", h.Var, strings.TrimSpace(outStr))
}

// --- Built-in `tako mcp` stdio JSON-RPC 2.0 Server ---

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ServerOptions configures the built-in `tako mcp` server.
type ServerOptions struct {
	Dir string
	Env []string
}

// Serve runs the Tako developer & ops MCP server over newline-delimited
// JSON-RPC 2.0 streams until ctx is cancelled or in reaches EOF.
func Serve(ctx context.Context, opts ServerOptions, in io.Reader, out io.Writer) error {
	if opts.Dir == "" {
		opts.Dir = "."
	}
	absDir, err := filepath.Abs(opts.Dir)
	if err != nil {
		return err
	}
	opts.Dir = absDir

	site, _ := docserver.Build(docs.FS)
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil || req.Method == "" {
			writeRPC(out, rpcResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &rpcError{Code: -32700, Message: "invalid JSON-RPC 2.0 request"},
			})
			continue
		}
		// Notifications carry no ID and expect no response frame.
		if len(req.ID) == 0 || string(req.ID) == "null" {
			continue
		}
		resp := handleRPC(ctx, opts, site, req)
		writeRPC(out, resp)
	}
	return scanner.Err()
}

func writeRPC(out io.Writer, resp rpcResponse) {
	raw, err := json.Marshal(resp)
	if err != nil {
		return
	}
	_, _ = out.Write(append(raw, '\n'))
}

func handleRPC(ctx context.Context, opts ServerOptions, site *docserver.Site, req rpcRequest) rpcResponse {
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		proto := "2025-03-26"
		if p.ProtocolVersion != "" {
			proto = p.ProtocolVersion
		}
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"protocolVersion": proto,
				"capabilities": map[string]any{
					"tools":     map[string]any{},
					"resources": map[string]any{},
				},
				"serverInfo": map[string]any{
					"name":    "tako-mcp",
					"version": "1.0.0",
				},
			},
		}

	case "ping":
		return rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}}

	case "resources/list":
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"resources": []map[string]any{
					{
						"uri":         "tako://graph",
						"name":        "AppGraph JSON",
						"description": "Derived services, handlers, resources, composite indexes, and IAM edges for this Tako app.",
						"mimeType":    "application/json",
					},
					{
						"uri":         "tako://agents",
						"name":        "Live AGENTS.md Context",
						"description": "Synthesized architecture, handler/entity inventory, and static analysis rules for this app.",
						"mimeType":    "text/markdown",
					},
					{
						"uri":         "tako://openapi",
						"name":        "OpenAPI 3.1 Specification",
						"description": "Compile-time OpenAPI 3.1.0 specification for all API endpoints.",
						"mimeType":    "application/json",
					},
					{
						"uri":         "tako://docs/llms.txt",
						"name":        "Tako Documentation Index (llms.txt)",
						"description": "Compact index of all embedded Tako guide chapters, reference pages, and SDK signatures.",
						"mimeType":    "text/plain",
					},
					{
						"uri":         "tako://docs/api",
						"name":        "Tako Exported SDK Surface",
						"description": "Every exported type, constructor, and method signature in the Tako SDK.",
						"mimeType":    "text/plain",
					},
				},
			},
		}

	case "resources/read":
		var p struct {
			URI string `json:"uri"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil || p.URI == "" {
			return rpcErr(req.ID, -32602, "resources/read requires params.uri")
		}
		text, mime, err := readResource(opts, site, p.URI)
		if err != nil {
			return rpcErr(req.ID, -32602, err.Error())
		}
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"contents": []map[string]any{
					{
						"uri":      p.URI,
						"mimeType": mime,
						"text":     text,
					},
				},
			},
		}

	case "tools/list":
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"tools": mcpToolDefinitions(),
			},
		}

	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil || p.Name == "" {
			return rpcErr(req.ID, -32602, "tools/call requires params.name")
		}
		res, isErr := dispatchTool(ctx, opts, site, p.Name, p.Arguments)
		rawText, _ := json.MarshalIndent(res, "", "  ")
		if str, ok := res.(string); ok {
			rawText = []byte(str)
		}
		return rpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"content": []map[string]any{
					{
						"type": "text",
						"text": string(rawText),
					},
				},
				"structuredContent": res,
				"isError":           isErr,
			},
		}

	default:
		return rpcErr(req.ID, -32601, fmt.Sprintf("method %q not found", req.Method))
	}
}

func rpcErr(id json.RawMessage, code int, msg string) rpcResponse {
	return rpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &rpcError{Code: code, Message: msg},
	}
}

func readResource(opts ServerOptions, site *docserver.Site, uri string) (string, string, error) {
	switch uri {
	case "tako://graph":
		g, err := analyzer.Analyze(analyzer.Config{Dir: opts.Dir, Env: opts.Env})
		if err != nil {
			return "", "", err
		}
		b, err := json.MarshalIndent(g, "", "  ")
		return string(b), "application/json", err
	case "tako://agents":
		tg, err := analyzer.AnalyzeTyped(analyzer.Config{Dir: opts.Dir, Env: opts.Env})
		if err != nil {
			return "", "", err
		}
		existing, _ := os.ReadFile(filepath.Join(opts.Dir, "AGENTS.md"))
		return schemagen.GenerateAgentsMD(tg, string(existing)), "text/markdown", nil
	case "tako://openapi":
		tg, err := analyzer.AnalyzeTyped(analyzer.Config{Dir: opts.Dir, Env: opts.Env})
		if err != nil {
			return "", "", err
		}
		spec, _, err := schemagen.OpenAPI(tg)
		return string(spec), "application/json", err
	case "tako://docs/llms.txt":
		if site == nil {
			return "", "", fmt.Errorf("docs unavailable")
		}
		return site.LLMsTxt(), "text/plain", nil
	case "tako://docs/api":
		if site == nil {
			return "", "", fmt.Errorf("docs unavailable")
		}
		return site.Surface, "text/plain", nil
	default:
		if topic, ok := strings.CutPrefix(uri, "tako://docs/"); ok && site != nil {
			if md, found := site.Topic(topic); found {
				return md, "text/markdown", nil
			}
		}
		return "", "", fmt.Errorf("unknown resource URI %q", uri)
	}
}

func mcpToolDefinitions() []map[string]any {
	return []map[string]any{
		{
			"name":        "tako_check",
			"description": "Run fast static analysis, site-shadow checks, optional version-skew check, and code generation under .tako/gen/. Returns structured diagnostics with actionable Hint and Doc fields.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"skew_baseline": map[string]any{
						"type":        "string",
						"description": "Optional path to a baseline AppGraph JSON file to check for rolling-deploy or rollback version skew.",
					},
				},
			},
		},
		{
			"name":        "tako_graph",
			"description": "Inspect the application's derived architecture: AppGraph JSON, Mermaid diagram, human summary, OpenAPI 3.1 spec, or live AGENTS.md context.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"format": map[string]any{
						"type":        "string",
						"description": "Output format: 'json' (default AppGraph), 'mermaid', 'summary', 'openapi', 'tools', or 'agents'.",
					},
				},
			},
		},
		{
			"name":        "tako_docs",
			"description": "Read embedded Tako documentation topics, search across guide/reference/SDK signatures, or explain an analysis error rule (e.g. 'tako/entity-id').",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"topic": map[string]any{
						"type":        "string",
						"description": "Documentation topic name ('list', 'api', 'entity', 'auth', 'flags', 'trace', 'llms.txt', etc.).",
					},
					"search": map[string]any{
						"type":        "string",
						"description": "Search query across all embedded documentation and exported SDK signatures.",
					},
					"explain_rule": map[string]any{
						"type":        "string",
						"description": "Analysis rule ID to explain with fix hints and examples (e.g. 'entity-id' or 'tako/const-route').",
					},
				},
			},
		},
		{
			"name":        "tako_call",
			"description": "Invoke any declared handler (API, Page, Worker, Subscribe, Cron, or Job) in-process against local .tako/data without starting a server. Returns the response and recorded trace spans.",
			"inputSchema": map[string]any{
				"type":     "object",
				"required": []string{"target"},
				"properties": map[string]any{
					"target": map[string]any{
						"type":        "string",
						"description": "Handler variable name (e.g. 'CreateOrder', 'shop.CreateOrder') or route ('POST /orders/{id}').",
					},
					"input": map[string]any{
						"type":        "object",
						"description": "JSON object with path parameters, query parameters, and request body fields.",
					},
					"as_user": map[string]any{
						"type":        "string",
						"description": "Optional dev user name (e.g. 'alice') to authenticate the request as.",
					},
					"roles": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "Optional roles to grant to as_user for this call.",
					},
					"flags": map[string]any{
						"type":        "object",
						"description": "Optional map of flag name to JSON value override for this call.",
					},
					"idempotency_key": map[string]any{
						"type":        "string",
						"description": "Optional Idempotency-Key header value.",
					},
				},
			},
		},
		{
			"name":        "tako_test",
			"description": "Analyze, regenerate .tako/gen/ and test harness overlays, and run `go test` on the application.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"run": map[string]any{
						"type":        "string",
						"description": "Optional -run regex filter for go test (e.g. 'TestCreateOrder').",
					},
				},
			},
		},
		{
			"name":        "tako_flags",
			"description": "Manage feature flags and experiments in local .tako/data (list, set, rollout, split, or reset).",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action": map[string]any{
						"type":        "string",
						"description": "'list' (default), 'set', 'rollout', 'split', or 'reset'.",
					},
					"name": map[string]any{
						"type":        "string",
						"description": "Flag name (required for set, rollout, split, reset).",
					},
					"value": map[string]any{
						"type":        "string",
						"description": "JSON or scalar value for 'set' or target value for 'rollout'.",
					},
					"percent": map[string]any{
						"type":        "integer",
						"description": "Rollout percentage (0-100) for 'rollout'.",
					},
					"variants": map[string]any{
						"type":        "object",
						"description": "Map of variant value to integer weight (summing to 100) for 'split'.",
					},
				},
			},
		},
		{
			"name":        "tako_data",
			"description": "Inspect local .tako/data state: stored entities by kind/ID, dead-lettered messages (DLQ), or local dev user accounts.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"source": map[string]any{
						"type":        "string",
						"description": "'entities' (default), 'dlq', or 'users'.",
					},
					"entity_kind": map[string]any{
						"type":        "string",
						"description": "Entity kind name to query when source is 'entities'.",
					},
					"entity_id": map[string]any{
						"type":        "string",
						"description": "Optional entity ID to fetch a single record.",
					},
					"limit": map[string]any{
						"type":        "integer",
						"description": "Max records to return (default 50).",
					},
				},
			},
		},
	}
}

func dispatchTool(ctx context.Context, opts ServerOptions, site *docserver.Site, name string, rawArgs json.RawMessage) (any, bool) {
	switch name {
	case "tako_check":
		var args struct {
			SkewBaseline string `json:"skew_baseline"`
		}
		_ = json.Unmarshal(rawArgs, &args)
		graph, err := analyzer.Analyze(analyzer.Config{Dir: opts.Dir, Env: opts.Env})
		if err != nil {
			if list, ok := err.(analyzer.ErrorList); ok {
				return map[string]any{
					"ok":     false,
					"errors": list,
				}, true
			}
			return map[string]any{
				"ok":         false,
				"load_error": err.Error(),
			}, true
		}
		warnings := analyzer.ShadowedSiteFiles(opts.Dir, graph)
		written, err := codegen.Generate(graph, opts.Dir)
		if err != nil {
			return map[string]any{"ok": false, "load_error": err.Error()}, true
		}
		var skew provision.SkewReport
		if args.SkewBaseline != "" {
			raw, rErr := os.ReadFile(args.SkewBaseline)
			if rErr != nil {
				return map[string]any{"ok": false, "load_error": rErr.Error()}, true
			}
			var prev analyzer.AppGraph
			if uErr := json.Unmarshal(raw, &prev); uErr != nil {
				return map[string]any{"ok": false, "load_error": uErr.Error()}, true
			}
			skew = provision.CheckSkew(&prev, graph, nil)
		}
		return map[string]any{
			"ok":        !skew.HasDanger(),
			"services":  len(graph.Services),
			"handlers":  len(graph.Handlers),
			"resources": len(graph.Resources),
			"written":   codegen.Mains(written),
			"warnings":  warnings,
			"skew":      skew.Findings,
		}, skew.HasDanger()

	case "tako_graph":
		var args struct {
			Format string `json:"format"`
		}
		_ = json.Unmarshal(rawArgs, &args)
		tg, err := analyzer.AnalyzeTyped(analyzer.Config{Dir: opts.Dir, Env: opts.Env})
		if err != nil {
			return map[string]any{"error": err.Error()}, true
		}
		switch strings.ToLower(strings.TrimSpace(args.Format)) {
		case "mermaid":
			return analyzer.Mermaid(tg.Graph), false
		case "summary":
			return analyzer.Summary(tg.Graph), false
		case "openapi":
			spec, _, err := schemagen.OpenAPI(tg)
			if err != nil {
				return map[string]any{"error": err.Error()}, true
			}
			var parsed any
			_ = json.Unmarshal(spec, &parsed)
			return parsed, false
		case "tools":
			tools, err := schemagen.Tools(tg)
			if err != nil {
				return map[string]any{"error": err.Error()}, true
			}
			return map[string]any{"tools": tools}, false
		case "agents":
			existing, _ := os.ReadFile(filepath.Join(opts.Dir, "AGENTS.md"))
			return schemagen.GenerateAgentsMD(tg, string(existing)), false
		default:
			return tg.Graph, false
		}

	case "tako_docs":
		var args struct {
			Topic       string `json:"topic"`
			Search      string `json:"search"`
			ExplainRule string `json:"explain_rule"`
		}
		_ = json.Unmarshal(rawArgs, &args)
		if args.ExplainRule != "" {
			if exp, ok := analyzer.ExplainRule(args.ExplainRule); ok {
				return exp, false
			}
			return fmt.Sprintf("unknown rule %q — run `tako docs errors` to see all analysis rules", args.ExplainRule), true
		}
		if site == nil {
			return "embedded documentation unavailable", true
		}
		if args.Search != "" {
			return site.Search(args.Search), false
		}
		if args.Topic == "" || args.Topic == "list" || args.Topic == "ls" {
			return site.List(), false
		}
		if md, ok := site.Topic(args.Topic); ok {
			return md, false
		}
		return fmt.Sprintf("unknown topic %q — use topic='list' or search=%q", args.Topic, args.Topic), true

	case "tako_call":
		var callOpts CallOptions
		if err := json.Unmarshal(rawArgs, &callOpts); err != nil {
			return map[string]any{"error": err.Error()}, true
		}
		callOpts.Env = opts.Env
		res, err := CallHandler(ctx, opts.Dir, callOpts)
		if err != nil {
			return map[string]any{"ok": false, "error": err.Error()}, true
		}
		return res, !res.OK

	case "tako_test":
		var args struct {
			Run string `json:"run"`
		}
		_ = json.Unmarshal(rawArgs, &args)
		graph, err := analyzer.Analyze(analyzer.Config{Dir: opts.Dir, Env: opts.Env})
		if err != nil {
			return map[string]any{"ok": false, "error": err.Error()}, true
		}
		if _, err := codegen.Generate(graph, opts.Dir); err != nil {
			return map[string]any{"ok": false, "error": err.Error()}, true
		}
		testArgs := []string{"test"}
		if overlay := codegen.HarnessOverlay(graph, opts.Dir); len(overlay) > 0 {
			ovBytes, _ := json.Marshal(struct {
				Replace map[string]string
			}{Replace: overlay})
			ovPath := filepath.Join(opts.Dir, ".tako", "gen", "overlay.json")
			if os.WriteFile(ovPath, ovBytes, 0o644) == nil {
				testArgs = append(testArgs, "-overlay="+ovPath)
			}
		}
		if args.Run != "" {
			testArgs = append(testArgs, "-run="+args.Run)
		}
		testArgs = append(testArgs, "./...")
		cmd := exec.CommandContext(ctx, "go", testArgs...)
		cmd.Dir = opts.Dir
		if len(opts.Env) > 0 {
			cmd.Env = append(os.Environ(), opts.Env...)
		}
		out, tErr := cmd.CombinedOutput()
		return map[string]any{
			"ok":     tErr == nil,
			"output": strings.TrimSpace(string(out)),
		}, tErr != nil

	case "tako_flags":
		return handleFlagsTool(ctx, opts.Dir, rawArgs)

	case "tako_data":
		return handleDataTool(ctx, opts, rawArgs)

	default:
		return map[string]any{"error": fmt.Sprintf("unknown tool %q", name)}, true
	}
}

func handleFlagsTool(ctx context.Context, dir string, rawArgs json.RawMessage) (any, bool) {
	var args struct {
		Action   string         `json:"action"`
		Name     string         `json:"name"`
		Value    string         `json:"value"`
		Percent  int            `json:"percent"`
		Variants map[string]int `json:"variants"`
	}
	_ = json.Unmarshal(rawArgs, &args)
	store := local.NewFlagStore(filepath.Join(dir, ".tako", "data"))
	action := strings.ToLower(strings.TrimSpace(args.Action))
	if action == "" {
		action = "list"
	}
	switch action {
	case "list":
		rules, err := store.List(ctx)
		if err != nil {
			return map[string]any{"error": err.Error()}, true
		}
		return map[string]any{"flags": rules}, false
	case "set":
		if args.Name == "" {
			return map[string]any{"error": "name is required for set"}, true
		}
		val := strings.TrimSpace(args.Value)
		if !json.Valid([]byte(val)) {
			b, _ := json.Marshal(val)
			val = string(b)
		}
		rule := backends.FlagRule{Name: args.Name, Value: json.RawMessage(val), UpdatedAt: time.Now().UTC()}
		if err := store.Put(ctx, rule); err != nil {
			return map[string]any{"error": err.Error()}, true
		}
		return map[string]any{"ok": true, "rule": rule}, false
	case "rollout":
		if args.Name == "" {
			return map[string]any{"error": "name is required for rollout"}, true
		}
		val := strings.TrimSpace(args.Value)
		if val == "" {
			val = "true"
		} else if !json.Valid([]byte(val)) {
			b, _ := json.Marshal(val)
			val = string(b)
		}
		rule := backends.FlagRule{
			Name:       args.Name,
			Value:      json.RawMessage(val),
			RolloutPct: args.Percent,
			UpdatedAt:  time.Now().UTC(),
		}
		if err := store.Put(ctx, rule); err != nil {
			return map[string]any{"error": err.Error()}, true
		}
		return map[string]any{"ok": true, "rule": rule}, false
	case "split":
		if args.Name == "" || len(args.Variants) == 0 {
			return map[string]any{"error": "name and variants are required for split"}, true
		}
		var variants []backends.FlagVariant
		for v, w := range args.Variants {
			rawV := v
			if !json.Valid([]byte(rawV)) {
				b, _ := json.Marshal(rawV)
				rawV = string(b)
			}
			variants = append(variants, backends.FlagVariant{Value: json.RawMessage(rawV), Weight: w})
		}
		rule := backends.FlagRule{Name: args.Name, Variants: variants, UpdatedAt: time.Now().UTC()}
		if err := store.Put(ctx, rule); err != nil {
			return map[string]any{"error": err.Error()}, true
		}
		return map[string]any{"ok": true, "rule": rule}, false
	case "reset":
		if args.Name == "" {
			return map[string]any{"error": "name is required for reset"}, true
		}
		if err := store.Delete(ctx, args.Name); err != nil {
			return map[string]any{"error": err.Error()}, true
		}
		return map[string]any{"ok": true, "deleted": args.Name}, false
	default:
		return map[string]any{"error": fmt.Sprintf("unknown flag action %q", action)}, true
	}
}

func handleDataTool(ctx context.Context, opts ServerOptions, rawArgs json.RawMessage) (any, bool) {
	var args struct {
		Source     string `json:"source"`
		EntityKind string `json:"entity_kind"`
		EntityID   string `json:"entity_id"`
		Limit      int    `json:"limit"`
	}
	_ = json.Unmarshal(rawArgs, &args)
	dataDir := filepath.Join(opts.Dir, ".tako", "data")
	source := strings.ToLower(strings.TrimSpace(args.Source))
	if source == "" {
		source = "entities"
	}
	limit := args.Limit
	if limit <= 0 {
		limit = 50
	}

	switch source {
	case "users":
		eStore, err := local.OpenEntity(dataDir)
		if err != nil {
			return map[string]any{"error": err.Error()}, true
		}
		defer eStore.Close()
		uStore := local.NewUserStore(eStore)
		accounts, _, err := uStore.List(ctx, limit, "")
		if err != nil {
			return map[string]any{"error": err.Error()}, true
		}
		return map[string]any{"users": accounts}, false

	case "dlq":
		dlqDir := filepath.Join(dataDir, "deadletter")
		var items []backends.DeadLetter
		_ = filepath.WalkDir(dlqDir, func(path string, e os.DirEntry, err error) error {
			if err != nil || e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				return nil
			}
			raw, rErr := os.ReadFile(path)
			if rErr != nil {
				return nil
			}
			if rec, _, decErr := local.DecodeDeadLetter(raw); decErr == nil {
				items = append(items, rec)
			}
			return nil
		})
		return map[string]any{"dead_letters": items}, false

	case "entities":
		eStore, err := local.OpenEntity(dataDir)
		if err != nil {
			return map[string]any{"error": err.Error()}, true
		}
		defer eStore.Close()
		if args.EntityKind == "" {
			// Return declared entity kinds from the app graph.
			var kinds []string
			if g, err := analyzer.Analyze(analyzer.Config{Dir: opts.Dir, Env: opts.Env}); err == nil {
				for _, r := range g.Resources {
					if r.Kind == "entity" {
						kinds = append(kinds, r.Name)
					}
				}
			}
			return map[string]any{"declared_entity_kinds": kinds}, false
		}
		if args.EntityID != "" {
			raw, err := eStore.Get(ctx, args.EntityKind, args.EntityID)
			if err != nil {
				return map[string]any{"error": err.Error()}, true
			}
			var doc any
			_ = json.Unmarshal(raw, &doc)
			return map[string]any{"kind": args.EntityKind, "id": args.EntityID, "entity": doc}, false
		}
		docs, err := eStore.Query(ctx, args.EntityKind, backends.EntityQuery{Limit: limit})
		if err != nil {
			return map[string]any{"error": err.Error()}, true
		}
		var records []any
		for _, raw := range docs {
			var doc any
			if json.Unmarshal(raw, &doc) == nil {
				records = append(records, doc)
			}
		}
		return map[string]any{"kind": args.EntityKind, "count": len(records), "entities": records}, false

	default:
		return map[string]any{"error": fmt.Sprintf("unknown data source %q (expected 'entities', 'dlq', or 'users')", source)}, true
	}
}
