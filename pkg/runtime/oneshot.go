package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	tako "github.com/gsandoval/tako"
	"github.com/gsandoval/tako/internal/trace"
)

// OneShotMarker prefixes the single-line JSON output emitted by a
// one-shot handler invocation (`tako call` / `tako_call` MCP tool),
// separating it from any console log lines emitted during boot or execution.
const OneShotMarker = "__TAKO_ONESHOT_RESULT__:"

// OneShotSpec is the JSON-encoded specification passed via TAKO_ONESHOT
// to a generated service binary.
type OneShotSpec struct {
	Kind           string                     `json:"kind"`            // "api", "page", "worker", "subscribe", "cron", "job"
	HandlerVar     string                     `json:"handler_var"`     // e.g. "shop.CreateOrder"
	ShortVar       string                     `json:"short_var"`       // e.g. "CreateOrder"
	Route          string                     `json:"route,omitempty"` // e.g. "POST /orders/{id}"
	PathParams     []string                   `json:"path_params,omitempty"`
	QueryParams    []string                   `json:"query_params,omitempty"`
	HasBody        bool                       `json:"has_body,omitempty"`
	Input          map[string]json.RawMessage `json:"input,omitempty"`
	RawPayload     json.RawMessage            `json:"raw_payload,omitempty"`
	AsUser         string                     `json:"as_user,omitempty"`
	Roles          []string                   `json:"roles,omitempty"`
	Flags          map[string]string          `json:"flags,omitempty"`
	IdempotencyKey string                     `json:"idempotency_key,omitempty"`
	DeviceID       string                     `json:"device_id,omitempty"`
}

// OneShotResult is the structured JSON envelope returned by a one-shot
// handler invocation.
type OneShotResult struct {
	OK          bool         `json:"ok"`
	Handler     string       `json:"handler"`
	Kind        string       `json:"kind"`
	Route       string       `json:"route,omitempty"`
	Status      int          `json:"status"`
	DurationMs  float64      `json:"duration_ms"`
	TraceID     string       `json:"trace_id,omitempty"`
	ContentType string       `json:"content_type,omitempty"`
	Response    any          `json:"response,omitempty"`
	RawBody     string       `json:"raw_body,omitempty"`
	Error       string       `json:"error,omitempty"`
	Spans       []trace.Span `json:"spans,omitempty"`
}

var asyncHandlers sync.Map // shortVar -> func(tako.Ctx, []byte) error

func recordAsyncHandler(name string, fn func(tako.Ctx, []byte) error) {
	asyncHandlers.Store(name, fn)
}

type memorySpanCollector struct {
	mu    sync.Mutex
	spans []trace.Span
}

func (m *memorySpanCollector) RecordSpan(s trace.Span) {
	m.mu.Lock()
	m.spans = append(m.spans, s)
	m.mu.Unlock()
}

func (m *memorySpanCollector) Spans() []trace.Span {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]trace.Span, len(m.spans))
	copy(out, m.spans)
	return out
}

func runOneShot(h http.Handler, rawSpec string) {
	var spec OneShotSpec
	if err := json.Unmarshal([]byte(rawSpec), &spec); err != nil {
		emitOneShot(OneShotResult{
			OK:     false,
			Status: 400,
			Error:  "invalid TAKO_ONESHOT spec: " + err.Error(),
		})
		return
	}

	collector := &memorySpanCollector{}
	prevSink := trace.GlobalSink()
	trace.SetGlobalSink(collector)
	defer trace.SetGlobalSink(prevSink)

	start := time.Now()
	var res OneShotResult
	switch spec.Kind {
	case "api", "page", "":
		res = executeOneShotHTTP(h, spec)
	default:
		res = executeOneShotBackground(spec)
	}
	res.DurationMs = float64(time.Since(start).Microseconds()) / 1000.0
	res.Spans = collector.Spans()
	if res.TraceID == "" && len(res.Spans) > 0 {
		res.TraceID = res.Spans[0].TraceID
	}

	runShutdownHooks(2 * time.Second)
	emitOneShot(res)
}

