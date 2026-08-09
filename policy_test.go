package tako

// AB1: authorization is declared, not repeated.
//
// These pin the SDK half — the record, the refusal, and the enforcement
// Endpoint.Call performs. The mount half is pkg/runtime; the declaration
// half is the analyzer's goldens.

import (
	"errors"
	"strings"
	"testing"
)

var testEditors = Policy(func(ctx Ctx, u User) error {
	if !strings.HasSuffix(u.Email, "@example.test") {
		return Deny("that address is not on the editor list for this site")
	}
	return nil
})

func TestADenialIsA403WithPublicText(t *testing.T) {
	err := Deny("that address is not on the editor list for this site")
	var status interface{ HTTPStatus() int }
	if !errors.As(err, &status) {
		t.Fatal("Deny does not carry an HTTP status")
	}
	if got := status.HTTPStatus(); got != 403 {
		t.Fatalf("Deny status = %d, want 403", got)
	}
	if !IsDenied(err) {
		t.Fatal("IsDenied does not recognise its own Deny")
	}
	if IsDenied(Errorf(403, "CSRF token missing or invalid")) {
		t.Fatal("IsDenied claims a plain 403 is a policy refusal — they are different problems")
	}
	if IsDenied(nil) {
		t.Fatal("IsDenied(nil) is true")
	}
}

func TestAPolicyAdmitsAndRefuses(t *testing.T) {
	app := NewTestApp(t)
	editor := app.SignIn("alice") // alice@example.test
	stranger := User{ID: "n", Email: "nobody@example.com", Provider: "google"}

	if err := testEditors.Check(app.Ctx(editor), editor); err != nil {
		t.Fatalf("an editor was refused: %v", err)
	}
	err := testEditors.Check(app.Ctx(stranger), stranger)
	if !IsDenied(err) {
		t.Fatalf("a stranger was not denied: %v", err)
	}
	if got := err.Error(); got != "that address is not on the editor list for this site" {
		t.Fatalf("denial text = %q", got)
	}
}

// A policy left as a zero handle — a var declared and never given a
// constructor — must not admit. The analyzer refuses this at build time;
// this is the runtime's answer if one ever reaches it.
func TestAnUndeclaredPolicyRefuses(t *testing.T) {
	var none *PolicyHandle
	app := NewTestApp(t)
	u := app.SignIn("alice")
	if err := none.Check(app.Ctx(u), u); err == nil {
		t.Fatal("a nil policy admitted a request")
	}
}

// --- what Endpoint.Call enforces --------------------------------------

var (
	guarded = API("GET /guarded", func(ctx Ctx, req struct{}) (string, error) {
		return "the goods: " + ctx.User().Email, nil
	}, Route{Allow: testEditors})

	signedIn = API("GET /signed-in", func(ctx Ctx, req struct{}) (string, error) {
		return ctx.User().Email, nil
	}, Route{SignIn: true})
)

// The load-bearing case. A policy that only ran at the generated mount
// would be bypassed by every in-package call, every test, and — the one
// that matters — every cross-service call, which is the one place a
// trust boundary is actually crossed.
func TestCallEnforcesTheDeclaredPolicy(t *testing.T) {
	app := NewTestApp(t)

	editor := app.SignIn("alice")
	if got, err := guarded.Call(app.Ctx(editor), struct{}{}); err != nil {
		t.Fatalf("an editor's call was refused: %v", err)
	} else if got != "the goods: alice@example.test" {
		t.Fatalf("result = %q", got)
	}

	stranger := User{ID: "n", Email: "nobody@example.com"}
	if _, err := guarded.Call(app.Ctx(stranger), struct{}{}); !IsDenied(err) {
		t.Fatalf("a stranger's call was not denied: %v", err)
	}
}

// Before this, a SignIn route called with an anonymous Ctx reached
// ctx.User() and PANICKED. A missing session is a 401, not a crash.
func TestCallOnAGuardedRouteWithNoUserIsA401(t *testing.T) {
	app := NewTestApp(t)

	for _, c := range []struct {
		name string
		call func() error
	}{
		{"SignIn", func() error { _, err := signedIn.Call(app.Ctx(), struct{}{}); return err }},
		{"Allow", func() error { _, err := guarded.Call(app.Ctx(), struct{}{}); return err }},
	} {
		err := c.call()
		if err == nil {
			t.Fatalf("%s: an anonymous call was admitted", c.name)
		}
		var status interface{ HTTPStatus() int }
		if !errors.As(err, &status) || status.HTTPStatus() != 401 {
			t.Fatalf("%s: err = %v, want a 401", c.name, err)
		}
		if IsDenied(err) {
			t.Fatalf("%s: a missing session was reported as a policy refusal", c.name)
		}
	}
}

// A policy that fails while DECIDING has not decided. A store read that
// timed out is a 500 whose text stays internal, never a 403 telling the
// caller they are not allowed.
func TestAPolicyThatBreaksIsNotARefusal(t *testing.T) {
	broken := Policy(func(ctx Ctx, u User) error {
		return errors.New("entity read failed: context deadline exceeded")
	})
	brokenDoor := API("GET /broken-door", func(ctx Ctx, req struct{}) (string, error) {
		return "unreachable", nil
	}, Route{Allow: broken})

	app := NewTestApp(t)
	u := app.SignIn("alice")
	_, err := brokenDoor.Call(app.Ctx(u), struct{}{})
	if err == nil {
		t.Fatal("a broken policy admitted the call")
	}
	if IsDenied(err) {
		t.Fatal("a broken policy's error was reported as a refusal")
	}
	var status interface{ HTTPStatus() int }
	if errors.As(err, &status) && status.HTTPStatus() == 403 {
		t.Fatalf("a broken policy answered 403: %v", err)
	}
}
