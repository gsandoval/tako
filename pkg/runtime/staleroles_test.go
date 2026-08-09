package runtime

// The stale-session refresh: the owner's decision 5, end to end.
//
// Roles live in an Identity Platform custom claim, which only reaches a
// token when the token is reissued — and tako's sessions last seven
// days. So a role granted at 10am would not be felt until the following
// week, which is not what "granted" means to anyone.
//
// The answer is neither a re-read per request nor a forced sign-out:
// the grant marks that account's sessions, and the FIRST request after
// it re-reads exactly one account. Steady state pays nothing.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	tako "github.com/gsandoval/tako"
	"github.com/gsandoval/tako/internal/hooks"
)

func TestARoleChangeLandsOnTheNextRequest(t *testing.T) {
	if err := BootLocal(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	moderator := tako.Role("moderator")

	// An account, and a session for it — the state a signed-in browser
	// is in when somebody grants them something.
	users := tako.Auth(tako.AuthConfig{Password: true}).Users()
	account, err := users.Create(takoCtx(ctx), tako.NewUser{Email: "ana@example.com"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	rec, err := mintSession(ctx, tako.User{ID: account.ID, Email: account.Email})
	if err != nil {
		t.Fatalf("mintSession: %v", err)
	}

	// A guarded route, so the assertion is what a REQUEST sees rather
	// than what the store holds.
	page := tako.Page("GET /desk", func(c tako.Ctx, _ struct{}) (tako.HTML, error) {
		return tako.HTML{}, nil
	}, tako.Route{Allow: tako.RequireRole(moderator)})
	handler := PageHandler(page, nil, RequireAuth(), RequirePolicy("Moderators"))

	get := func() int {
		r := httptest.NewRequest("GET", "/desk", nil)
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: rec.ID})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Code
	}

	if got := get(); got != http.StatusForbidden {
		t.Fatalf("before the grant: %d, want 403", got)
	}

	if err := users.Grant(takoCtx(ctx), account.ID, moderator); err != nil {
		t.Fatalf("Grant: %v", err)
	}

	// No new sign-in, no waiting for the session to expire.
	if got := get(); got != http.StatusOK {
		t.Fatalf("after the grant: %d, want 200 — the role did not land", got)
	}

	// And the refresh happened ONCE: the record is no longer stale, so
	// the next request reads no account at all.
	stored := readSession(t, ctx, rec.ID)
	if stored.Stale {
		t.Error("the session is still marked stale after refreshing")
	}
	if len(stored.Roles) != 1 || stored.Roles[0] != "moderator" {
		t.Errorf("session roles = %v", stored.Roles)
	}

	// Revoking lands the same way, in the other direction.
	if err := users.Revoke(takoCtx(ctx), account.ID, moderator); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if got := get(); got != http.StatusForbidden {
		t.Fatalf("after the revoke: %d, want 403 — the door stayed open", got)
	}
}

// Disabling an account ends its sessions outright. A disabled user who
// stays signed in for a week is not disabled.
func TestDisablingEndsTheSession(t *testing.T) {
	if err := BootLocal(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	users := tako.Auth(tako.AuthConfig{Password: true}).Users()

	account, err := users.Create(takoCtx(ctx), tako.NewUser{Email: "off@example.com"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	rec, err := mintSession(ctx, tako.User{ID: account.ID, Email: account.Email})
	if err != nil {
		t.Fatalf("mintSession: %v", err)
	}

	if err := users.Disable(takoCtx(ctx), account.ID); err != nil {
		t.Fatalf("Disable: %v", err)
	}

	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: rec.ID})
	if loadSession(r) != nil {
		t.Fatal("a disabled account still has a live session")
	}
}

// A grant for somebody with no live session costs nothing and breaks
// nothing — their next sign-in reads the roles fresh.
func TestGrantingToNobodySignedInIsFine(t *testing.T) {
	if err := BootLocal(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	users := tako.Auth(tako.AuthConfig{Password: true}).Users()
	account, err := users.Create(takoCtx(ctx), tako.NewUser{Email: "quiet@example.com"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := users.Grant(takoCtx(ctx), account.ID, tako.Role("editor")); err != nil {
		t.Fatalf("Grant: %v", err)
	}
	got, _ := users.Get(takoCtx(ctx), account.ID)
	if len(got.Roles) != 1 {
		t.Fatalf("roles = %v", got.Roles)
	}
}

func readSession(t *testing.T, ctx context.Context, id string) sessionRecord {
	t.Helper()
	doc, err := hooks.Entity.Get(ctx, sessionKind, id)
	if err != nil {
		t.Fatalf("reading the session: %v", err)
	}
	var rec sessionRecord
	if err := json.Unmarshal(doc, &rec); err != nil {
		t.Fatalf("decoding the session: %v", err)
	}
	return rec
}

// takoCtx adapts a plain context for the SDK calls, which take tako.Ctx.
func takoCtx(ctx context.Context) tako.Ctx {
	return &requestCtx{Context: ctx, log: requestLogger(httptest.NewRequest("GET", "/", nil), "test")}
}
