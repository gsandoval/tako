package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	tako "github.com/gsandoval/tako"
	"github.com/gsandoval/tako/internal/hooks"
)

// Cross-service call headers (§2.12 + the W9 session amendment).
const (
	sessionHeader  = "X-Tako-Session"  // caller's session id; the callee VERIFIES by record lookup
	deadlineHeader = "X-Tako-Deadline" // absolute RFC3339Nano deadline the callee honors
	requestHeader  = "X-Tako-Request"  // trace id, so tako logs correlates a chain
)

// Deadline budget policy: propagate the caller's remaining deadline
// minus the margin; below the floor, fail before the network does.
const (
	deadlineMargin = 500 * time.Millisecond
	deadlineFloor  = time.Second
)

type ctxKey int

const (
	sessionIDKey ctxKey = iota
	requestIDKey
)

// RegisterRemoteCall wires the remote transport for one endpoint —
// generated mains call it at boot for each REMOTE callee (never for
// co-located packages, so dev and tests stay direct in-process).
// transport is the generated typed closure func(tako.Ctx, Req) (Res, error).
func RegisterRemoteCall(pkgPath, route string, transport any) {
	hooks.RegisterRemoteCall(pkgPath+" "+route, transport)
}

// callError carries the callee's status through the caller — billing's
// 422 is placeOrder's 422 to handle, never a mystery 500.
type callError struct {
	status  int
	message string
}

func (e *callError) Error() string   { return e.message }
func (e *callError) HTTPStatus() int { return e.status }

// RemoteJSON is the wire half of a generated remote-call closure: one
// authenticated HTTP round trip over the endpoint's OWN route. The
// closure has already rendered path params into path; req rides as the
// JSON body exactly as the generated decoder expects.
func RemoteJSON[Req, Res any](ctx tako.Ctx, service, base, method, path string, req Req) (Res, error) {
	var zero Res
	callCtx := context.Context(ctx)
	var deadline time.Time
	if d, ok := ctx.Deadline(); ok {
		remaining := time.Until(d) - deadlineMargin
		if remaining < deadlineFloor {
			return zero, &callError{status: http.StatusGatewayTimeout,
				message: fmt.Sprintf("deadline budget exhausted before calling %s (%s remaining after the %s margin)", service, remaining.Round(time.Millisecond), deadlineMargin)}
		}
		deadline = time.Now().Add(remaining)
		var cancel context.CancelFunc
		callCtx, cancel = context.WithDeadline(callCtx, deadline)
		defer cancel()
	}

	raw, err := json.Marshal(req)
	if err != nil {
		return zero, err
	}
	httpReq, err := http.NewRequestWithContext(callCtx, method, base+path, bytes.NewReader(raw))
	if err != nil {
		return zero, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	token, err := identityToken(callCtx, base)
	if err != nil {
		return zero, fmt.Errorf("minting the OIDC token for %s: %w", service, err)
	}
	if token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+token)
	}
	if sid, ok := ctx.Value(sessionIDKey).(string); ok && sid != "" {
		httpReq.Header.Set(sessionHeader, sid)
	}
	if !deadline.IsZero() {
		httpReq.Header.Set(deadlineHeader, deadline.UTC().Format(time.RFC3339Nano))
	}
	if rid, ok := ctx.Value(requestIDKey).(string); ok && rid != "" {
		httpReq.Header.Set(requestHeader, rid)
	}

	res, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return zero, fmt.Errorf("calling %s: %w", service, err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode/100 != 2 {
		return zero, remoteError(service, res.StatusCode, body)
	}
	var out Res
	if err := json.Unmarshal(body, &out); err != nil {
		return zero, fmt.Errorf("decoding %s's response: %w", service, err)
	}
	return out, nil
}

// remoteError maps a callee failure to something actionable. Front-door
// IAM answers (401/403, non-JSON) get the grant remediation; a typed
// error body passes through with its own status.
func remoteError(service string, status int, body []byte) error {
	var payload struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &payload) == nil && payload.Error != "" {
		return &callError{status: status, message: payload.Error}
	}
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return &callError{status: http.StatusBadGateway,
			message: fmt.Sprintf("this service cannot invoke %s — the derived grant run.invoker is missing or still propagating; re-run tako deploy, and file it if it persists", service)}
	case http.StatusNotFound:
		return &callError{status: http.StatusBadGateway,
			message: fmt.Sprintf("%s answered 404 — its URL may have changed; re-run tako deploy", service)}
	}
	return &callError{status: http.StatusBadGateway,
		message: fmt.Sprintf("%s answered HTTP %d: %s", service, status, strings.TrimSpace(string(body)))}
}

// identityToken mints (and caches) an OIDC ID token for the audience
// from the metadata server — this service's own identity, validated by
// Cloud Run at the callee's front door. Overridable in tests; empty on
// hosts without a metadata server only if the override says so.
var identityToken = metadataIdentityToken

var tokenCache sync.Map // audience → cachedToken

type cachedToken struct {
	token   string
	expires time.Time
}

func metadataIdentityToken(ctx context.Context, audience string) (string, error) {
	if c, ok := tokenCache.Load(audience); ok {
		if t := c.(cachedToken); time.Now().Before(t.expires) {
			return t.token, nil
		}
	}
	u := "http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/identity?audience=" + audience
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Metadata-Flavor", "Google")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("no metadata server: %w", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 {
		return "", fmt.Errorf("metadata identity: HTTP %d", res.StatusCode)
	}
	token := strings.TrimSpace(string(raw))
	// ID tokens live 1h; refresh comfortably early.
	tokenCache.Store(audience, cachedToken{token: token, expires: time.Now().Add(50 * time.Minute)})
	return token, nil
}

// callContext threads the cross-service context values (and the
// propagated deadline) into a handler's base context.
func callContext(r *http.Request, session *sessionRecord, reqID string) (context.Context, context.CancelFunc) {
	base := r.Context()
	if session != nil {
		base = context.WithValue(base, sessionIDKey, session.ID)
	}
	base = context.WithValue(base, requestIDKey, reqID)
	if h := r.Header.Get(deadlineHeader); h != "" {
		if t, err := time.Parse(time.RFC3339Nano, h); err == nil && time.Until(t) > 0 {
			return context.WithDeadline(base, t)
		}
	}
	return base, func() {}
}

// incomingRequestID reuses a propagated trace id, else mints one.
func incomingRequestID(r *http.Request) string {
	if rid := r.Header.Get(requestHeader); rid != "" {
		return rid
	}
	return newRequestID()
}