func executeOneShotHTTP(h http.Handler, spec OneShotSpec) OneShotResult {
	method := http.MethodGet
	pathPattern := spec.Route
	if i := strings.IndexByte(spec.Route, ' '); i >= 0 {
		method = strings.TrimSpace(spec.Route[:i])
		pathPattern = strings.TrimSpace(spec.Route[i+1:])
	}
	pathPattern = strings.ReplaceAll(pathPattern, "{$}", "")
	if pathPattern == "" {
		pathPattern = "/"
	}

	usedKeys := map[string]bool{}
	targetPath := pathPattern
	for _, param := range spec.PathParams {
		usedKeys[param] = true
		rawVal, ok := spec.Input[param]
		if !ok {
			return OneShotResult{
				OK:      false,
				Handler: spec.HandlerVar,
				Kind:    spec.Kind,
				Route:   spec.Route,
				Status:  http.StatusBadRequest,
				Error:   fmt.Sprintf("missing required path parameter %q in input JSON", param),
			}
		}
		strVal := unquoteScalar(rawVal)
		if strings.Contains(targetPath, "{"+param+"...}") {
			targetPath = strings.Replace(targetPath, "{"+param+"...}", strVal, 1)
		} else {
			targetPath = strings.Replace(targetPath, "{"+param+"}", url.PathEscape(strVal), 1)
		}
	}

	q := url.Values{}
	for _, qp := range spec.QueryParams {
		usedKeys[qp] = true
		rawVal, ok := spec.Input[qp]
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
		for k, v := range spec.Input {
			if !usedKeys[k] {
				bodyMap[k] = v
			}
		}
		if len(bodyMap) == 0 && len(spec.RawPayload) > 0 {
			bodyReader = bytes.NewReader(spec.RawPayload)
		} else {
			encodedBody, _ := json.Marshal(bodyMap)
			bodyReader = bytes.NewReader(encodedBody)
		}
	}

	req := httptest.NewRequest(method, targetPath, bodyReader)
	if spec.HasBody {
		req.Header.Set("Content-Type", "application/json")
	}
	if spec.IdempotencyKey != "" {
		req.Header.Set("Idempotency-Key", spec.IdempotencyKey)
	}
	if spec.DeviceID != "" {
		req.Header.Set("X-Tako-Device-ID", spec.DeviceID)
	}
	if len(spec.Flags) > 0 {
		if hdr := trace.EncodeFlagsHeader(spec.Flags); hdr != "" {
			req.Header.Set(trace.FlagsHeader, hdr)
		}
	}
	if spec.AsUser != "" {
		u, err := devAccount(context.Background(), spec.AsUser, spec.Roles, len(spec.Roles) > 0, "", "dev")
		if err != nil {
			u = tako.User{ID: "dev:" + spec.AsUser, Email: spec.AsUser + "@dev.local", Name: spec.AsUser, Roles: spec.Roles}
		}
		if rec, err := mintSession(context.Background(), u); err == nil {
			req.AddCookie(&http.Cookie{Name: sessionCookie, Value: rec.ID})
		}
	}

	rec := httptest.NewRecorder()
	logRequests(h).ServeHTTP(rec, req)

	respBytes := bytes.TrimSpace(rec.Body.Bytes())
	ct := rec.Header().Get("Content-Type")
	out := OneShotResult{
		OK:          rec.Code >= 200 && rec.Code < 400,
		Handler:     spec.HandlerVar,
		Kind:        spec.Kind,
		Route:       spec.Route,
		Status:      rec.Code,
		ContentType: ct,
	}
	var parsed any
	if len(respBytes) > 0 && json.Unmarshal(respBytes, &parsed) == nil {
		out.Response = parsed
	} else if len(respBytes) > 0 {
		out.RawBody = string(respBytes)
	}
	if !out.OK {
		if m, ok := out.Response.(map[string]any); ok {
			if errStr, ok := m["error"].(string); ok {
				out.Error = errStr
			}
		}
		if out.Error == "" && len(respBytes) > 0 {
			out.Error = string(respBytes)
		}
	}
	return out
}

func executeOneShotBackground(spec OneShotSpec) OneShotResult {
	var fn func(tako.Ctx, []byte) error
	if spec.Kind == "cron" {
		if v, ok := cronHandlers.Load(spec.ShortVar); ok {
			fn, _ = v.(func(tako.Ctx, []byte) error)
		}
	}
	if fn == nil {
		if v, ok := asyncHandlers.Load(spec.ShortVar); ok {
			fn, _ = v.(func(tako.Ctx, []byte) error)
		}
	}
	if fn == nil {
		return OneShotResult{
			OK:      false,
			Handler: spec.HandlerVar,
			Kind:    spec.Kind,
			Status:  http.StatusNotFound,
			Error:   fmt.Sprintf("handler %q (%s) is not registered in this service", spec.ShortVar, spec.Kind),
		}
	}

	payload := spec.RawPayload
	if len(payload) == 0 && len(spec.Input) > 0 {
		payload, _ = json.Marshal(spec.Input)
	}
	if len(payload) == 0 {
		payload = []byte(`{}`)
	}

	baseCtx := context.Background()
	if len(spec.Flags) > 0 {
		baseCtx = trace.WithPropagatedFlags(baseCtx, spec.Flags)
	}
	ctx, tc, finish := trace.StartRoot(baseCtx, spec.Kind, spec.ShortVar, os.Getenv("TAKO_SERVICE"), newRequestID(), "", "")
	ctx = pinFlagsOnCtx(ctx)
	rctx := &requestCtx{Context: ctx, log: slog.Default().With(spec.Kind, spec.ShortVar)}
	if spec.AsUser != "" {
		rctx.user = &tako.User{ID: "dev:" + spec.AsUser, Email: spec.AsUser + "@dev.local", Name: spec.AsUser, Roles: spec.Roles}
	}

	err := fn(rctx, payload)
	status := http.StatusOK
	if err != nil {
		status = http.StatusInternalServerError
	}
	finish(status, err)
	if err != nil {
		return OneShotResult{
			OK:      false,
			Handler: spec.HandlerVar,
			Kind:    spec.Kind,
			Status:  status,
			TraceID: tc.TraceID,
			Error:   err.Error(),
		}
	}
	return OneShotResult{
		OK:       true,
		Handler:  spec.HandlerVar,
		Kind:     spec.Kind,
		Status:   status,
		TraceID:  tc.TraceID,
		Response: map[string]string{"status": "completed"},
	}
}

func emitOneShot(res OneShotResult) {
	b, _ := json.Marshal(res)
	fmt.Printf("\n%s%s\n", OneShotMarker, b)
}
