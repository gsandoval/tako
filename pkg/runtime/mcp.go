package runtime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	"github.com/gsandoval/tako/internal/routes"
	"github.com/gsandoval/tako/internal/trace"
)

// MCPToolSpec describes one Model Context Protocol (MCP) tool mounted by
// generated code when an API handler declares tako.Route{Tool: "..."}.
type MCPToolSpec struct {
	// Name is the snake_case MCP tool name.
	Name string
	// Description is the human/LLM description from Route.Tool.
	Description string
	// Route is the underlying HTTP method and pattern (e.g. "POST /orders/{id}").
	Route string
	// InputSchema is the compile-time serialized JSON Schema object.
	InputSchema string
	// PathParams are the {param} names in Route, in order.
	PathParams []string
	// QueryParams are the query:"name" parameter names accepted by the endpoint.
	QueryParams []string
	// HasBody reports whether untagged fields are sent in the JSON request body.
	HasBody bool
}

type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type mcpResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *mcpRPCError    `json:"error,omitempty"`
}

type mcpRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type mcpCallResult struct {
	Content           []mcpContentBlock `json:"content"`
	StructuredContent any               `json:"structuredContent,omitempty"`
	IsError           bool              `json:"isError,omitempty"`
}

// MCPHandler serves the Streamable HTTP JSON-RPC 2.0 Model Context
// Protocol endpoint at POST /_tako/api/mcp for the given app mux and
// declared tool specifications.
//
// Every tools/call dispatches through appMux so Route.SignIn,
// Route.Allow, Route.Idempotent, Route.RateLimit, and distributed
// trace spans apply identically to direct HTTP requests.
func MCPHandler(appMux http.Handler, tools ...MCPToolSpec) http.Handler {
	byName := make(map[string]MCPToolSpec, len(tools))
	for _, t := range tools {
		byName[t.Name] = t
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<20))
		if err != nil {
			writeMCPError(w, nil, -32700, "failed to read request body")
			return
		}
		var req mcpRequest
		if err := json.Unmarshal(raw, &req); err != nil || req.Method == "" {
			writeMCPError(w, req.ID, -32700, "invalid JSON-RPC 2.0 request")
			return
		}

		// JSON-RPC notifications (no id) such as notifications/initialized.
		if len(req.ID) == 0 || string(req.ID) == "null" {
			if strings.HasPrefix(req.Method, "notifications/") {
				w.WriteHeader(http.StatusAccepted)
				return
			}
		}

		switch req.Method {
		case "initialize":
			var initParams struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			_ = json.Unmarshal(req.Params, &initParams)
			proto := "2025-03-26"
			if initParams.ProtocolVersion != "" {
				proto = initParams.ProtocolVersion
			}
			writeMCPResult(w, req.ID, map[string]any{
				"protocolVersion": proto,
				"capabilities": map[string]any{
					"tools": map[string]any{},
				},
				"serverInfo": map[string]any{
					"name":    "tako-app-mcp",
					"version": "1.0.0",
				},
			})

		case "notifications/initialized":
			writeMCPResult(w, req.ID, map[string]any{})

		case "ping":
			writeMCPResult(w, req.ID, map[string]any{})

		case "tools/list":
			list := make([]map[string]any, 0, len(tools))
			for _, t := range tools {
				var schema any = map[string]any{"type": "object"}
				if t.InputSchema != "" {
					var parsed any
					if json.Unmarshal([]byte(t.InputSchema), &parsed) == nil {
						schema = parsed
					}
				}
				list = append(list, map[string]any{
					"name":        t.Name,
					"description": t.Description,
					"inputSchema": schema,
				})
			}
			writeMCPResult(w, req.ID, map[string]any{"tools": list})

		case "tools/call":
			var callParams struct {
				Name      string                     `json:"name"`
				Arguments map[string]json.RawMessage `json:"arguments"`
			}
			if err := json.Unmarshal(req.Params, &callParams); err != nil || callParams.Name == "" {
				writeMCPError(w, req.ID, -32602, "tools/call requires params.name")
				return
			}
			spec, ok := byName[callParams.Name]
			if !ok {
				writeMCPError(w, req.ID, -32602, fmt.Sprintf("unknown tool %q", callParams.Name))
				return
			}
			RefineRoute(r, routes.MCP+":"+spec.Name)
			res := invokeMCPTool(r, appMux, spec, callParams.Arguments)
			writeMCPResult(w, req.ID, res)

		default:
			writeMCPError(w, req.ID, -32601, fmt.Sprintf("method %q not found", req.Method))
		}
	})
}

