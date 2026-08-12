package runtime

// CR1: ?next= is the one piece of attacker-controlled text tako's own
// pages interpolate, and it reached three sinks with three different
// escapes — two of them absent.
//
// The origin guard is one function, and these are its rules. The
// per-sink escaping is tested through the pages themselves, because a
// correct safeNext cannot save a sink that forgets to escape: a path
// may legally contain a quote or an angle bracket.

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSafeNextAdmitsOnlySameOriginPaths(t *testing.T) {
	for _, ok := range []string{"/", "/write", "/a/b?c=d", "/p/1#top"} {
		if got := safeNext(ok); got != ok {
			t.Errorf("safeNext(%q) = %q, want it kept", ok, got)
		}
	}
	for _, bad := range []string{
		"",
		"write",                // relative: not ours to resolve
		"https://evil.example", //
		"//evil.example",       // protocol-relative IS absolute to a browser
		`/\evil.example`,       // browsers normalise \ to / right after a slash
		"/\t/evil.example",     // control chars are stripped first, leaving //
		"javascript:alert(1)",  //
		"\n/write",             //
	} {
		if got := safeNext(bad); got != "/" {
			t.Errorf("safeNext(%q) = %q, want %q", bad, got, "/")
		}
	}
}

func TestTheDevSignInPageEscapesNext(t *testing.T) {
	t.Setenv("TAKO_BACKEND", "local")
	w := httptest.NewRecorder()
	SignInPage("email").ServeHTTP(w, httptest.NewRequest("GET",
		`/_tako/signin?next=/%22%3E%3Cscript%3Ealert(1)%3C/script%3E`, nil))
	if body := w.Body.String(); strings.Contains(body, "<script>alert(1)</script>") {
		t.Errorf("next escaped into markup:\n%s", lineWith(body, "alert(1)"))
	}
}

func TestTheCloudSignInPageEscapesNext(t *testing.T) {
	cloudSignIn(t)
	// A </script> inside a JS string literal still ends the script
	// ELEMENT: %q escapes quotes and backslashes, and neither of those
	// is what closes a <script>.
	w := httptest.NewRecorder()
	SignInPage("google").ServeHTTP(w, httptest.NewRequest("GET",
		`/_tako/signin?next=/%3C/script%3E%3Cscript%3Ealert(1)%3C/script%3E`, nil))
	body := w.Body.String()
	if strings.Contains(body, "</script><script>alert(1)</script>") {
		t.Errorf("next broke out of the script block:\n%s", lineWith(body, "alert(1)"))
	}
	if !strings.Contains(body, "const next =") {
		t.Error("the page no longer carries a next binding at all")
	}
}

func TestTheCloudSignInPageRefusesAnotherOrigin(t *testing.T) {
	cloudSignIn(t)
	w := httptest.NewRecorder()
	SignInPage("google").ServeHTTP(w, httptest.NewRequest("GET",
		`/_tako/signin?next=//evil.example/`, nil))
	if strings.Contains(w.Body.String(), "evil.example") {
		t.Errorf("a protocol-relative next survived:\n%s", lineWith(w.Body.String(), "evil.example"))
	}
}

// The sign-in pages are the one place tako wrote every byte, so they
// can carry an ENFORCING policy where an app's own pages cannot.
//
// It is deliberately narrow: script-src, object-src and base-uri, and
// nothing else. A default-src here would have to predict every endpoint
// the Identity Platform SDK talks to, and a policy that breaks sign-in
// is worse than the reflected script it was guarding against. These
// three are what an injected script needs.
func TestTakosOwnSignInPagesEnforceAScriptPolicy(t *testing.T) {
	cloudSignIn(t)
	w := httptest.NewRecorder()
	SignInPage("google").ServeHTTP(w, httptest.NewRequest("GET", "/_tako/signin", nil))
	csp := w.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("no enforcing CSP on the one page where a reflected script reads a password")
	}
	for _, want := range []string{"script-src", "https://www.gstatic.com", "'nonce-", "object-src 'none'", "base-uri 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q is missing %q", csp, want)
		}
	}
	// The nonce is per response, or it is not a nonce.
	first := w.Header().Get("Content-Security-Policy")
	w2 := httptest.NewRecorder()
	SignInPage("google").ServeHTTP(w2, httptest.NewRequest("GET", "/_tako/signin", nil))
	if first == w2.Header().Get("Content-Security-Policy") {
		t.Error("the same nonce twice is a constant an injected script can name")
	}
	// The page's own inline script must carry it, or the page is broken.
	if !strings.Contains(w2.Body.String(), "<script nonce=") {
		t.Error("the page's inline script has no nonce — the policy would block tako's own sign-in")
	}
}

// The dev page is pure HTML — no script at all — so its policy is the
// strict one.
func TestTheDevSignInPageForbidsScriptEntirely(t *testing.T) {
	t.Setenv("TAKO_BACKEND", "local")
	w := httptest.NewRecorder()
	SignInPage("email").ServeHTTP(w, httptest.NewRequest("GET", "/_tako/signin", nil))
	if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'none'") {
		t.Errorf("the dev sign-in page runs no script; its CSP should say so, got %q", csp)
	}
}

func cloudSignIn(t *testing.T) {
	t.Helper()
	t.Setenv("TAKO_BACKEND", "gcp")
	t.Setenv("TAKO_AUTH_APIKEY", "k")
	t.Setenv("TAKO_AUTH_DOMAIN", "d")
	t.Setenv("TAKO_PROJECT", "p")
}

func lineWith(body, needle string) string {
	for _, l := range strings.Split(body, "\n") {
		if strings.Contains(l, needle) {
			return l
		}
	}
	return "(not present)"
}
