package runtime

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gsandoval/tako/internal/routes"
)

func echoes(name string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, name)
	})
}

// appMux is the shape that used to be fatal: a site at "/" plus wildcard
// page routes at every arity a real app reaches for.
func appMux() *http.ServeMux {
	m := http.NewServeMux()
	m.Handle("GET /{section}/{slug}", echoes("page2"))
	m.Handle("GET /{year}/{month}/{slug}", echoes("page3"))
	m.Handle("GET /{user}/{album}/{photo}/{size}", echoes("page4"))
	m.Handle("POST /api/orders", echoes("user-api"))
	m.Handle("/", echoes("site"))
	return m
}

func frameworkMux() *http.ServeMux {
	m := http.NewServeMux()
	for _, mount := range routes.All {
		m.Handle(mount.Pattern, echoes(mount.Name))
	}
	return m
}

// Every framework mount, beside every wildcard arity, on one mux. This is
// the FL5 bug, and it is a registration-time panic rather than a bad
// response — the service dies before serving anything.
func TestOneMuxCannotHoldBothAndSaysSoLoudly(t *testing.T) {
	for _, mount := range routes.All {
		t.Run(mount.Name, func(t *testing.T) {
			panicked := func() (p bool) {
				defer func() { p = recover() != nil }()
				m := appMux()
				m.Handle(mount.Pattern, echoes(mount.Name))
				return false
			}()
			// Method-scoped mounts survive; the method-less ones are the
			// two that crashed real apps. Either way the point is that
			// the answer depends on the app's routes, which is exactly
			// what a framework mount must not do.
			methodless := mount.Pattern == routes.PatternSession || mount.Pattern == routes.PatternSigned
			if panicked != methodless {
				t.Fatalf("%s on the app's mux: panicked=%v, want %v", mount.Name, panicked, methodless)
			}
		})
	}
}

// The same set, dispatched instead of shared. Nothing panics, at any
// arity, for any mount.
func TestReservedRegistersEveryMountBesideEveryWildcard(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("registration panicked: %v", r)
		}
	}()
	Reserved(frameworkMux(), appMux())
}

func TestReservedRoutesEachSideToItsOwner(t *testing.T) {
	h := Reserved(frameworkMux(), appMux())
	for _, c := range []struct {
		method, path, want string
	}{
		{"POST", routes.Session, "Session"},
		{"DELETE", routes.Session, "Session"},
		{"POST", routes.SignOut, "SignOut"},
		{"POST", routes.Push, "Push"},
		{"PUT", routes.Signed + "tok123", "Signed"},
		{"POST", routes.CronRoute("Cleanup"), "Cron"},
		{"GET", routes.SignIn, "SignIn"},

		{"GET", "/opinion/una-nota", "page2"},
		{"GET", "/2026/08/una-nota", "page3"},
		{"GET", "/memo/trip/img/large", "page4"},
		{"POST", "/api/orders", "user-api"},
		{"GET", "/assets/css/site.css", "page3"}, // the app's problem, not ours
		{"GET", "/favicon.ico", "site"},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(c.method, c.path, nil))
		if got := rec.Body.String(); got != c.want {
			t.Errorf("%s %s → %q, want %q", c.method, c.path, got, c.want)
		}
	}
}

// An app cannot reach into the namespace, and tako's 404 is the one that
// answers — not the site's, which would suggest the path was ever the
// app's to serve.
func TestTheNamespaceIsNotTheAppsToServe(t *testing.T) {
	app := appMux()
	app.Handle("GET /_tako/sneaky", echoes("USER"))
	app.Handle("GET /_tako/z/dashboard", echoes("USER-Z"))
	h := Reserved(frameworkMux(), app)

	for _, path := range []string{"/_tako/sneaky", "/_tako/z/dashboard", "/_tako/z/", "/_tako/nope", "/_tako"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s → %d %q, want 404 from tako's mux", path, rec.Code, rec.Body.String())
		}
	}
}

// The handler keeps its own 405. On a shared mux this could not be true:
// method-scoping the mount was the only way to avoid the panic there,
// and it would have sent an unlisted method to the site's 404 instead.
func TestAnUnlistedMethodIsStillTheHandlersToRefuse(t *testing.T) {
	fw := http.NewServeMux()
	fw.Handle(routes.PatternSession, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost && r.Method != http.MethodDelete {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		fmt.Fprint(w, "Session")
	}))
	h := Reserved(fw, appMux())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("PUT", routes.Session, nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("PUT %s → %d, want 405 from the session handler", routes.Session, rec.Code)
	}
}

// A path that only becomes reserved after cleaning must not slide past
// the check. The app's mux cleans and redirects, and the clean form
// re-enters through Reserved rather than being served by whatever the
// app has at that shape.
func TestAPathThatCleansIntoTheNamespaceIsRedirectedNotServed(t *testing.T) {
	h := Reserved(frameworkMux(), appMux())
	for _, path := range []string{"/foo/../_tako/signin", "//_tako/signin"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code < 300 || rec.Code >= 400 {
			t.Fatalf("GET %s → %d %q, want a redirect to the clean path", path, rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Location"); got != routes.SignIn {
			t.Fatalf("GET %s redirected to %q, want %q", path, got, routes.SignIn)
		}
	}
}
