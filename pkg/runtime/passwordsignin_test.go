package runtime

// AB10c: signing in with an email and a password, locally.
//
// In the cloud this exchange is Identity Platform's — the browser posts
// credentials to Google, and tako only ever sees the ID token that comes
// back. There is nothing like that on a laptop, so without this branch
// auth.Users().Create(email, password) creates an account that cannot
// sign in, and the local half of the accounts contract is a stub the
// conformance suite cannot hold honest.
//
// The branch is local-only by construction (isLocalBackend), which is
// what keeps password verification out of the deployed path entirely.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	tako "github.com/gsandoval/tako"
)

func signInHandler(t *testing.T, providers ...string) http.Handler {
	t.Helper()
	if err := BootLocal(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	return SessionHandler(providers...)
}

func postSignIn(h http.Handler, form url.Values) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "/_tako/api/session", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestAnAccountCanSignInWithItsPassword(t *testing.T) {
	h := signInHandler(t, "email")
	users := tako.Auth(tako.AuthConfig{Password: true}).Users()
	ctx := takoCtx(context.Background())

	if _, err := users.Create(ctx, tako.NewUser{
		Email: "ana@example.com", Name: "Ana", Password: "correct horse battery",
		Roles: []string{"editor"},
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	w := postSignIn(h, url.Values{"email": {"ana@example.com"}, "password": {"correct horse battery"}})
	if w.Code != http.StatusSeeOther && w.Code != http.StatusOK {
		t.Fatalf("sign-in = %d: %s", w.Code, w.Body.String())
	}
	cookie := ""
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookie {
			cookie = c.Value
		}
	}
	if cookie == "" {
		t.Fatal("sign-in set no session cookie")
	}

	// The session carries the account's roles, so a door opens without
	// anything else being told about the sign-in method.
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	rec := loadSession(r)
	if rec == nil {
		t.Fatal("the session did not load")
	}
	if rec.Email != "ana@example.com" || len(rec.Roles) != 1 || rec.Roles[0] != "editor" {
		t.Fatalf("session = %+v", rec)
	}
}

// One sentence for both halves. Which of them was wrong is exactly what
// somebody guessing wants to learn.
func TestAWrongPasswordAndAnUnknownAddressAnswerTheSame(t *testing.T) {
	h := signInHandler(t, "email")
	users := tako.Auth(tako.AuthConfig{Password: true}).Users()
	if _, err := users.Create(takoCtx(context.Background()), tako.NewUser{
		Email: "ana@example.com", Password: "correct horse battery",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	wrong := postSignIn(h, url.Values{"email": {"ana@example.com"}, "password": {"guess"}})
	unknown := postSignIn(h, url.Values{"email": {"nobody@example.com"}, "password": {"guess"}})

	for name, w := range map[string]*httptest.ResponseRecorder{"wrong password": wrong, "unknown address": unknown} {
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s: %d, want 401", name, w.Code)
		}
		if !strings.Contains(w.Body.String(), signInRefused) {
			t.Errorf("%s: body = %q", name, w.Body.String())
		}
	}
	if wrong.Body.String() != unknown.Body.String() {
		t.Errorf("the two refusals differ:\n  %q\n  %q", wrong.Body.String(), unknown.Body.String())
	}
}

// An account with no password has no password door — an account that
// signs in with Google must not be reachable by posting an empty one.
func TestAnAccountWithNoPasswordCannotBeSignedInto(t *testing.T) {
	h := signInHandler(t, "email")
	users := tako.Auth(tako.AuthConfig{Password: true}).Users()
	if _, err := users.Create(takoCtx(context.Background()), tako.NewUser{Email: "google@example.com"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	for _, pw := range []string{"", "anything"} {
		w := postSignIn(h, url.Values{"email": {"google@example.com"}, "password": {pw}})
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("password %q = %d, want 401", pw, w.Code)
		}
	}
}

// A disabled account is refused, and refused with the same sentence:
// whether an address exists is not something a sign-in page tells you.
func TestADisabledAccountCannotSignIn(t *testing.T) {
	h := signInHandler(t, "email")
	users := tako.Auth(tako.AuthConfig{Password: true}).Users()
	ctx := takoCtx(context.Background())
	u, err := users.Create(ctx, tako.NewUser{Email: "off@example.com", Password: "a good long one"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := users.Disable(ctx, u.ID); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	w := postSignIn(h, url.Values{"email": {"off@example.com"}, "password": {"a good long one"}})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("disabled sign-in = %d, want 401", w.Code)
	}
	if !strings.Contains(w.Body.String(), signInRefused) {
		t.Errorf("body = %q", w.Body.String())
	}
}

// The declaration is the door, here exactly as in the cloud: an app that
// never declared Password has no password sign-in, whatever is posted.
func TestPasswordSignInNeedsTheDeclaration(t *testing.T) {
	h := signInHandler(t, "google")
	users := tako.Auth(tako.AuthConfig{Password: true}).Users()
	if _, err := users.Create(takoCtx(context.Background()), tako.NewUser{
		Email: "ana@example.com", Password: "correct horse battery",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	w := postSignIn(h, url.Values{"email": {"ana@example.com"}, "password": {"correct horse battery"}})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("sign-in against a google-only app = %d, want 401", w.Code)
	}
}

// The form appears only when the app declared the provider: a form for a
// door that does not exist is a lie.
func TestTheDevPageOffersThePasswordFormOnlyWhenDeclared(t *testing.T) {
	if err := BootLocal(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		providers []string
		want      bool
	}{
		{[]string{"email"}, true},
		{[]string{"google"}, false},
	} {
		w := httptest.NewRecorder()
		SignInPage(c.providers...).ServeHTTP(w, httptest.NewRequest("GET", "/_tako/signin", nil))
		has := strings.Contains(w.Body.String(), `name="password"`)
		if has != c.want {
			t.Errorf("providers %v: password form present = %v, want %v", c.providers, has, c.want)
		}
	}
}

func TestInlineFirstAdminAndInviteOnlyInSessionHandler(t *testing.T) {
	h := signInHandler(t, "email")
	adminRole := tako.Role("admin")
	posterRole := tako.Role("poster")
	auth := tako.Auth(tako.AuthConfig{
		Password:        true,
		FirstAdmin:      adminRole,
		FirstAdminEmail: "founder@example.com",
		DefaultRole:     posterRole,
		InviteOnly:      true,
	})
	t.Cleanup(func() {
		_ = tako.Auth(tako.AuthConfig{Password: true})
	})
	ctx := takoCtx(context.Background())
	founder, err := auth.Users().Create(ctx, tako.NewUser{
		Email:    "founder@example.com",
		Password: "correct horse battery",
	})
	if err != nil {
		t.Fatalf("Create founder: %v", err)
	}
	outsider, err := auth.Users().Create(ctx, tako.NewUser{
		Email:    "outsider@example.com",
		Password: "correct horse battery",
	})
	if err != nil {
		t.Fatalf("Create outsider: %v", err)
	}

	// 1. Founder signs in -> granted both FirstAdmin ("admin") and DefaultRole ("poster") inline.
	w := postSignIn(h, url.Values{"email": {"founder@example.com"}, "password": {"correct horse battery"}})
	if w.Code != http.StatusOK {
		t.Fatalf("founder sign-in = %d: %s", w.Code, w.Body.String())
	}
	gotFounder, err := auth.Users().Get(ctx, founder.ID)
	if err != nil {
		t.Fatalf("Get founder: %v", err)
	}
	if !gotFounder.HasRole(adminRole) || !gotFounder.HasRole(posterRole) {
		t.Errorf("founder roles = %v, want [admin poster]", gotFounder.Roles)
	}

	// 2. Outsider signs in while InviteOnly is true -> does NOT receive DefaultRole ("poster").
	w2 := postSignIn(h, url.Values{"email": {"outsider@example.com"}, "password": {"correct horse battery"}})
	if w2.Code != http.StatusOK {
		t.Fatalf("outsider sign-in = %d: %s", w2.Code, w2.Body.String())
	}
	gotOutsider, err := auth.Users().Get(ctx, outsider.ID)
	if err != nil {
		t.Fatalf("Get outsider: %v", err)
	}
	if gotOutsider.HasRole(posterRole) || gotOutsider.HasRole(adminRole) {
		t.Errorf("outsider roles under InviteOnly=true = %v, want empty", gotOutsider.Roles)
	}

	// 3. Admin disables InviteOnly at runtime -> outsider signs in again and receives DefaultRole ("poster").
	if err := auth.SetInviteOnly(ctx, false); err != nil {
		t.Fatalf("SetInviteOnly(false): %v", err)
	}
	w3 := postSignIn(h, url.Values{"email": {"outsider@example.com"}, "password": {"correct horse battery"}})
	if w3.Code != http.StatusOK {
		t.Fatalf("outsider sign-in after SetInviteOnly(false) = %d: %s", w3.Code, w3.Body.String())
	}
	gotOutsider, err = auth.Users().Get(ctx, outsider.ID)
	if err != nil {
		t.Fatalf("Get outsider: %v", err)
	}
	if !gotOutsider.HasRole(posterRole) {
		t.Errorf("outsider roles after SetInviteOnly(false) = %v, want [poster]", gotOutsider.Roles)
	}
}
