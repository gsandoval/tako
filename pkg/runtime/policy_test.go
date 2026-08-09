package runtime

// AB1, the mount half: a policy's refusal is a PAGE, and a status page
// can tell it from every other 403.
//
// The rule itself runs in Endpoint.Call (one enforcement point for
// browsers, in-process callers and cross-service calls alike), so what
// these pin is what the mount adds: the name, the status, and the fact
// that CSRF's 403 on the same route does not borrow it.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	tako "github.com/gsandoval/tako"
)

var testEditors = tako.Policy(func(ctx tako.Ctx, u tako.User) error {
	if !strings.HasSuffix(u.Email, ".test") {
		return tako.Deny("that address is not on the editor list for this site")
	}
	return nil
})

var guardedPage = tako.Page("GET /admin", func(ctx tako.Ctx, _ struct{}) (tako.HTML, error) {
	return tako.HTML{}, nil
}, tako.Route{Allow: testEditors})

var guardedPost = tako.Page("POST /admin/save", func(ctx tako.Ctx, _ struct{}) (tako.HTML, error) {
	return tako.HTML{}, nil
}, tako.Route{Allow: testEditors})

// policySite renders a status page that prints the two things a refusal
// has to distinguish: the code, and WHICH door said no.
func policySite(t *testing.T) {
	t.Helper()
	fsys := fstest.MapFS{
		"templates/layout.html": {Data: []byte(`{{ define "layout.html" }}<html>{{ block "content" . }}{{ end }}</html>{{ end }}`)},
		"templates/status.html": {Data: []byte(`{{ define "content" }}{{ .Code }}|policy={{ .Policy }}|{{ .Message }}{{ end }}`)},
	}
	RegisterSiteTemplates(fsys, SiteOptions{})
	t.Cleanup(func() { RegisterSiteTemplates(fstest.MapFS{"templates/x": {Data: []byte("")}}, SiteOptions{}) })
	site := tako.Site(".")
	withStatusPage(t, func(ctx tako.Ctx, s tako.Status) (tako.HTML, error) {
		return site.Render(ctx, "status.html", s)
	})
}

func signedIn(t *testing.T, email string) *sessionRecord {
	t.Helper()
	rec, err := mintSession(context.Background(), tako.User{ID: email, Email: email, Name: email})
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func TestAPolicyRefusalIsAPageThatNamesTheDoor(t *testing.T) {
	if err := BootLocal(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	policySite(t)
	handler := PageHandler(guardedPage, nil, RequireAuth(), RequirePolicy("Editors"))

	t.Run("a stranger", func(t *testing.T) {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/admin", nil)
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: signedIn(t, "nobody@example.com").ID})
		handler.ServeHTTP(rec, r)

		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "policy=Editors") {
			t.Errorf("the refusal does not name its policy: %q", body)
		}
		if !strings.Contains(body, "that address is not on the editor list") {
			t.Errorf("the refusal lost its public text: %q", body)
		}
	})

	t.Run("an editor", func(t *testing.T) {
		rec := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/admin", nil)
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: signedIn(t, "alice@example.test").ID})
		handler.ServeHTTP(rec, r)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 — an editor was refused: %s", rec.Code, rec.Body.String())
		}
	})

	// Allow implies SignIn, so no session is a 401 before the rule runs.
	t.Run("nobody", func(t *testing.T) {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest("GET", "/admin", nil))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
		if body := rec.Body.String(); strings.Contains(body, "policy=Editors") {
			t.Errorf("a missing session was blamed on the policy: %q", body)
		}
	})
}

// The distinction the first app to need this had to sniff a sentinel
// string for: several different things answer 403 on a guarded route,
// and only ONE of them is the door. Telling an editor who tripped CSRF
// that they are not an editor sends them to ask for access they already
// have.
//
// Both cases below are 403 on a route that HAS a policy, so each one can
// only pass if the name rides on the denial rather than on the route.
func TestAnother403OnAGuardedRouteIsNotBlamedOnTheDoor(t *testing.T) {
	if err := BootLocal(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	policySite(t)

	// (1) The handler's own 403. It got past the policy — the rule
	// admitted this editor — and then refused for a reason of its own.
	ownRefusal := tako.Page("GET /admin/own", func(ctx tako.Ctx, _ struct{}) (tako.HTML, error) {
		return tako.HTML{}, tako.Errorf(403, "this draft is locked by another editor")
	}, tako.Route{Allow: testEditors})

	rec := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/admin/own", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: signedIn(t, "alice@example.test").ID})
	PageHandler(ownRefusal, nil, RequireAuth(), RequirePolicy("Editors")).ServeHTTP(rec, r)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "policy=|") {
		t.Errorf("a handler's own 403 was attributed to the policy: %q", body)
	}

	// (2) CSRF. It is refused before the handler and before the rule, so
	// this also pins that the earlier gate does not learn the name.
	rec = httptest.NewRecorder()
	r = httptest.NewRequest("POST", "/admin/save", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: signedIn(t, "alice@example.test").ID})
	PageHandler(guardedPost, nil, RequireAuth(), RequirePolicy("Editors")).ServeHTTP(rec, r)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("CSRF status = %d, want 403", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "policy=|") {
		t.Errorf("CSRF's 403 claims a policy refused it: %q", body)
	}
	if !strings.Contains(body, csrfRejection) {
		t.Errorf("body = %q, want the CSRF text", body)
	}
}
