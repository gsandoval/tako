package runtime

// The W9 wire contract: a registered remote transport carries OIDC,
// the caller's session id, the deadline budget, and the trace id; the
// callee's typed errors pass through with their own status; IAM
// front-door refusals surface the grant remediation.

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tako "github.com/gsandoval/tako"
)

type chargeReq struct {
	OrderID     string `json:"orderId"`
	AmountCents int    `json:"amountCents"`
}

type receipt struct {
	ID          string `json:"id"`
	AmountCents int    `json:"amountCents"`
}

func fakeToken(t *testing.T) {
	t.Helper()
	old := identityToken
	identityToken = func(context.Context, string) (string, error) { return "fake-oidc-token", nil }
	t.Cleanup(func() { identityToken = old })
}

// chainCtx builds the ctx a handler mid-request would carry: session
// and trace ids as values, an optional deadline.
func chainCtx(deadline time.Duration) tako.Ctx {
	base := context.WithValue(context.Background(), sessionIDKey, "sess-123")
	base = context.WithValue(base, requestIDKey, "req-abc")
	if deadline > 0 {
		base, _ = context.WithDeadline(base, time.Now().Add(deadline))
	}
	return &requestCtx{Context: base, log: slog.Default()}
}

func TestRemoteCallCarriesTheChain(t *testing.T) {
	fakeToken(t)
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		var req chargeReq
		_ = json.NewDecoder(r.Body).Decode(&req)
		writeJSON(w, 200, receipt{ID: "r1", AmountCents: req.AmountCents})
	}))
	t.Cleanup(srv.Close)

	ep := tako.API("POST /w9-charge", func(tako.Ctx, chargeReq) (receipt, error) {
		t.Fatal("the local handler must not run when a remote transport is wired")
		return receipt{}, nil
	})
	RegisterRemoteCall("github.com/gsandoval/tako/pkg/runtime", "POST /w9-charge",
		func(ctx tako.Ctx, req chargeReq) (receipt, error) {
			return RemoteJSON[chargeReq, receipt](ctx, "billing", srv.URL, "POST", "/w9-charge", req)
		})

	res, err := ep.Call(chainCtx(10*time.Second), chargeReq{OrderID: "o1", AmountCents: 700})
	if err != nil || res.ID != "r1" || res.AmountCents != 700 {
		t.Fatalf("call = %+v, %v", res, err)
	}
	if got.Get("Authorization") != "Bearer fake-oidc-token" {
		t.Fatalf("Authorization = %q", got.Get("Authorization"))
	}
	if got.Get(sessionHeader) != "sess-123" {
		t.Fatalf("session header = %q — the user did not propagate", got.Get(sessionHeader))
	}
	if got.Get(requestHeader) != "req-abc" {
		t.Fatalf("trace header = %q", got.Get(requestHeader))
	}
	if d := got.Get(deadlineHeader); d == "" {
		t.Fatal("no deadline header — budgets must propagate")
	} else if parsed, err := time.Parse(time.RFC3339Nano, d); err != nil || time.Until(parsed) > 10*time.Second {
		t.Fatalf("deadline %q is not the margin-reduced budget", d)
	}
}

func TestRemoteCallErrorsPassThroughTyped(t *testing.T) {
	fakeToken(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 422, map[string]string{"error": "amount must be positive"})
	}))
	t.Cleanup(srv.Close)

	_, err := RemoteJSON[chargeReq, receipt](chainCtx(0), "billing", srv.URL, "POST", "/charge", chargeReq{})
	he, ok := err.(interface{ HTTPStatus() int })
	if !ok || he.HTTPStatus() != 422 || !strings.Contains(err.Error(), "amount must be positive") {
		t.Fatalf("err = %v — billing's 422 must surface as the caller's 422", err)
	}
}

func TestRemoteCallGrantRefusalIsReadable(t *testing.T) {
	fakeToken(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "<html>Forbidden</html>", http.StatusForbidden) // the IAM front door speaks HTML
	}))
	t.Cleanup(srv.Close)

	_, err := RemoteJSON[chargeReq, receipt](chainCtx(0), "billing", srv.URL, "POST", "/charge", chargeReq{})
	if err == nil || !strings.Contains(err.Error(), "run.invoker") {
		t.Fatalf("err = %v — a front-door 403 must name the missing grant", err)
	}
}

func TestDeadlineBudgetFloor(t *testing.T) {
	fakeToken(t)
	_, err := RemoteJSON[chargeReq, receipt](chainCtx(800*time.Millisecond), "billing", "http://unreachable.invalid", "POST", "/charge", chargeReq{})
	if err == nil || !strings.Contains(err.Error(), "deadline budget exhausted") {
		t.Fatalf("err = %v — below the floor the call must fail before the network", err)
	}
}

func TestCalleeHonorsPropagatedChain(t *testing.T) {
	if err := BootLocal(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	// Mint a session the callee can resolve from the HEADER — the
	// verification IS the record lookup.
	rec, err := mintSession(context.Background(), tako.User{ID: "alice", Email: "a@example.test", Name: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	who := tako.API("GET /whoami", func(ctx tako.Ctx, _ struct{}) (string, error) {
		u, ok := ctx.MaybeUser()
		if !ok {
			return "anonymous", nil
		}
		return u.ID, nil
	})
	srv := httptest.NewServer(JSONHandler(who, nil))
	t.Cleanup(srv.Close)

	get := func(header, value string) string {
		req, _ := http.NewRequest("GET", srv.URL, nil)
		if header != "" {
			req.Header.Set(header, value)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out string
		_ = json.NewDecoder(res.Body).Decode(&out)
		return out
	}
	if got := get(sessionHeader, rec.ID); got != "alice" {
		t.Fatalf("propagated session resolved to %q, want alice", got)
	}
	if got := get("", ""); got != "anonymous" {
		t.Fatalf("no session = %q, want anonymous", got)
	}
	if got := get(sessionHeader, "forged-or-revoked"); got != "anonymous" {
		t.Fatalf("bogus session id resolved to %q — the lookup is the verification", got)
	}
}