func invokeMCPTool(origReq *http.Request, appMux http.Handler, spec MCPToolSpec, args map[string]json.RawMessage) mcpCallResult {
	ctx, finish := trace.StartSpan(origReq.Context(), "mcp", spec.Name, "mcp.tool", spec.Name, "mcp.route", spec.Route)
	status := http.StatusOK
	var spanErr error
	defer func() { finish(spanErr) }()

	method := http.MethodGet
	pathPattern := spec.Route
	if i := strings.IndexByte(spec.Route, ' '); i >= 0 {
		method = strings.TrimSpace(spec.Route[:i])
		pathPattern = strings.TrimSpace(spec.Route[i+1:])
	}

	usedKeys := map[string]bool{}
	targetPath := pathPattern
	for _, param := range spec.PathParams {
		usedKeys[param] = true
		rawVal, ok := args[param]
		if !ok {
			status = http.StatusBadRequest
			spanErr = fmt.Errorf("missing required path parameter %q", param)
			return mcpErrorResult(spanErr.Error())
		}
		strVal := unquoteScalar(rawVal)
		escaped := url.PathEscape(strVal)
		if strings.Contains(targetPath, "{"+param+"...}") {
			targetPath = strings.Replace(targetPath, "{"+param+"...}", strVal, 1)
		} else {
			targetPath = strings.Replace(targetPath, "{"+param+"}", escaped, 1)
		}
	}

	q := url.Values{}
	for _, qp := range spec.QueryParams {
		usedKeys[qp] = true
		rawVal, ok := args[qp]
		if !ok || len(rawVal) == 0 || string(rawVal) == "null" {
			continue
		}
		var sliceVal []any
		if json.Unmarshal(rawVal, &sliceVal) == nil {
			for _, item := range sliceVal {
				q.Add(qp, fmt.Sprint(item))
			}
			continue
		}
		q.Set(qp, unquoteScalar(rawVal))
	}
	if encodedQ := q.Encode(); encodedQ != "" {
		targetPath += "?" + encodedQ
	}

	var bodyReader io.Reader
	if spec.HasBody {
		bodyMap := map[string]json.RawMessage{}
		for k, v := range args {
			if !usedKeys[k] {
				bodyMap[k] = v
			}
		}
		encodedBody, err := json.Marshal(bodyMap)
		if err != nil {
			status = http.StatusBadRequest
			spanErr = err
			return mcpErrorResult(err.Error())
		}
		bodyReader = bytes.NewReader(encodedBody)
	}

	subReq, err := http.NewRequestWithContext(ctx, method, targetPath, bodyReader)
	if err != nil {
		status = http.StatusBadRequest
		spanErr = err
		return mcpErrorResult(err.Error())
	}
	if spec.HasBody {
		subReq.Header.Set("Content-Type", "application/json")
	}
	// Forward caller auth, session, safety, flag, and trace headers.
	for _, hdr := range []string{
		"Cookie",
		"Authorization",
		sessionHeader,
		"X-Tako-Device-ID",
		"X-Tako-Flags",
		"Idempotency-Key",
		"X-Cloud-Trace-Context",
		"X-Tako-Request",
		"X-Tako-Parent-Span",
		"X-Forwarded-For",
		"User-Agent",
	} {
		if vals := origReq.Header.Values(hdr); len(vals) > 0 {
			for _, v := range vals {
				subReq.Header.Add(hdr, v)
			}
		}
	}
	subReq.RemoteAddr = origReq.RemoteAddr

	rec := httptest.NewRecorder()
	appMux.ServeHTTP(rec, subReq)
	status = rec.Code
	respBytes := bytes.TrimSpace(rec.Body.Bytes())
	respText := string(respBytes)
	if respText == "" {
		respText = http.StatusText(status)
	}

	var structured any
	_ = json.Unmarshal(respBytes, &structured)

	if status >= 400 {
		spanErr = fmt.Errorf("HTTP %d", status)
		return mcpCallResult{
			Content:           []mcpContentBlock{{Type: "text", Text: respText}},
			StructuredContent: structured,
			IsError:           true,
		}
	}
	return mcpCallResult{
		Content:           []mcpContentBlock{{Type: "text", Text: respText}},
		StructuredContent: structured,
		IsError:           false,
	}
}

func unquoteScalar(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return strings.TrimSpace(string(raw))
}

func mcpErrorResult(msg string) mcpCallResult {
	b, _ := json.Marshal(map[string]string{"error": msg})
	return mcpCallResult{
		Content: []mcpContentBlock{{Type: "text", Text: string(b)}},
		IsError: true,
	}
}

func writeMCPResult(w http.ResponseWriter, id json.RawMessage, result any) {
	writeJSON(w, http.StatusOK, mcpResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	})
}

func writeMCPError(w http.ResponseWriter, id json.RawMessage, code int, msg string) {
	writeJSON(w, http.StatusOK, mcpResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &mcpRPCError{
			Code:    code,
			Message: msg,
		},
	})
}
