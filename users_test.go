package tako

// AB10b: the account surface.
//
// The backend contract is pinned by the conformance suite; these pin the
// SDK's own behaviour — the shapes an app meets, the errors it can
// branch on, and the read-modify-write that Grant and Revoke are.

import (
	"testing"
)

func TestCreateReadAndList(t *testing.T) {
	app := NewTestApp(t)
	ctx := app.Ctx()
	users := Auth(AuthConfig{Password: true}).Users()

	made, err := users.Create(ctx, NewUser{Email: "ana@example.com", Name: "Ana", Password: "a long enough one"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if made.ID == "" || made.Email != "ana@example.com" || made.Name != "Ana" {
		t.Fatalf("Create returned %+v", made)
	}

	got, err := users.Get(ctx, made.ID)
	if err != nil || got.ID != made.ID {
		t.Fatalf("Get = %+v, %v", got, err)
	}
	byEmail, err := users.ByEmail(ctx, "ana@example.com")
	if err != nil || byEmail.ID != made.ID {
		t.Fatalf("ByEmail = %+v, %v", byEmail, err)
	}

	page, err := users.List(ctx).Limit(10).Page("")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Users) != 1 || page.Users[0].ID != made.ID {
		t.Fatalf("List returned %+v", page.Users)
	}
}

// The two refusals an app branches on, and they are different questions:
// "is this address free" and "is there anybody here".
func TestTheTwoRefusalsAreTellableApart(t *testing.T) {
	app := NewTestApp(t)
	ctx := app.Ctx()
	users := Auth(AuthConfig{Password: true}).Users()

	if _, err := users.Create(ctx, NewUser{Email: "taken@example.com"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, err := users.Create(ctx, NewUser{Email: "taken@example.com"})
	if !IsEmailTaken(err) {
		t.Fatalf("duplicate Create = %v, want IsEmailTaken", err)
	}
	if IsNotFound(err) {
		t.Error("a taken address reads as not-found")
	}

	if _, err := users.Get(ctx, "nobody"); !IsNotFound(err) {
		t.Fatalf("Get(missing) = %v, want IsNotFound", err)
	}
}

func TestGrantAndRevokeAreIdempotent(t *testing.T) {
	app := NewTestApp(t)
	ctx := app.Ctx()
	users := Auth(AuthConfig{Password: true}).Users()
	editor := Role("editor")
	moderator := Role("moderator")

	u, err := users.Create(ctx, NewUser{Email: "roles@example.com"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	for range 2 { // granting twice is granting once
		if err := users.Grant(ctx, u.ID, editor); err != nil {
			t.Fatalf("Grant: %v", err)
		}
	}
	got, _ := users.Get(ctx, u.ID)
	if len(got.Roles) != 1 || !got.HasRole(editor) {
		t.Fatalf("roles = %v, want [editor]", got.Roles)
	}

	if err := users.Grant(ctx, u.ID, moderator); err != nil {
		t.Fatalf("Grant(moderator): %v", err)
	}
	if got, _ = users.Get(ctx, u.ID); !got.HasRole(editor) || !got.HasRole(moderator) {
		t.Fatalf("roles = %v, want both", got.Roles)
	}

	// Revoking one leaves the other — the read-modify-write's whole job.
	if err := users.Revoke(ctx, u.ID, editor); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	got, _ = users.Get(ctx, u.ID)
	if got.HasRole(editor) || !got.HasRole(moderator) {
		t.Fatalf("roles after revoke = %v, want [moderator]", got.Roles)
	}
	// Revoking what they do not have is not an error and not a write.
	if err := users.Revoke(ctx, u.ID, editor); err != nil {
		t.Fatalf("second Revoke: %v", err)
	}
}

func TestDisableAndDelete(t *testing.T) {
	app := NewTestApp(t)
	ctx := app.Ctx()
	users := Auth(AuthConfig{Password: true}).Users()

	u, err := users.Create(ctx, NewUser{Email: "off@example.com"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := users.Disable(ctx, u.ID); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	if err := users.Enable(ctx, u.ID); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if err := users.Delete(ctx, u.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := users.Get(ctx, u.ID); !IsNotFound(err) {
		t.Fatalf("Get after Delete = %v", err)
	}
	// The address is free again, which is what deletion has to mean.
	if _, err := users.Create(ctx, NewUser{Email: "off@example.com"}); err != nil {
		t.Fatalf("Create after Delete: %v", err)
	}
}

// A role that cannot be stored is refused before the write, with a
// message about the app's mistake rather than a relayed API error.
func TestTooManyRolesIsRefusedWithItsNumbers(t *testing.T) {
	app := NewTestApp(t)
	ctx := app.Ctx()
	users := Auth(AuthConfig{Password: true}).Users()

	u, err := users.Create(ctx, NewUser{Email: "big@example.com"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	for i := range 200 {
		err := users.Grant(ctx, u.ID, Role("role-with-a-fairly-long-name-"+string(rune('a'+i%26))+string(rune('a'+i/26))))
		if err == nil {
			continue
		}
		if !contains(err.Error(), "1000-byte") || !contains(err.Error(), "a role is a name, not a payload") {
			t.Fatalf("refusal = %v", err)
		}
		return
	}
	t.Fatal("200 roles were accepted; the ceiling is not enforced")
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
