package gcp

// AB10b, the cloud half. These pin the WIRE: the identitytoolkit paths,
// the field names, and the two shapes that are easy to get subtly wrong
// and impossible to notice locally —
//
//   - customAttributes is a JSON STRING nested inside the JSON body, and
//   - setting it REPLACES the whole claims blob, so a role change has to
//     read what is there first or it silently deletes an app's own claims.
//
// A real project would prove the same things, and cannot run in CI.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gsandoval/tako/pkg/backends"
)

// identityFake is a fake identitytoolkit that records the bodies it was
// sent and answers from a scripted account.
type identityFake struct {
	t        *testing.T
	account  identityUser
	bodies   map[string]map[string]any // path suffix → last body
	paths    []string
	notFound bool
}

func newIdentityFake(t *testing.T, account identityUser) (*Client, *identityFake) {
	t.Helper()
	f := &identityFake{t: t, account: account, bodies: map[string]map[string]any{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, path, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
		path = "/" + path
		f.paths = append(f.paths, r.Method+" "+path)

		var body map[string]any
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&body)
		}
		key := path[strings.LastIndexByte(path, ':')+1:]
		f.bodies[key] = body

		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(path, ":lookup"):
			if f.notFound {
				_ = json.NewEncoder(w).Encode(map[string]any{})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"users": []identityUser{f.account}})
		case strings.HasSuffix(path, ":signUp"):
			f.notFound = false
			_ = json.NewEncoder(w).Encode(map[string]any{"localId": f.account.LocalID})
		case strings.HasSuffix(path, ":batchGet"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"users": []identityUser{f.account}, "nextPageToken": "page-2",
			})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{})
		}
	}))
	t.Cleanup(srv.Close)
	return &Client{HC: srv.Client(), Opts: Options{Project: "p"}, Base: srv.URL}, f
}

func TestAccountCallsHitTheIdentityToolkitPaths(t *testing.T) {
	c, f := newIdentityFake(t, identityUser{LocalID: "uid-1", Email: "ana@example.com"})
	s := NewUserStore(c)
	ctx := context.Background()

	if _, err := s.Get(ctx, "uid-1"); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if _, _, err := s.List(ctx, 10, ""); err != nil {
		t.Fatalf("List: %v", err)
	}
	if err := s.SetDisabled(ctx, "uid-1", true); err != nil {
		t.Fatalf("SetDisabled: %v", err)
	}
	if err := s.Delete(ctx, "uid-1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	want := []string{
		"POST /v1/projects/p/accounts:lookup",
		"GET /v1/projects/p/accounts:batchGet",
		"POST /v1/projects/p/accounts:update",
		"POST /v1/projects/p/accounts:delete",
	}
	for _, w := range want {
		if !hasPath(f.paths, w) {
			t.Errorf("missing call %q; got %v", w, f.paths)
		}
	}
}

// The finding this test exists for: setCustomUserClaims replaces the
// WHOLE blob. An app that keeps its own claim beside tako's must not
// lose it when somebody is granted a role.
func TestGrantingARoleKeepsClaimsTakoDoesNotOwn(t *testing.T) {
	c, f := newIdentityFake(t, identityUser{
		LocalID:          "uid-1",
		Email:            "ana@example.com",
		CustomAttributes: `{"roles":["editor"],"tenant":"acme"}`,
	})
	s := NewUserStore(c)

	if err := s.SetRoles(context.Background(), "uid-1", []string{"editor", "moderator"}); err != nil {
		t.Fatalf("SetRoles: %v", err)
	}

	body := f.bodies["update"]
	raw, _ := body["customAttributes"].(string)
	if raw == "" {
		t.Fatalf("update carried no customAttributes: %v", body)
	}
	var claims map[string]any
	if err := json.Unmarshal([]byte(raw), &claims); err != nil {
		t.Fatalf("customAttributes is not a JSON string holding an object: %v", err)
	}
	if claims["tenant"] != "acme" {
		t.Errorf("a claim tako does not own was dropped: %v", claims)
	}
	roles, _ := claims["roles"].([]any)
	if len(roles) != 2 || roles[0] != "editor" || roles[1] != "moderator" {
		t.Errorf("roles = %v", claims["roles"])
	}
}

// Clearing removes the key rather than writing an empty list, so a blob
// does not accumulate leftovers from every revoke.
func TestClearingRolesRemovesTheClaim(t *testing.T) {
	c, f := newIdentityFake(t, identityUser{
		LocalID: "uid-1", CustomAttributes: `{"roles":["editor"],"tenant":"acme"}`,
	})
	if err := NewUserStore(c).SetRoles(context.Background(), "uid-1", nil); err != nil {
		t.Fatalf("SetRoles(nil): %v", err)
	}
	raw, _ := f.bodies["update"]["customAttributes"].(string)
	var claims map[string]any
	_ = json.Unmarshal([]byte(raw), &claims)
	if _, still := claims["roles"]; still {
		t.Errorf("the roles claim survived being cleared: %v", claims)
	}
	if claims["tenant"] != "acme" {
		t.Errorf("clearing roles dropped another claim: %v", claims)
	}
}

// Roles come back off the nested claims document, not off a top-level
// field — the shape a record() written from memory gets wrong.
func TestRolesAreReadFromTheNestedClaims(t *testing.T) {
	c, _ := newIdentityFake(t, identityUser{
		LocalID: "uid-1", CustomAttributes: `{"roles":["moderator"]}`,
	})
	rec, err := NewUserStore(c).Get(context.Background(), "uid-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(rec.Roles) != 1 || rec.Roles[0] != "moderator" {
		t.Fatalf("roles = %v", rec.Roles)
	}
}

// A lookup that matches nothing answers 200 with an empty body, not a
// 404. Read literally that is "success, here is a blank account".
func TestAnEmptyLookupIsNotFound(t *testing.T) {
	c, f := newIdentityFake(t, identityUser{})
	f.notFound = true
	_, err := NewUserStore(c).ByEmail(context.Background(), "nobody@example.com")
	if !errors.Is(err, backends.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// Create refuses a taken address with the error whose remedy is obvious,
// rather than relaying the API's generic 400.
func TestCreateRefusesATakenAddress(t *testing.T) {
	c, _ := newIdentityFake(t, identityUser{LocalID: "uid-1", Email: "ana@example.com"})
	_, err := NewUserStore(c).Create(context.Background(), backends.NewUserSpec{Email: "ana@example.com"})
	if !errors.Is(err, backends.ErrEmailTaken) {
		t.Fatalf("err = %v, want ErrEmailTaken", err)
	}
}

// The two 403s are different problems with different remediations, and
// an operator reads the message before they read the code.
func TestThePermissionFailuresSayWhatToDo(t *testing.T) {
	for _, c := range []struct {
		name, body, want string
	}{
		{"missing grant", `{"error":{"message":"PERMISSION_DENIED"}}`, "re-run tako deploy"},
		{"api disabled", `{"error":{"message":"SERVICE_DISABLED: identitytoolkit has not been used in project p"}}`, "enable identitytoolkit.googleapis.com"},
	} {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, c.body, http.StatusForbidden)
			}))
			t.Cleanup(srv.Close)
			s := NewUserStore(&Client{HC: srv.Client(), Opts: Options{Project: "p"}, Base: srv.URL})
			_, err := s.Get(context.Background(), "uid-1")
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want it to say %q", err, c.want)
			}
		})
	}
}

func hasPath(paths []string, want string) bool {
	for _, p := range paths {
		if p == want {
			return true
		}
	}
	return false
}
