package runtime

import (
	"strings"
	"testing"

	tako "github.com/gsandoval/tako"
)

// claims builds an ID token's claim set with a given provider.
func claims(provider string, verified bool) idTokenClaims {
	c := idTokenClaims{Sub: "uid-1", Email: "someone@example.com", Name: "Someone", EmailVerified: verified}
	c.Firebase.SignInProvider = provider
	return c
}

// The finding, in one test: tako.Auth(AuthConfig{Google: true}) reads
// like a restriction, and until FL14 it was not one. Identity Platform
// projects can have email/password enabled independently of what the
// app declares — tako-test-02 did, with self-service sign-up open — so
// a self-registered password account produced a session tako accepted.
func TestASessionCannotComeFromAnUndeclaredProvider(t *testing.T) {
	_, err := userFromClaims(claims("password", true), []string{"google"})
	if err == nil {
		t.Fatal("a password token was accepted by an app that declares only google")
	}
	for _, want := range []string{"minted by email", "this app declares google"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message missing %q: %v", want, err)
		}
	}
}

func TestADeclaredProviderIsAccepted(t *testing.T) {
	for _, c := range []struct {
		provider string
		declared []string
	}{
		{"google.com", []string{"google"}},
		{"password", []string{"email"}},
		{"password", []string{"google", "email"}},
		{"google.com", []string{"google", "email"}},
	} {
		u, err := userFromClaims(claims(c.provider, true), c.declared)
		if err != nil {
			t.Errorf("%s against %v: %v", c.provider, c.declared, err)
			continue
		}
		if u.ID != "uid-1" {
			t.Errorf("%s: ID = %q", c.provider, u.ID)
		}
	}
}

// An absent claim is not "any provider". A token tako cannot place is
// one it should not accept — the safe reading of missing information.
func TestATokenThatNamesNoProviderIsRefused(t *testing.T) {
	if _, err := userFromClaims(claims("", true), []string{"google"}); err == nil {
		t.Error("a token with no firebase.sign_in_provider was accepted")
	}
	// Not even when the app declares nothing.
	if _, err := userFromClaims(claims("", true), nil); err == nil {
		t.Error("a token with no provider was accepted by an app declaring none")
	}
}

// A provider tako has no name for is still refused when it is not
// declared — the rule is a whitelist, so phone, anonymous and anything
// Identity Platform adds later are covered without being enumerated.
func TestAnUnknownProviderIsRefusedUnlessDeclared(t *testing.T) {
	if _, err := userFromClaims(claims("anonymous", false), []string{"google"}); err == nil {
		t.Error("an anonymous token was accepted")
	}
}

// The claims an app cannot otherwise see. EmailVerified is the one that
// matters: an allowlist keyed on email is only as strong as the
// guarantee the address belongs to whoever signed in.
func TestTheProviderClaimsReachTheApp(t *testing.T) {
	u, err := userFromClaims(claims("google.com", true), []string{"google"})
	if err != nil {
		t.Fatal(err)
	}
	if u.Provider != "google" {
		t.Errorf("Provider = %q, want google (the name the app declared, not Identity Platform's)", u.Provider)
	}
	if !u.EmailVerified {
		t.Error("EmailVerified = false on a verified claim")
	}

	// Absent claim → false. An unverified address must never look
	// verified because the field was missing.
	u, err = userFromClaims(claims("password", false), []string{"email"})
	if err != nil {
		t.Fatal(err)
	}
	if u.EmailVerified {
		t.Error("EmailVerified = true when the claim said otherwise")
	}
	if u.Provider != "email" {
		t.Errorf("Provider = %q, want email", u.Provider)
	}
}

// tako.User is what a handler reads, so the fields have to survive the
// trip through a session record, not just the verifier.
func TestUserCarriesTheClaimsItWasGiven(t *testing.T) {
	u := tako.User{ID: "x", Provider: "google", EmailVerified: true}
	if u.Provider != "google" || !u.EmailVerified {
		t.Error("tako.User does not carry the provider claims")
	}
}
